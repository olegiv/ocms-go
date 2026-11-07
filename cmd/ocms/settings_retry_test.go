// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"modernc.org/sqlite"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
	"github.com/olegiv/ocms-go/modules/analytics_int"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
	"github.com/olegiv/ocms-go/modules/privacy"
)

// TestSettingsRetryPreservesAdminSave pauses a request's retry after SQLite
// has returned the old settings, then submits the real admin form. Both the
// database and the next dashboard must show the saved value. Repeat with a
// reload after recovery, when the save does not have to reload first.
func TestSettingsRetryPreservesAdminSave(t *testing.T) {
	for _, tc := range []struct {
		mod interface {
			module.Module
			ReloadSettings() error
		}
		table, corrupt, repair, get string
		path, savePath, form        string
		stored, rendered            string
	}{
		{
			mod: hcaptcha.New(), table: "hcaptcha_settings",
			corrupt: `UPDATE hcaptcha_settings SET enabled = 'broken', theme = 'light' WHERE id = 1`,
			repair:  `UPDATE hcaptcha_settings SET enabled = 1 WHERE id = 1`,
			get:     `SELECT theme FROM hcaptcha_settings WHERE id = 1`,
			path:    "/hcaptcha", savePath: "/hcaptcha", form: "enabled=1&site_key=fixture-site&secret_key=fixture-secret&theme=dark",
			stored: "dark", rendered: `<option value="dark" selected>`,
		},
		{
			mod: privacy.New(), table: "privacy_settings",
			corrupt: `UPDATE privacy_settings SET cookie_expires_days = 'broken', cookie_name = 'old-cookie' WHERE id = 1`,
			repair:  `UPDATE privacy_settings SET cookie_expires_days = 365 WHERE id = 1`,
			get:     `SELECT cookie_name FROM privacy_settings WHERE id = 1`,
			path:    "/privacy", savePath: "/privacy", form: "enabled=1&cookie_name=new-cookie",
			stored: "new-cookie", rendered: `name="cookie_name" value="new-cookie"`,
		},
		{
			mod: analytics_int.New(), table: "page_analytics_settings",
			corrupt: `UPDATE page_analytics_settings SET salt_created_at = 'broken', retention_days = 30 WHERE id = 1`,
			repair:  `UPDATE page_analytics_settings SET salt_created_at = CURRENT_TIMESTAMP WHERE id = 1`,
			get:     `SELECT retention_days FROM page_analytics_settings WHERE id = 1`,
			path:    "/internal-analytics", savePath: "/internal-analytics/settings", form: "enabled=1&retention_days=365",
			stored: "365", rendered: `name="retention_days" value="365"`,
		},
	} {
		t.Run(tc.mod.Name(), func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)
			moduleutil.RunMigrations(t, db, tc.mod.Migrations())
			if _, err := db.Exec(tc.corrupt); err != nil {
				t.Fatal(err)
			}
			pausedDB, nextRead := openSettingsRetryDB(t, db, tc.table)
			ctx, _ := moduleutil.TestModuleContextWithStore(t, pausedDB)
			if err := tc.mod.Init(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := tc.mod.Shutdown(); err != nil {
					t.Error(err)
				}
			})
			if _, err := db.Exec(tc.repair); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			tc.mod.RegisterAdminRoutes(router)
			request := func(method, path, form string) <-chan *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(form))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				admin := store.User{ID: 1, Email: "admin@example.test", Role: "admin"}
				req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
				result := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					result <- rec
				}()
				return result
			}
			response := func(t *testing.T, result <-chan *httptest.ResponseRecorder, want int) string {
				t.Helper()
				select {
				case rec := <-result:
					if rec.Code != want {
						t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
					}
					return rec.Body.String()
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for a settings response")
					return ""
				}
			}
			run := func(t *testing.T, retryingStartup bool) {
				t.Helper()
				pause := &settingsReadPause{read: make(chan struct{}), release: make(chan struct{})}
				resume := sync.OnceFunc(func() { close(pause.release) })
				defer resume()
				nextRead.Store(pause)
				var finishReload func()
				if retryingStartup {
					retry := request(http.MethodGet, tc.path, "")
					finishReload = func() { response(t, retry, http.StatusOK) }
				} else {
					reload := make(chan error, 1)
					go func() { reload <- tc.mod.ReloadSettings() }()
					finishReload = func() {
						select {
						case err := <-reload:
							if err != nil {
								t.Fatal(err)
							}
						case <-time.After(5 * time.Second):
							t.Fatal("timed out waiting for reload")
						}
					}
				}
				select {
				case <-pause.read:
				case <-time.After(5 * time.Second):
					t.Fatal("settings reload did not read the stored row")
				}
				if retryingStartup {
					if body := response(t, request(http.MethodGet, tc.path, ""), http.StatusOK); !strings.Contains(body, `name="from_fallback"`) {
						t.Error("another dashboard should still render the fallback while the retry is paused")
					}
				}
				save := request(http.MethodPost, tc.savePath, tc.form)
				// Give an overlapping save a chance to finish before the stale
				// read resumes. A serialized save waits for the read to finish.
				select {
				case rec := <-save:
					if rec.Code != http.StatusSeeOther {
						t.Errorf("save status = %d, want %d", rec.Code, http.StatusSeeOther)
					}
					save = nil
				case <-time.After(time.Second):
				}
				resume()
				finishReload()
				if save != nil {
					response(t, save, http.StatusSeeOther)
				}
				var stored string
				if err := db.QueryRow(tc.get).Scan(&stored); err != nil || stored != tc.stored {
					t.Fatalf("persisted setting = %q, %v; want %q", stored, err, tc.stored)
				}
				if body := response(t, request(http.MethodGet, tc.path, ""), http.StatusOK); !strings.Contains(body, tc.rendered) || strings.Contains(body, `name="from_fallback"`) {
					t.Errorf("dashboard lost the saved setting after the delayed reload, want %s", tc.rendered)
				}
			}
			if t.Run("startup retry", func(t *testing.T) { run(t, true) }) {
				t.Run("settings already loaded", func(t *testing.T) {
					// Return the DB row to its old values while leaving the
					// module loaded, so the save skips its recovery reload.
					if _, err := db.Exec(tc.corrupt); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(tc.repair); err != nil {
						t.Fatal(err)
					}
					run(t, false)
				})
			}
		})
	}
}

type settingsReadPause struct {
	read    chan struct{}
	release chan struct{}
}

func openSettingsRetryDB(t *testing.T, db *sql.DB, table string) (*sql.DB, *atomic.Pointer[settingsReadPause]) {
	t.Helper()
	var path string
	if err := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	connector, err := sqlite.NewConnector(path + "?_pragma=busy_timeout(5000)&_time_format=sqlite&_timezone=UTC")
	if err != nil {
		t.Fatal(err)
	}
	nextRead := new(atomic.Pointer[settingsReadPause])
	pausedDB := sql.OpenDB(settingsRetryConnector{Connector: connector, match: "FROM " + table, nextRead: nextRead})
	t.Cleanup(func() {
		if closeErr := pausedDB.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return pausedDB, nextRead
}

type settingsRetryConnector struct {
	driver.Connector
	match    string
	nextRead *atomic.Pointer[settingsReadPause]
}

func (c settingsRetryConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return settingsRetryConn{Conn: conn, match: c.match, nextRead: c.nextRead}, nil
}

type settingsRetryConn struct {
	driver.Conn
	match    string
	nextRead *atomic.Pointer[settingsReadPause]
}

func (c settingsRetryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err == nil && strings.Contains(query, c.match) {
		if pause := c.nextRead.Swap(nil); pause != nil {
			return settingsRetryRows{Rows: rows, pause: pause}, nil
		}
	}
	return rows, err
}

type settingsRetryRows struct {
	driver.Rows
	pause *settingsReadPause
}

func (r settingsRetryRows) Close() error {
	if err := r.Rows.Close(); err != nil {
		return err
	}
	// The values have been scanned, and SQLite has released the cursor.
	// Block only the caller that would publish this snapshot.
	close(r.pause.read)
	<-r.pause.release
	return nil
}
