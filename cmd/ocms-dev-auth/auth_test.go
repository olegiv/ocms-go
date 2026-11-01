// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/mfa"
	cmsstore "github.com/olegiv/ocms-go/internal/store"
)

// RFC 6238's public SHA1 test seed, never a live credential.
const fixtureSeed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

type memoryStore struct {
	items map[string][]byte
	reads int
}

func (m *memoryStore) get(_ context.Context, id string) ([]byte, error) {
	m.reads++
	if value, exists := m.items[id]; exists {
		return value, nil
	}
	return nil, errMissing
}

func (m *memoryStore) add(_ context.Context, id string, value []byte) error {
	if _, exists := m.items[id]; exists {
		return errors.New("already exists")
	}
	m.items[id] = value
	return nil
}

func fixture(t *testing.T) (*authenticator, authTarget, *memoryStore) {
	t.Helper()
	target, err := resolveTarget("mcp-staging", "", "ocms-agent@example.test")
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{items: make(map[string][]byte)}
	s := newAuthenticator(store, t.TempDir())
	return s, target, store
}

func TestDevelopmentTargets(t *testing.T) {
	for _, tc := range []struct {
		profile, origin, account string
		valid                    bool
	}{
		{"mcp-staging", "", "agent@example.test", true},
		{"mcp-staging", stagingOrigin + "/", "agent@example.test", true},
		{"local", "http://127.0.0.1:18080", "agent@example.test", true},
		{"local", "https://[::1]:8080", "agent@example.test", true},
		{"production", "https://ocms.tech", "agent@example.test", false},
		{"mcp-staging", "https://ocms.tech", "agent@example.test", false},
		{"local", "http://localhost:8080", "agent@example.test", false},
		{"local", "http://192.0.2.1:8080", "agent@example.test", false},
		{"local", "http://127.0.0.1:70000", "agent@example.test", false},
		{"local", "http://127.0.0.1/path", "agent@example.test", false},
		{"local", "http://agent@127.0.0.1", "agent@example.test", false},
		{"local", "http://127.0.0.1?x=1", "agent@example.test", false},
		{"mcp-staging", "", "Agent <agent@example.test>", false},
	} {
		_, err := resolveTarget(tc.profile, tc.origin, tc.account)
		if (err == nil) != tc.valid {
			t.Errorf("target %s/%s: valid=%v, error=%v", tc.profile, tc.origin, tc.valid, err)
		}
	}
}

func TestEnrollmentValidation(t *testing.T) {
	validURI := "otpauth://totp/oCMS:ocms-agent@example.test?secret=" + fixtureSeed + "&issuer=oCMS&algorithm=SHA1&digits=6&period=30"
	for _, input := range []string{fixtureSeed, strings.ToLower(fixtureSeed), "GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ", validURI} {
		got, err := parseSeed(input, "ocms-agent@example.test")
		if err != nil || got != fixtureSeed {
			t.Fatal("valid public fixture was rejected")
		}
	}
	for _, input := range []string{
		"", "JBSWY3DPEHPK3PXP", strings.Repeat("A", 110),
		strings.Replace(validURI, "totp/", "hotp/", 1),
		strings.Replace(validURI, "SHA1", "SHA256", 1),
		strings.Replace(validURI, "digits=6", "digits=8", 1),
		strings.Replace(validURI, "period=30", "period=60", 1),
		strings.Replace(validURI, "ocms-agent@", "someone-else@", 1),
		strings.Replace(validURI, "issuer=oCMS", "issuer=other", 1),
		validURI + "&secret=" + fixtureSeed,
		validURI + "&unknown=1", validURI + "#fragment",
	} {
		s, target, store := fixture(t)
		err := s.importSeed(t.Context(), target, input)
		if !errors.Is(err, errSeed) || store.reads != 0 || len(store.items) != 0 {
			t.Fatal("invalid enrollment reached the credential store or returned the wrong error")
		}
	}
}

func TestImportStatusAndBinding(t *testing.T) {
	s, target, store := fixture(t)
	status, err := s.status(t.Context(), target)
	if err != nil || status.Configured {
		t.Fatal("missing enrollment must report configured=false")
	}
	if err := s.importSeed(t.Context(), target, fixtureSeed); err != nil {
		t.Fatal(err)
	}
	if err := s.importSeed(t.Context(), target, fixtureSeed); err == nil {
		t.Fatal("replacement was allowed")
	}
	status, err = s.status(t.Context(), target)
	if err != nil || !status.Configured {
		t.Fatal("imported enrollment missing")
	}
	data, err := json.Marshal(status)
	if err != nil || bytes.Contains(data, []byte(fixtureSeed)) {
		t.Fatal("status exposed enrollment data")
	}
	store.items[target.id()] = []byte(`{"version":1,"origin":"https://ocms.tech","account":"other@example.test","secret":"` + fixtureSeed + `"}`)
	if _, err := s.issue(t.Context(), target); err == nil {
		t.Fatal("mismatched metadata allowed code generation")
	}
}

func readPrivateCode(t *testing.T, result codeResult) string {
	t.Helper()
	t.Cleanup(func() { _ = os.RemoveAll(result.directory) })
	for path, mode := range map[string]os.FileMode{result.directory: 0o700, result.CodeFile: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private output permissions: %s", path)
		}
	}
	data, err := os.ReadFile(result.CodeFile)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(result)
	if err != nil || bytes.Contains(metadata, []byte(`"`+string(data)+`"`)) || bytes.Contains(metadata, []byte(fixtureSeed)) {
		t.Fatal("metadata exposed code or seed")
	}
	return string(data)
}

func TestRFC6238VectorsThroughPrivateDelivery(t *testing.T) {
	for _, tc := range []struct {
		unix int64
		want string
	}{
		{1111111111, "050471"}, {1234567890, "005924"},
		{2000000000, "279037"}, {20000000000, "353130"},
	} {
		s, target, _ := fixture(t)
		if err := s.importSeed(t.Context(), target, fixtureSeed); err != nil {
			t.Fatal(err)
		}
		s.now = func() time.Time { return time.Unix(tc.unix, 0) }
		result, err := s.issue(t.Context(), target)
		if err != nil {
			t.Fatal(err)
		}
		if got := readPrivateCode(t, result); got != tc.want {
			t.Errorf("RFC fixture at %d differs", tc.unix)
		}
	}
}

func TestFreshWindowReplayReservationAndRollback(t *testing.T) {
	s, target, _ := fixture(t)
	if err := s.importSeed(t.Context(), target, fixtureSeed); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1234567890, 0).Add(25 * time.Second)
	waits := 0
	s.now = func() time.Time { return now }
	s.wait = func(_ context.Context, d time.Duration) error {
		waits++
		now = now.Add(d)
		return nil
	}
	first, err := s.issue(t.Context(), target)
	if err != nil || waits != 1 || first.ExpiresAt.Sub(now) != 30*time.Second {
		t.Fatal("short window did not wait for a fresh code")
	}
	_ = readPrivateCode(t, first)
	// A separate agent instance uses the same on-disk reservation.
	other := newAuthenticator(s.store, s.cache)
	other.now, other.wait = s.now, s.wait
	second, err := other.issue(t.Context(), target)
	if err != nil || waits != 2 || !second.ExpiresAt.After(first.ExpiresAt) {
		t.Fatal("another agent reused an issued code step")
	}
	_ = readPrivateCode(t, second)
	now = now.Add(-60 * time.Second)
	if _, err := s.issue(t.Context(), target); err == nil {
		t.Fatal("clock rollback was ignored")
	}
}

func TestLockAndWaitCancellation(t *testing.T) {
	s, target, _ := fixture(t)
	unlock, _, err := s.lock(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, _, err = s.lock(ctx, target)
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("another process's lock was not respected")
	}
	if err := waitContext(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting ignored context cancellation")
	}
}

func TestSeparateAgentProcessRespectsLock(t *testing.T) {
	target := authTarget{Origin: stagingOrigin, Account: "agent@example.test"}
	if cache := os.Getenv("OCMS_DEV_AUTH_LOCK_FIXTURE"); cache != "" {
		s := newAuthenticator(nil, cache)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		unlock, _, err := s.lock(ctx, target)
		if err == nil {
			unlock()
			t.Fatal("child process bypassed the held lock")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		return
	}
	s := newAuthenticator(nil, t.TempDir())
	unlock, _, err := s.lock(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSeparateAgentProcessRespectsLock$")
	child.Env = append(os.Environ(), "OCMS_DEV_AUTH_LOCK_FIXTURE="+s.cache)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("independent agent lock test: %v: %s", err, output)
	}
}

func TestRealMFAEnrollmentAndOneUseVerification(t *testing.T) {
	db, err := cmsstore.NewDB(filepath.Join(t.TempDir(), "mfa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := cmsstore.Migrate(db); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO users (email,password_hash,role,name,created_at,updated_at) VALUES (?, 'fixture', 'editor', 'Agent', ?, ?)`, "ocms-agent@example.test", time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	service, err := mfa.NewService(db, "public-test-sealing-key-at-least-32-bytes", true)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := service.BeginEnrollment(userID, nil, "oCMS staging", "ocms-agent@example.test")
	if err != nil {
		t.Fatal(err)
	}
	s, target, _ := fixture(t)
	if err := s.importSeed(t.Context(), target, enrollment.URI); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(30 * time.Second).Add(time.Second)
	s.now = func() time.Time { return now }
	s.wait = func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
	first, err := s.issue(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmEnrollmentAt(t.Context(), userID, enrollment.Sealed, readPrivateCode(t, first), now); err != nil {
		t.Fatal("the server rejected helper-generated enrollment code")
	}
	second, err := s.issue(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	code := readPrivateCode(t, second)
	if _, err := service.Verify(t.Context(), userID, code); err != nil {
		t.Fatal("the server rejected the next helper-generated code")
	}
	if _, err := service.Verify(t.Context(), userID, code); err == nil {
		t.Fatal("the server accepted a replay")
	}
}

func TestCLIRejectsUnsafeArgumentsWithoutEcho(t *testing.T) {
	for _, args := range [][]string{
		nil, {"export"}, {"code", "--secret", fixtureSeed},
		{"code", "--profile", "production", "--account", "agent@example.test"},
	} {
		var output bytes.Buffer
		err := run(t.Context(), args, strings.NewReader(""), &output)
		if err == nil || strings.Contains(err.Error(), fixtureSeed) || output.Len() != 0 {
			t.Fatal("unsafe arguments were accepted or echoed")
		}
	}
}

func TestKeychainTransportKeepsSeedOutOfArguments(t *testing.T) {
	value := []byte(`{"fixture":"private-transport-sentinel"}`)
	target := authTarget{Origin: stagingOrigin, Account: "agent@example.test"}
	var calls int
	store := keychainStore{runner: func(_ context.Context, args []string, input io.Reader) ([]byte, error) {
		calls++
		if strings.Contains(strings.Join(args, " "), "sentinel") {
			t.Fatal("secret passed in process arguments")
		}
		if calls == 1 {
			data, err := io.ReadAll(input)
			if err != nil || len(args) != 1 || args[0] != "-i" || !strings.Contains(string(data), "add-generic-password") || strings.Contains(string(data), " -A ") || strings.Contains(string(data), " -U ") {
				t.Fatal("unsafe Keychain write command")
			}
			return nil, nil
		}
		return []byte("eyJmaXh0dXJlIjoicHJpdmF0ZS10cmFuc3BvcnQtc2VudGluZWwifQ==\n"), nil
	}}
	if err := store.add(t.Context(), target.id(), value); err != nil || calls != 2 {
		t.Fatal("private Keychain transport failed")
	}
	store.runner = func(context.Context, []string, io.Reader) ([]byte, error) {
		return nil, errors.New("private-transport-sentinel")
	}
	if _, err := store.get(t.Context(), target.id()); err == nil || strings.Contains(err.Error(), "sentinel") {
		t.Fatal("Keychain errors exposed subprocess output")
	}
}
