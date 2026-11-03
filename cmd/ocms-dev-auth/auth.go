// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const stagingOrigin = "https://ocms-mcp-staging.fly.dev"

var (
	errMissing = errors.New("no development authenticator enrolled for this origin and account")
	errSeed    = errors.New("invalid enrollment: require matching account, SHA1 TOTP, six digits, 30 seconds and a 20–64 byte base32 seed")
)

type authTarget struct {
	Origin  string
	Account string
}

func (t authTarget) id() string {
	hash := sha256.Sum256([]byte(t.Origin + "\n" + t.Account))
	return hex.EncodeToString(hash[:])
}

func resolveTarget(profile, origin, account string) (authTarget, error) {
	address, err := mail.ParseAddress(account)
	if err != nil || address.Address != account || strings.ContainsAny(account, "\r\n") {
		return authTarget{}, errors.New("account must be a plain development account email")
	}
	origin = strings.TrimSuffix(origin, "/")
	switch profile {
	case "mcp-staging":
		if origin == "" {
			origin = stagingOrigin
		}
		if origin != stagingOrigin {
			return authTarget{}, errors.New("mcp-staging is bound to the canonical Fly staging origin")
		}
	case "local":
		u, parseErr := url.Parse(origin)
		if parseErr != nil || u == nil || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return authTarget{}, errors.New("local requires an HTTP(S) IP-literal loopback origin")
		}
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return authTarget{}, errors.New("local requires an HTTP(S) IP-literal loopback origin")
		}
		if port := u.Port(); port != "" {
			value, portErr := strconv.Atoi(port)
			if portErr != nil || value < 1 || value > 65535 {
				return authTarget{}, errors.New("invalid loopback port")
			}
		}
	default:
		return authTarget{}, errors.New("only mcp-staging and local development profiles are supported")
	}
	return authTarget{Origin: origin, Account: account}, nil
}

// parseSeed deliberately rejects unsupported parameters instead of silently
// producing codes that differ from the application's TOTP configuration.
func parseSeed(value, account string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "otpauth:") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "otpauth" || u.Host != "totp" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
			return "", errSeed
		}
		params, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", errSeed
		}
		for key, values := range params {
			if len(values) != 1 {
				return "", errSeed
			}
			switch key {
			case "secret", "issuer":
			case "algorithm":
				if values[0] != "SHA1" {
					return "", errSeed
				}
			case "digits":
				if values[0] != "6" {
					return "", errSeed
				}
			case "period":
				if values[0] != "30" {
					return "", errSeed
				}
			default:
				return "", errSeed
			}
		}
		label := strings.TrimPrefix(u.Path, "/")
		labelAccount := label
		if at := strings.LastIndexByte(label, ':'); at >= 0 {
			labelAccount = label[at+1:]
			if issuer := params.Get("issuer"); issuer != "" && label[:at] != issuer {
				return "", errSeed
			}
		}
		if labelAccount != account {
			return "", errSeed
		}
		value = params.Get("secret")
	}
	value = strings.ToUpper(strings.Join(strings.Fields(value), ""))
	value = strings.TrimRight(value, "=")
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	raw, err := encoding.DecodeString(value)
	if err != nil || len(raw) < 20 || len(raw) > 64 {
		return "", errSeed
	}
	return encoding.EncodeToString(raw), nil
}

type seedRecord struct {
	Version int    `json:"version"`
	Origin  string `json:"origin"`
	Account string `json:"account"`
	Secret  string `json:"secret"`
}

type seedStore interface {
	get(context.Context, string) ([]byte, error)
	add(context.Context, string, []byte) error
}

type authenticator struct {
	store seedStore
	cache string
	now   func() time.Time
	wait  func(context.Context, time.Duration) error
}

func newAuthenticator(store seedStore, cache string) *authenticator {
	return &authenticator{store: store, cache: cache, now: time.Now, wait: waitContext}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *authenticator) importSeed(ctx context.Context, target authTarget, input string) error {
	secret, err := parseSeed(input, target.Account)
	if err != nil {
		return err
	}
	unlock, _, err := s.lock(ctx, target)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := s.store.get(ctx, target.id()); !errors.Is(err, errMissing) {
		if err == nil {
			return errors.New("authenticator already exists; replacement is not supported")
		}
		return err
	}
	record, err := json.Marshal(seedRecord{Version: 1, Origin: target.Origin, Account: target.Account, Secret: secret})
	if err != nil {
		return errors.New("cannot encode enrollment")
	}
	return s.store.add(ctx, target.id(), record)
}

func (s *authenticator) load(ctx context.Context, target authTarget) (seedRecord, error) {
	data, err := s.store.get(ctx, target.id())
	if err != nil {
		return seedRecord{}, err
	}
	var record seedRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version != 1 || record.Origin != target.Origin || record.Account != target.Account {
		return seedRecord{}, errors.New("keychain record does not match this development origin and account")
	}
	if _, err := parseSeed(record.Secret, record.Account); err != nil {
		return seedRecord{}, errSeed
	}
	return record, nil
}

type statusResult struct {
	Configured bool   `json:"configured"`
	Origin     string `json:"origin"`
	Account    string `json:"account"`
}

func (s *authenticator) status(ctx context.Context, target authTarget) (statusResult, error) {
	_, err := s.load(ctx, target)
	result := statusResult{Configured: err == nil, Origin: target.Origin, Account: target.Account}
	if errors.Is(err, errMissing) {
		return result, nil
	}
	return result, err
}

type codeResult struct {
	CodeFile  string    `json:"code_file"`
	Origin    string    `json:"origin"`
	Account   string    `json:"account"`
	ExpiresAt time.Time `json:"expires_at"`
	directory string
}

func (s *authenticator) issue(ctx context.Context, target authTarget) (codeResult, error) {
	// Code files are normally removed by their consumer; sweep up the ones a
	// crashed consumer or a killed run left behind. This uses the real clock
	// because it is compared with file modification times.
	pruneStaleCodeDirectories(os.TempDir(), time.Now())
	unlock, directory, err := s.lock(ctx, target)
	if err != nil {
		return codeResult{}, err
	}
	defer unlock()
	record, err := s.load(ctx, target)
	if err != nil {
		return codeResult{}, err
	}
	statePath := filepath.Join(directory, "last-step")
	last, err := readLastStep(statePath)
	if err != nil {
		return codeResult{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return codeResult{}, err
		}
		now := s.now()
		step := now.Unix() / 30
		expires := time.Unix((step+1)*30, 0).UTC()
		if last > step || step < 0 {
			return codeResult{}, errors.New("clock moved behind the last issued code; check system time")
		}
		if last == step || expires.Sub(now) < 10*time.Second {
			if err := s.wait(ctx, expires.Sub(now)); err != nil {
				return codeResult{}, err
			}
			continue
		}
		code, err := totp.GenerateCodeCustom(record.Secret, now, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return codeResult{}, errors.New("cannot generate development code")
		}
		return writeCode(target, statePath, step, code, expires)
	}
}

func readLastStep(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return -1, nil
	}
	if err != nil {
		return 0, errors.New("cannot read development authenticator state")
	}
	step, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil || step < 0 {
		return 0, errors.New("invalid development authenticator state")
	}
	return step, nil
}

func writeCode(target authTarget, statePath string, step int64, code string, expires time.Time) (codeResult, error) {
	directory, err := os.MkdirTemp("", codeDirPrefix)
	if err != nil {
		return codeResult{}, errors.New("cannot create private code directory")
	}
	result := codeResult{CodeFile: filepath.Join(directory, codeFileName), Origin: target.Origin, Account: target.Account, ExpiresAt: expires, directory: directory}
	if err := os.WriteFile(result.CodeFile, []byte(code), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return codeResult{}, errors.New("cannot write private code file")
	}
	// Store the reservation before returning a code to either agent. A failed
	// delivery consumes this step locally, just as a submitted code would.
	if err := writePrivateFileAtomic(statePath, fmt.Appendf(nil, "%d", step)); err != nil {
		_ = os.RemoveAll(directory)
		return codeResult{}, errors.New("cannot reserve development code step")
	}
	return result, nil
}

// writePrivateFileAtomic replaces path with data in a 0600 file. It writes and
// syncs a temporary file in the same directory and renames it over path, so a
// crash leaves the previous file instead of an empty or truncated one, which
// readLastStep would reject until it was deleted by hand.
func writePrivateFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*") // created 0600
	if err != nil {
		return err
	}
	name := temp.Name()
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
