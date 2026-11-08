// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
)

// sessionSecretBytes is the number of random bytes used to generate a new
// OCMS_SESSION_SECRET. Base64-encoding 32 bytes yields a 44-character secret,
// comfortably above config.MinSessionSecretLength with high character diversity.
const sessionSecretBytes = 32

// generateSessionSecret returns a base64-encoded, cryptographically random
// session secret suitable for OCMS_SESSION_SECRET.
func generateSessionSecret() (string, error) {
	b := make([]byte, sessionSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// runInit scaffolds a ready-to-run oCMS site directory: it creates the data,
// uploads, and custom subdirectories and writes a .env file with a freshly
// generated session secret and OCMS_MFA_KEY. The directory can then be started with
// `ocms serve` from inside it.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	var (
		force bool
		host  string
		port  int
	)
	fs.BoolVar(&force, "force", false, "Scaffold into an existing non-empty directory")
	fs.StringVar(&host, "host", "localhost", "Loopback bind host written to .env")
	fs.IntVar(&port, "port", 8080, "Server port written to .env")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: ocms init [options] <dir>\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Scaffold a new oCMS site directory (.env + data dirs).\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("init requires exactly one target directory")
	}
	if host != "localhost" {
		literal := host
		if len(host) > 2 && host[0] == '[' && host[len(host)-1] == ']' {
			literal = host[1 : len(host)-1]
		}
		addr, parseErr := netip.ParseAddr(literal)
		if parseErr != nil || addr.Zone() != "" || !addr.Unmap().IsLoopback() {
			return errors.New("init requires a loopback -host because the development scaffold seeds a default password")
		}
		host = addr.String()
		if addr.Is6() {
			host = "[" + host + "]"
		}
	}
	dir := fs.Arg(0)

	// Refuse to clobber an existing non-empty directory unless -force.
	if !force {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			return fmt.Errorf("directory %q already exists and is not empty (use -force to scaffold anyway)", dir)
		}
	}

	// Create the directory tree (idempotent). The site root is owner-only
	// because it holds the .env session secret and the SQLite database (with
	// password hashes); subdirectories are protected by the 0700 root.
	// MkdirAll does not change the mode of a pre-existing directory, so chmod
	// the root explicitly to enforce owner-only even when init targets a
	// directory the user already created (e.g. `mkdir my-site && ocms init`).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("securing %s: %w", dir, err)
	}
	for _, sub := range []string{"data", "uploads", "custom"} {
		target := filepath.Join(dir, sub)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", target, err)
		}
	}

	secret, err := generateSessionSecret()
	if err != nil {
		return err
	}
	mfaKey, err := generateSessionSecret()
	if err != nil {
		return err
	}

	envPath := filepath.Join(dir, ".env")
	createdEnv, err := writeEnvFile(envPath, secret, mfaKey, host, port)
	if err != nil {
		return err
	}

	printInitNextSteps(os.Stdout, dir, host, port, createdEnv)
	return nil
}

// writeEnvFile writes a starter .env at path with owner-only (0600)
// permissions because it holds the session secret and the OCMS_MFA_KEY that
// seals authenticator secrets. It never overwrites an existing .env (the
// secrets it holds must be preserved); in that case it returns created=false
// without error.
func writeEnvFile(path, secret, mfaKey, host string, port int) (created bool, err error) {
	content := fmt.Sprintf(`OCMS_SESSION_SECRET=%s
OCMS_MFA_KEY=%s
OCMS_DB_PATH=./data/ocms.db
OCMS_UPLOADS_DIR=./uploads
OCMS_CUSTOM_DIR=./custom
OCMS_SERVER_HOST=%s
OCMS_SERVER_PORT=%d
OCMS_ENV=development
OCMS_DO_SEED=true
`, secret, mfaKey, host, port)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil // keep existing .env (preserve its secret)
		}
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(content); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// printInitNextSteps prints copy-pasteable instructions for starting the new
// site. It uses the running executable's path so the serve command resolves
// even when ocms is not on PATH.
func printInitNextSteps(w io.Writer, dir, host string, port int, createdEnv bool) {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "ocms"
	}

	_, _ = fmt.Fprintf(w, "Scaffolded oCMS site in %s\n\n", dir)
	if !createdEnv {
		_, _ = fmt.Fprintf(w, "Kept existing %s (session secret preserved).\n\n", filepath.Join(dir, ".env"))
	}
	_, _ = fmt.Fprintf(w, "Next steps:\n  cd %s\n  %s serve\n\n", dir, exe)
	if createdEnv {
		_, _ = fmt.Fprintf(w, "Then open http://%s:%d/admin/ and sign in with:\n  admin@example.com / changeme1234\n\n", host, port)
		_, _ = fmt.Fprintf(w, "Change those credentials, then set OCMS_DO_SEED=false in %s.\n", filepath.Join(dir, ".env"))
	} else {
		_, _ = fmt.Fprintln(w, "Open /admin/ on the host and port in the existing .env and use the site's credentials.")
	}
	_, _ = fmt.Fprintln(w, "Before serving public traffic, set OCMS_ENV=production and complete the production configuration checklist in the README.")
}
