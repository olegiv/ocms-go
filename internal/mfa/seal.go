// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mfa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
)

// sealVersion prefixes every sealed secret, so a later change of key or
// algorithm can tell the ciphertexts it wrote from older ones.
const sealVersion byte = 1

// sealInfo binds the key HKDF derives from OCMS_MFA_KEY to this one use.
const sealInfo = "ocms/mfa/totp-secret/v1"

// errUnsealable reports a sealed secret that does not open: a different
// OCMS_MFA_KEY, another user's ciphertext, or damaged bytes.
var errUnsealable = errors.New("sealed TOTP secret does not open")

// sealer encrypts TOTP secrets with AES-256-GCM under a key derived from
// OCMS_MFA_KEY. The user ID is the associated data, so a ciphertext copied
// into another user's row or session does not open there.
type sealer struct {
	aead cipher.AEAD
}

func newSealer(key string) (*sealer, error) {
	derived, err := hkdf.Key(sha256.New, []byte(key), nil, sealInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("deriving the TOTP sealing key: %w", err)
	}
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("creating the TOTP cipher: %w", err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("creating the TOTP cipher: %w", err)
	}
	return &sealer{aead: aead}, nil
}

// seal returns version || nonce || ciphertext for userID's secret.
func (s *sealer) seal(userID int64, secret []byte) []byte {
	return s.aead.Seal([]byte{sealVersion}, nil, secret, associatedData(userID))
}

// open reverses seal for the same userID.
func (s *sealer) open(userID int64, sealed []byte) ([]byte, error) {
	if len(sealed) == 0 || sealed[0] != sealVersion {
		return nil, errUnsealable
	}
	secret, err := s.aead.Open(nil, nil, sealed[1:], associatedData(userID))
	if err != nil {
		return nil, errUnsealable
	}
	return secret, nil
}

func associatedData(userID int64) []byte {
	return []byte("ocms-user:" + strconv.FormatInt(userID, 10))
}
