// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package geoip

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPrivateCIDRsAllParsed fails when a prefix in the private-range list is
// malformed. init() ignores parse errors, so a typo would silently drop a whole
// range and start reporting private addresses as unknown instead of "LOCAL".
func TestPrivateCIDRsAllParsed(t *testing.T) {
	const want = 5 // 10/8, 172.16/12, 192.168/16, fc00::/7, fe80::/10
	if len(privateCIDRs) != want {
		t.Fatalf("privateCIDRs has %d entries, want %d; a prefix failed to parse in init()",
			len(privateCIDRs), want)
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"fc00::1", true},
		{"fd12:3456::1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"172.32.0.1", false}, // just outside 172.16/12
		{"2606:4700::1111", false},
	}
	for _, tt := range tests {
		addr, err := netip.ParseAddr(tt.addr)
		if err != nil {
			t.Fatalf("parsing %q: %v", tt.addr, err)
		}
		if got := isPrivateIP(addr); got != tt.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

// TestLookupCountryBeforeInit pins the contract that a Lookup that was never
// initialized answers "" rather than panicking on a nil reader.
func TestLookupCountryBeforeInit(t *testing.T) {
	g := NewLookup()
	if got := g.LookupCountry("8.8.8.8"); got != "" {
		t.Errorf("LookupCountry before Init = %q, want empty", got)
	}
	if g.IsEnabled() {
		t.Error("IsEnabled before Init = true, want false")
	}
}

// TestInitEmptyPathDisables covers the documented graceful-degradation path:
// no OCMS_GEOIP_DB_PATH means lookups are off, not an error.
func TestInitEmptyPathDisables(t *testing.T) {
	g := NewLookup()
	if err := g.Init(""); err != nil {
		t.Fatalf("Init(\"\") = %v, want nil", err)
	}
	if g.IsEnabled() {
		t.Error("IsEnabled after Init(\"\") = true, want false")
	}
	if got := g.LookupCountry("8.8.8.8"); got != "" {
		t.Errorf("LookupCountry with no database = %q, want empty", got)
	}
	if err := g.Reload(); err != nil {
		t.Errorf("Reload with empty path = %v, want nil", err)
	}
}

func TestInitMissingFile(t *testing.T) {
	g := NewLookup()
	err := g.Init(filepath.Join(t.TempDir(), "absent.mmdb"))
	if err == nil {
		t.Fatal("Init with a missing database = nil, want error")
	}
	if g.IsEnabled() {
		t.Error("IsEnabled after a failed Init = true, want false")
	}
}

// TestLookupCountryLocalAndInvalid exercises every branch reachable without a
// database. These are the branches the netip migration rewrote, so they carry
// the migration risk: parsing, private-range matching, and 4-in-6 unmapping.
func TestLookupCountryLocalAndInvalid(t *testing.T) {
	g := NewLookup()
	if err := g.Init(""); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tests := []struct {
		name string
		ip   string
		want string
	}{
		{"private IPv4", "192.168.1.50", "LOCAL"},
		{"private IPv4 10/8", "10.1.2.3", "LOCAL"},
		{"private IPv6 ULA", "fd00::1", "LOCAL"},
		{"link-local IPv6", "fe80::abcd", "LOCAL"},
		{"IPv4-mapped private", "::ffff:10.0.0.1", "LOCAL"},
		{"loopback IPv4", "127.0.0.1", "LOCAL"},
		{"loopback IPv6", "::1", "LOCAL"},
		{"public without database", "8.8.8.8", ""},
		{"not an IP", "definitely-not-an-ip", ""},
		{"empty", "", ""},
		{"host:port", "8.8.8.8:53", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := g.LookupCountry(tt.ip); got != tt.want {
				t.Errorf("LookupCountry(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	g := NewLookup()
	if err := g.Init(""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Errorf("first Close = %v, want nil", err)
	}
	if err := g.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestCountryName(t *testing.T) {
	tests := []struct{ code, want string }{
		{"LOCAL", "Local Network"},
		{"DE", "Germany"},
		{"", "Unknown"},
		{"ZZ", "ZZ"}, // unknown codes pass through
	}
	for _, tt := range tests {
		if got := CountryName(tt.code); got != tt.want {
			t.Errorf("CountryName(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

// fixtureDB is a small GeoLite2-Country-shaped database built by
// testdata/gen_mmdb.go. It exists so the tests below can drive a real
// maxminddb Lookup/Decode: GeoLite2 needs a MaxMind licence, and the library
// keeps its own test databases in a git submodule, so neither is reachable
// from here.
const fixtureDB = "testdata/country-test.mmdb"

// TestLookupCountryWithDatabase exercises the maxminddb v2 Lookup/Decode call
// itself — the one part of the v1→v2 migration that no database-less test can
// reach, and the part where a wrong call would compile perfectly and return
// nothing at runtime.
func TestLookupCountryWithDatabase(t *testing.T) {
	g := NewLookup()
	if err := g.Init(fixtureDB); err != nil {
		t.Fatalf("Init(%q): %v", fixtureDB, err)
	}
	defer func() { _ = g.Close() }()

	if !g.IsEnabled() {
		t.Fatal("IsEnabled after loading the fixture = false, want true")
	}

	tests := []struct {
		name string
		ip   string
		want string
	}{
		{"IPv4 in database", "81.2.69.142", "GB"},
		{"IPv4 in database, second network", "8.8.8.8", "US"},
		{"IPv6 in database", "2606:4700::1111", "US"},
		{"IPv4-mapped form of a listed address", "::ffff:81.2.69.142", "GB"},
		{"absent from database", "1.1.1.1", ""},
		{"private address never reaches the database", "10.0.0.1", "LOCAL"},
		{"malformed", "not-an-ip", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := g.LookupCountry(tt.ip); got != tt.want {
				t.Errorf("LookupCountry(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

// TestReloadKeepsDatabaseUsable covers loadDatabase's reload branch: an
// unchanged file is skipped by mod time, and a changed one is reopened without
// leaving the lookup disabled or the old reader leaked.
func TestReloadKeepsDatabaseUsable(t *testing.T) {
	raw, err := os.ReadFile(fixtureDB)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing fixture copy: %v", err)
	}

	g := NewLookup()
	if err := g.Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = g.Close() }()

	if err := g.Reload(); err != nil {
		t.Fatalf("Reload on an unchanged database = %v, want nil", err)
	}
	if got := g.LookupCountry("81.2.69.142"); got != "GB" {
		t.Fatalf("after a no-op Reload, LookupCountry = %q, want \"GB\"", got)
	}

	// Rewrite with a newer mod time so loadDatabase takes the reopen path.
	future := time.Now().Add(time.Minute)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("rewriting fixture copy: %v", err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("touching fixture copy: %v", err)
	}
	if err := g.Reload(); err != nil {
		t.Fatalf("Reload after modification = %v, want nil", err)
	}
	if !g.IsEnabled() {
		t.Fatal("IsEnabled after a reload = false, want true")
	}
	if got := g.LookupCountry("81.2.69.142"); got != "GB" {
		t.Errorf("after reopening the database, LookupCountry = %q, want \"GB\"", got)
	}
}

// TestLookupCountryWithRealDatabase repeats the fixture assertions against a
// genuine GeoLite2-Country release when one is available, so the fixture's
// shape can be confirmed against the real thing.
//
// Point OCMS_GEOIP_TEST_DB at a GeoLite2-Country.mmdb to run it.
func TestLookupCountryWithRealDatabase(t *testing.T) {
	dbPath := os.Getenv("OCMS_GEOIP_TEST_DB")
	if dbPath == "" {
		t.Skip("set OCMS_GEOIP_TEST_DB to a GeoLite2-Country.mmdb to cross-check against real data")
	}

	g := NewLookup()
	if err := g.Init(dbPath); err != nil {
		t.Fatalf("Init(%q): %v", dbPath, err)
	}
	defer func() { _ = g.Close() }()

	if !g.IsEnabled() {
		t.Fatal("IsEnabled after loading a real database = false, want true")
	}
	// Both are MaxMind's own documentation addresses and are stable across
	// GeoLite2-Country releases.
	if got := g.LookupCountry("81.2.69.142"); got != "GB" {
		t.Errorf("LookupCountry(81.2.69.142) = %q, want \"GB\"", got)
	}
	if got := g.LookupCountry("8.8.8.8"); got != "US" {
		t.Errorf("LookupCountry(8.8.8.8) = %q, want \"US\"", got)
	}
	if got := g.LookupCountry("192.168.1.1"); got != "LOCAL" {
		t.Errorf("LookupCountry(192.168.1.1) = %q, want \"LOCAL\"", got)
	}
}
