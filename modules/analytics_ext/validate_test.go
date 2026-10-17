// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestValidateTrackerFormats(t *testing.T) {
	for name, tc := range map[string]struct {
		settings Settings
		want     []string
	}{
		"empty values pass":  {Settings{}, nil},
		"documented formats": {Settings{GA4MeasurementID: "G-ABC123XYZ0", GTMContainerID: "GTM-A1B2C3", MatomoURL: "https://matomo.example.com/", MatomoSiteID: "42"}, nil},
		"ga4 lowercase":      {Settings{GA4MeasurementID: "g-abc123"}, []string{errGA4IDInvalid}},
		"ga4 quote":          {Settings{GA4MeasurementID: "G-X');alert(1)//"}, []string{errGA4IDInvalid}},
		"gtm missing prefix": {Settings{GTMContainerID: "ABC1234"}, []string{errGTMIDInvalid}},
		"matomo scheme":      {Settings{MatomoURL: "javascript:alert(1)"}, []string{errMatomoURLInvalid}},
		"matomo relative":    {Settings{MatomoURL: "/matomo/"}, []string{errMatomoURLInvalid}},
		"matomo query":       {Settings{MatomoURL: "https://matomo.example.com/?x=1"}, []string{errMatomoURLInvalid}},
		"matomo credentials": {Settings{MatomoURL: "https://user:pw@matomo.example.com/"}, []string{errMatomoURLInvalid}},
		"matomo quote":       {Settings{MatomoURL: `https://matomo.example.com/");alert(1)//`}, []string{errMatomoURLInvalid}},
		"matomo site id":     {Settings{MatomoSiteID: "1'"}, []string{errMatomoSiteIDInvalid}},
		// The origin is written into the CSP: a wildcard would allow every
		// host, and ";" or "," would end the directive or the whole policy.
		"matomo wildcard host":      {Settings{MatomoURL: "https://*/"}, []string{errMatomoURLInvalid}},
		"matomo wildcard subdomain": {Settings{MatomoURL: "https://*.example.com/"}, []string{errMatomoURLInvalid}},
		"matomo directive break":    {Settings{MatomoURL: "https://a.example;sandbox/"}, []string{errMatomoURLInvalid}},
		"matomo policy break":       {Settings{MatomoURL: "https://a.example,script-src/"}, []string{errMatomoURLInvalid}},
		"matomo port out of range":  {Settings{MatomoURL: "https://a.example:99999/"}, []string{errMatomoURLInvalid}},
		"matomo empty port":         {Settings{MatomoURL: "https://a.example:/"}, []string{errMatomoURLInvalid}},
		// CSP host sources cannot name an IPv6 address, so the browser would
		// block matomo.js after a successful save.
		"matomo ipv6 host":      {Settings{MatomoURL: "https://[2001:db8::1]/"}, []string{errMatomoURLInvalid}},
		"matomo host with port": {Settings{MatomoURL: "https://stats.example.com:8443/matomo/"}, nil},
		"matomo ip host":        {Settings{MatomoURL: "http://192.168.1.10/"}, nil},
		"disabled still checked": {
			Settings{GA4Enabled: false, GA4MeasurementID: "bad", GTMEnabled: false, GTMContainerID: "bad"},
			[]string{errGA4IDInvalid, errGTMIDInvalid},
		},
	} {
		if got := validateTrackerFormats(&tc.settings); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: validateTrackerFormats = %v, want %v", name, got, tc.want)
		}
		// Every accepted URL must yield an origin the CSP keeps, or the
		// tracker saves fine and is then blocked in the browser.
		if raw := tc.settings.MatomoURL; raw != "" && validMatomoURL(raw) && !middleware.IsCSPHostSource(matomoOrigin(raw)) {
			t.Errorf("%s: %q is accepted but its origin %q is not a CSP host source", name, raw, matomoOrigin(raw))
		}
	}
}

// TestHandleSaveSettings_RejectsMalformedIDs fails when the save handler
// stores a tracker identifier that is not in its documented format.
func TestHandleSaveSettings_RejectsMalformedIDs(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{
		"ga4_enabled":        {"1"},
		"ga4_measurement_id": {"G-X');alert(1);//"},
		"matomo_enabled":     {"1"},
		"matomo_url":         {"https://matomo.example.com/?evil=1"},
		"matomo_site_id":     {"7"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", w.Code)
	}
	if m.settings.GA4Enabled || m.settings.GA4MeasurementID != "" || m.settings.MatomoURL != "" {
		t.Errorf("malformed values were stored: %+v", m.settings)
	}
	stored, err := loadSettings(m.ctx.DB)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GA4MeasurementID != "" || stored.MatomoURL != "" {
		t.Errorf("malformed values were persisted: %+v", stored)
	}
}
