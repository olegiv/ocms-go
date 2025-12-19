package page_analytics

import (
	"github.com/mileusna/useragent"
)

// parseUserAgent extracts browser, OS, and device type from a user agent string.
func parseUserAgent(uaString string) ParsedUA {
	ua := useragent.Parse(uaString)

	result := ParsedUA{
		Browser: ua.Name,
		OS:      ua.OS,
	}

	// Handle empty/unknown values
	if result.Browser == "" {
		result.Browser = "Unknown"
	}
	if result.OS == "" {
		result.OS = "Unknown"
	}

	// Determine device type
	switch {
	case ua.Mobile:
		result.DeviceType = "mobile"
	case ua.Tablet:
		result.DeviceType = "tablet"
	case ua.Bot:
		result.DeviceType = "bot"
	default:
		result.DeviceType = "desktop"
	}

	return result
}
