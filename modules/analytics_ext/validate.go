// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"net/url"
	"regexp"
)

// Tracker identifiers are rendered into inline scripts on every public page,
// so they are accepted only in their documented shapes. The patterns are
// deliberately wider than today's vendor formats (GA4 "G-" plus ten
// characters, GTM "GTM-" plus five to eight) so a vendor change does not lock
// administrators out, while still excluding every quote, bracket and slash.
var (
	ga4MeasurementIDPattern = regexp.MustCompile(`^G-[A-Z0-9]{4,20}$`)
	gtmContainerIDPattern   = regexp.MustCompile(`^GTM-[A-Z0-9]{4,12}$`)
	matomoSiteIDPattern     = regexp.MustCompile(`^[0-9]{1,10}$`)
)

// Message keys reported by validateTrackerFormats.
const (
	errGA4IDInvalid        = "analytics_ext.error_ga4_id_invalid"
	errGTMIDInvalid        = "analytics_ext.error_gtm_id_invalid"
	errMatomoURLInvalid    = "analytics_ext.error_matomo_url_invalid"
	errMatomoSiteIDInvalid = "analytics_ext.error_matomo_site_id_invalid"
)

// validateTrackerFormats returns the message keys of every non-empty
// identifier that is not in its documented format. Values are checked whether
// or not their tracker is enabled, so a malformed value cannot be stored while
// disabled and injected later by a single toggle.
func validateTrackerFormats(s *Settings) []string {
	var keys []string
	if s.GA4MeasurementID != "" && !validGA4MeasurementID(s.GA4MeasurementID) {
		keys = append(keys, errGA4IDInvalid)
	}
	if s.GTMContainerID != "" && !validGTMContainerID(s.GTMContainerID) {
		keys = append(keys, errGTMIDInvalid)
	}
	if s.MatomoURL != "" && !validMatomoURL(s.MatomoURL) {
		keys = append(keys, errMatomoURLInvalid)
	}
	if s.MatomoSiteID != "" && !validMatomoSiteID(s.MatomoSiteID) {
		keys = append(keys, errMatomoSiteIDInvalid)
	}
	return keys
}

func validGA4MeasurementID(id string) bool { return ga4MeasurementIDPattern.MatchString(id) }

func validGTMContainerID(id string) bool { return gtmContainerIDPattern.MatchString(id) }

func validMatomoSiteID(id string) bool { return matomoSiteIDPattern.MatchString(id) }

// validMatomoURL accepts an absolute http(s) URL with a host and no
// credentials, query or fragment: the value is concatenated with
// "/matomo.php" and "/matomo.js" inside the tracking script.
func validMatomoURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	for _, c := range raw {
		if c < 0x21 || c > 0x7e || c == '"' || c == '\'' || c == '<' || c == '>' || c == '\\' {
			return false
		}
	}
	return true
}
