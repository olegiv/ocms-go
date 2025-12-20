package analytics_int

import (
	"net"
	"sync"
)

// GeoIPLookup handles IP to country lookup.
// This is a simple implementation that can be enhanced with MaxMind GeoLite2
// or IP2Location database for more accurate results.
type GeoIPLookup struct {
	initialized bool
	mu          sync.RWMutex
}

// NewGeoIPLookup creates a new GeoIP lookup instance.
func NewGeoIPLookup() *GeoIPLookup {
	return &GeoIPLookup{}
}

// Init initializes the GeoIP database.
// In a production environment, this would load a GeoIP database file.
// For now, we provide a basic implementation that returns empty for unknown IPs.
func (g *GeoIPLookup) Init() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initialized = true
	return nil
}

// LookupCountry returns the 2-letter country code for an IP address.
// Returns empty string if the country cannot be determined.
func (g *GeoIPLookup) LookupCountry(ip string) string {
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

	// For a full implementation, integrate with:
	// - MaxMind GeoLite2 (free): https://dev.maxmind.com/geoip/geolite2-free-geolocation-data
	// - IP2Location LITE (free): https://lite.ip2location.com/
	//
	// Example with oschwald/maxminddb-golang:
	// db, _ := maxminddb.Open("GeoLite2-Country.mmdb")
	// var record struct { Country struct { ISOCode string `maxminddb:"iso_code"` } `maxminddb:"country"` }
	// db.Lookup(parsedIP, &record)
	// return record.Country.ISOCode

	return ""
}

// isPrivateIP checks if an IP address is in a private range.
func isPrivateIP(ip net.IP) bool {
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",   // IPv6 unique local
		"fe80::/10",  // IPv6 link-local
	}

	for _, block := range privateBlocks {
		_, cidr, err := net.ParseCIDR(block)
		if err != nil {
			continue
		}
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
