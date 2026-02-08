// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strings"
)

// Settings holds the privacy/consent configuration.
type Settings struct {
	Enabled bool
	Debug   bool // Enable console logging for debugging

	// Privacy Policy
	PrivacyPolicyURL string

	// Cookie Configuration
	CookieName        string
	CookieExpiresDays int

	// Appearance
	Theme    string // "light" or "dark"
	Position string // "bottom-left", "bottom-right", "top-left", "top-right"

	// Google Consent Mode v2
	GCMEnabled                  bool
	GCMDefaultAnalytics         bool
	GCMDefaultAdStorage         bool
	GCMDefaultAdUserData        bool
	GCMDefaultAdPersonalization bool
	GCMWaitForUpdate            int // milliseconds

	// Services
	Services []Service
}

// Service represents a consent service configuration.
type Service struct {
	Name           string   `json:"name"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Purposes       []string `json:"purposes"`
	Required       bool     `json:"required"`
	Default        bool     `json:"default"`
	Cookies        []Cookie `json:"cookies,omitempty"`
	GCMConsentType string   `json:"gcm_consent_type,omitempty"` // Maps to GCM parameters
}

// Cookie represents a cookie pattern for a service.
type Cookie struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path,omitempty"`
	Domain       string `json:"domain,omitempty"`
	ExpiresAfter string `json:"expiresAfter,omitempty"`
}

// PredefinedServices provides common service configurations.
var PredefinedServices = []Service{
	{
		Name:        "klaro",
		Title:       "Essential Cookies",
		Description: "Stores consent choices and UI preferences (required)",
		Purposes:    []string{"essential"},
		Required:    true,
		Default:     true,
		Cookies: []Cookie{
			{Pattern: "^klaro"},
			{Pattern: "^ocms_lang$"},
			{Pattern: "^(__Host-)?session$"},
			{Pattern: "^ocms_informer_dismissed"},
		},
	},
	{
		Name:           "google-analytics",
		Title:          "Google Analytics",
		Description:    "Website traffic analysis and statistics",
		Purposes:       []string{"analytics"},
		GCMConsentType: "analytics_storage",
		Cookies: []Cookie{
			{Pattern: "^_ga"},
			{Pattern: "^_gid"},
			{Pattern: "^_gat"},
		},
	},
	{
		Name:           "google-ads",
		Title:          "Google Ads",
		Description:    "Conversion tracking and personalized advertising",
		Purposes:       []string{"marketing"},
		GCMConsentType: "ad_storage,ad_user_data,ad_personalization",
		Cookies: []Cookie{
			{Pattern: "^_gcl"},
			{Pattern: "^_gac"},
		},
	},
	{
		Name:        "google-tag-manager",
		Title:       "Google Tag Manager",
		Description: "Tag management system for analytics and marketing tags",
		Purposes:    []string{"functional"},
	},
	{
		Name:        "matomo",
		Title:       "Matomo",
		Description: "Privacy-focused website analytics",
		Purposes:    []string{"analytics"},
		Cookies: []Cookie{
			{Pattern: "^_pk_"},
			{Pattern: "^mtm_"},
		},
	},
}

// loadSettings loads privacy settings from the database.
func loadSettings(db *sql.DB) (*Settings, error) {
	row := db.QueryRow(`
		SELECT enabled, COALESCE(debug, 0), privacy_policy_url, cookie_name, cookie_expires_days,
		       theme, position,
		       gcm_enabled, gcm_default_analytics, gcm_default_ad_storage,
		       gcm_default_ad_user_data, gcm_default_ad_personalization, gcm_wait_for_update,
		       services
		FROM privacy_settings WHERE id = 1
	`)

	s := &Settings{}
	var enabled, debug, gcmEnabled, gcmAnalytics, gcmAdStorage, gcmAdUserData, gcmAdPersonalization int
	var servicesJSON string
	err := row.Scan(
		&enabled, &debug, &s.PrivacyPolicyURL, &s.CookieName, &s.CookieExpiresDays,
		&s.Theme, &s.Position,
		&gcmEnabled, &gcmAnalytics, &gcmAdStorage,
		&gcmAdUserData, &gcmAdPersonalization, &s.GCMWaitForUpdate,
		&servicesJSON,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultSettings(), nil
		}
		return nil, fmt.Errorf("scanning privacy settings: %w", err)
	}

	s.Enabled = enabled == 1
	s.Debug = debug == 1
	s.GCMEnabled = gcmEnabled == 1
	s.GCMDefaultAnalytics = gcmAnalytics == 1
	s.GCMDefaultAdStorage = gcmAdStorage == 1
	s.GCMDefaultAdUserData = gcmAdUserData == 1
	s.GCMDefaultAdPersonalization = gcmAdPersonalization == 1

	// Parse services JSON
	if servicesJSON != "" && servicesJSON != "[]" {
		if err := json.Unmarshal([]byte(servicesJSON), &s.Services); err != nil {
			// Log error but continue with empty services
			s.Services = nil
		}
	}

	return s, nil
}

// saveSettings saves privacy settings to the database.
func saveSettings(db *sql.DB, s *Settings) error {
	enabled := boolToInt(s.Enabled)
	debug := boolToInt(s.Debug)
	gcmEnabled := boolToInt(s.GCMEnabled)
	gcmAnalytics := boolToInt(s.GCMDefaultAnalytics)
	gcmAdStorage := boolToInt(s.GCMDefaultAdStorage)
	gcmAdUserData := boolToInt(s.GCMDefaultAdUserData)
	gcmAdPersonalization := boolToInt(s.GCMDefaultAdPersonalization)

	// Serialize services to JSON
	servicesJSON := "[]"
	if len(s.Services) > 0 {
		data, err := json.Marshal(s.Services)
		if err == nil {
			servicesJSON = string(data)
		}
	}

	_, err := db.Exec(`
		UPDATE privacy_settings SET
			enabled = ?,
			debug = ?,
			privacy_policy_url = ?,
			cookie_name = ?,
			cookie_expires_days = ?,
			theme = ?,
			position = ?,
			gcm_enabled = ?,
			gcm_default_analytics = ?,
			gcm_default_ad_storage = ?,
			gcm_default_ad_user_data = ?,
			gcm_default_ad_personalization = ?,
			gcm_wait_for_update = ?,
			services = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, enabled, debug, s.PrivacyPolicyURL, s.CookieName, s.CookieExpiresDays,
		s.Theme, s.Position,
		gcmEnabled, gcmAnalytics, gcmAdStorage, gcmAdUserData, gcmAdPersonalization, s.GCMWaitForUpdate,
		servicesJSON,
	)
	return err
}

// defaultSettings returns default privacy settings.
func defaultSettings() *Settings {
	return &Settings{
		CookieName:        "klaro",
		CookieExpiresDays: 365,
		Theme:             "light",
		Position:          "bottom-right",
		GCMEnabled:        true,
		GCMWaitForUpdate:  500,
	}
}

// boolToInt converts a boolean to an integer (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// renderHeadScripts generates privacy/consent scripts for the <head> section.
// This MUST be called BEFORE analytics scripts for proper GCM initialization.
func (m *Module) renderHeadScripts() template.HTML {
	if m.settings == nil || !m.settings.Enabled {
		return ""
	}

	var scripts strings.Builder

	// 1. Debug script FIRST (if enabled) - must intercept gtag before it's called
	if m.settings.Debug {
		scripts.WriteString(m.renderDebugScript())
	}

	// 2. Google Consent Mode v2 defaults (before any gtag calls)
	if m.settings.GCMEnabled {
		scripts.WriteString(m.renderGCMDefaults())
	}

	// 3. Klaro CSS
	scripts.WriteString(`<link rel="stylesheet" href="/static/dist/css/klaro.css">
`)

	// 4. Position CSS overrides
	positionCSS := m.getPositionCSS()
	if positionCSS != "" {
		scripts.WriteString(fmt.Sprintf("<style>\n%s</style>\n", positionCSS))
	}

	// 5. Klaro configuration
	scripts.WriteString(fmt.Sprintf(`<script>
%s
</script>
`, m.buildKlaroConfig()))

	// 6. Klaro script
	scripts.WriteString(`<script defer src="/static/dist/js/klaro.min.js"></script>
`)

	return template.HTML(scripts.String())
}

// getPositionCSS returns CSS for positioning the Klaro notice.
func (m *Module) getPositionCSS() string {
	if m.settings == nil {
		return ""
	}

	// Base responsive CSS for mobile
	css := `
@media (max-width: 1023px) {
  .klaro .cookie-notice:not(.cookie-modal-notice) {
    left: 10px !important;
    right: 10px !important;
    bottom: 90px !important;
    width: calc(100% - 20px) !important;
    max-width: calc(100% - 20px) !important;
    box-sizing: border-box !important;
  }
  .klaro .cookie-notice:not(.cookie-modal-notice) .cn-body .cn-buttons {
    flex-wrap: wrap !important;
    gap: 0.5em !important;
  }
}
`

	// Add position-specific CSS
	switch m.settings.Position {
	case "bottom-left":
		css += `.klaro .cookie-notice { right: auto !important; left: 20px !important; }`
	case "top-right":
		css += `.klaro .cookie-notice { bottom: auto !important; top: 20px !important; }`
	case "top-left":
		css += `.klaro .cookie-notice { bottom: auto !important; top: 20px !important; right: auto !important; left: 20px !important; }`
	}

	return css
}

// renderDebugScript returns JavaScript for debugging Klaro consent events.
func (m *Module) renderDebugScript() string {
	return `<script>
(function() {
    console.log('%c[GCM Debug] Debug mode enabled', 'color: #E91E63; font-weight: bold; font-size: 14px');

    // Intercept gtag function to capture all consent calls
    var origGtag = window.gtag;
    window.gtag = function() {
        var args = Array.prototype.slice.call(arguments);
        if (args[0] === 'consent') {
            console.log('%c[GCM Debug] gtag consent ' + args[1], 'color: #2196F3; font-weight: bold', args[2]);
        }
        if (typeof origGtag === 'function') {
            return origGtag.apply(this, arguments);
        } else {
            window.dataLayer = window.dataLayer || [];
            window.dataLayer.push(arguments);
        }
    };

    // Intercept dataLayer.push for klaro events
    var klaroCount = 0;
    var setupDataLayerIntercept = function() {
        var origPush = window.dataLayer && window.dataLayer.push;
        if (origPush && !origPush._gcmDebug) {
            var newPush = function() {
                var arg = arguments[0];
                if (arg && arg.event === 'klaro_update') {
                    console.log('%c[GCM Debug] klaro_update #' + (++klaroCount), 'color: #4CAF50; font-weight: bold', arg);
                }
                return origPush.apply(this, arguments);
            };
            newPush._gcmDebug = true;
            window.dataLayer.push = newPush;
        }
    };
    setupDataLayerIntercept();

    // Watch Klaro manager for consent changes
    window.addEventListener('DOMContentLoaded', function() {
        setupDataLayerIntercept(); // Re-setup after potential overwrites
        if (typeof klaro !== 'undefined' && klaro.getManager) {
            var manager = klaro.getManager();
            if (manager) {
                console.log('%c[GCM Debug] Initial Klaro consents:', 'color: #FF9800; font-weight: bold', manager.consents);
                manager.watch({
                    update: function(mgr, eventType, data) {
                        console.log('%c[GCM Debug] Klaro event:', 'color: #9C27B0; font-weight: bold', eventType, data);
                        console.log('%c[GCM Debug] Current consents:', 'color: #FF9800; font-weight: bold', mgr.consents);
                    }
                });
            }
        }
    });
})();
</script>
`
}

// renderFooterLink returns an HTML link to open the Klaro consent modal.
func (m *Module) renderFooterLink() template.HTML {
	if m.settings == nil || !m.settings.Enabled {
		return ""
	}

	return template.HTML(`<a href="#" onclick="if(typeof klaro!=='undefined')klaro.show();return false;" class="privacy-settings-link">Cookie Settings</a>`)
}

// renderGCMDefaults generates the Google Consent Mode v2 default consent state.
func (m *Module) renderGCMDefaults() string {
	analyticsDefault := "denied"
	if m.settings.GCMDefaultAnalytics {
		analyticsDefault = "granted"
	}
	adStorageDefault := "denied"
	if m.settings.GCMDefaultAdStorage {
		adStorageDefault = "granted"
	}
	adUserDataDefault := "denied"
	if m.settings.GCMDefaultAdUserData {
		adUserDataDefault = "granted"
	}
	adPersonalizationDefault := "denied"
	if m.settings.GCMDefaultAdPersonalization {
		adPersonalizationDefault = "granted"
	}

	return fmt.Sprintf(`<!-- Google Consent Mode v2 Defaults -->
<script>
window.dataLayer = window.dataLayer || [];
if (typeof window.gtag !== 'function') { window.gtag = function(){dataLayer.push(arguments);}; }
gtag('consent', 'default', {
    'ad_storage': '%s',
    'ad_user_data': '%s',
    'ad_personalization': '%s',
    'analytics_storage': '%s',
    'wait_for_update': %d
});
</script>
`, adStorageDefault, adUserDataDefault, adPersonalizationDefault, analyticsDefault, m.settings.GCMWaitForUpdate)
}
