// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/mfa"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	accountviews "github.com/olegiv/ocms-go/internal/views/account"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
)

// Session keys of a sign-in that passed the password and waits for its
// second factor. The session carries no SessionKeyUserID until it passes,
// so every route behind middleware.Auth still sees an anonymous visitor.
const (
	sessionKeyMFAPendingUserID  = "mfa_pending_user_id"
	sessionKeyMFAPendingVersion = "mfa_pending_session_version"
	sessionKeyMFAPendingSince   = "mfa_pending_since"
	sessionKeyMFAPendingEnroll  = "mfa_pending_enroll"
	sessionKeyMFAPendingTries   = "mfa_pending_attempts"
	// sessionKeyMFASetupSecret holds the sealed secret of an authenticator
	// being enrolled, so a reload shows the QR code already scanned.
	sessionKeyMFASetupSecret = "mfa_setup_secret"
)

const (
	// mfaPendingTTL is how long a sign-in may wait for its second factor,
	// long enough to install an authenticator app during enrollment.
	mfaPendingTTL = 10 * time.Minute
	// mfaMaxAttempts is how many wrong codes end a pending sign-in. The
	// login lockout counts the same failures across sign-ins.
	mfaMaxAttempts = 5
)

// pendingSignIn is the state of a sign-in waiting for its second factor.
type pendingSignIn struct {
	user   store.User
	enroll bool // the account has no second factor yet and must set one up
}

// startPendingSignIn records that user passed the password and must now
// pass, or enroll, a second factor. It drops any identity the session
// already had, so a second account's password cannot ride on the first
// account's sign-in.
func (h *AuthHandler) startPendingSignIn(ctx context.Context, user store.User, enroll bool) error {
	if err := h.sessionManager.RenewToken(ctx); err != nil {
		return fmt.Errorf("renewing the session token: %w", err)
	}
	h.sessionManager.Remove(ctx, middleware.SessionKeyUserID)
	h.sessionManager.Remove(ctx, middleware.SessionKeyUserSessionVersion)
	h.sessionManager.Remove(ctx, middleware.SessionKeyMFAVerified)
	h.sessionManager.Remove(ctx, sessionKeyMFASetupSecret)
	h.sessionManager.Put(ctx, sessionKeyMFAPendingUserID, user.ID)
	h.sessionManager.Put(ctx, sessionKeyMFAPendingVersion, user.SessionVersion)
	h.sessionManager.Put(ctx, sessionKeyMFAPendingSince, time.Now().Unix())
	h.sessionManager.Put(ctx, sessionKeyMFAPendingEnroll, enroll)
	h.sessionManager.Put(ctx, sessionKeyMFAPendingTries, 0)
	return nil
}

// clearPendingSignIn forgets a pending sign-in.
func (h *AuthHandler) clearPendingSignIn(ctx context.Context) {
	for _, key := range []string{
		sessionKeyMFAPendingUserID, sessionKeyMFAPendingVersion, sessionKeyMFAPendingSince,
		sessionKeyMFAPendingEnroll, sessionKeyMFAPendingTries, sessionKeyMFASetupSecret,
	} {
		h.sessionManager.Remove(ctx, key)
	}
}

// pendingSignIn returns the sign-in waiting on this session. It is gone once
// it is older than mfaPendingTTL, or the account was deleted or had its
// credentials changed since the password was checked.
func (h *AuthHandler) pendingSignIn(ctx context.Context) (pendingSignIn, bool, error) {
	userID := h.sessionManager.GetInt64(ctx, sessionKeyMFAPendingUserID)
	if userID == 0 {
		return pendingSignIn{}, false, nil
	}
	since := time.Unix(h.sessionManager.GetInt64(ctx, sessionKeyMFAPendingSince), 0)
	if time.Since(since) > mfaPendingTTL {
		h.clearPendingSignIn(ctx)
		return pendingSignIn{}, false, nil
	}
	user, err := h.queries.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		h.clearPendingSignIn(ctx)
		return pendingSignIn{}, false, nil
	}
	if err != nil {
		return pendingSignIn{}, false, fmt.Errorf("loading the pending sign-in user: %w", err)
	}
	if user.SessionVersion != h.sessionManager.GetInt64(ctx, sessionKeyMFAPendingVersion) {
		h.clearPendingSignIn(ctx)
		return pendingSignIn{}, false, nil
	}
	return pendingSignIn{user: user, enroll: h.sessionManager.GetBool(ctx, sessionKeyMFAPendingEnroll)}, true, nil
}

// requirePendingSignIn returns the pending sign-in, or redirects to the login
// form when there is none or it is not at the step wantEnroll names.
func (h *AuthHandler) requirePendingSignIn(w http.ResponseWriter, r *http.Request, wantEnroll bool) (pendingSignIn, bool) {
	pending, ok, err := h.pendingSignIn(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to load the pending sign-in", "error", err)
		return pendingSignIn{}, false
	}
	if !ok || pending.enroll != wantEnroll {
		// A user who goes Back to a sign-in step after signing in has nothing
		// pending and nothing expired.
		if dest, signedIn := h.signedInDestination(r.Context()); signedIn && !ok {
			http.Redirect(w, r, dest, http.StatusSeeOther)
			return pendingSignIn{}, false
		}
		flashError(w, r, h.renderer, redirectLogin, i18n.T(middleware.GetAdminLang(r), "mfa.sign_in_expired"))
		return pendingSignIn{}, false
	}
	return pending, true
}

// grantSession signs user in on this session. It is the one place a session
// gains SessionKeyUserID (TestOnlyGrantSessionSignsIn), so every sign-in path
// clears the lockout, records the login and logs it the same way. secondFactor
// is the method the sign-in passed, empty when it used the password alone.
func (h *AuthHandler) grantSession(r *http.Request, user store.User, secondFactor mfa.Method) error {
	ctx := r.Context()
	clientIP := hcaptcha.GetRemoteIP(r)

	if h.loginProtection != nil {
		h.loginProtection.RecordSuccessfulLogin(user.Email, clientIP)
	}
	if err := h.queries.UpdateUserLastLogin(ctx, store.UpdateUserLastLoginParams{
		LastLoginAt: sql.NullTime{Time: time.Now(), Valid: true},
		ID:          user.ID,
	}); err != nil {
		slog.Error("failed to update last login time", "error", err, "user_id", user.ID)
		// Don't block login on this error
	}

	// Regenerate session ID to prevent session fixation
	if err := h.sessionManager.RenewToken(ctx); err != nil {
		return fmt.Errorf("renewing the session token: %w", err)
	}
	h.clearPendingSignIn(ctx)

	// Store user ID in session along with the user's current session_version
	// so the auth middleware can detect if the credential is rotated under us.
	h.sessionManager.Put(ctx, middleware.SessionKeyUserID, user.ID)
	h.sessionManager.Put(ctx, middleware.SessionKeyUserSessionVersion, user.SessionVersion)
	h.sessionManager.Put(ctx, middleware.SessionKeyMFAVerified, secondFactor != "")

	metadata := map[string]any{"email": user.Email}
	if secondFactor != "" {
		metadata["second_factor"] = string(secondFactor)
	}
	slog.Info("user logged in", "user_id", user.ID, "email", user.Email, "second_factor", string(secondFactor))
	_ = h.eventService.LogAuthEvent(ctx, model.EventLevelInfo, "User logged in", &user.ID, clientIP, middleware.GetRequestURL(r), metadata)
	return nil
}

// adoptSessionVersion keeps the current session valid after the user changed
// their own second factor, which made every session of theirs stale.
func (h *AuthHandler) adoptSessionVersion(ctx context.Context, version int64, secondFactor bool) error {
	if err := h.sessionManager.RenewToken(ctx); err != nil {
		return fmt.Errorf("renewing the session token: %w", err)
	}
	h.sessionManager.Put(ctx, middleware.SessionKeyUserSessionVersion, version)
	h.sessionManager.Put(ctx, middleware.SessionKeyMFAVerified, secondFactor)
	return nil
}

// authPageData is the shell data of a standalone sign-in step.
func (h *AuthHandler) authPageData(r *http.Request, titleKey string) adminviews.AuthPageData {
	lang := middleware.GetAdminLang(r)
	d := adminviews.AuthPageData{Title: i18n.T(lang, titleKey), AdminLang: lang}
	if flash := h.sessionManager.PopString(r.Context(), "flash"); flash != "" {
		d.Flash = flash
		d.FlashType = h.sessionManager.PopString(r.Context(), "flash_type")
		if d.FlashType == "" {
			d.FlashType = "info"
		}
	}
	return d
}

// LoginMFAForm handles GET /login/mfa - asks for the second factor.
func (h *AuthHandler) LoginMFAForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requirePendingSignIn(w, r, false); !ok {
		return
	}
	setNoStore(w)
	renderTempl(w, r, adminviews.MFAChallengePage(h.authPageData(r, "mfa.challenge_title")))
}

// LoginMFA handles POST /login/mfa - checks the second factor and signs in.
func (h *AuthHandler) LoginMFA(w http.ResponseWriter, r *http.Request) {
	pending, ok := h.requirePendingSignIn(w, r, false)
	if !ok {
		return
	}
	lang := middleware.GetAdminLang(r)
	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, RouteLoginMFA, i18n.T(lang, "auth.invalid_form_data"))
		return
	}
	user := pending.user
	if h.refuseLockedAccount(w, r, user, "Login attempt on locked account", func() { h.clearPendingSignIn(r.Context()) }, redirectLogin) {
		return
	}

	method, err := h.mfa.Verify(r.Context(), user.ID, r.FormValue("code"))
	if err != nil {
		h.rejectSignInCode(w, r, user, err)
		return
	}
	if err := h.grantSession(r, user, method); err != nil {
		logAndInternalError(w, "session renewal error", "error", err)
		return
	}
	if method == mfa.MethodRecoveryCode {
		h.flashRecoveryCodeUsed(r, user)
	} else {
		h.renderer.SetFlash(r, i18n.T(lang, "auth.welcome_back", user.Name), "success")
	}
	http.Redirect(w, r, loginDestination(user.Role), http.StatusSeeOther)
}

// flashRecoveryCodeUsed records a sign-in with a recovery code and tells the
// user how many are left.
func (h *AuthHandler) flashRecoveryCodeUsed(r *http.Request, user store.User) {
	lang := middleware.GetAdminLang(r)
	metadata := map[string]any{"email": user.Email}
	message := i18n.T(lang, "mfa.signed_in_with_recovery_code_uncounted")
	if status, err := h.mfa.Status(r.Context(), user.ID); err != nil {
		slog.Error("failed to count recovery codes", "error", err, "user_id", user.ID)
	} else {
		metadata["recovery_codes_left"] = status.RecoveryCodesLeft
		message = i18n.T(lang, "mfa.signed_in_with_recovery_code", status.RecoveryCodesLeft)
	}
	_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Recovery code used to sign in", &user.ID, hcaptcha.GetRemoteIP(r), middleware.GetRequestURL(r), metadata)
	h.renderer.SetFlash(r, message, "warning")
}

// refuseLockedAccount answers a locked account, logs message as the
// refusal, and reports true. onLocked runs first, to drop state the lockout
// ends.
func (h *AuthHandler) refuseLockedAccount(w http.ResponseWriter, r *http.Request, user store.User, message string, onLocked func(), redirectURL string) bool {
	if h.loginProtection == nil {
		return false
	}
	locked, remaining := h.loginProtection.IsAccountLocked(user.Email, hcaptcha.GetRemoteIP(r))
	if !locked {
		return false
	}
	h.logRefusal(r, user, model.EventLevelWarning, message)
	if onLocked != nil {
		onLocked()
	}
	flashError(w, r, h.renderer, redirectURL, i18n.T(middleware.GetAdminLang(r), "auth.account_locked", formatDuration(remaining)))
	return true
}

// logRefusal logs message, a refused sign-in step or second-factor change,
// as an auth event at level for user.
func (h *AuthHandler) logRefusal(r *http.Request, user store.User, level, message string) {
	_ = h.eventService.LogAuthEvent(r.Context(), level, message, &user.ID, hcaptcha.GetRemoteIP(r), middleware.GetRequestURL(r), map[string]any{"email": user.Email})
}

// recordWrongCode logs message for a wrong second-factor code and counts it
// against the account lockout. It returns the lockout duration when it
// locked the account, or 0.
func (h *AuthHandler) recordWrongCode(r *http.Request, user store.User, message string) time.Duration {
	h.logRefusal(r, user, model.EventLevelWarning, message)
	return countFailedAttempt(r, h.loginProtection, h.eventService, user)
}

// rejectSignInCode answers a second-factor code the pending sign-in could
// not use. Wrong codes count against the lockout and end the pending
// sign-in after mfaMaxAttempts.
func (h *AuthHandler) rejectSignInCode(w http.ResponseWriter, r *http.Request, user store.User, err error) {
	lang := middleware.GetAdminLang(r)
	switch {
	case errors.Is(err, mfa.ErrUnavailable):
		slog.Error("authenticator codes cannot be verified", "error", err, "user_id", user.ID)
		flashError(w, r, h.renderer, RouteLoginMFA, i18n.T(lang, "mfa.totp_unavailable"))
		return
	case !errors.Is(err, mfa.ErrInvalidCode) && !errors.Is(err, mfa.ErrNotEnrolled):
		logAndInternalError(w, "failed to verify two-factor code", "error", err, "user_id", user.ID)
		return
	}

	ctx := r.Context()
	if lockDuration := h.recordWrongCode(r, user, "Login failed: invalid two-factor code"); lockDuration > 0 {
		h.clearPendingSignIn(ctx)
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.too_many_attempts", formatDuration(lockDuration)))
		return
	}
	attempts := h.sessionManager.GetInt(ctx, sessionKeyMFAPendingTries) + 1
	if attempts >= mfaMaxAttempts {
		h.clearPendingSignIn(ctx)
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "mfa.too_many_codes"))
		return
	}
	h.sessionManager.Put(ctx, sessionKeyMFAPendingTries, attempts)
	flashError(w, r, h.renderer, RouteLoginMFA, i18n.T(lang, "mfa.invalid_code"))
}

// beginEnrollment returns the authenticator setup for user, reusing the
// secret this session is already enrolling.
func (h *AuthHandler) beginEnrollment(r *http.Request, user store.User) (mfa.Enrollment, error) {
	ctx := r.Context()
	pending := h.sessionManager.GetBytes(ctx, sessionKeyMFASetupSecret)
	enrollment, err := h.mfa.BeginEnrollment(user.ID, pending, middleware.GetSiteName(r), user.Email)
	if err != nil {
		return mfa.Enrollment{}, err
	}
	h.sessionManager.Put(ctx, sessionKeyMFASetupSecret, enrollment.Sealed)
	return enrollment, nil
}

// totpSetup is the view of an enrollment.
func totpSetup(e mfa.Enrollment, action string) adminviews.TOTPSetup {
	return adminviews.TOTPSetup{QRCode: e.QRCode, Secret: mfa.GroupSecret(e.Secret), Action: action}
}

// confirmEnrollment enrolls the authenticator this session set up. checkedAt
// is when CheckEnrollmentCode accepted the code, or zero to check it now. It
// answers a wrong or expired code itself, redirecting to back, and reports
// false when it did.
func (h *AuthHandler) confirmEnrollment(w http.ResponseWriter, r *http.Request, user store.User, back string, checkedAt time.Time) (mfa.Enrolled, bool) {
	ctx := r.Context()
	pending, code := h.sessionManager.GetBytes(ctx, sessionKeyMFASetupSecret), r.FormValue("code")
	var enrolled mfa.Enrolled
	var err error
	if checkedAt.IsZero() {
		enrolled, err = h.mfa.ConfirmEnrollment(ctx, user.ID, pending, code)
	} else {
		enrolled, err = h.mfa.ConfirmEnrollmentAt(ctx, user.ID, pending, code, checkedAt)
	}
	if err != nil {
		h.refuseEnrollmentCode(w, r, user, err, back)
		return mfa.Enrolled{}, false
	}
	h.sessionManager.Remove(ctx, sessionKeyMFASetupSecret)
	slog.Info("two-factor authentication enabled", "user_id", user.ID)
	_ = h.eventService.LogAuthEvent(ctx, model.EventLevelInfo, "Two-factor authentication enabled", &user.ID, hcaptcha.GetRemoteIP(r), middleware.GetRequestURL(r), map[string]any{"email": user.Email})
	return enrolled, true
}

// refuseEnrollmentCode answers a code that did not confirm the authenticator
// this session set up, redirecting to back.
func (h *AuthHandler) refuseEnrollmentCode(w http.ResponseWriter, r *http.Request, user store.User, err error, back string) {
	lang := middleware.GetAdminLang(r)
	switch {
	case errors.Is(err, mfa.ErrInvalidCode):
		flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.invalid_code"))
	case errors.Is(err, mfa.ErrNoEnrollment):
		flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.setup_expired"))
	default:
		logAndInternalError(w, "failed to enroll an authenticator", "error", err, "user_id", user.ID)
	}
}

// LoginMFASetupForm handles GET /login/mfa/setup - enrolls an authenticator
// for an account that must use one and has none yet.
func (h *AuthHandler) LoginMFASetupForm(w http.ResponseWriter, r *http.Request) {
	pending, ok := h.requirePendingSignIn(w, r, true)
	if !ok {
		return
	}
	enrollment, err := h.beginEnrollment(r, pending.user)
	if err != nil {
		logAndInternalError(w, "failed to start authenticator setup", "error", err, "user_id", pending.user.ID)
		return
	}
	setNoStore(w)
	renderTempl(w, r, adminviews.MFASetupPage(h.authPageData(r, "mfa.setup_title"), totpSetup(enrollment, RouteLoginMFASetup)))
}

// LoginMFASetup handles POST /login/mfa/setup - confirms the new
// authenticator, signs in and shows the recovery codes once.
func (h *AuthHandler) LoginMFASetup(w http.ResponseWriter, r *http.Request) {
	pending, ok := h.requirePendingSignIn(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, RouteLoginMFASetup, i18n.T(middleware.GetAdminLang(r), "auth.invalid_form_data"))
		return
	}
	enrolled, ok := h.confirmEnrollment(w, r, pending.user, RouteLoginMFASetup, time.Time{})
	if !ok {
		return
	}
	user := pending.user
	user.SessionVersion = enrolled.SessionVersion
	if err := h.grantSession(r, user, mfa.MethodTOTP); err != nil {
		logAndInternalError(w, "session renewal error", "error", err)
		return
	}
	setNoStore(w)
	renderTempl(w, r, adminviews.RecoveryCodesPage(h.authPageData(r, "mfa.recovery_codes_title"), enrolled.RecoveryCodes, loginDestination(user.Role)))
}

// accountPageContext is the page context of the account pages.
func (h *AuthHandler) accountPageContext(r *http.Request, user *store.User, titleKey string) *adminviews.PageContext {
	lang := middleware.GetAdminLang(r)
	pc := &adminviews.PageContext{
		Title:         i18n.T(lang, titleKey),
		AccountLayout: true,
		AdminLang:     lang,
		SiteName:      middleware.GetSiteName(r),
		CurrentPath:   r.URL.Path,
		User:          adminviews.UserInfo{Name: user.Name, Email: user.Email},
		Flash:         h.sessionManager.PopString(r.Context(), "flash"),
		FlashType:     h.sessionManager.PopString(r.Context(), "flash_type"),
	}
	if pc.FlashType == "" {
		pc.FlashType = "info"
	}
	pc.LangOptions = adminLangOptions(h.renderer)
	return pc
}

// accountUser returns the signed-in user of an account page, or answers the
// request itself and returns nil. Auth and LoadUser must run first.
func accountUser(w http.ResponseWriter, r *http.Request) *store.User {
	setNoStore(w)
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, redirectLogin, http.StatusSeeOther)
		return nil
	}
	// Unknown roles share the public permission level, so require a known role.
	if !isValidRole(user.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}
	return user
}

// Security handles GET /account/security - the two-factor overview.
func (h *AuthHandler) Security(w http.ResponseWriter, r *http.Request) {
	user := accountUser(w, r)
	if user == nil {
		return
	}
	status, err := h.mfa.Status(r.Context(), user.ID)
	if err != nil {
		logAndInternalError(w, "failed to load two-factor status", "error", err, "user_id", user.ID)
		return
	}
	pc := h.accountPageContext(r, user, "mfa.title")
	data := accountviews.SecurityData{
		Enrolled:          status.Enrolled,
		RecoveryCodesLeft: status.RecoveryCodesLeft,
		Available:         h.mfa.Available(),
		Required:          h.mfa.Required(),
		DemoMode:          middleware.IsDemoMode(),
	}
	if status.Enrolled {
		data.EnrolledAt = h.renderer.FormatDateTimeLocale(status.EnrolledAt, pc.AdminLang)
	}
	renderTempl(w, r, accountviews.SecurityPage(pc, data))
}

// TOTPSetupForm handles GET /account/security/totp - enrolls an
// authenticator app or replaces the enrolled one.
func (h *AuthHandler) TOTPSetupForm(w http.ResponseWriter, r *http.Request) {
	user := accountUser(w, r)
	if user == nil || h.refuseTOTPSetup(w, r) {
		return
	}
	enrolled, err := h.mfa.Enrolled(r.Context(), user.ID)
	if err != nil {
		logAndInternalError(w, "failed to load two-factor status", "error", err, "user_id", user.ID)
		return
	}
	enrollment, err := h.beginEnrollment(r, *user)
	if err != nil {
		logAndInternalError(w, "failed to start authenticator setup", "error", err, "user_id", user.ID)
		return
	}
	setup := totpSetup(enrollment, RouteAccountSecurityTOTP)
	setup.RequireCurrentCode = enrolled
	setup.RequirePassword = !enrolled
	setup.CancelURL = RouteAccountSecurity
	renderTempl(w, r, accountviews.TOTPSetupPage(h.accountPageContext(r, user, "mfa.setup_title"), setup))
}

// TOTPSetup handles POST /account/security/totp - confirms the new
// authenticator and shows the recovery codes once. Replacing an enrolled
// authenticator first needs a code from it, or a recovery code.
func (h *AuthHandler) TOTPSetup(w http.ResponseWriter, r *http.Request) {
	user := accountUser(w, r)
	if user == nil || h.refuseTOTPSetup(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, RouteAccountSecurityTOTP, i18n.T(middleware.GetAdminLang(r), "auth.invalid_form_data"))
		return
	}
	enrolled, err := h.mfa.Enrolled(r.Context(), user.ID)
	if err != nil {
		logAndInternalError(w, "failed to load two-factor status", "error", err, "user_id", user.ID)
		return
	}
	var checkedAt time.Time
	if !enrolled && !h.verifyPassword(w, r, *user, RouteAccountSecurityTOTP) {
		return
	}
	if enrolled {
		// Check the new code before the current one is spent, so a mistake
		// in it does not use up a recovery code or this period's code, and
		// confirm it at the same moment, so it cannot expire in between.
		pending := h.sessionManager.GetBytes(r.Context(), sessionKeyMFASetupSecret)
		if checkedAt, err = h.mfa.CheckEnrollmentCode(user.ID, pending, r.FormValue("code")); err != nil {
			h.refuseEnrollmentCode(w, r, *user, err, RouteAccountSecurityTOTP)
			return
		}
		if !h.verifyCurrentCode(w, r, *user, RouteAccountSecurityTOTP) {
			return
		}
	}
	result, ok := h.confirmEnrollment(w, r, *user, RouteAccountSecurityTOTP, checkedAt)
	if !ok {
		return
	}
	if err := h.adoptSessionVersion(r.Context(), result.SessionVersion, true); err != nil {
		logAndInternalError(w, "session renewal error", "error", err)
		return
	}
	renderTempl(w, r, accountviews.RecoveryCodesPage(h.accountPageContext(r, user, "mfa.recovery_codes_title"), result.RecoveryCodes))
}

// refuseTOTPSetup answers a setup request the site cannot serve and reports
// true: in demo mode, or without OCMS_MFA_KEY.
func (h *AuthHandler) refuseTOTPSetup(w http.ResponseWriter, r *http.Request) bool {
	if demoGuard(w, r, h.renderer, middleware.RestrictionTwoFactor, RouteAccountSecurity) {
		return true
	}
	if !h.mfa.Available() {
		flashError(w, r, h.renderer, RouteAccountSecurity, i18n.T(middleware.GetAdminLang(r), "mfa.not_configured"))
		return true
	}
	return false
}

// RegenerateRecoveryCodes handles POST /account/security/recovery-codes -
// replaces the recovery codes after a current code confirms it.
func (h *AuthHandler) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user := accountUser(w, r)
	if user == nil || demoGuard(w, r, h.renderer, middleware.RestrictionTwoFactor, RouteAccountSecurity) {
		return
	}
	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, RouteAccountSecurity, i18n.T(middleware.GetAdminLang(r), "auth.invalid_form_data"))
		return
	}
	if !h.verifyCurrentCode(w, r, *user, RouteAccountSecurity) {
		return
	}
	codes, err := h.mfa.RegenerateRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		logAndInternalError(w, "failed to regenerate recovery codes", "error", err, "user_id", user.ID)
		return
	}
	_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelInfo, "Recovery codes regenerated", &user.ID, hcaptcha.GetRemoteIP(r), middleware.GetRequestURL(r), map[string]any{"email": user.Email})
	renderTempl(w, r, accountviews.RecoveryCodesPage(h.accountPageContext(r, user, "mfa.recovery_codes_title"), codes))
}

// DisableTOTP handles POST /account/security/disable - turns two-factor
// authentication off after a current code confirms it. A site that requires
// a second factor does not allow it.
func (h *AuthHandler) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	user := accountUser(w, r)
	if user == nil || demoGuard(w, r, h.renderer, middleware.RestrictionTwoFactor, RouteAccountSecurity) {
		return
	}
	lang := middleware.GetAdminLang(r)
	if h.mfa.Required() {
		flashError(w, r, h.renderer, RouteAccountSecurity, i18n.T(lang, "mfa.required_note"))
		return
	}
	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, RouteAccountSecurity, i18n.T(lang, "auth.invalid_form_data"))
		return
	}
	if !h.verifyCurrentCode(w, r, *user, RouteAccountSecurity) {
		return
	}
	version, err := h.mfa.Disable(r.Context(), user.ID)
	if err != nil {
		logAndInternalError(w, "failed to disable two-factor authentication", "error", err, "user_id", user.ID)
		return
	}
	// Record the change before the session update, which can still fail.
	slog.Info("two-factor authentication disabled", "user_id", user.ID)
	_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Two-factor authentication disabled", &user.ID, hcaptcha.GetRemoteIP(r), middleware.GetRequestURL(r), map[string]any{"email": user.Email})
	if err := h.adoptSessionVersion(r.Context(), version, false); err != nil {
		logAndInternalError(w, "session renewal error", "error", err)
		return
	}
	flashAndRedirect(w, r, h.renderer, RouteAccountSecurity, i18n.T(lang, "mfa.disabled_done"), "success")
}

// verifyPassword checks the password a signed-in user gives before adding a
// first authenticator, so a stolen session cannot enroll one of its own and
// lock the owner out. Wrong passwords count against the login lockout. It
// answers the request itself and reports false on failure.
func (h *AuthHandler) verifyPassword(w http.ResponseWriter, r *http.Request, user store.User, back string) bool {
	lang := middleware.GetAdminLang(r)
	c := confirmPassword(r, h.loginProtection, h.eventService, user, r.FormValue("password"), func() {
		h.logRefusal(r, user, model.EventLevelWarning, "Two-factor change refused: invalid password")
	})
	switch c.Outcome {
	case passwordOK:
		return true
	case passwordLocked:
		h.logRefusal(r, user, model.EventLevelWarning, "Two-factor change refused: account locked")
		flashError(w, r, h.renderer, back, i18n.T(lang, "auth.account_locked", formatDuration(c.LockedFor)))
	case passwordMissing:
		// A form from before the account's status changed has no password
		// field: ask again, without counting it.
		h.logRefusal(r, user, model.EventLevelWarning, "Two-factor change refused: no password")
		flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.password_required"))
	case passwordUnverified:
		h.logRefusal(r, user, model.EventLevelError, "Two-factor change refused: password could not be verified")
		logAndInternalError(w, "password check error", "error", c.Err, "user_id", user.ID)
	default:
		if c.LockedFor > 0 {
			flashError(w, r, h.renderer, back, i18n.T(lang, "auth.too_many_attempts", formatDuration(c.LockedFor)))
		} else {
			flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.invalid_password"))
		}
	}
	return false
}

// verifyCurrentCode checks the current_code a signed-in user gives to confirm
// a change to their second factor: an authenticator code or a recovery code.
// Wrong codes count against the login lockout, so a stolen session cannot
// guess one. It answers the request itself and reports false on failure.
func (h *AuthHandler) verifyCurrentCode(w http.ResponseWriter, r *http.Request, user store.User, back string) bool {
	if h.refuseLockedAccount(w, r, user, "Two-factor change refused: account locked", nil, back) {
		return false
	}
	lang := middleware.GetAdminLang(r)
	_, err := h.mfa.Verify(r.Context(), user.ID, r.FormValue("current_code"))
	switch {
	case err == nil:
		return true
	case errors.Is(err, mfa.ErrUnavailable):
		slog.Error("authenticator codes cannot be verified", "error", err, "user_id", user.ID)
		flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.totp_unavailable"))
	case errors.Is(err, mfa.ErrInvalidCode), errors.Is(err, mfa.ErrNotEnrolled):
		if lockDuration := h.recordWrongCode(r, user, "Two-factor change refused: invalid code"); lockDuration > 0 {
			flashError(w, r, h.renderer, back, i18n.T(lang, "auth.too_many_attempts", formatDuration(lockDuration)))
			return false
		}
		flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.invalid_code"))
	default:
		logAndInternalError(w, "failed to verify two-factor code", "error", err, "user_id", user.ID)
	}
	return false
}

// setNoStore keeps a response out of every cache: the two-factor pages show
// secrets and one-time codes.
func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}
