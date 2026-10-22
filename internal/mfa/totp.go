// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mfa

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"
)

// TOTP parameters. SHA-1, six digits and a 30 second period are the RFC 6238
// defaults, the only combination every authenticator app supports.
const (
	totpPeriod = 30
	// totpSkew is how many periods either side of now a code is accepted for,
	// to absorb clock drift between the server and the phone.
	totpSkew = 1
	// secretSize is the secret length in bytes, the 160 bits RFC 4226
	// recommends for HMAC-SHA1.
	secretSize = 20
	// qrSize is the side of the enrollment QR code in pixels.
	qrSize = 200
	// defaultIssuer names the account in authenticator apps when the site
	// has no name.
	defaultIssuer = "oCMS"
)

var totpValidateOpts = hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpKey builds the otpauth:// key an authenticator app scans for raw.
func totpKey(raw []byte, issuer, account string) (*otp.Key, error) {
	if strings.TrimSpace(issuer) == "" {
		issuer = defaultIssuer
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      totpPeriod,
		Secret:      raw,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("building the TOTP key: %w", err)
	}
	return key, nil
}

// qrDataURI renders key as a PNG QR code in a data: URI.
func qrDataURI(key *otp.Key) (string, error) {
	img, err := key.Image(qrSize, qrSize)
	if err != nil {
		return "", fmt.Errorf("rendering the TOTP QR code: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encoding the TOTP QR code: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// matchTOTP returns the time step code is valid for. Only steps within
// totpSkew of now that come after lastStep count, so a code that was already
// accepted, or an older one, is refused (RFC 6238 section 5.2).
func matchTOTP(raw []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if !isTOTPCode(code) {
		return 0, false
	}
	secret := secretEncoding.EncodeToString(raw)
	current := now.Unix() / totpPeriod
	for step := current - totpSkew; step <= current+totpSkew; step++ {
		if step <= lastStep || step < 0 {
			continue
		}
		if ok, err := hotp.ValidateCustom(code, uint64(step), secret, totpValidateOpts); err == nil && ok {
			return step, true
		}
	}
	return 0, false
}

// isTOTPCode reports whether code is six ASCII digits.
func isTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// compactCode drops the spaces and dashes people type inside a code.
func compactCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

// GroupSecret splits a base32 secret into blocks of four for manual entry.
func GroupSecret(secret string) string {
	return groupByFour(secret, ' ')
}

// groupByFour joins s in blocks of four characters with sep.
func groupByFour(s string, sep byte) string {
	var b strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			b.WriteByte(sep)
		}
		b.WriteString(s[i:min(i+4, len(s))])
	}
	return b.String()
}
