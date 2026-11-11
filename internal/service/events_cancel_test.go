// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// A request that ends while its event is written is not a storage fault: the
// lost write is returned to the caller but not logged at ERROR (T-15/N-4).
// Real storage failures are still logged.
func TestLogEventFailureLoggingIgnoresRequestCancellation(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, tt := range []struct {
		name      string
		prepare   func(t *testing.T, db *sql.DB) context.Context
		wantErr   func(error) bool
		wantError bool
	}{
		{
			name: "canceled",
			prepare: func(t *testing.T, _ *sql.DB) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			wantErr: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
		{
			name: "storage failure",
			prepare: func(t *testing.T, db *sql.DB) context.Context {
				if _, err := db.ExecContext(t.Context(), "DROP TABLE events"); err != nil {
					t.Fatal(err)
				}
				return t.Context()
			},
			wantErr:   func(err error) bool { return err != nil && !errors.Is(err, context.Canceled) },
			wantError: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupEventTestDB(t)
			t.Cleanup(func() { _ = db.Close() })
			svc := NewEventService(db)
			ctx := tt.prepare(t, db)

			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			err := svc.LogEvent(ctx, model.EventLevelInfo, model.EventCategoryMigrator, "Lost event", nil, "192.0.2.1", "/admin", nil)
			if !tt.wantErr(err) {
				t.Fatalf("LogEvent error = %v", err)
			}
			logged := strings.Contains(logs.String(), "level=ERROR") && strings.Contains(logs.String(), "failed to record an event in the Event Log")
			if logged != tt.wantError {
				t.Fatalf("ERROR logged = %v, want %v; logs:\n%s", logged, tt.wantError, logs.String())
			}
		})
	}
}
