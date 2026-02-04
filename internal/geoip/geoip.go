// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package geoip provides IP-to-country lookup using MaxMind GeoLite2-Country database.
package geoip

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// privateCIDRs contains parsed CIDR blocks for private IP ranges.
// Initialized once at package load time for efficiency.
var privateCIDRs []*net.IPNet

func init() {
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",  // IPv6 unique local
		"fe80::/10", // IPv6 link-local
	}

	for _, block := range privateBlocks {
		_, cidr, err := net.ParseCIDR(block)
		if err == nil {
			privateCIDRs = append(privateCIDRs, cidr)
		}
	}
}

// Lookup handles IP to country lookup using MaxMind GeoLite2-Country database.
type Lookup struct {
	db          *maxminddb.Reader
	dbPath      string
	dbModTime   time.Time
	initialized bool
	enabled     bool
	mu          sync.RWMutex
}

// geoRecord matches the GeoLite2-Country database structure.
type geoRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// NewLookup creates a new GeoIP lookup instance.
func NewLookup() *Lookup {
	return &Lookup{}
}

// Init initializes the GeoIP database from the given path.
// If path is empty, GeoIP lookups are disabled (graceful degradation).
// Returns an error if the database cannot be loaded (logs warning instead).
func (g *Lookup) Init(dbPath string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.initialized = true
	g.dbPath = dbPath

	if dbPath == "" {
		g.enabled = false
		return nil
	}

	return g.loadDatabase()
}

// loadDatabase loads or reloads the MaxMind database.
// Caller must hold g.mu write lock.
func (g *Lookup) loadDatabase() error {
	// Check if file exists and get mod time
	info, err := os.Stat(g.dbPath)
	if err != nil {
		g.enabled = false
		if os.IsNotExist(err) {
			return fmt.Errorf("GeoIP database not found: %s", g.dbPath)
		}
		return fmt.Errorf("GeoIP database stat error: %w", err)
	}

	// Skip reload if not modified
	if g.db != nil && info.ModTime().Equal(g.dbModTime) {
		return nil
	}

	// Close existing database if any
	if g.db != nil {
		_ = g.db.Close()
		g.db = nil
	}

	// Open the database
	db, err := maxminddb.Open(g.dbPath)
	if err != nil {
		g.enabled = false
		return fmt.Errorf("failed to open GeoIP database: %w", err)
	}

	g.db = db
	g.dbModTime = info.ModTime()
	g.enabled = true

	return nil
}

// Reload reloads the GeoIP database if it has been updated.
// Safe to call periodically (e.g., from a cron job).
func (g *Lookup) Reload() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.dbPath == "" {
		return nil
	}

	return g.loadDatabase()
}

// LookupCountry returns the 2-letter ISO country code for an IP address.
// Returns empty string if:
// - GeoIP is not enabled/initialized
// - IP address is invalid
// - Country cannot be determined
// Returns "LOCAL" for private/local IPs.
func (g *Lookup) LookupCountry(ip string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.initialized {
		return ""
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return ""
	}

	// Check for private/local IPs
	if isPrivateIP(parsedIP) {
		return "LOCAL"
	}

	// Check for loopback
	if parsedIP.IsLoopback() {
		return "LOCAL"
	}

	// If database not enabled, return empty
	if !g.enabled || g.db == nil {
		return ""
	}

	// Lookup in MaxMind database
	var record geoRecord
	if err := g.db.Lookup(parsedIP, &record); err != nil {
		return ""
	}

	return record.Country.ISOCode
}

// IsEnabled returns whether GeoIP lookups are available.
func (g *Lookup) IsEnabled() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// Close closes the GeoIP database.
func (g *Lookup) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.db != nil {
		err := g.db.Close()
		g.db = nil
		g.enabled = false
		return err
	}
	return nil
}

// isPrivateIP checks if an IP address is in a private range.
func isPrivateIP(ip net.IP) bool {
	for _, cidr := range privateCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// CountryName returns the full country name for a 2-letter country code.
func CountryName(code string) string {
	countries := map[string]string{
		"LOCAL": "Local Network",
		"US":    "United States",
		"GB":    "United Kingdom",
		"DE":    "Germany",
		"FR":    "France",
		"ES":    "Spain",
		"IT":    "Italy",
		"NL":    "Netherlands",
		"BE":    "Belgium",
		"AT":    "Austria",
		"CH":    "Switzerland",
		"PL":    "Poland",
		"CZ":    "Czech Republic",
		"SE":    "Sweden",
		"NO":    "Norway",
		"DK":    "Denmark",
		"FI":    "Finland",
		"RU":    "Russia",
		"UA":    "Ukraine",
		"CA":    "Canada",
		"MX":    "Mexico",
		"BR":    "Brazil",
		"AR":    "Argentina",
		"AU":    "Australia",
		"NZ":    "New Zealand",
		"JP":    "Japan",
		"CN":    "China",
		"KR":    "South Korea",
		"IN":    "India",
		"SG":    "Singapore",
		"HK":    "Hong Kong",
		"TW":    "Taiwan",
		"TH":    "Thailand",
		"VN":    "Vietnam",
		"ID":    "Indonesia",
		"MY":    "Malaysia",
		"PH":    "Philippines",
		"ZA":    "South Africa",
		"EG":    "Egypt",
		"NG":    "Nigeria",
		"KE":    "Kenya",
		"IL":    "Israel",
		"AE":    "United Arab Emirates",
		"SA":    "Saudi Arabia",
		"TR":    "Turkey",
		"GR":    "Greece",
		"PT":    "Portugal",
		"IE":    "Ireland",
		"RO":    "Romania",
		"HU":    "Hungary",
		"BG":    "Bulgaria",
		"HR":    "Croatia",
		"SK":    "Slovakia",
		"SI":    "Slovenia",
		"LT":    "Lithuania",
		"LV":    "Latvia",
		"EE":    "Estonia",
	}

	if name, ok := countries[code]; ok {
		return name
	}
	if code == "" {
		return "Unknown"
	}
	return code
}
