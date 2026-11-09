//go:build darwin || linux

// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (s *authenticator) lock(ctx context.Context, target authTarget) (func(), string, error) {
	directory := filepath.Join(s.cache, "ocms-dev-auth", target.id())
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", errors.New("cannot create authenticator state directory")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, "", errors.New("authenticator state directory must be private (0700)")
	}
	file, err := os.OpenFile(filepath.Join(directory, "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, "", errors.New("cannot open authenticator lock")
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, "", err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = file.Close() }, directory, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, "", errors.New("cannot lock development authenticator")
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			_ = file.Close()
			return nil, "", err
		}
	}
}
