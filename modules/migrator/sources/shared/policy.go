// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// EnvAllowedDBHosts names the environment variable holding the optional
// allowlist of source-database hosts a migrator source may connect to.
const EnvAllowedDBHosts = "OCMS_MIGRATOR_ALLOWED_DB_HOSTS"

// CheckDBHostAllowed reports whether a source database host may be connected to.
//
// Deliberately, this is an allowlist and not the private-IP denylist used for
// webhooks (util.IsPrivateIP): a CMS database being migrated from almost always
// lives on a private address, so denying RFC1918 would break the feature's main
// use case. Migrator routes are admin-only, and an operator who wants the
// connection pinned down sets OCMS_MIGRATOR_ALLOWED_DB_HOSTS. An empty
// allowlist means "no restriction".
func CheckDBHostAllowed(host string) error {
	return checkHostAgainstList(host, os.Getenv(EnvAllowedDBHosts))
}

// hostForbiddenChars are characters that never appear in a hostname or IP
// literal but are structurally meaningful inside a MySQL DSN.
//
// mysql.Config.FormatDSN escapes DBName and parameter values but writes Addr
// verbatim between "(" and ")". Since ParseDSN scans backwards for the last
// "@" and forwards for the first "(", a host such as
// "x@unix(/var/run/mysqld/mysqld.sock" re-parses with Net == "unix" instead of
// the intended "tcp". Rejecting these characters keeps the address a plain
// host, so the escaping guarantees of FormatDSN hold for the whole DSN.
const hostForbiddenChars = "@()/\\?#& \t\r\n"

// validateHostShape rejects hosts that could alter DSN structure.
func validateHostShape(host string) error {
	if strings.ContainsAny(host, hostForbiddenChars) {
		return fmt.Errorf("invalid database host %q: must be a bare hostname or IP", host)
	}
	return nil
}

// checkHostAgainstList validates host shape, then checks it against a
// comma-separated allowlist.
func checkHostAgainstList(host, raw string) error {
	// Shape is validated even when the allowlist is empty: an empty allowlist
	// means "any host", not "any string".
	if err := validateHostShape(host); err != nil {
		return err
	}

	allowed, err := ParseAllowedHosts(raw)
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		return nil
	}

	normalized, err := normalizeHost(host)
	if err != nil {
		return err
	}
	if _, ok := allowed[normalized]; !ok {
		return fmt.Errorf("database host %q is not in %s", host, EnvAllowedDBHosts)
	}
	return nil
}

// ParseAllowedHosts parses a comma-separated host allowlist. Entries carrying a
// scheme, path or port are rejected rather than silently reinterpreted, which
// would let "example.com:3306" act as a wildcard over every port.
func ParseAllowedHosts(raw string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{})
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.ContainsAny(entry, "/?#@\\ ") {
			return nil, fmt.Errorf("invalid host entry %q in %s: must be a bare hostname or IP", entry, EnvAllowedDBHosts)
		}
		normalized, err := normalizeHost(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid host entry %q in %s: %w", entry, EnvAllowedDBHosts, err)
		}
		allowed[normalized] = struct{}{}
	}
	return allowed, nil
}

// normalizeHost lowercases a hostname, trims a trailing dot, and canonicalizes
// IP literals so that "::1", "[::1]" and "0:0:0:0:0:0:0:1" all compare equal.
func normalizeHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	return host, nil
}
