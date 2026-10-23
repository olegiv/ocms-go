// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mfa

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/olegiv/ocms-go/internal/store"
)

const (
	testKey      = "k9Qw2xZt7Lp4Vn8sRb3Yh6Mc1Ud5Ef0Gj2Hx4Nz8Tq="
	otherTestKey = "Zp8Lq3Wn6Rx1Tc9Vb4Ym7Kd2Hf5Gj0Sa8Ne3Ux6Qi="
)

func newTestService(t *testing.T, key string) (*Service, *sql.DB) {
	t.Helper()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "mfa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	s, err := NewService(db, key, false)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func createUser(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	now := time.Now()
	res, err := db.Exec(`INSERT INTO users (email, password_hash, role, name, created_at, updated_at) VALUES (?, 'x', 'editor', 'Test', ?, ?)`, email, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// enroll runs a full enrollment for userID at now and returns the secret and
// recovery codes.
func enroll(t *testing.T, s *Service, userID int64, now time.Time) (string, []string) {
	t.Helper()
	s.now = func() time.Time { return now }
	e, err := s.BeginEnrollment(userID, nil, "Test site", "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ConfirmEnrollment(context.Background(), userID, e.Sealed, codeAt(t, e.Secret, now))
	if err != nil {
		t.Fatalf("ConfirmEnrollment: %v", err)
	}
	return e.Secret, got.RecoveryCodes
}

func TestSealerBindsKeyAndUser(t *testing.T) {
	a, err := newSealer(testKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newSealer(otherTestKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed := a.seal(7, []byte("secret bytes"))
	if got, err := a.open(7, sealed); err != nil || string(got) != "secret bytes" {
		t.Fatalf("open = %q, %v; want the sealed secret", got, err)
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	wrongVersion := append([]byte{sealVersion + 1}, sealed[1:]...)
	for name, open := range map[string]func() ([]byte, error){
		"other user":    func() ([]byte, error) { return a.open(8, sealed) },
		"other key":     func() ([]byte, error) { return b.open(7, sealed) },
		"tampered":      func() ([]byte, error) { return a.open(7, tampered) },
		"wrong version": func() ([]byte, error) { return a.open(7, wrongVersion) },
		"empty":         func() ([]byte, error) { return a.open(7, nil) },
	} {
		if _, err := open(); !errors.Is(err, errUnsealable) {
			t.Errorf("%s: open error = %v, want errUnsealable", name, err)
		}
	}
}

func TestMatchTOTPWindowAndReplay(t *testing.T) {
	raw := []byte("12345678901234567890")
	secret := secretEncoding.EncodeToString(raw)
	now := time.Unix(1_800_000_000, 0)
	step := now.Unix() / totpPeriod

	if got, ok := matchTOTP(raw, codeAt(t, secret, now), now, 0); !ok || got != step {
		t.Fatalf("current code: step %d, %t; want %d, true", got, ok, step)
	}
	if got, ok := matchTOTP(raw, codeAt(t, secret, now.Add(-totpPeriod*time.Second)), now, 0); !ok || got != step-1 {
		t.Errorf("previous-period code: step %d, %t; want %d, true", got, ok, step-1)
	}
	if _, ok := matchTOTP(raw, codeAt(t, secret, now.Add(-2*totpPeriod*time.Second)), now, 0); ok {
		t.Error("a code two periods old was accepted")
	}
	if _, ok := matchTOTP(raw, codeAt(t, secret, now), now, step); ok {
		t.Error("a code for an already accepted step was accepted again")
	}
	for _, code := range []string{"", "12345", "1234567", "12a456"} {
		if _, ok := matchTOTP(raw, code, now, 0); ok {
			t.Errorf("malformed code %q was accepted", code)
		}
	}
}

func TestRecoveryCodesFormatAndNormalize(t *testing.T) {
	codes, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), RecoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if len(code) != recoveryCodeLength+3 || strings.Count(code, "-") != 3 {
			t.Errorf("code %q is not formatted xxxx-xxxx-xxxx-xxxx", code)
		}
		if seen[code] {
			t.Errorf("duplicate code %q", code)
		}
		seen[code] = true
	}
	if got := normalizeRecoveryCode(" ABCD-efgh 0OIL-1234 "); got != "abcdefgh00111234" {
		t.Errorf("normalizeRecoveryCode = %q", got)
	}
}

func TestServiceEnrollVerifyAndDisable(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t, testKey)
	userID := createUser(t, db, "user@example.com")
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }

	first, err := s.BeginEnrollment(userID, nil, "Test site", "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.QRCode, "data:image/png;base64,") || !strings.HasPrefix(first.URI, "otpauth://totp/") {
		t.Fatalf("enrollment QR %q / URI %q", first.QRCode[:min(30, len(first.QRCode))], first.URI)
	}
	again, err := s.BeginEnrollment(userID, first.Sealed, "Test site", "user@example.com")
	if err != nil || again.Secret != first.Secret {
		t.Fatalf("resumed enrollment secret = %q, %v; want the pending secret", again.Secret, err)
	}
	if other, err := s.BeginEnrollment(userID+1, first.Sealed, "Test site", "x@example.com"); err != nil || other.Secret == first.Secret {
		t.Fatalf("another user's pending secret was reused: %v", err)
	}
	code := codeAt(t, first.Secret, now)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	// CheckEnrollmentCode answers like ConfirmEnrollment but writes nothing.
	if _, err := s.CheckEnrollmentCode(userID, first.Sealed, wrong); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("CheckEnrollmentCode(wrong) = %v, want ErrInvalidCode", err)
	}
	if _, err := s.CheckEnrollmentCode(userID, nil, code); !errors.Is(err, ErrNoEnrollment) {
		t.Fatalf("CheckEnrollmentCode(no pending secret) = %v, want ErrNoEnrollment", err)
	}
	if checkedAt, err := s.CheckEnrollmentCode(userID, first.Sealed, code); err != nil || !checkedAt.Equal(now) {
		t.Fatalf("CheckEnrollmentCode(right) = %v", err)
	}
	if ok, err := s.Enrolled(ctx, userID); err != nil || ok {
		t.Fatalf("CheckEnrollmentCode enrolled the authenticator: %t, %v", ok, err)
	}
	if _, err := s.ConfirmEnrollment(ctx, userID, first.Sealed, wrong); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong confirmation code: %v, want ErrInvalidCode", err)
	}
	if _, err := s.ConfirmEnrollment(ctx, userID, nil, code); !errors.Is(err, ErrNoEnrollment) {
		t.Fatalf("confirm without pending secret: %v, want ErrNoEnrollment", err)
	}
	enrolled, err := s.ConfirmEnrollment(ctx, userID, first.Sealed, code)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.SessionVersion != 1 || len(enrolled.RecoveryCodes) != RecoveryCodeCount {
		t.Fatalf("enrolled = version %d, %d codes", enrolled.SessionVersion, len(enrolled.RecoveryCodes))
	}

	// The confirmation code is spent; the next period's code is not.
	if _, err := s.Verify(ctx, userID, code); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replayed confirmation code: %v, want ErrInvalidCode", err)
	}
	now = now.Add(totpPeriod * time.Second)
	next := codeAt(t, first.Secret, now)
	if method, err := s.Verify(ctx, userID, next[:3]+" "+next[3:]); err != nil || method != MethodTOTP {
		t.Fatalf("Verify(next code) = %q, %v", method, err)
	}
	if _, err := s.Verify(ctx, userID, next); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replayed code: %v, want ErrInvalidCode", err)
	}

	recovery := enrolled.RecoveryCodes[0]
	if method, err := s.Verify(ctx, userID, strings.ToUpper(recovery)); err != nil || method != MethodRecoveryCode {
		t.Fatalf("Verify(recovery code) = %q, %v", method, err)
	}
	if _, err := s.Verify(ctx, userID, recovery); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("reused recovery code: %v, want ErrInvalidCode", err)
	}
	status, err := s.Status(ctx, userID)
	if err != nil || !status.Enrolled || status.RecoveryCodesLeft != RecoveryCodeCount-1 {
		t.Fatalf("Status = %+v, %v", status, err)
	}

	fresh, err := s.RegenerateRecoveryCodes(ctx, userID)
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("RegenerateRecoveryCodes = %d codes, %v", len(fresh), err)
	}
	if _, err := s.Verify(ctx, userID, enrolled.RecoveryCodes[1]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("a replaced recovery code still works: %v", err)
	}

	version, err := s.Disable(ctx, userID)
	if err != nil || version != 2 {
		t.Fatalf("Disable = %d, %v; want version 2", version, err)
	}
	if ok, err := s.Enrolled(ctx, userID); err != nil || ok {
		t.Fatalf("Enrolled after Disable = %t, %v", ok, err)
	}
	if _, err := s.Verify(ctx, userID, codeAt(t, first.Secret, now.Add(totpPeriod*time.Second))); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("Verify after Disable: %v, want ErrNotEnrolled", err)
	}
	if _, err := s.Verify(ctx, userID, fresh[0]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("recovery code after Disable: %v, want ErrInvalidCode", err)
	}
	if _, err := s.RegenerateRecoveryCodes(ctx, userID); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("RegenerateRecoveryCodes when not enrolled: %v, want ErrNotEnrolled", err)
	}
}

// TestServiceWithChangedKey covers OCMS_MFA_KEY being rotated or lost:
// authenticator codes stop verifying with ErrUnavailable, not ErrInvalidCode,
// and recovery codes keep working so the user can still sign in.
func TestServiceWithChangedKey(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t, testKey)
	userID := createUser(t, db, "user@example.com")
	now := time.Unix(1_800_000_000, 0)
	secret, codes := enroll(t, s, userID, now)

	for name, key := range map[string]string{"changed key": otherTestKey, "no key": ""} {
		t.Run(name, func(t *testing.T) {
			other, err := NewService(db, key, false)
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(totpPeriod * time.Second)
			other.now = func() time.Time { return now }
			if _, err := other.Verify(ctx, userID, codeAt(t, secret, now)); !errors.Is(err, ErrUnavailable) {
				t.Errorf("TOTP with %s: %v, want ErrUnavailable", name, err)
			}
		})
	}
	other, err := NewService(db, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify(ctx, userID, codes[0]); err != nil {
		t.Errorf("recovery code without a key: %v, want it accepted", err)
	}
}

func TestNewServiceRefusesRequirementWithoutKey(t *testing.T) {
	if _, err := NewService(nil, "", true); err == nil {
		t.Fatal("NewService(required, no key) succeeded")
	}
	s, err := NewService(nil, testKey, true)
	if err != nil || !s.Required() || !s.Available() {
		t.Fatalf("NewService(required, key) = %v, required %t", err, s != nil && s.Required())
	}
}

func TestGroupSecret(t *testing.T) {
	if got := GroupSecret("ABCDEFGHIJ"); got != "ABCD EFGH IJ" {
		t.Errorf("GroupSecret = %q", got)
	}
}

// TestConfirmEnrollmentAtTheCheckedMoment covers a code from the previous
// period that passes CheckEnrollmentCode just before a period boundary: at
// the checked moment it still confirms, while checking it again after the
// boundary would refuse it.
//
// Bug state: have ConfirmEnrollmentAt use s.now() instead of at.
func TestConfirmEnrollmentAtTheCheckedMoment(t *testing.T) {
	s, db := newTestService(t, testKey)
	userID := createUser(t, db, "user@example.com")
	checked := time.Unix(1_800_000_000/totpPeriod*totpPeriod+totpPeriod-1, 0) // last second of a period
	s.now = func() time.Time { return checked }
	e, err := s.BeginEnrollment(userID, nil, "Test site", "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	previous := codeAt(t, e.Secret, checked.Add(-totpPeriod*time.Second))
	at, err := s.CheckEnrollmentCode(userID, e.Sealed, previous)
	if err != nil {
		t.Fatalf("CheckEnrollmentCode(previous period) = %v", err)
	}
	s.now = func() time.Time { return checked.Add(time.Second) } // next period: previous is now two steps old
	if _, err := s.CheckEnrollmentCode(userID, e.Sealed, previous); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("the code still checks after the boundary (%v); the test does not cross one", err)
	}
	if _, err := s.ConfirmEnrollmentAt(context.Background(), userID, e.Sealed, previous, at); err != nil {
		t.Errorf("ConfirmEnrollmentAt(checked moment) = %v, want the checked code enrolled", err)
	}
}

// TestOffServiceIgnoresEnrollments checks the service demo mode uses: an
// authenticator enrolled before the site switched to demo mode does not
// count, so its account is not sent to a code nobody can check, and
// nothing can be enrolled.
func TestOffServiceIgnoresEnrollments(t *testing.T) {
	s, db := newTestService(t, testKey)
	userID := createUser(t, db, "enrolled@example.com")
	enroll(t, s, userID, time.Now())

	off := NewOffService(db)
	if enrolled, err := off.Enrolled(t.Context(), userID); err != nil || enrolled {
		t.Errorf("Enrolled = %t, %v; want no account enrolled while off", enrolled, err)
	}
	if off.Available() || off.Required() {
		t.Errorf("off service: available %t, required %t; want neither", off.Available(), off.Required())
	}
	if _, err := off.BeginEnrollment(userID, nil, "Test", "enrolled@example.com"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("BeginEnrollment = %v, want ErrUnavailable", err)
	}
}

// TestHasCurrentCode checks when a change to the second factor can still be
// confirmed with a current code: with recovery codes left, or with an
// authenticator that opens under the current key. After the key changes
// and the last recovery code is spent, no current code exists.
func TestHasCurrentCode(t *testing.T) {
	s, db := newTestService(t, testKey)
	userID := createUser(t, db, "current@example.com")
	if has, err := s.HasCurrentCode(t.Context(), userID); err != nil || has {
		t.Fatalf("without an authenticator = %t, %v; want false", has, err)
	}
	_, codes := enroll(t, s, userID, time.Now())
	rotated, err := NewService(db, otherTestKey, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range []*Service{s, rotated} {
		if has, err := svc.HasCurrentCode(t.Context(), userID); err != nil || !has {
			t.Fatalf("with recovery codes left = %t, %v; want true", has, err)
		}
	}
	for _, code := range codes {
		if _, err := s.Verify(t.Context(), userID, code); err != nil {
			t.Fatal(err)
		}
	}
	if has, err := s.HasCurrentCode(t.Context(), userID); err != nil || !has {
		t.Errorf("readable authenticator, no codes = %t, %v; want true", has, err)
	}
	if has, err := rotated.HasCurrentCode(t.Context(), userID); err != nil || has {
		t.Errorf("changed key, no codes = %t, %v; want false", has, err)
	}
}
