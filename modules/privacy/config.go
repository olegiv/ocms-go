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
	config.WriteString("            consentNotice: {\n")
	config.WriteString("                description: 'We use cookies and similar technologies to enhance your experience.',\n")
	config.WriteString("                changeDescription: 'Cookie Settings'\n")
	config.WriteString("            },\n")
	config.WriteString("            purposes: {\n")
	config.WriteString("                essential: {title: 'Essential', description: 'Login session, language preference, cookie consent, and notification settings. These cookies cannot be disabled.'},\n")
	config.WriteString("                analytics: {title: 'Analytics', description: 'Services that collect anonymous usage data to help us improve the website experience'},\n")
	config.WriteString("                marketing: {title: 'Marketing', description: 'Services used for conversion tracking and personalized advertising'},\n")
	config.WriteString("                functional: {title: 'Functional', description: 'Additional services such as tag management that enhance website capabilities'}\n")
	config.WriteString("            }\n")
	config.WriteString("        },\n")
	config.WriteString("        ru: {\n")
	config.WriteString("            consentModal: {\n")
	config.WriteString("                title: '\\u041D\\u0430\\u0441\\u0442\\u0440\\u043E\\u0439\\u043A\\u0438 \\u043A\\u043E\\u043D\\u0444\\u0438\\u0434\\u0435\\u043D\\u0446\\u0438\\u0430\\u043B\\u044C\\u043D\\u043E\\u0441\\u0442\\u0438',\n")
	config.WriteString("                description: '\\u041C\\u044B \\u0438\\u0441\\u043F\\u043E\\u043B\\u044C\\u0437\\u0443\\u0435\\u043C cookie \\u0438 \\u0430\\u043D\\u0430\\u043B\\u043E\\u0433\\u0438\\u0447\\u043D\\u044B\\u0435 \\u0442\\u0435\\u0445\\u043D\\u043E\\u043B\\u043E\\u0433\\u0438\\u0438. \\u0412\\u044B \\u043C\\u043E\\u0436\\u0435\\u0442\\u0435 \\u0432\\u044B\\u0431\\u0440\\u0430\\u0442\\u044C, \\u043A\\u0430\\u043A\\u0438\\u0435 \\u0441\\u0435\\u0440\\u0432\\u0438\\u0441\\u044B \\u0440\\u0430\\u0437\\u0440\\u0435\\u0448\\u0438\\u0442\\u044C.'\n")
	config.WriteString("            },\n")
	config.WriteString("            consentNotice: {\n")
	config.WriteString("                description: '\\u041C\\u044B \\u0438\\u0441\\u043F\\u043E\\u043B\\u044C\\u0437\\u0443\\u0435\\u043C cookie \\u0438 \\u0430\\u043D\\u0430\\u043B\\u043E\\u0433\\u0438\\u0447\\u043D\\u044B\\u0435 \\u0442\\u0435\\u0445\\u043D\\u043E\\u043B\\u043E\\u0433\\u0438\\u0438.',\n")
	config.WriteString("                changeDescription: '\\u041D\\u0430\\u0441\\u0442\\u0440\\u043E\\u0439\\u043A\\u0438 cookie'\n")
	config.WriteString("            },\n")
	config.WriteString("            ok: '\\u041F\\u0440\\u0438\\u043D\\u044F\\u0442\\u044C',\n")
	config.WriteString("            decline: '\\u041E\\u0442\\u043A\\u043B\\u043E\\u043D\\u0438\\u0442\\u044C',\n")
	config.WriteString("            acceptAll: '\\u041F\\u0440\\u0438\\u043D\\u044F\\u0442\\u044C \\u0432\\u0441\\u0435',\n")
	config.WriteString("            acceptSelected: '\\u041F\\u0440\\u0438\\u043D\\u044F\\u0442\\u044C \\u0432\\u044B\\u0431\\u0440\\u0430\\u043D\\u043D\\u044B\\u0435',\n")
	config.WriteString("            save: '\\u0421\\u043E\\u0445\\u0440\\u0430\\u043D\\u0438\\u0442\\u044C',\n")
	config.WriteString("            close: '\\u0417\\u0430\\u043A\\u0440\\u044B\\u0442\\u044C',\n")
	config.WriteString("            purposeItem: {\n")
	config.WriteString("                service: '\\u0441\\u0435\\u0440\\u0432\\u0438\\u0441',\n")
	config.WriteString("                services: '\\u0441\\u0435\\u0440\\u0432\\u0438\\u0441\\u043E\\u0432'\n")
	config.WriteString("            },\n")
	config.WriteString("            purposes: {\n")
	config.WriteString("                essential: {title: '\\u041E\\u0431\\u044F\\u0437\\u0430\\u0442\\u0435\\u043B\\u044C\\u043D\\u044B\\u0435', description: '\\u0421\\u0435\\u0441\\u0441\\u0438\\u044F, \\u044F\\u0437\\u044B\\u043A, \\u0441\\u043E\\u0433\\u043B\\u0430\\u0441\\u0438\\u0435 \\u043D\\u0430 cookie. \\u041D\\u0435\\u043B\\u044C\\u0437\\u044F \\u043E\\u0442\\u043A\\u043B\\u044E\\u0447\\u0438\\u0442\\u044C.'},\n")
	config.WriteString("                analytics: {title: '\\u0410\\u043D\\u0430\\u043B\\u0438\\u0442\\u0438\\u043A\\u0430', description: '\\u0421\\u0435\\u0440\\u0432\\u0438\\u0441\\u044B \\u0441\\u0431\\u043E\\u0440\\u0430 \\u0430\\u043D\\u043E\\u043D\\u0438\\u043C\\u043D\\u043E\\u0439 \\u0441\\u0442\\u0430\\u0442\\u0438\\u0441\\u0442\\u0438\\u043A\\u0438'},\n")
	config.WriteString("                marketing: {title: '\\u041C\\u0430\\u0440\\u043A\\u0435\\u0442\\u0438\\u043D\\u0433', description: '\\u0421\\u0435\\u0440\\u0432\\u0438\\u0441\\u044B \\u043E\\u0442\\u0441\\u043B\\u0435\\u0436\\u0438\\u0432\\u0430\\u043D\\u0438\\u044F \\u043A\\u043E\\u043D\\u0432\\u0435\\u0440\\u0441\\u0438\\u0439 \\u0438 \\u0440\\u0435\\u043A\\u043B\\u0430\\u043C\\u044B'},\n")
	config.WriteString("                functional: {title: '\\u0424\\u0443\\u043D\\u043A\\u0446\\u0438\\u043E\\u043D\\u0430\\u043B\\u044C\\u043D\\u044B\\u0435', description: '\\u0414\\u043E\\u043F\\u043E\\u043B\\u043D\\u0438\\u0442\\u0435\\u043B\\u044C\\u043D\\u044B\\u0435 \\u0441\\u0435\\u0440\\u0432\\u0438\\u0441\\u044B'}\n")
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
