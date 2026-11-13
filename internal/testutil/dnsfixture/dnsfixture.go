// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dnsfixture resolves the public host names that tests use as
// stand-ins, so SSRF validation tests need no network or DNS. It imports no
// other oCMS package, so any package's tests (util included) can use it.
package dnsfixture

import (
	"context"
	"net"
	"strings"
)

// PublicIP is the public address every fixture host resolves to. Tests only
// need it to be outside the private and reserved ranges.
const PublicIP = "93.184.216.34"

// Domains are the fixture domains. Each one, and every subdomain of it,
// resolves to PublicIP. Add a domain here when a test needs a new public host.
var Domains = []string{
	"example.com",
	"example.org",
	"example.net",
	"api.dify.ai",
	"httpbin.org",
}

// Lookup resolves fixture hosts to PublicIP and defers everything else
// (localhost, IP literals, deliberately unresolvable names) to the process
// resolver, so tests that expect those answers keep their real behaviour.
func Lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	name := strings.TrimSuffix(strings.ToLower(host), ".")
	for _, domain := range Domains {
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return []net.IPAddr{{IP: net.ParseIP(PublicIP)}}, nil
		}
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}
