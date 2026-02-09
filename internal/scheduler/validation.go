// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// privateIPRanges defines CIDR blocks for private/reserved networks.
var privateIPRanges = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16", // link-local (includes cloud metadata 169.254.169.254)
	"0.0.0.0/8",
	"100.64.0.0/10",  // shared address space (RFC 6598)
	"192.0.0.0/24",   // IETF protocol assignments
	"192.0.2.0/24",   // documentation (TEST-NET-1)
	"198.18.0.0/15",  // benchmarking
	"198.51.100.0/24", // documentation (TEST-NET-2)
	"203.0.113.0/24", // documentation (TEST-NET-3)
	"fc00::/7",       // unique local
	"fe80::/10",      // link-local
	"::1/128",        // IPv6 loopback
	"::/128",         // IPv6 unspecified
}

// parsedPrivateRanges holds parsed CIDRs (initialized once).
var parsedPrivateRanges []*net.IPNet

func init() {
	for _, cidr := range privateIPRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("invalid CIDR in privateIPRanges: " + cidr)
		}
		parsedPrivateRanges = append(parsedPrivateRanges, network)
	}
}

// blockedHostnames lists hostnames that must never be accessed.
var blockedHostnames = []string{
	"metadata.google.internal",
	"metadata.goog",
}

// ValidateTaskURL checks that a URL is safe for server-side HTTP requests.
// It blocks private IPs, localhost, cloud metadata endpoints, and non-HTTP schemes.
func ValidateTaskURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL is required")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow http and https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are allowed")
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	// Block known dangerous hostnames
	lower := strings.ToLower(hostname)
	if lower == "localhost" {
		return fmt.Errorf("localhost URLs are not allowed")
	}
	for _, blocked := range blockedHostnames {
		if lower == blocked {
			return fmt.Errorf("cloud metadata endpoints are not allowed")
		}
	}

	// Resolve hostname to IP addresses
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("cannot resolve hostname %q: %w", hostname, err)
	}

	// Check all resolved IPs against private ranges
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("URL resolves to private/reserved IP address (%s)", ipStr)
		}
	}

	return nil
}

// isPrivateIP checks whether an IP falls within any private or reserved range.
func isPrivateIP(ip net.IP) bool {
	for _, network := range parsedPrivateRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
