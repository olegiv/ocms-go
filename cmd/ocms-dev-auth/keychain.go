// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const keychainService = "tech.ocms.dev-auth.v1"

type securityRunner func(context.Context, []string, io.Reader) ([]byte, error)

type keychainStore struct {
	runner securityRunner // Tests supply a private fake; production always uses security.
}

func (s keychainStore) invoke(ctx context.Context, args []string, input io.Reader) ([]byte, error) {
	if s.runner != nil {
		return s.runner(ctx, args, input)
	}
	if runtime.GOOS != "darwin" {
		return nil, errors.New("the shared seed store requires the macOS login Keychain")
	}
	command := exec.CommandContext(ctx, "/usr/bin/security", args...)
	command.Stdin = input
	// security's interactive output can contain enrollment data. Never forward it.
	command.Stderr = io.Discard
	return command.Output()
}

func loginKeychain() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate the login Keychain")
	}
	return filepath.Join(home, "Library", "Keychains", "login.keychain-db"), nil
}

func (s keychainStore) get(ctx context.Context, id string) ([]byte, error) {
	path, err := loginKeychain()
	if err != nil {
		return nil, err
	}
	value, err := s.invoke(ctx, []string{"find-generic-password", "-w", "-a", id, "-s", keychainService, path}, nil)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return nil, errMissing
		}
		return nil, errors.New("cannot read development authenticator; the login Keychain must be available and unlocked")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(value)))
	if err != nil || len(decoded) > 4096 {
		return nil, errors.New("invalid development authenticator Keychain item")
	}
	return decoded, nil
}

func (s keychainStore) add(ctx context.Context, id string, value []byte) error {
	path, err := loginKeychain()
	if err != nil {
		return err
	}
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return errors.New("invalid development authenticator identifier")
	}
	// security -i receives its own command language over a pipe. There is no
	// shell, password argument, temporary seed file or change to the default
	// keychain. Base64 prevents enrollment data from becoming command syntax.
	quotedPath := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`
	if strings.ContainsAny(path, "\r\n") {
		return errors.New("unsupported login Keychain path")
	}
	input := "add-generic-password -a " + id + " -s " + keychainService +
		" -l ocms-development-authenticator -w " + base64.StdEncoding.EncodeToString(value) + " " + quotedPath + "\n"
	if _, invokeErr := s.invoke(ctx, []string{"-i"}, strings.NewReader(input)); invokeErr != nil {
		return errors.New("cannot store development authenticator in the login Keychain")
	}
	// Interactive security does not reliably propagate a subcommand's exit
	// status. Verify privately; -U is intentionally absent, so no replacement.
	saved, err := s.get(ctx, id)
	if err != nil || !bytes.Equal(saved, value) {
		return errors.New("development authenticator was not saved; check the login Keychain")
	}
	return nil
}
