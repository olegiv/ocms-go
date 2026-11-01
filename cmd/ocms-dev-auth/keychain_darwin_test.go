// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This opt-in check uses only a public RFC seed in a uniquely named, disposable
// login Keychain item. It never reads or modifies a real enrolled account.
func TestLoginKeychainRoundTrip(t *testing.T) {
	if os.Getenv("OCMS_DEV_AUTH_KEYCHAIN_TEST") != "1" {
		t.Skip("set OCMS_DEV_AUTH_KEYCHAIN_TEST=1 to check the macOS login Keychain")
	}
	account := "dev-auth-fixture-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.test"
	target := authTarget{Origin: stagingOrigin, Account: account}
	path, err := loginKeychain()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password", "-a", target.id(), "-s", keychainService, path).Run(); err != nil {
			t.Error("could not remove disposable Keychain fixture")
		}
		cache, err := os.UserCacheDir()
		if err == nil {
			_ = os.RemoveAll(filepath.Join(cache, "ocms-dev-auth", target.id()))
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := run(ctx, []string{"status", "--profile", "mcp-staging", "--account", account}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var status statusResult
	if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.Configured {
		t.Fatal("new fixture unexpectedly exists")
	}
	output.Reset()
	if err := run(ctx, []string{"import", "--profile", "mcp-staging", "--account", account}, strings.NewReader(fixtureSeed), &output); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte(fixtureSeed)) {
		t.Fatal("import printed the seed")
	}
	output.Reset()
	if err := run(ctx, []string{"code", "--profile", "mcp-staging", "--account", account}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var result codeResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal("invalid code delivery metadata")
	}
	result.directory = filepath.Dir(result.CodeFile)
	if code := readPrivateCode(t, result); len(code) != 6 || bytes.Contains(output.Bytes(), []byte(`"`+code+`"`)) {
		t.Fatal("code delivery failed or printed a code")
	}
}
