// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package account

import (
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/connectedapps"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/views/admin"
)

func TestConnectionsLocalizedDisplayAndPagination(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		pc := &admin.PageContext{AdminLang: lang}
		data := ConnectionsData{BasePath: "/account/connected-apps", Page: connectedapps.Page{Pagination: connectedapps.Pagination{OAuthPage: 2, KeyPage: 1}, OAuthMore: true,
			OAuth: []connectedapps.Connection{{ID: "selector", Kind: "oauth", Name: "<script>untrusted</script>", Host: "client.test", ClientID: "https://client.test/meta", Scopes: "mcp:access", Status: "active", CreatedAt: time.Now()}}}}
		var body strings.Builder
		if err := ConnectionsPanel(pc, data).Render(t.Context(), &body); err != nil {
			t.Fatal(err)
		}
		text := body.String()
		for _, want := range []string{i18n.T(lang, "connected.oauth"), i18n.T(lang, "connected.revoke"), i18n.T(lang, "connected.never_used"), "&lt;script&gt;untrusted&lt;/script&gt;", `action="/account/connected-apps/revoke"`, `name="kind" value="oauth"`, `name="id" value="selector"`, `key_page=1&amp;oauth_page=3`, `key_page=1&amp;oauth_page=1`} {
			if !strings.Contains(text, want) {
				t.Errorf("%s display missing %s", lang, want)
			}
		}
		if strings.Contains(text, "<script>untrusted</script>") {
			t.Fatal("unescaped app name")
		}
	}
}
