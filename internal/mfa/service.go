// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mfa implements two-factor authentication: authenticator apps
// (TOTP, RFC 6238) and single-use recovery codes.
//
// TOTP secrets are sealed at rest with a key derived from OCMS_MFA_KEY.
// Without that key nobody can enroll an authenticator app or verify a code
// from one, while recovery codes, which are stored as hashes, still verify.
package mfa

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// Method names the second factor a code was verified with.
type Method string

// Second-factor methods.
const (
	MethodTOTP         Method = "totp"
	MethodRecoveryCode Method = "recovery_code"
)

var (
	// ErrUnavailable means authenticator codes cannot be issued or checked:
	// OCMS_MFA_KEY is unset, or it is not the key the secret was sealed with.
	ErrUnavailable = errors.New("authenticator codes are unavailable: OCMS_MFA_KEY is unset or has changed")
	// ErrInvalidCode means the code is wrong, expired or already used.
	ErrInvalidCode = errors.New("invalid two-factor code")
	// ErrNotEnrolled means the user has no authenticator app enrolled.
	ErrNotEnrolled = errors.New("two-factor authentication is not enabled")
	// ErrNoEnrollment means there is no enrollment in progress to confirm.
	ErrNoEnrollment = errors.New("no authenticator setup in progress")
)

// Service enrolls, verifies and removes second factors.
type Service struct {
	db       *sql.DB
	queries  *store.Queries
	sealer   *sealer
	required bool
	now      func() time.Time
}

// NewService returns the two-factor service. key is OCMS_MFA_KEY and may be
// empty. required makes every account pass a second factor at login, which
// needs a key: the combination nobody could meet is an error.
func NewService(db *sql.DB, key string, required bool) (*Service, error) {
	s := &Service{db: db, queries: store.New(db), required: required, now: time.Now}
	if key != "" {
		sl, err := newSealer(key)
		if err != nil {
			return nil, err
		}
		s.sealer = sl
	}
	if required && s.sealer == nil {
		return nil, errors.New("two-factor authentication is required but OCMS_MFA_KEY is not set")
	}
	return s, nil
}

// Available reports whether authenticator apps can be enrolled.
func (s *Service) Available() bool {
	return s.sealer != nil
}

// Required reports whether every account must pass a second factor.
func (s *Service) Required() bool {
	return s.required
}

// Status describes a user's second factor.
type Status struct {
	Enrolled          bool
	EnrolledAt        time.Time
	RecoveryCodesLeft int64
}

// Status returns the second-factor state of userID.
func (s *Service) Status(ctx context.Context, userID int64) (Status, error) {
	row, err := s.queries.GetUserTOTP(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("loading the authenticator for user %d: %w", userID, err)
	}
	left, err := s.queries.CountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return Status{}, fmt.Errorf("counting recovery codes for user %d: %w", userID, err)
	}
	return Status{Enrolled: true, EnrolledAt: row.EnrolledAt, RecoveryCodesLeft: left}, nil
}

// Enrolled reports whether userID has an authenticator app enrolled.
func (s *Service) Enrolled(ctx context.Context, userID int64) (bool, error) {
	status, err := s.Status(ctx, userID)
	return status.Enrolled, err
}

// Enrollment is an authenticator app being set up. Sealed is kept in the
// session until the user confirms a code; the rest is shown on the page.
type Enrollment struct {
	Sealed []byte
	Secret string
	URI    string
	QRCode string
}

// BeginEnrollment returns the authenticator setup for userID. pending is the
// Sealed value of an earlier call: when it opens for this user the same
// secret is shown again, so reloading the page does not invalidate a QR code
// already scanned; otherwise a new secret is generated.
func (s *Service) BeginEnrollment(userID int64, pending []byte, issuer, account string) (Enrollment, error) {
	if s.sealer == nil {
		return Enrollment{}, ErrUnavailable
	}
	raw, openErr := s.sealer.open(userID, pending)
	if openErr != nil {
		raw = make([]byte, secretSize)
		if _, err := rand.Read(raw); err != nil {
			return Enrollment{}, fmt.Errorf("generating a TOTP secret: %w", err)
		}
		pending = s.sealer.seal(userID, raw)
	}
	key, err := totpKey(raw, issuer, account)
	if err != nil {
		return Enrollment{}, err
	}
	qr, err := qrDataURI(key)
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{Sealed: pending, Secret: key.Secret(), URI: key.URL(), QRCode: qr}, nil
}

// Enrolled is the outcome of a confirmed enrollment: the recovery codes to
// show once, and the user's new session version. Every session the user had
// before is now stale.
type Enrolled struct {
	RecoveryCodes  []string
	SessionVersion int64
}

// ConfirmEnrollment enrolls the pending secret once code proves the user's
// app produces it. It replaces any earlier authenticator and recovery codes.
func (s *Service) ConfirmEnrollment(ctx context.Context, userID int64, pending []byte, code string) (Enrolled, error) {
	now := s.now()
	step, err := s.pendingStep(userID, pending, code, now)
	if err != nil {
		return Enrolled{}, err
	}
	codes, err := newRecoveryCodes()
	if err != nil {
		return Enrolled{}, err
	}

	var version int64
	txErr := s.inTx(ctx, func(q *store.Queries) error {
		if err := q.UpsertUserTOTP(ctx, store.UpsertUserTOTPParams{
			UserID: userID, SecretEnc: pending, LastStep: step, EnrolledAt: now,
		}); err != nil {
			return fmt.Errorf("storing the authenticator: %w", err)
		}
		if err := replaceRecoveryCodes(ctx, q, userID, codes, now); err != nil {
			return err
		}
		v, err := bumpSessionVersion(ctx, q, userID, now)
		version = v
		return err
	})
	if txErr != nil {
		return Enrolled{}, txErr
	}
	return Enrolled{RecoveryCodes: codes, SessionVersion: version}, nil
}

// CheckEnrollmentCode reports whether code proves the pending secret, the way
// ConfirmEnrollment would, without enrolling it or recording anything.
// Replacing an authenticator checks the new code first, so a mistake in it
// does not spend the current code that confirms the change.
func (s *Service) CheckEnrollmentCode(userID int64, pending []byte, code string) error {
	_, err := s.pendingStep(userID, pending, code, s.now())
	return err
}

// pendingStep opens userID's pending secret and returns the time step code
// is valid for.
func (s *Service) pendingStep(userID int64, pending []byte, code string, now time.Time) (int64, error) {
	if s.sealer == nil {
		return 0, ErrUnavailable
	}
	raw, err := s.sealer.open(userID, pending)
	if err != nil {
		return 0, ErrNoEnrollment
	}
	step, ok := matchTOTP(raw, compactCode(code), now, 0)
	if !ok {
		return 0, ErrInvalidCode
	}
	return step, nil
}

// Verify checks a second-factor code for userID: six digits are an
// authenticator code, anything else a recovery code. Each code is accepted
// once. A wrong code is ErrInvalidCode; other errors mean it could not be
// checked.
func (s *Service) Verify(ctx context.Context, userID int64, code string) (Method, error) {
	if compact := compactCode(code); isTOTPCode(compact) {
		return MethodTOTP, s.verifyTOTP(ctx, userID, compact)
	}
	return MethodRecoveryCode, s.verifyRecoveryCode(ctx, userID, code)
}

func (s *Service) verifyTOTP(ctx context.Context, userID int64, code string) error {
	row, err := s.queries.GetUserTOTP(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotEnrolled
	}
	if err != nil {
		return fmt.Errorf("loading the authenticator for user %d: %w", userID, err)
	}
	if s.sealer == nil {
		return ErrUnavailable
	}
	raw, err := s.sealer.open(userID, row.SecretEnc)
	if err != nil {
		return fmt.Errorf("%w (user %d)", ErrUnavailable, userID)
	}
	now := s.now()
	step, ok := matchTOTP(raw, code, now, row.LastStep)
	if !ok {
		return ErrInvalidCode
	}
	advanced, err := s.queries.AdvanceUserTOTPStep(ctx, store.AdvanceUserTOTPStepParams{
		Step: step, UsedAt: sql.NullTime{Time: now, Valid: true}, UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("recording the authenticator code: %w", err)
	}
	if advanced == 0 {
		return ErrInvalidCode // another request accepted this code first
	}
	return nil
}

func (s *Service) verifyRecoveryCode(ctx context.Context, userID int64, code string) error {
	normalized := normalizeRecoveryCode(code)
	if len(normalized) != recoveryCodeLength {
		return ErrInvalidCode
	}
	used, err := s.queries.UseRecoveryCode(ctx, store.UseRecoveryCodeParams{
		UsedAt:   sql.NullTime{Time: s.now(), Valid: true},
		UserID:   userID,
		CodeHash: hashRecoveryCode(normalized),
	})
	if err != nil {
		return fmt.Errorf("using a recovery code: %w", err)
	}
	if used == 0 {
		return ErrInvalidCode
	}
	return nil
}

// RegenerateRecoveryCodes replaces userID's recovery codes and returns the
// new ones to show once.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	enrolled, err := s.Enrolled(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !enrolled {
		return nil, ErrNotEnrolled
	}
	codes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := s.inTx(ctx, func(q *store.Queries) error {
		return replaceRecoveryCodes(ctx, q, userID, codes, now)
	}); err != nil {
		return nil, err
	}
	return codes, nil
}

// Disable removes userID's authenticator and recovery codes and returns the
// user's new session version: every session the user had is now stale. It
// serves the user turning two-factor off, an administrator resetting it and
// `ocms mfa-reset`.
func (s *Service) Disable(ctx context.Context, userID int64) (int64, error) {
	now := s.now()
	var version int64
	txErr := s.inTx(ctx, func(q *store.Queries) error {
		if err := q.DeleteUserTOTP(ctx, userID); err != nil {
			return fmt.Errorf("removing the authenticator: %w", err)
		}
		if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
			return fmt.Errorf("removing recovery codes: %w", err)
		}
		v, err := bumpSessionVersion(ctx, q, userID, now)
		version = v
		return err
	})
	return version, txErr
}

// bumpSessionVersion makes every existing session of userID stale.
func bumpSessionVersion(ctx context.Context, q *store.Queries, userID int64, now time.Time) (int64, error) {
	version, err := q.BumpUserSessionVersion(ctx, store.BumpUserSessionVersionParams{UpdatedAt: now, ID: userID})
	if err != nil {
		return 0, fmt.Errorf("invalidating sessions: %w", err)
	}
	return version, nil
}

func replaceRecoveryCodes(ctx context.Context, q *store.Queries, userID int64, codes []string, now time.Time) error {
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("removing recovery codes: %w", err)
	}
	for _, code := range codes {
		if err := q.CreateRecoveryCode(ctx, store.CreateRecoveryCodeParams{
			UserID: userID, CodeHash: hashRecoveryCode(normalizeRecoveryCode(code)), CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("storing recovery codes: %w", err)
		}
	}
	return nil
}

func (s *Service) inTx(ctx context.Context, fn func(q *store.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting a transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}
