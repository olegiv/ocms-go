// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/mfa"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

const mfaTestPassword = "Mfa-test-password!"

// mfaTestEnv is an auth handler wired to the two-factor routes the way
// cmd/ocms registers them.
type mfaTestEnv struct {
	h      *AuthHandler
	db     *sql.DB
	sm     *scs.SessionManager
	svc    *mfa.Service
	router http.Handler
}

func newMFATestEnv(t *testing.T, required bool) *mfaTestEnv {
	t.Helper()
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	middleware.SetSessionManager(sm)
	middleware.SetMFARequired(required)
	t.Cleanup(func() {
		middleware.SetSessionManager(nil)
		middleware.SetMFARequired(false)
	})
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := mfa.NewService(db, testMFAKey, required)
	if err != nil {
		t.Fatal(err)
	}
	env := &mfaTestEnv{h: NewAuthHandler(db, renderer, sm, nil, nil, svc), db: db, sm: sm, svc: svc}

	r := chi.NewRouter()
	r.Use(sm.LoadAndSave)
	r.Use(middleware.CSRF(middleware.DefaultCSRFConfig([]byte("test-secret-key-32-bytes-long!!!"), true)))
	r.Post(RouteLogin, env.h.Login)
	r.Post(RouteLogout, env.h.Logout)
	r.Get(RouteLoginMFA, env.h.LoginMFAForm)
	r.Post(RouteLoginMFA, env.h.LoginMFA)
	r.Get(RouteLoginMFASetup, env.h.LoginMFASetupForm)
	r.Post(RouteLoginMFASetup, env.h.LoginMFASetup)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(sm), middleware.LoadUser(sm, db), middleware.LoadSiteConfig(db, nil))
		r.Get(RouteAccountSecurity, env.h.Security)
		r.Get(RouteAccountSecurityTOTP, env.h.TOTPSetupForm)
		r.Post(RouteAccountSecurityTOTP, env.h.TOTPSetup)
		r.Post(RouteAccountSecurityRecoveryCodes, env.h.RegenerateRecoveryCodes)
		r.Post(RouteAccountSecurityDisable, env.h.DisableTOTP)
		r.With(middleware.RequireEditor()).Get("/admin", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	env.router = r
	return env
}

func (env *mfaTestEnv) createUser(t *testing.T, role string) store.User {
	t.Helper()
	hash, err := auth.HashPassword(mfaTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	return createTestUser(t, env.db, testUser{Email: role + "@example.com", Name: "MFA " + role, Role: role, PasswordHash: hash})
}

// enroll gives user an authenticator through the service and returns its
// secret and recovery codes. It spends the previous period's code, so the
// current and next period's codes remain for the test.
func (env *mfaTestEnv) enroll(t *testing.T, user store.User) (string, []string) {
	t.Helper()
	e, err := env.svc.BeginEnrollment(user.ID, nil, "Test", user.Email)
	if err != nil {
		t.Fatal(err)
	}
	got, err := env.svc.ConfirmEnrollment(t.Context(), user.ID, e.Sealed, totpCode(t, e.Secret, -1))
	if err != nil {
		t.Fatal(err)
	}
	return e.Secret, got.RecoveryCodes
}

// totpCode is secret's code periods 30-second periods from now.
func totpCode(t *testing.T, secret string, periods int) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, time.Now().Add(time.Duration(periods)*30*time.Second),
		totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// wrongCode is a six-digit code that differs from secret's current codes.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	for _, candidate := range []string{"000000", "111111", "222222", "333333"} {
		if candidate != totpCode(t, secret, -1) && candidate != totpCode(t, secret, 0) && candidate != totpCode(t, secret, 1) {
			return candidate
		}
	}
	t.Fatal("no wrong code found")
	return ""
}

// signInWithPassword posts the password and returns the response and its
// session cookie.
func (env *mfaTestEnv) signInWithPassword(t *testing.T, user store.User) (*http.Cookie, string) {
	t.Helper()
	rr := authRequest(env.router, http.MethodPost, RouteLogin, url.Values{"email": {user.Email}, "password": {mfaTestPassword}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("password step = %d", rr.Code)
	}
	return sessionCookie(t, rr, env.sm), rr.Header().Get("Location")
}

// sessionValue reads key from the session behind cookie.
func (env *mfaTestEnv) session(t *testing.T, cookie *http.Cookie) func(key string) any {
	t.Helper()
	ctx, err := env.sm.Load(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	return func(key string) any { return env.sm.Get(ctx, key) }
}

func TestLoginWithAuthenticatorNeedsSecondFactor(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RoleEditor)
	secret, _ := env.enroll(t, user)

	cookie, next := env.signInWithPassword(t, user)
	if next != RouteLoginMFA {
		t.Fatalf("password step redirected to %q, want %s", next, RouteLoginMFA)
	}
	if got := env.session(t, cookie)(middleware.SessionKeyUserID); got != nil {
		t.Fatalf("password alone put user %v in the session", got)
	}
	if rr := authRequest(env.router, http.MethodGet, "/admin", nil, cookie); rr.Header().Get("Location") != RouteLogin {
		t.Fatalf("pending sign-in reached /admin: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if rr := authRequest(env.router, http.MethodGet, RouteLoginMFA, nil, cookie); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `name="code"`) {
		t.Fatalf("challenge page = %d", rr.Code)
	}

	rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {wrongCode(t, secret)}}, cookie)
	if rr.Header().Get("Location") != RouteLoginMFA {
		t.Fatalf("wrong code = %d %q, want a redirect back to the challenge", rr.Code, rr.Header().Get("Location"))
	}
	if got := env.session(t, cookie)(middleware.SessionKeyUserID); got != nil {
		t.Fatal("a wrong code signed in")
	}

	rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/admin" {
		t.Fatalf("right code = %d %q, want 303 /admin", rr.Code, rr.Header().Get("Location"))
	}
	signedIn := sessionCookie(t, rr, env.sm)
	if signedIn.Value == cookie.Value {
		t.Error("the second factor did not rotate the session token")
	}
	get := env.session(t, signedIn)
	if get(middleware.SessionKeyUserID) != user.ID || get(middleware.SessionKeyMFAVerified) != true || get(sessionKeyMFAPendingUserID) != nil {
		t.Errorf("session after sign-in: user %v, mfa %v, pending %v", get(middleware.SessionKeyUserID), get(middleware.SessionKeyMFAVerified), get(sessionKeyMFAPendingUserID))
	}
	if rr := authRequest(env.router, http.MethodGet, "/admin", nil, signedIn); rr.Code != http.StatusNoContent {
		t.Errorf("/admin after sign-in = %d", rr.Code)
	}

	// Going Back to the code page after signing in returns to the dashboard
	// without a false "sign-in expired" message.
	rr = authRequest(env.router, http.MethodGet, RouteLoginMFA, nil, signedIn)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/admin" {
		t.Errorf("code page after sign-in = %d %q, want 303 /admin", rr.Code, rr.Header().Get("Location"))
	}
	if flash := env.session(t, signedIn)("flash"); flash == i18n.T("en", "mfa.sign_in_expired") {
		t.Errorf("code page after sign-in set flash %v", flash)
	}
}

func TestLoginWithRecoveryCode(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RolePublic)
	_, codes := env.enroll(t, user)
	cookie, _ := env.signInWithPassword(t, user)

	rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {strings.ToUpper(codes[0])}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != RouteAccount {
		t.Fatalf("recovery code = %d %q, want 303 %s", rr.Code, rr.Header().Get("Location"), RouteAccount)
	}
	get := env.session(t, sessionCookie(t, rr, env.sm))
	if get(middleware.SessionKeyUserID) != user.ID || get("flash_type") != "warning" {
		t.Errorf("session after recovery sign-in: user %v, flash %v", get(middleware.SessionKeyUserID), get("flash_type"))
	}

	cookie, _ = env.signInWithPassword(t, user)
	rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {codes[0]}}, cookie)
	if rr.Header().Get("Location") != RouteLoginMFA {
		t.Errorf("reused recovery code = %d %q, want it refused", rr.Code, rr.Header().Get("Location"))
	}
}

func TestPendingSignInEnds(t *testing.T) {
	t.Run("after too many wrong codes", func(t *testing.T) {
		env := newMFATestEnv(t, false)
		user := env.createUser(t, model.RoleEditor)
		secret, _ := env.enroll(t, user)
		cookie, _ := env.signInWithPassword(t, user)
		var rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {wrongCode(t, secret)}}, cookie)
		for i := 1; i < mfaMaxAttempts; i++ {
			rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {wrongCode(t, secret)}}, cookie)
		}
		if rr.Header().Get("Location") != RouteLogin {
			t.Fatalf("attempt %d = %q, want the sign-in to end", mfaMaxAttempts, rr.Header().Get("Location"))
		}
		rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
		if rr.Header().Get("Location") != RouteLogin {
			t.Errorf("right code after the sign-in ended = %q, want %s", rr.Header().Get("Location"), RouteLogin)
		}
	})
	t.Run("when the password changes", func(t *testing.T) {
		env := newMFATestEnv(t, false)
		user := env.createUser(t, model.RoleEditor)
		secret, _ := env.enroll(t, user)
		cookie, _ := env.signInWithPassword(t, user)
		if _, err := env.db.Exec(`UPDATE users SET session_version = session_version + 1 WHERE id = ?`, user.ID); err != nil {
			t.Fatal(err)
		}
		rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
		if rr.Header().Get("Location") != RouteLogin {
			t.Errorf("code after a credential change = %q, want %s", rr.Header().Get("Location"), RouteLogin)
		}
	})
	t.Run("after the time limit", func(t *testing.T) {
		env := newMFATestEnv(t, false)
		user := env.createUser(t, model.RoleEditor)
		env.enroll(t, user)
		cookie, _ := env.signInWithPassword(t, user)
		ctx, err := env.sm.Load(t.Context(), cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		env.sm.Put(ctx, sessionKeyMFAPendingSince, time.Now().Add(-mfaPendingTTL-time.Minute).Unix())
		token, _, err := env.sm.Commit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rr := authRequest(env.router, http.MethodGet, RouteLoginMFA, nil, &http.Cookie{Name: env.sm.Cookie.Name, Value: token})
		if rr.Header().Get("Location") != RouteLogin {
			t.Errorf("expired challenge = %d %q, want %s", rr.Code, rr.Header().Get("Location"), RouteLogin)
		}
	})
}

var setupSecret = regexp.MustCompile(`<code>([A-Z2-7 ]{16,})</code>`)

// TestRequiredMFAEnrollsAtSignIn covers an account that must use a second
// factor and has none: the password leads to setup, never to a session, and
// confirming a code signs in and shows the recovery codes.
func TestRequiredMFAEnrollsAtSignIn(t *testing.T) {
	env := newMFATestEnv(t, true)
	user := env.createUser(t, model.RoleAdmin)

	cookie, next := env.signInWithPassword(t, user)
	if next != RouteLoginMFASetup {
		t.Fatalf("password step redirected to %q, want %s", next, RouteLoginMFASetup)
	}
	if rr := authRequest(env.router, http.MethodGet, RouteLoginMFA, nil, cookie); rr.Header().Get("Location") != RouteLogin {
		t.Errorf("the challenge served an enrollment sign-in: %d", rr.Code)
	}
	page := authRequest(env.router, http.MethodGet, RouteLoginMFASetup, nil, cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "data:image/png;base64,") {
		t.Fatalf("setup page = %d", page.Code)
	}
	if page.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("setup page Cache-Control = %q", page.Header().Get("Cache-Control"))
	}
	m := setupSecret.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("setup page shows no secret")
	}
	secret := strings.ReplaceAll(m[1], " ", "")
	if again := authRequest(env.router, http.MethodGet, RouteLoginMFASetup, nil, cookie); !strings.Contains(again.Body.String(), m[1]) {
		t.Error("reloading the setup page changed the secret")
	}

	rr := authRequest(env.router, http.MethodPost, RouteLoginMFASetup, url.Values{"code": {wrongCode(t, secret)}}, cookie)
	if rr.Header().Get("Location") != RouteLoginMFASetup {
		t.Fatalf("wrong setup code = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	rr = authRequest(env.router, http.MethodPost, RouteLoginMFASetup, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
	if rr.Code != http.StatusOK || strings.Count(rr.Body.String(), "<li><code>") != mfa.RecoveryCodeCount {
		t.Fatalf("setup confirmation = %d, want the recovery codes page", rr.Code)
	}
	signedIn := sessionCookie(t, rr, env.sm)
	if get := env.session(t, signedIn); get(middleware.SessionKeyUserID) != user.ID || get(middleware.SessionKeyMFAVerified) != true {
		t.Fatalf("session after enrollment: user %v, mfa %v", get(middleware.SessionKeyUserID), get(middleware.SessionKeyMFAVerified))
	}
	if rr := authRequest(env.router, http.MethodGet, "/admin", nil, signedIn); rr.Code != http.StatusNoContent {
		t.Errorf("/admin after enrollment = %d", rr.Code)
	}
	if enrolled, err := env.svc.Enrolled(t.Context(), user.ID); err != nil || !enrolled {
		t.Errorf("Enrolled = %t, %v", enrolled, err)
	}
}

// TestRequiredMFAEndsSessionsWithoutIt checks that a session signed in with
// the password alone, from before the requirement was switched on, is
// signed out.
//
// Bug state: drop the mfaRequired check from middleware.isSessionStale and
// this fails.
func TestRequiredMFAEndsSessionsWithoutIt(t *testing.T) {
	env := newMFATestEnv(t, true)
	user := env.createUser(t, model.RoleEditor)
	ctx, err := env.sm.Load(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	env.sm.Put(ctx, middleware.SessionKeyUserID, user.ID)
	token, _, err := env.sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rr := authRequest(env.router, http.MethodGet, "/admin", nil, &http.Cookie{Name: env.sm.Cookie.Name, Value: token})
	if rr.Header().Get("Location") != RouteLogin {
		t.Errorf("password-only session under the requirement = %d %q, want a redirect to %s", rr.Code, rr.Header().Get("Location"), RouteLogin)
	}
}

// signedInCookie signs user in with the password and their next TOTP code.
func (env *mfaTestEnv) signedInCookie(t *testing.T, user store.User, secret string) *http.Cookie {
	t.Helper()
	cookie, _ := env.signInWithPassword(t, user)
	rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") == RouteLoginMFA {
		t.Fatalf("sign-in = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	return sessionCookie(t, rr, env.sm)
}

func TestAccountSecurityChangesNeedCurrentCode(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RolePublic)
	secret, codes := env.enroll(t, user)
	cookie := env.signedInCookie(t, user, secret)

	if rr := authRequest(env.router, http.MethodGet, RouteAccountSecurity, nil, cookie); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), RouteAccountSecurityDisable) {
		t.Fatalf("security page = %d", rr.Code)
	}

	for _, action := range []string{RouteAccountSecurityRecoveryCodes, RouteAccountSecurityDisable} {
		rr := authRequest(env.router, http.MethodPost, action, url.Values{"current_code": {wrongCode(t, secret)}}, cookie)
		if rr.Header().Get("Location") != RouteAccountSecurity {
			t.Errorf("%s with a wrong code = %d %q", action, rr.Code, rr.Header().Get("Location"))
		}
	}
	if enrolled, err := env.svc.Enrolled(t.Context(), user.ID); err != nil || !enrolled {
		t.Fatalf("a wrong code disabled two-factor: %t, %v", enrolled, err)
	}

	rr := authRequest(env.router, http.MethodPost, RouteAccountSecurityRecoveryCodes, url.Values{"current_code": {codes[0]}}, cookie)
	if rr.Code != http.StatusOK || strings.Count(rr.Body.String(), "<li><code>") != mfa.RecoveryCodeCount {
		t.Fatalf("regenerate = %d, want the new codes", rr.Code)
	}

	rr = authRequest(env.router, http.MethodPost, RouteAccountSecurityDisable, url.Values{"current_code": {totpCode(t, secret, 1)}}, cookie)
	if rr.Header().Get("Location") != RouteAccountSecurity {
		t.Fatalf("disable = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if enrolled, err := env.svc.Enrolled(t.Context(), user.ID); err != nil || enrolled {
		t.Fatalf("Enrolled after disable = %t, %v", enrolled, err)
	}
	// Disabling bumped the session version; the session that did it stays.
	if rr := authRequest(env.router, http.MethodGet, RouteAccountSecurity, nil, sessionCookie(t, rr, env.sm)); rr.Code != http.StatusOK {
		t.Errorf("security page after disable = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	// The earlier session token was rotated away.
	if rr := authRequest(env.router, http.MethodGet, RouteAccountSecurity, nil, cookie); rr.Header().Get("Location") != RouteLogin {
		t.Errorf("pre-change session token still valid: %d", rr.Code)
	}
}

func TestAccountSecurityDisableRefusedWhenRequired(t *testing.T) {
	env := newMFATestEnv(t, true)
	user := env.createUser(t, model.RoleEditor)
	secret, _ := env.enroll(t, user)
	cookie := env.signedInCookie(t, user, secret)

	rr := authRequest(env.router, http.MethodPost, RouteAccountSecurityDisable, url.Values{"current_code": {totpCode(t, secret, 1)}}, cookie)
	if rr.Header().Get("Location") != RouteAccountSecurity {
		t.Fatalf("disable = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if enrolled, err := env.svc.Enrolled(t.Context(), user.ID); err != nil || !enrolled {
		t.Errorf("two-factor was turned off although required: %t, %v", enrolled, err)
	}
}

func TestAccountEnrollsAuthenticator(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RoleEditor)
	cookie, next := env.signInWithPassword(t, user)
	if next != "/admin" {
		t.Fatalf("password-only sign-in redirected to %q", next)
	}
	page := authRequest(env.router, http.MethodGet, RouteAccountSecurityTOTP, nil, cookie)
	m := setupSecret.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || m == nil {
		t.Fatalf("setup page = %d", page.Code)
	}
	if strings.Contains(page.Body.String(), `name="current_code"`) {
		t.Error("a first enrollment asked for a current code")
	}
	secret := strings.ReplaceAll(m[1], " ", "")
	rr := authRequest(env.router, http.MethodPost, RouteAccountSecurityTOTP, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
	if rr.Code != http.StatusOK || strings.Count(rr.Body.String(), "<li><code>") != mfa.RecoveryCodeCount {
		t.Fatalf("enroll = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	get := env.session(t, sessionCookie(t, rr, env.sm))
	if get(middleware.SessionKeyMFAVerified) != true {
		t.Error("enrolling did not mark the session as having passed a second factor")
	}

	// Replacing the authenticator now needs a code from it.
	replace := authRequest(env.router, http.MethodGet, RouteAccountSecurityTOTP, nil, sessionCookie(t, rr, env.sm))
	if !strings.Contains(replace.Body.String(), `name="current_code"`) {
		t.Error("replacing an authenticator did not ask for a current code")
	}
}

func TestUsersHandlerResetMFA(t *testing.T) {
	env := newMFATestEnv(t, false)
	admin := env.createUser(t, model.RoleAdmin)
	editor := env.createUser(t, model.RoleEditor)
	env.enroll(t, editor)
	env.enroll(t, admin)
	h := NewUsersHandler(env.db, env.h.renderer, env.sm)
	h.SetMFAService(env.svc)

	request := func(method, target string, id int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req = requestWithURLParams(req, map[string]string{"id": strconv.FormatInt(id, 10)})
		req = requestWithSession(env.sm, req)
		req = addUserToContext(req, &admin)
		rr := httptest.NewRecorder()
		if method == http.MethodGet {
			h.EditForm(rr, req)
		} else {
			h.ResetMFA(rr, req)
		}
		return rr
	}

	if rr := request(http.MethodGet, "/admin/users/2", editor.ID); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/mfa/reset") {
		t.Fatalf("edit page = %d, want the reset action", rr.Code)
	}
	if rr := request(http.MethodGet, "/admin/users/1", admin.ID); strings.Contains(rr.Body.String(), "/mfa/reset") {
		t.Error("an administrator's own edit page offers the reset")
	}
	if rr := request(http.MethodPost, "/admin/users/1/mfa/reset", admin.ID); rr.Code != http.StatusSeeOther {
		t.Fatalf("self reset = %d", rr.Code)
	}
	if enrolled, err := env.svc.Enrolled(t.Context(), admin.ID); err != nil || !enrolled {
		t.Fatalf("an administrator reset their own second factor: %t, %v", enrolled, err)
	}

	rr := request(http.MethodPost, "/admin/users/2/mfa/reset", editor.ID)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != fmt.Sprintf(redirectAdminUsersID, editor.ID) {
		t.Fatalf("reset = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if enrolled, err := env.svc.Enrolled(t.Context(), editor.ID); err != nil || enrolled {
		t.Fatalf("Enrolled after reset = %t, %v", enrolled, err)
	}
	var version int64
	if err := env.db.QueryRow(`SELECT session_version FROM users WHERE id = ?`, editor.ID).Scan(&version); err != nil || version < 2 {
		t.Errorf("session_version after reset = %d, %v; want the editor signed out", version, err)
	}

	// A form that fails validation is shown again with the two-factor card.
	form := url.Values{"name": {""}, "email": {editor.Email}, "role": {model.RoleEditor}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/2", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = addUserToContext(requestWithSession(env.sm, requestWithURLParams(req, map[string]string{"id": strconv.FormatInt(editor.ID, 10)})), &admin)
	update := httptest.NewRecorder()
	h.Update(update, req)
	if update.Code != http.StatusOK || !strings.Contains(update.Body.String(), i18n.T("en", "users.mfa_not_enabled")) {
		t.Errorf("re-rendered edit form = %d, without the two-factor card", update.Code)
	}

	// A second reset finds nothing to remove and signs nobody out.
	if rr := request(http.MethodPost, "/admin/users/2/mfa/reset", editor.ID); rr.Code != http.StatusSeeOther {
		t.Fatalf("reset without a second factor = %d", rr.Code)
	}
	var again int64
	if err := env.db.QueryRow(`SELECT session_version FROM users WHERE id = ?`, editor.ID).Scan(&again); err != nil || again != version {
		t.Errorf("session_version after a no-op reset = %d, %v; want %d", again, err, version)
	}
}

// withLockout gives the environment's handler a login protection that locks
// an email + address pair after attempts failures.
func (env *mfaTestEnv) withLockout(t *testing.T, attempts int) *middleware.LoginProtection {
	t.Helper()
	lockoutDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	lp := middleware.NewLoginProtection(lockoutDB, middleware.LoginProtectionConfig{
		MaxFailedAttempts: attempts, LockoutDuration: time.Minute, AttemptWindow: time.Minute,
	})
	env.h.loginProtection = lp
	return lp
}

// TestLoginMFALockout checks that wrong second-factor codes count toward the
// account lockout, and that a locked account cannot finish a pending
// sign-in, even with the right code.
func TestLoginMFALockout(t *testing.T) {
	t.Run("wrong codes lock the account", func(t *testing.T) {
		env := newMFATestEnv(t, false)
		env.withLockout(t, 2)
		user := env.createUser(t, model.RoleEditor)
		secret, _ := env.enroll(t, user)
		cookie, _ := env.signInWithPassword(t, user)

		authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {wrongCode(t, secret)}}, cookie)
		rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {wrongCode(t, secret)}}, cookie)
		if rr.Header().Get("Location") != RouteLogin {
			t.Fatalf("locking code = %d %q, want the sign-in to end", rr.Code, rr.Header().Get("Location"))
		}
		rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
		if got := env.session(t, cookie)(middleware.SessionKeyUserID); got != nil || rr.Header().Get("Location") != RouteLogin {
			t.Errorf("right code after the lockout signed in: user %v, %q", got, rr.Header().Get("Location"))
		}
	})
	t.Run("a locked account cannot finish", func(t *testing.T) {
		env := newMFATestEnv(t, false)
		lp := env.withLockout(t, 2)
		user := env.createUser(t, model.RoleEditor)
		secret, _ := env.enroll(t, user)
		cookie, _ := env.signInWithPassword(t, user)
		for range 2 {
			lp.RecordFailedAttempt(user.Email, "192.0.2.1") // authRequest's RemoteAddr
		}
		rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
		if rr.Header().Get("Location") != RouteLogin {
			t.Fatalf("code on a locked account = %d %q, want %s", rr.Code, rr.Header().Get("Location"), RouteLogin)
		}
		get := env.session(t, cookie)
		if get(middleware.SessionKeyUserID) != nil || get(sessionKeyMFAPendingUserID) != nil {
			t.Errorf("locked account: user %v, pending %v; want neither", get(middleware.SessionKeyUserID), get(sessionKeyMFAPendingUserID))
		}
	})
}

// TestLoginMFAWithChangedKey covers OCMS_MFA_KEY being lost or rotated: an
// authenticator code cannot be checked, which the user is told and which
// does not count as a wrong code, and a recovery code still signs in.
func TestLoginMFAWithChangedKey(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RoleEditor)
	secret, codes := env.enroll(t, user)
	other, err := mfa.NewService(env.db, "Qm4Tx8Lc2Wv6Rn1Zp9Hb3Jd7Kf5Gs0Ye8Ua2Ni6Ow=", false)
	if err != nil {
		t.Fatal(err)
	}
	env.h.mfa = other

	cookie, _ := env.signInWithPassword(t, user)
	rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
	get := env.session(t, cookie)
	if rr.Header().Get("Location") != RouteLoginMFA || get("flash") != i18n.T("en", "mfa.totp_unavailable") {
		t.Fatalf("code under another key = %q, flash %v", rr.Header().Get("Location"), get("flash"))
	}
	if tries := get(sessionKeyMFAPendingTries); tries != 0 {
		t.Errorf("an uncheckable code counted as attempt %v", tries)
	}
	rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {codes[0]}}, cookie)
	if rr.Header().Get("Location") != "/admin" {
		t.Errorf("recovery code under another key = %d %q, want /admin", rr.Code, rr.Header().Get("Location"))
	}
}

func TestAccountReplacesAuthenticator(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RoleEditor)
	oldSecret, codes := env.enroll(t, user)
	cookie := env.signedInCookie(t, user, oldSecret)

	page := authRequest(env.router, http.MethodGet, RouteAccountSecurityTOTP, nil, cookie)
	m := setupSecret.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatalf("replace page = %d, shows no secret", page.Code)
	}
	newSecret := strings.ReplaceAll(m[1], " ", "")

	// A mistake in the new code must not spend the current code: codes[0]
	// still confirms the replacement below.
	// Bug state: verify current_code before checking the new code.
	rr := authRequest(env.router, http.MethodPost, RouteAccountSecurityTOTP,
		url.Values{"current_code": {codes[0]}, "code": {wrongCode(t, newSecret)}}, cookie)
	if rr.Header().Get("Location") != RouteAccountSecurityTOTP {
		t.Fatalf("replace with a wrong new code = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if status, err := env.svc.Status(t.Context(), user.ID); err != nil || status.RecoveryCodesLeft != mfa.RecoveryCodeCount {
		t.Fatalf("recovery codes left after a wrong new code = %d, %v; want %d, none spent", status.RecoveryCodesLeft, err, mfa.RecoveryCodeCount)
	}

	rr = authRequest(env.router, http.MethodPost, RouteAccountSecurityTOTP,
		url.Values{"current_code": {wrongCode(t, oldSecret)}, "code": {totpCode(t, newSecret, 0)}}, cookie)
	if rr.Header().Get("Location") != RouteAccountSecurityTOTP {
		t.Fatalf("replace with a wrong current code = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if _, err := env.svc.Verify(t.Context(), user.ID, totpCode(t, newSecret, 1)); !errors.Is(err, mfa.ErrInvalidCode) {
		t.Fatalf("the new authenticator was enrolled without the current code: %v", err)
	}

	rr = authRequest(env.router, http.MethodPost, RouteAccountSecurityTOTP,
		url.Values{"current_code": {codes[0]}, "code": {totpCode(t, newSecret, 0)}}, cookie)
	if rr.Code != http.StatusOK || strings.Count(rr.Body.String(), "<li><code>") != mfa.RecoveryCodeCount {
		t.Fatalf("replace = %d %q, want new recovery codes", rr.Code, rr.Header().Get("Location"))
	}
	if _, err := env.svc.Verify(t.Context(), user.ID, totpCode(t, oldSecret, 1)); !errors.Is(err, mfa.ErrInvalidCode) {
		t.Errorf("the replaced authenticator still verifies: %v", err)
	}
	if _, err := env.svc.Verify(t.Context(), user.ID, totpCode(t, newSecret, 1)); err != nil {
		t.Errorf("the new authenticator does not verify: %v", err)
	}
	if _, err := env.svc.Verify(t.Context(), user.ID, codes[1]); !errors.Is(err, mfa.ErrInvalidCode) {
		t.Errorf("an old recovery code survived the replacement: %v", err)
	}
}

func TestAccountSetupWithoutKey(t *testing.T) {
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RoleEditor)
	noKey, err := mfa.NewService(env.db, "", false)
	if err != nil {
		t.Fatal(err)
	}
	env.h.mfa = noKey
	cookie, _ := env.signInWithPassword(t, user)

	page := authRequest(env.router, http.MethodGet, RouteAccountSecurity, nil, cookie)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `href="`+RouteAccountSecurityTOTP) {
		t.Fatalf("security page without a key = %d, offers setup", page.Code)
	}
	if !strings.Contains(page.Body.String(), "OCMS_MFA_KEY") {
		t.Error("security page does not say the server lacks OCMS_MFA_KEY")
	}
	if rr := authRequest(env.router, http.MethodGet, RouteAccountSecurityTOTP, nil, cookie); rr.Header().Get("Location") != RouteAccountSecurity {
		t.Errorf("setup without a key = %d %q, want a redirect to %s", rr.Code, rr.Header().Get("Location"), RouteAccountSecurity)
	}
}

// mfaDemoChildMarker proves the demo-mode child process ran the scenario.
const mfaDemoChildMarker = "two-factor demo scenario verified"

// TestTwoFactorDemoMode checks that demo mode refuses every two-factor
// change: the seeded demo accounts are shared, so one visitor's
// authenticator would lock the others out.
func TestTwoFactorDemoMode(t *testing.T) {
	// Demo mode is cached globally; run this scenario in a fresh test process.
	if os.Getenv("OCMS_MFA_DEMO_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestTwoFactorDemoMode$", "-test.v")
		command.Env = append(os.Environ(), "OCMS_DEMO_MODE=true", "OCMS_MFA_DEMO_CHILD=1")
		out, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(out), mfaDemoChildMarker) {
			t.Fatalf("demo scenario: %v\n%s", err, out)
		}
		return
	}
	env := newMFATestEnv(t, false)
	user := env.createUser(t, model.RoleAdmin)
	cookie, _ := env.signInWithPassword(t, user)

	page := authRequest(env.router, http.MethodGet, RouteAccountSecurity, nil, cookie)
	if !strings.Contains(page.Body.String(), i18n.T("en", "mfa.demo_mode")) || strings.Contains(page.Body.String(), `href="`+RouteAccountSecurityTOTP) {
		t.Fatalf("demo security page = %d, missing the demo notice or offering setup", page.Code)
	}
	for _, rr := range []*httptest.ResponseRecorder{
		authRequest(env.router, http.MethodGet, RouteAccountSecurityTOTP, nil, cookie),
		authRequest(env.router, http.MethodPost, RouteAccountSecurityTOTP, url.Values{"code": {"123456"}}, cookie),
		authRequest(env.router, http.MethodPost, RouteAccountSecurityRecoveryCodes, url.Values{"current_code": {"123456"}}, cookie),
		authRequest(env.router, http.MethodPost, RouteAccountSecurityDisable, url.Values{"current_code": {"123456"}}, cookie),
	} {
		if rr.Header().Get("Location") != RouteAccountSecurity {
			t.Errorf("demo-mode change = %d %q, want a redirect to %s", rr.Code, rr.Header().Get("Location"), RouteAccountSecurity)
		}
	}

	h := NewUsersHandler(env.db, env.h.renderer, env.sm)
	h.SetMFAService(env.svc)
	req := requestWithURLParams(httptest.NewRequest(http.MethodPost, "/admin/users/1/mfa/reset", nil), map[string]string{"id": strconv.FormatInt(user.ID, 10)})
	rr := httptest.NewRecorder()
	h.ResetMFA(rr, addUserToContext(requestWithSession(env.sm, req), &user))
	if rr.Header().Get("Location") != redirectAdminUsers {
		t.Errorf("demo-mode reset = %d %q, want a redirect to %s", rr.Code, rr.Header().Get("Location"), redirectAdminUsers)
	}
	t.Log(mfaDemoChildMarker)
}
