// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"strings"

	"github.com/olegiv/ocms-go/internal/util"
)

// Settings holds the analytics configuration.
type Settings struct {
	GA4Enabled       bool
	GA4MeasurementID string // e.g., "G-XXXXXXXXXX"

	GTMEnabled     bool
	GTMContainerID string // e.g., "GTM-XXXXXXX"

	MatomoEnabled bool
	MatomoURL     string // e.g., "https://matomo.example.com/"
	MatomoSiteID  string // e.g., "1"
}

// loadSettings loads analytics settings from the database.
func loadSettings(db *sql.DB) (*Settings, error) {
	row := db.QueryRow(`
		SELECT ga4_enabled, ga4_measurement_id,
		       gtm_enabled, gtm_container_id,
		       matomo_enabled, matomo_url, matomo_site_id
		FROM analytics_settings WHERE id = 1
	`)

	s := &Settings{}
	var ga4Enabled, gtmEnabled, matomoEnabled int
	err := row.Scan(
		&ga4Enabled, &s.GA4MeasurementID,
		&gtmEnabled, &s.GTMContainerID,
		&matomoEnabled, &s.MatomoURL, &s.MatomoSiteID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Settings{}, nil
		}
		return nil, fmt.Errorf("scanning analytics settings: %w", err)
	}

	s.GA4Enabled = ga4Enabled == 1
	s.GTMEnabled = gtmEnabled == 1
	s.MatomoEnabled = matomoEnabled == 1

	return s, nil
}

// saveSettings saves analytics settings to the database.
func saveSettings(db *sql.DB, s *Settings) error {
	ga4Enabled := 0
	if s.GA4Enabled {
		ga4Enabled = 1
	}
	gtmEnabled := 0
	if s.GTMEnabled {
		gtmEnabled = 1
	}
	matomoEnabled := 0
	if s.MatomoEnabled {
		matomoEnabled = 1
	}

	_, err := db.Exec(`
		UPDATE analytics_settings SET
			ga4_enabled = ?,
			ga4_measurement_id = ?,
			gtm_enabled = ?,
			gtm_container_id = ?,
			matomo_enabled = ?,
			matomo_url = ?,
			matomo_site_id = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, ga4Enabled, s.GA4MeasurementID,
		gtmEnabled, s.GTMContainerID,
		matomoEnabled, s.MatomoURL, s.MatomoSiteID,
	)
	return err
}

// renderHeadScripts generates the tracking scripts for the <head> section.
// SECURITY: Output is cast to template.HTML. Every admin-controlled value is
// first checked against its documented format (values stored before format
// validation existed are skipped rather than injected), then escaped for the
// context it lands in: template.JSEscapeString inside script string literals
// and template.HTMLEscapeString inside HTML attributes.
func (m *Module) renderHeadScripts(nonce string) template.HTML {
	s := m.currentSettings()
	if s == nil {
		return ""
	}

	var scripts strings.Builder

	// Google Tag Manager (head script)
	if gtmActive(s) {
		fmt.Fprintf(&scripts, `<!-- Google Tag Manager -->
<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':
new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],
j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src=
'https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);
})(window,document,'script','dataLayer','%s');</script>
<!-- End Google Tag Manager -->
`, template.JSEscapeString(s.GTMContainerID))
	}

	// Google Analytics 4 (only if GTM is not enabled, since GA4 is typically loaded via GTM)
	if ga4Active(s) && !s.GTMEnabled {
		fmt.Fprintf(&scripts, `<!-- Google Analytics 4 -->
<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
<script>
  window.dataLayer = window.dataLayer || [];
  if (typeof window.gtag !== 'function') { window.gtag = function(){dataLayer.push(arguments);}; }
  gtag('js', new Date());
  gtag('config', '%s');
</script>
<!-- End Google Analytics 4 -->
`, template.HTMLEscapeString(s.GA4MeasurementID),
			template.JSEscapeString(s.GA4MeasurementID))
	}

	// Matomo tracking code (head portion - tracking code)
	if matomoActive(s) {
		matomoURL := strings.TrimSuffix(s.MatomoURL, "/")
		fmt.Fprintf(&scripts, `<!-- Matomo -->
<script>
  var _paq = window._paq = window._paq || [];
  _paq.push(['trackPageView']);
  _paq.push(['enableLinkTracking']);
  (function() {
    var u="%s/";
    _paq.push(['setTrackerUrl', u+'matomo.php']);
    _paq.push(['setSiteId', '%s']);
    var d=document, g=d.createElement('script'), s=d.getElementsByTagName('script')[0];
    g.async=true; g.src=u+'matomo.js'; s.parentNode.insertBefore(g,s);
  })();
</script>
<!-- End Matomo Code -->
`, template.JSEscapeString(matomoURL),
			template.JSEscapeString(s.MatomoSiteID))
	}

	return template.HTML(util.AddNonceToScriptTags(scripts.String(), nonce))
}

// renderBodyScripts generates the tracking scripts for the end of <body>.
// SECURITY: Same protections as renderHeadScripts; these fragments are HTML
// attributes, so values are HTML-escaped after the format check.
func (m *Module) renderBodyScripts(nonce string) template.HTML {
	s := m.currentSettings()
	if s == nil {
		return ""
	}

	var scripts strings.Builder

	// Google Tag Manager (noscript fallback)
	if gtmActive(s) {
		fmt.Fprintf(&scripts, `<!-- Google Tag Manager (noscript) -->
<noscript><iframe src="https://www.googletagmanager.com/ns.html?id=%s"
height="0" width="0" style="display:none;visibility:hidden"></iframe></noscript>
<!-- End Google Tag Manager (noscript) -->
`, template.HTMLEscapeString(s.GTMContainerID))
	}

	// Matomo noscript fallback
	if matomoActive(s) {
		matomoURL := strings.TrimSuffix(s.MatomoURL, "/")
		fmt.Fprintf(&scripts, `<!-- Matomo Image Tracker -->
<noscript><img referrerpolicy="no-referrer-when-downgrade" src="%s/matomo.php?idsite=%s&amp;rec=1" style="border:0" alt="" /></noscript>
<!-- End Matomo -->
`, template.HTMLEscapeString(matomoURL),
			template.HTMLEscapeString(s.MatomoSiteID))
	}

	return template.HTML(util.AddNonceToScriptTags(scripts.String(), nonce))
}

// gtmActive reports whether GTM is enabled with a well-formed container ID.
func gtmActive(s *Settings) bool {
	return s.GTMEnabled && validGTMContainerID(s.GTMContainerID)
}

// ga4Active reports whether GA4 is enabled with a well-formed measurement ID.
func ga4Active(s *Settings) bool {
	return s.GA4Enabled && validGA4MeasurementID(s.GA4MeasurementID)
}

// matomoActive reports whether Matomo is enabled with a well-formed URL and site ID.
func matomoActive(s *Settings) bool {
	return s.MatomoEnabled && validMatomoURL(s.MatomoURL) && validMatomoSiteID(s.MatomoSiteID)
}
