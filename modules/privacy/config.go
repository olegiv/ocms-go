// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

// safeCookiePattern matches characters safe for use inside a JS regex literal.
var safeCookiePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-^$.*+?{}()\[\]|]+$`)

// validGCMConsentTypes is the allowlist of Google Consent Mode v2 consent types.
var validGCMConsentTypes = map[string]bool{
	"analytics_storage":       true,
	"ad_storage":              true,
	"ad_user_data":            true,
	"ad_personalization":      true,
	"functionality_storage":   true,
	"personalization_storage": true,
	"security_storage":        true,
}

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
	if s.PrivacyPolicyURL != "" {
		config.WriteString("            privacyPolicy: {text: 'Learn more in our {privacyPolicy}.', name: 'Privacy Policy'},\n")
	}
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
	if s.PrivacyPolicyURL != "" {
		config.WriteString("            privacyPolicy: {text: 'Подробнее в нашей {privacyPolicy}.', name: 'Политике конфиденциальности'},\n")
	}
	config.WriteString("            consentModal: {\n")
	config.WriteString("                title: 'Настройки конфиденциальности',\n")
	config.WriteString("                description: 'Мы используем cookie и аналогичные технологии. Вы можете выбрать, какие сервисы разрешить.'\n")
	config.WriteString("            },\n")
	config.WriteString("            consentNotice: {\n")
	config.WriteString("                description: 'Мы используем cookie и аналогичные технологии.',\n")
	config.WriteString("                changeDescription: 'Настройки cookie'\n")
	config.WriteString("            },\n")
	config.WriteString("            ok: 'Принять',\n")
	config.WriteString("            decline: 'Отклонить',\n")
	config.WriteString("            acceptAll: 'Принять все',\n")
	config.WriteString("            acceptSelected: 'Принять выбранные',\n")
	config.WriteString("            save: 'Сохранить',\n")
	config.WriteString("            close: 'Закрыть',\n")
	config.WriteString("            purposeItem: {\n")
	config.WriteString("                service: 'сервис',\n")
	config.WriteString("                services: 'сервисов'\n")
	config.WriteString("            },\n")
	config.WriteString("            purposes: {\n")
	config.WriteString("                essential: {title: 'Обязательные', description: 'Сессия, язык, согласие на cookie. Нельзя отключить.'},\n")
	config.WriteString("                analytics: {title: 'Аналитика', description: 'Сервисы сбора анонимной статистики'},\n")
	config.WriteString("                marketing: {title: 'Маркетинг', description: 'Сервисы отслеживания конверсий и рекламы'},\n")
	config.WriteString("                functional: {title: 'Функциональные', description: 'Дополнительные сервисы'}\n")
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

	// Cookies (as regex patterns — only safe characters allowed)
	if len(svc.Cookies) > 0 {
		config.WriteString("            cookies: [")
		first := true
		for _, cookie := range svc.Cookies {
			if !safeCookiePattern.MatchString(cookie.Pattern) {
				continue // skip invalid patterns
			}
			if !first {
				config.WriteString(", ")
			}
			config.WriteString(fmt.Sprintf("/%s/", cookie.Pattern))
			first = false
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
		if ct == "" || !validGCMConsentTypes[ct] {
			continue // skip empty or unrecognized consent types
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
