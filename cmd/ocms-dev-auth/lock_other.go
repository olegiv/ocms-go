//go:build !darwin && !linux

// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
)

func (*authenticator) lock(context.Context, authTarget) (func(), string, error) {
	return nil, "", errors.New("development authenticator locking requires macOS or Linux")
}
