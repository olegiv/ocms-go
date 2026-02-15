// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// MaxWebhookURLLength is the maximum allowed length for a webhook URL.
const MaxWebhookURLLength = 2048

// privateIPBlocks contains CIDR ranges for private/reserved IP addresses
// per RFC 1918, RFC 4193, RFC 3927, and RFC 5737.
var privateIPBlocks []*net.IPNet

func init() {
	cidrs := []string{
		"10.0.0.0/8",      // RFC 1918 - private
		"172.16.0.0/12",   // RFC 1918 - private
		"192.168.0.0/16",  // RFC 1918 - private
		"127.0.0.0/8",     // RFC 1122 - loopback
		"169.254.0.0/16",  // RFC 3927 - link-local
		"0.0.0.0/8",       // RFC 1122 - "this" network
		"100.64.0.0/10",   // RFC 6598 - shared address (CGNAT)
		"192.0.0.0/24",    // RFC 6890 - IETF protocol assignments
		"192.0.2.0/24",    // RFC 5737 - documentation
		"198.18.0.0/15",   // RFC 2544 - benchmarking
		"198.51.100.0/24", // RFC 5737 - documentation
		"203.0.113.0/24",  // RFC 5737 - documentation
		"224.0.0.0/4",     // RFC 5771 - multicast
		"240.0.0.0/4",     // RFC 1112 - reserved
		"::1/128",   // IPv6 loopback
		"fe80::/10", // IPv6 link-local
		"fc00::/7",  // RFC 4193 - IPv6 unique local
		"::/128",    // IPv6 unspecified
	}
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPBlocks = append(privateIPBlocks, block)
		}
	}
}

// IsPrivateIP checks if an IP address falls within a private or reserved range.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true // Treat nil IP as private (deny by default)
	}
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateWebhookURL validates a webhook URL for SSRF protection.
// It checks the URL scheme, resolves the hostname via DNS, and verifies
// that none of the resolved IPs are in private/reserved ranges.
func ValidateWebhookURL(rawURL string) error {
	if len(rawURL) > MaxWebhookURLLength {
		return fmt.Errorf("URL exceeds maximum length of %d characters", MaxWebhookURLLength)
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	// Block localhost variants
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("localhost URLs are not allowed")
	}

	// Check if hostname is a raw IP address
	if ip := net.ParseIP(hostname); ip != nil {
		if IsPrivateIP(ip) {
			return fmt.Errorf("private or reserved IP addresses are not allowed")
		}
		return nil
	}

	// Resolve hostname and check all IPs
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname %q: %w", hostname, err)
	}

	if len(ips) == 0 {
		return fmt.Errorf("hostname %q did not resolve to any IP addresses", hostname)
	}

	for _, ipAddr := range ips {
		if IsPrivateIP(ipAddr.IP) {
			return fmt.Errorf("hostname %q resolves to private IP address %s", hostname, ipAddr.IP)
		}
	}

	return nil
}

// SSRFSafeDialContext returns a DialContext function that prevents connections
// to private/reserved IP addresses. Use this in http.Transport to protect
// against DNS rebinding and redirect-based SSRF at connection time.
func SSRFSafeDialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", addr, err)
		}

		// Resolve hostname to IPs
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve %q: %w", host, err)
		}

		// Check all resolved IPs before connecting
		for _, ipAddr := range ips {
			if IsPrivateIP(ipAddr.IP) {
				return nil, fmt.Errorf("connection to private IP %s (resolved from %q) is blocked", ipAddr.IP, host)
			}
		}

		// Connect to the first valid IP
		// Use the resolved IP directly to prevent TOCTOU from DNS rebinding
		for _, ipAddr := range ips {
			ipStr := ipAddr.IP.String()
			if ipAddr.IP.To4() == nil {
				// IPv6 addresses need brackets
				ipStr = "[" + ipStr + "]"
			}
			conn, dialErr := dialer.DialContext(ctx, network, ipStr+":"+port)
			if dialErr == nil {
				return conn, nil
			}
			err = dialErr
		}

		return nil, fmt.Errorf("failed to connect to %q: %w", host, err)
	}
}
