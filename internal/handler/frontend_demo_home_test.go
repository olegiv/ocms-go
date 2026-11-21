// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestDemoHomepageWalkthrough(t *testing.T) {
	// Demo mode is configured once at startup.
	mode := os.Getenv("OCMS_TEST_HOME_DEMO")
	if mode == "" {
		for _, mode := range []string{"true", "false"} {
			t.Run("demo="+mode, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestDemoHomepageWalkthrough$")
				cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE="+mode, "OCMS_TEST_HOME_DEMO="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, _ := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	id := createPublishedLanguagePage(t, db, store.DemoArticleSlug, "en", user.ID)
	router := languageAwareListingTestRouter(db, NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil))
	for _, published := range []bool{true, false} {
		if !published {
			if _, err := db.Exec("UPDATE pages SET status = 'draft' WHERE id = ?", id); err != nil {
				t.Fatal(err)
			}
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		body := w.Body.String()
		if w.Code != http.StatusOK || strings.Contains(body, "Open demo admin") != (mode == "true") ||
			strings.Contains(body, "View on GitHub") != (mode == "true") {
			t.Fatalf("demo=%s, published=%t: incorrect homepage actions (HTTP %d)", mode, published, w.Code)
		}
		wantArticle := mode == "true" && published
		if strings.Contains(body, "Read “") != wantArticle {
			t.Fatalf("demo=%s, published=%t: sample article link should be present=%t", mode, published, wantArticle)
		}
	}
}
