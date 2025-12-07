package analytics

import (
	"database/sql"
	"fmt"
	"html/template"
	"strings"
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
		if err == sql.ErrNoRows {
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
func (m *Module) renderHeadScripts() template.HTML {
	if m.settings == nil {
		return ""
	}

	var scripts strings.Builder

	// Google Tag Manager (head script)
	if m.settings.GTMEnabled && m.settings.GTMContainerID != "" {
		scripts.WriteString(fmt.Sprintf(`<!-- Google Tag Manager -->
<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':
new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],
j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src=
'https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);
})(window,document,'script','dataLayer','%s');</script>
<!-- End Google Tag Manager -->
`, template.HTMLEscapeString(m.settings.GTMContainerID)))
	}

	// Google Analytics 4 (only if GTM is not enabled, since GA4 is typically loaded via GTM)
	if m.settings.GA4Enabled && m.settings.GA4MeasurementID != "" && !m.settings.GTMEnabled {
		scripts.WriteString(fmt.Sprintf(`<!-- Google Analytics 4 -->
<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
<script>
  window.dataLayer = window.dataLayer || [];
  function gtag(){dataLayer.push(arguments);}
  gtag('js', new Date());
  gtag('config', '%s');
</script>
<!-- End Google Analytics 4 -->
`, template.HTMLEscapeString(m.settings.GA4MeasurementID),
			template.HTMLEscapeString(m.settings.GA4MeasurementID)))
	}

	// Matomo tracking code (head portion - tracking code)
	if m.settings.MatomoEnabled && m.settings.MatomoURL != "" && m.settings.MatomoSiteID != "" {
		matomoURL := strings.TrimSuffix(m.settings.MatomoURL, "/")
		scripts.WriteString(fmt.Sprintf(`<!-- Matomo -->
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
`, template.HTMLEscapeString(matomoURL),
			template.HTMLEscapeString(m.settings.MatomoSiteID)))
	}

	return template.HTML(scripts.String())
}

// renderBodyScripts generates the tracking scripts for the end of <body>.
func (m *Module) renderBodyScripts() template.HTML {
	if m.settings == nil {
		return ""
	}

	var scripts strings.Builder

	// Google Tag Manager (noscript fallback)
	if m.settings.GTMEnabled && m.settings.GTMContainerID != "" {
		scripts.WriteString(fmt.Sprintf(`<!-- Google Tag Manager (noscript) -->
<noscript><iframe src="https://www.googletagmanager.com/ns.html?id=%s"
height="0" width="0" style="display:none;visibility:hidden"></iframe></noscript>
<!-- End Google Tag Manager (noscript) -->
`, template.HTMLEscapeString(m.settings.GTMContainerID)))
	}

	// Matomo noscript fallback
	if m.settings.MatomoEnabled && m.settings.MatomoURL != "" && m.settings.MatomoSiteID != "" {
		matomoURL := strings.TrimSuffix(m.settings.MatomoURL, "/")
		scripts.WriteString(fmt.Sprintf(`<!-- Matomo Image Tracker -->
<noscript><img referrerpolicy="no-referrer-when-downgrade" src="%s/matomo.php?idsite=%s&amp;rec=1" style="border:0" alt="" /></noscript>
<!-- End Matomo -->
`, template.HTMLEscapeString(matomoURL),
			template.HTMLEscapeString(m.settings.MatomoSiteID)))
	}

	return template.HTML(scripts.String())
}
