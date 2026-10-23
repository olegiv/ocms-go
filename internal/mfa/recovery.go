// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mfa

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

// RecoveryCodeCount is how many recovery codes an enrollment issues.
const RecoveryCodeCount = 10

// recoveryCodeBytes is the randomness in one code: 80 bits, which keeps a
// stolen SHA-256 hash out of brute-force reach, written as 16 characters.
const (
	recoveryCodeBytes  = 10
	recoveryCodeLength = 16
)

// recoveryEncoding is Crockford's base32 alphabet in lower case. It has no
// i, l, o or u, so a code read off paper has no look-alike characters.
var recoveryEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// newRecoveryCodes returns RecoveryCodeCount codes formatted for display.
func newRecoveryCodes() ([]string, error) {
	codes := make([]string, RecoveryCodeCount)
	buf := make([]byte, recoveryCodeBytes)
	for i := range codes {
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generating recovery codes: %w", err)
		}
		codes[i] = formatRecoveryCode(recoveryEncoding.EncodeToString(buf))
	}
	return codes, nil
}

// formatRecoveryCode groups a code as xxxx-xxxx-xxxx-xxxx.
func formatRecoveryCode(code string) string {
	return groupByFour(code, '-')
}

// normalizeRecoveryCode undoes the grouping and maps the look-alikes Crockford
// decoding accepts (o for 0, i and l for 1), so a code typed the way it reads
// matches the one that was issued.
func normalizeRecoveryCode(input string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(compactCode(input)) {
		switch r {
		case 'o':
			b.WriteByte('0')
		case 'i', 'l':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hashRecoveryCode is what the database stores for a normalized code.
func hashRecoveryCode(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
