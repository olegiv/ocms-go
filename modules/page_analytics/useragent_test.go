package page_analytics

import (
	"testing"
)

func TestParseUserAgent_Browsers(t *testing.T) {
	tests := []struct {
		name            string
		userAgent       string
		expectedBrowser string
	}{
		{
			name:            "Chrome on Windows",
			userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			expectedBrowser: "Chrome",
		},
		{
			name:            "Firefox on Linux",
			userAgent:       "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0",
			expectedBrowser: "Firefox",
		},
		{
			name:            "Safari on macOS",
			userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
			expectedBrowser: "Safari",
		},
		{
			name:            "Edge on Windows",
			userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
			expectedBrowser: "Edge",
		},
		{
			name:            "curl",
			userAgent:       "curl/8.1.2",
			expectedBrowser: "curl",
		},
		{
			name:            "empty",
			userAgent:       "",
			expectedBrowser: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseUserAgent(tt.userAgent)
			if result.Browser != tt.expectedBrowser {
				t.Errorf("parseUserAgent(%q).Browser = %q, want %q", tt.userAgent, result.Browser, tt.expectedBrowser)
			}
		})
	}
}

func TestParseUserAgent_DeviceTypes(t *testing.T) {
	tests := []struct {
		name               string
		userAgent          string
		expectedDeviceType string
	}{
		{
			name:               "Desktop Chrome",
			userAgent:          "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			expectedDeviceType: "desktop",
		},
		{
			name:               "iPhone Safari",
			userAgent:          "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1",
			expectedDeviceType: "mobile",
		},
		{
			name:               "Android Chrome",
			userAgent:          "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36",
			expectedDeviceType: "mobile",
		},
		{
			name:               "iPad Safari",
			userAgent:          "Mozilla/5.0 (iPad; CPU OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1",
			expectedDeviceType: "tablet",
		},
		{
			name:               "Googlebot",
			userAgent:          "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			expectedDeviceType: "bot",
		},
		{
			name:               "Bingbot",
			userAgent:          "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
			expectedDeviceType: "bot",
		},
		{
			name:               "curl",
			userAgent:          "curl/8.1.2",
			expectedDeviceType: "desktop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseUserAgent(tt.userAgent)
			if result.DeviceType != tt.expectedDeviceType {
				t.Errorf("parseUserAgent(%q).DeviceType = %q, want %q", tt.userAgent, result.DeviceType, tt.expectedDeviceType)
			}
		})
	}
}

func TestParseUserAgent_OS(t *testing.T) {
	tests := []struct {
		name       string
		userAgent  string
		expectedOS string
	}{
		{
			name:       "Windows 10",
			userAgent:  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			expectedOS: "Windows",
		},
		{
			name:       "macOS",
			userAgent:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
			expectedOS: "macOS",
		},
		{
			name:       "Linux",
			userAgent:  "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0",
			expectedOS: "Linux",
		},
		{
			name:       "iOS",
			userAgent:  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1",
			expectedOS: "iOS",
		},
		{
			name:       "Android",
			userAgent:  "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36",
			expectedOS: "Android",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseUserAgent(tt.userAgent)
			if result.OS != tt.expectedOS {
				t.Errorf("parseUserAgent(%q).OS = %q, want %q", tt.userAgent, result.OS, tt.expectedOS)
			}
		})
	}
}

func TestParseUserAgent_EmptyAndUnknown(t *testing.T) {
	// Empty user agent
	result := parseUserAgent("")
	if result.Browser != "Unknown" {
		t.Errorf("parseUserAgent(\"\").Browser = %q, want \"Unknown\"", result.Browser)
	}
	if result.OS != "Unknown" {
		t.Errorf("parseUserAgent(\"\").OS = %q, want \"Unknown\"", result.OS)
	}
	if result.DeviceType != "desktop" {
		t.Errorf("parseUserAgent(\"\").DeviceType = %q, want \"desktop\"", result.DeviceType)
	}

	// Unknown/random string
	result = parseUserAgent("some random string")
	// Should not panic and return sensible defaults
	if result.DeviceType == "" {
		t.Error("parseUserAgent should always return a DeviceType")
	}
}
