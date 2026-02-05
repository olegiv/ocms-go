// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
)

// buildKlaroConfig generates the Klaro configuration JavaScript.
func (m *Module) buildKlaroConfig() string {
	s := m.settings
	if s == nil {
		return "var klaroConfig = {};"
	}

	var config strings.Builder
	config.WriteString("var klaroConfig = {\n")

	// Version
	config.WriteString("    version: 1,\n")

	// Element ID
	config.WriteString("    elementID: 'klaro',\n")

	// Storage configuration
	config.WriteString("    storageMethod: 'cookie',\n")
	config.WriteString(fmt.Sprintf("    storageName: '%s',\n", template.JSEscapeString(s.CookieName)))
	config.WriteString(fmt.Sprintf("    cookieExpiresAfterDays: %d,\n", s.CookieExpiresDays))

	// Privacy policy URL
	if s.PrivacyPolicyURL != "" {
		config.WriteString(fmt.Sprintf("    privacyPolicy: '%s',\n", template.JSEscapeString(s.PrivacyPolicyURL)))
	}

	// Default consent state (require explicit consent)
	config.WriteString("    default: false,\n")
	config.WriteString("    mustConsent: false,\n")
	config.WriteString("    acceptAll: true,\n")
	config.WriteString("    hideDeclineAll: false,\n")
	config.WriteString("    hideLearnMore: false,\n")

	// Styling based on theme
	if s.Theme == "dark" {
		config.WriteString("    styling: { theme: ['dark'] },\n")
	} else {
		config.WriteString("    styling: { theme: ['light'] },\n")
	}

	// Translations
	config.WriteString("    translations: {\n")
	config.WriteString("        zz: {\n")
	if s.PrivacyPolicyURL != "" {
		config.WriteString(fmt.Sprintf("            privacyPolicyUrl: '%s'\n", template.JSEscapeString(s.PrivacyPolicyURL)))
	}
	config.WriteString("        },\n")
	config.WriteString("        en: {\n")
	config.WriteString("            consentModal: {\n")
	config.WriteString("                title: 'Privacy Settings',\n")
	config.WriteString("                description: 'We use cookies and similar technologies to enhance your experience. You can choose which services you allow below.'\n")
	config.WriteString("            },\n")
	config.WriteString("            purposes: {\n")
	config.WriteString("                analytics: {title: 'Analytics', description: 'Services that help us understand how visitors use our website'},\n")
	config.WriteString("                marketing: {title: 'Marketing', description: 'Services used for targeted advertising and marketing campaigns'},\n")
	config.WriteString("                functional: {title: 'Functional', description: 'Services that provide additional functionality'}\n")
	config.WriteString("            }\n")
	config.WriteString("        }\n")
	config.WriteString("    },\n")

	// Services
	config.WriteString("    services: [\n")
	for i, svc := range s.Services {
		config.WriteString(m.buildServiceConfig(svc))
		if i < len(s.Services)-1 {
			config.WriteString(",")
		}
		config.WriteString("\n")
	}
	config.WriteString("    ]\n")

	config.WriteString("};")
	return config.String()
}

// buildServiceConfig generates the Klaro service configuration for a single service.
func (m *Module) buildServiceConfig(svc Service) string {
	var config strings.Builder

	config.WriteString("        {\n")
	config.WriteString(fmt.Sprintf("            name: '%s',\n", template.JSEscapeString(svc.Name)))
	config.WriteString(fmt.Sprintf("            title: '%s',\n", template.JSEscapeString(svc.Title)))
	config.WriteString(fmt.Sprintf("            description: '%s',\n", template.JSEscapeString(svc.Description)))

	// Purposes
	if len(svc.Purposes) > 0 {
		purposesJSON, _ := json.Marshal(svc.Purposes)
		config.WriteString(fmt.Sprintf("            purposes: %s,\n", string(purposesJSON)))
	}

	// Required and default
	if svc.Required {
		config.WriteString("            required: true,\n")
	} else {
		config.WriteString("            required: false,\n")
	}
	if svc.Default {
		config.WriteString("            default: true,\n")
	} else {
		config.WriteString("            default: false,\n")
	}

	// Cookies (as regex patterns)
	if len(svc.Cookies) > 0 {
		config.WriteString("            cookies: [")
		for i, cookie := range svc.Cookies {
			config.WriteString(fmt.Sprintf("/%s/", cookie.Pattern))
			if i < len(svc.Cookies)-1 {
				config.WriteString(", ")
			}
		}
		config.WriteString("],\n")
	}

	// Google Consent Mode callback
	if svc.GCMConsentType != "" && m.settings.GCMEnabled {
		config.WriteString(m.buildGCMCallback(svc.GCMConsentType))
	}

	config.WriteString("        }")
	return config.String()
}

// buildGCMCallback generates the callback function for Google Consent Mode updates.
func (m *Module) buildGCMCallback(gcmConsentType string) string {
	// Parse comma-separated consent types
	consentTypes := strings.Split(gcmConsentType, ",")
	var updates strings.Builder

	for _, ct := range consentTypes {
		ct = strings.TrimSpace(ct)
		if ct == "" {
			continue
		}
		updates.WriteString(fmt.Sprintf("                    '%s': consent ? 'granted' : 'denied',\n", ct))
	}

	return fmt.Sprintf(`            callback: function(consent, service) {
                if (typeof window.gtag === 'function') {
                    window.gtag('consent', 'update', {
%s                    });
                }
            },
`, updates.String())
}
