package analytics_int

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"time"
)

// timeNow is a variable so it can be mocked in tests.
var timeNow = time.Now

// generateRandomSalt generates a random salt for hashing.
func generateRandomSalt() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based salt if crypto/rand fails
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}

// anonymizeIP masks the IP address for privacy.
// For IPv4: zeros the last octet (e.g., 192.168.1.100 -> 192.168.1.0)
// For IPv6: zeros the last 80 bits
func anonymizeIP(ip string) string {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return ""
	}

	// Check if it's IPv4
	if ipv4 := parsedIP.To4(); ipv4 != nil {
		ipv4[3] = 0 // Zero last octet
		return ipv4.String()
	}

	// IPv6: zero last 80 bits (bytes 6-15)
	ipv6 := parsedIP.To16()
	if ipv6 == nil {
		return ""
	}
	for i := 6; i < 16; i++ {
		ipv6[i] = 0
	}
	return ipv6.String()
}

// getCurrentSalt returns the current salt, rotating if necessary.
func (m *Module) getCurrentSalt() string {
	m.saltMu.RLock()
	rotationDuration := time.Duration(m.settings.SaltRotationHours) * time.Hour
	if time.Since(m.settings.SaltCreatedAt) < rotationDuration {
		salt := m.settings.CurrentSalt
		m.saltMu.RUnlock()
		return salt
	}
	m.saltMu.RUnlock()

	// Need to rotate salt
	m.saltMu.Lock()
	defer m.saltMu.Unlock()

	// Double-check after acquiring write lock
	rotationDuration = time.Duration(m.settings.SaltRotationHours) * time.Hour
	if time.Since(m.settings.SaltCreatedAt) < rotationDuration {
		return m.settings.CurrentSalt
	}

	// Generate new salt
	newSalt := generateRandomSalt()
	m.settings.CurrentSalt = newSalt
	m.settings.SaltCreatedAt = timeNow()

	// Save to database (ignore errors, will retry on next request)
	if err := m.saveSalt(newSalt); err != nil {
		m.ctx.Logger.Warn("failed to save rotated salt", "error", err)
	} else {
		m.ctx.Logger.Debug("analytics salt rotated")
	}

	return newSalt
}

// CreateVisitorHash creates an anonymized visitor fingerprint hash.
// The hash combines anonymized IP + user agent + date + salt.
// This allows counting unique visitors per day without tracking across days.
func (m *Module) CreateVisitorHash(ip, userAgent string) string {
	salt := m.getCurrentSalt()
	anonIP := anonymizeIP(ip)
	date := timeNow().Format("2006-01-02")

	hasher := sha256.New()
	hasher.Write([]byte(anonIP + userAgent + date + "visitor" + salt))
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}

// CreateSessionHash creates a session proxy hash for grouping page views.
// Similar to visitor hash but with different suffix for distinct identification.
func (m *Module) CreateSessionHash(ip, userAgent string) string {
	salt := m.getCurrentSalt()
	anonIP := anonymizeIP(ip)
	date := timeNow().Format("2006-01-02")

	hasher := sha256.New()
	hasher.Write([]byte(anonIP + userAgent + date + "session" + salt))
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}
