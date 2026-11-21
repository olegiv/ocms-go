// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestDemoWalkthroughResolvesCurrentPublishedArticle(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	user := createTestAdminUser(t, db)
	h := NewAdminHandler(db, nil, nil, nil)
	checkRedirect := func(want string) {
		t.Helper()
		response := httptest.NewRecorder()
		h.DemoArticle(response, httptest.NewRequest(http.MethodGet, "/admin/demo/article", nil))
		if response.Code != http.StatusFound || response.Header().Get("Location") != want {
			t.Fatalf("got %d redirect to %q, want redirect to %q", response.Code, response.Header().Get("Location"), want)
		}
	}
	checkRedirect("/admin/pages")
	for range 2 {
		id := createPublishedLanguagePage(t, db, store.DemoArticleSlug, "en", user.ID)
		checkRedirect(fmt.Sprintf("/admin/pages/%d", id))
		if _, err := db.Exec("DELETE FROM pages WHERE id = ?", id); err != nil {
			t.Fatal(err)
		}
	}
}
