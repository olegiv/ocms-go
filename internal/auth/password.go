// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth provides password hashing and verification utilities
// using the argon2id algorithm for secure credential storage.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2 parameters (OWASP recommended second choice: m=19456, t=2, p=1)
const (
	Argon2Time    = 2
	Argon2Memory  = 19 * 1024 // 19 MB — fits on 256MB VMs
	Argon2Threads = 1
	Argon2KeyLen  = 32
	Argon2SaltLen = 16
)

// NeedsRehash checks whether an encoded hash uses different parameters than
// the current defaults. Returns true if the hash should be re-created.
func NeedsRehash(encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return true
	}

	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return true
	}

	return memory != Argon2Memory || timeCost != Argon2Time || threads != Argon2Threads
}

// HashArgon2 creates an Argon2id hash of the input string.
// Returns encoded hash in format: $argon2id$v=19$m=19456,t=2,p=1$salt$hash
func HashArgon2(input string) (string, error) {
	salt := make([]byte, Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(input), salt, Argon2Time, Argon2Memory, Argon2Threads, Argon2KeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, Argon2Memory, Argon2Time, Argon2Threads, b64Salt, b64Hash), nil
}

// VerifyArgon2 verifies an input string against an Argon2id hash.
// Uses constant-time comparison to prevent timing attacks.
func VerifyArgon2(input, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false, fmt.Errorf("invalid hash format")
	}

	if parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported hash type: %s", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parsing version: %w", err)
	}

	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, fmt.Errorf("parsing parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decoding salt: %w", err)
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decoding hash: %w", err)
	}

	hash := argon2.IDKey([]byte(input), salt, timeCost, memory, threads, uint32(len(expectedHash)))
	return subtle.ConstantTimeCompare(hash, expectedHash) == 1, nil
}

// HashPassword creates an Argon2id hash of the password.
func HashPassword(password string) (string, error) {
	return HashArgon2(password)
}

// CheckPassword verifies a password against an Argon2id hash.
func CheckPassword(password, encodedHash string) (bool, error) {
	return VerifyArgon2(password, encodedHash)
}
