// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// passwordOutcome is the result of confirmPassword.
type passwordOutcome int

const (
	// passwordOK means the password is the user's current one.
	passwordOK passwordOutcome = iota
	// passwordMissing means none was given. It is not counted as a wrong
	// password: a form from before the field was needed has none.
	passwordMissing
	// passwordWrong means a wrong password, counted toward the lockout.
	passwordWrong
	// passwordLocked means the account was locked before the check.
	passwordLocked
	// passwordUnverified means the stored hash could not be checked.
	passwordUnverified
)

// passwordConfirmation is what confirmPassword found.
type passwordConfirmation struct {
	Outcome passwordOutcome
	// LockedFor is how long the account stays locked: the time left for
	// passwordLocked, or the new lockout when a passwordWrong locked it.
	LockedFor time.Duration
	// Err is the hash check's error for passwordUnverified.
	Err error
}

// confirmPassword checks the current password a signed-in user gives to
// confirm a sensitive change, under the login lockout, so a stolen session
// cannot guess it any faster than at the login form. A locked account is
// refused before the check; a wrong password counts as a failed sign-in, and
// the lock it causes is logged; the right one clears the counters like a
// sign-in. Callers log their own refusal. lp and events may be nil (tests).
func confirmPassword(r *http.Request, lp *middleware.LoginProtection, events *service.EventService, user store.User, password string) passwordConfirmation {
	clientIP := middleware.GetClientIP(r)
	if lp != nil {
		if locked, remaining := lp.IsAccountLocked(user.Email, clientIP); locked {
			return passwordConfirmation{Outcome: passwordLocked, LockedFor: remaining}
		}
	}
	if password == "" {
		return passwordConfirmation{Outcome: passwordMissing}
	}
	valid, err := auth.CheckPassword(password, user.PasswordHash)
	switch {
	case err != nil:
		return passwordConfirmation{Outcome: passwordUnverified, Err: err}
	case valid:
		if lp != nil {
			lp.RecordSuccessfulLogin(user.Email, clientIP)
		}
		return passwordConfirmation{Outcome: passwordOK}
	}
	return passwordConfirmation{Outcome: passwordWrong, LockedFor: countFailedAttempt(r, lp, events, user)}
}

// countFailedAttempt counts a wrong password or second-factor code user gave
// toward the login lockout and logs the lock when it locks the account. It
// returns the lockout duration, or 0 when the account is not locked.
func countFailedAttempt(r *http.Request, lp *middleware.LoginProtection, events *service.EventService, user store.User) time.Duration {
	if lp == nil {
		return 0
	}
	clientIP := middleware.GetClientIP(r)
	locked, lockDuration := lp.RecordFailedAttempt(user.Email, clientIP)
	if !locked {
		return 0
	}
	if events != nil {
		_ = events.LogAuthEvent(r.Context(), model.EventLevelWarning, "Account locked due to failed attempts", &user.ID, clientIP, middleware.GetRequestURL(r), map[string]any{"email": user.Email, "duration": lockDuration.String()})
	}
	return lockDuration
}
