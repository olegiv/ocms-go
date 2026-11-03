// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"errors"
	"testing"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
)

// TestPageWritesTellCredentialFailuresFromStorageFaults checks the three ways
// the acting account can fail a REST or agent page write: a deleted account is
// 401, an account that lost its role is 403, and a failed account read is a
// server error rather than either (2026-11-03 review S12: every lookup error
// was reported as 401 "Account unavailable"). Both write modes are covered:
// the ordinary REST write and the trusted draft write.
//
// Bug state: map every GetUserByID error in authorizeWrite to 401 again and
// the storage-fault cases fail.
func TestPageWritesTellCredentialFailuresFromStorageFaults(t *testing.T) {
	type mode struct {
		name  string
		actor func(writeFixture) v2.Actor
		write func(writeFixture, v2.Actor) error
	}
	modes := []mode{
		{"rest", func(f writeFixture) v2.Actor { return f.human }, func(f writeFixture, a v2.Actor) error {
			_, err := f.svc.Create(context.Background(), a, pages.CreatePageBody{Title: "Probe", Slug: "probe"})
			return err
		}},
		{"draft", func(f writeFixture) v2.Actor { return f.actor }, func(f writeFixture, a v2.Actor) error {
			_, err := f.svc.CreateWith(context.Background(), a, pages.CreatePageBody{Title: "Probe", Slug: "probe"}, draftOptions())
			return err
		}},
	}
	cases := []struct {
		name    string
		breakIt func(*testing.T, writeFixture)
		want    v2.ErrorKind
	}{
		{"storage fault", func(t *testing.T, f writeFixture) {
			t.Helper()
			if _, err := f.db.Exec(`ALTER TABLE users RENAME TO users_away`); err != nil {
				t.Fatal(err)
			}
		}, v2.ErrInternal},
		{"deleted account", func(t *testing.T, f writeFixture) {
			t.Helper()
			if _, err := f.db.Exec(`DELETE FROM api_keys`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`DELETE FROM users`); err != nil {
				t.Fatal(err)
			}
		}, v2.ErrUnauthorized},
		{"demoted account", func(t *testing.T, f writeFixture) {
			t.Helper()
			if _, err := f.db.Exec(`UPDATE users SET role = ?`, model.RolePublic); err != nil {
				t.Fatal(err)
			}
		}, v2.ErrForbidden},
	}
	for _, m := range modes {
		for _, tc := range cases {
			t.Run(m.name+"/"+tc.name, func(t *testing.T) {
				f := setupWrites(t)
				actor := m.actor(f)
				tc.breakIt(t, f)
				err := m.write(f, actor)
				var domain *v2.Error
				if !errors.As(err, &domain) || domain.Kind != tc.want {
					t.Fatalf("write returned %v, want a %v error", err, tc.want)
				}
			})
		}
	}
}
