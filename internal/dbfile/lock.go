// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dbfile coordinates use and replacement of on-disk SQLite databases.
package dbfile

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Path returns the file named by a SQLite DSN.
func Path(dsn string) (string, bool) {
	uri, isURI := strings.CutPrefix(dsn, "file:")
	if !isURI {
		// Match the SQLite driver's handling of query options in plain paths.
		if pos := strings.IndexByte(dsn, '?'); pos > 0 {
			dsn = dsn[:pos]
		}
		if dsn == "" || dsn == ":memory:" {
			return "", false
		}
		return dsn, true
	}
	if authority, ok := strings.CutPrefix(uri, "//"); ok {
		host, rest, found := strings.Cut(authority, "/")
		if !found || (host != "" && host != "localhost") {
			return "", false
		}
		uri = "/" + rest
	}
	uri, _, _ = strings.Cut(uri, "#")
	rawPath, query, _ := strings.Cut(uri, "?")
	path := decodePart(rawPath)
	mode := ""
	for option := range strings.SplitSeq(query, "&") {
		key, value, _ := strings.Cut(option, "=")
		if decodePart(key) == "mode" {
			mode = decodePart(value)
		}
	}
	if path == "" || path == ":memory:" || mode == "memory" {
		return "", false
	}
	return path, true
}

// SQLite decodes valid escapes, keeps literal percent signs, and stops
// reading the current filename or option at an escaped NUL.
func decodePart(part string) string {
	var decoded strings.Builder
	for i := 0; i < len(part); i++ {
		if part[i] == '%' && i+3 <= len(part) {
			if octet, err := url.PathUnescape(part[i : i+3]); err == nil {
				if octet == "\x00" {
					break
				}
				decoded.WriteString(octet)
				i += 2
				continue
			}
		}
		decoded.WriteByte(part[i])
	}
	return decoded.String()
}

// LockDSN holds a shared lock for commands that use the existing database.
// It returns no lock for an in-memory database.
func LockDSN(dsn string) (io.Closer, error) {
	path, ok := Path(dsn)
	if !ok {
		return nil, nil
	}
	return Lock(path, false)
}

// Lock holds a shared lock while an oCMS process uses path, or an exclusive
// lock while pull replaces it. Close releases the lock. The file stays in
// place so a concurrent process always locks the same inode.
func Lock(path string, exclusive bool) (io.Closer, error) {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, linkErr := os.Lstat(path); !errors.Is(linkErr, os.ErrNotExist) {
			return nil, err
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
		if parentErr != nil {
			return nil, parentErr
		}
		target, err = filepath.Join(parent, filepath.Base(path)), nil
	}
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(target+".ocms-lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("invalid database lock file for %s: %w", path, errors.Join(err, os.ErrInvalid))
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("database %s is in use by another oCMS process: %w", path, err)
		}
		return nil, err
	}
	return f, nil
}
