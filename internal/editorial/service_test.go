// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package editorial_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/editorial"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestSeededGuidanceLanguageOverridesAndAdministratorBoundary(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q, ctx := store.New(db), context.Background()
	now := time.Now()
	admin, err := q.CreateUser(ctx, store.CreateUserParams{Email: "editorial-admin@example.test", Role: "admin", Name: "Admin", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	editor, err := q.CreateUser(ctx, store.CreateUserParams{Email: "editorial-editor@example.test", Role: "editor", Name: "Editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"ru", "de"} {
		if _, err = q.CreateLanguage(ctx, store.CreateLanguageParams{Code: code, Name: code, NativeName: code, IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	service := editorial.New(q)
	guide, err := service.Guide(ctx, "de")
	if err != nil || !guide.Inherited || guide.SourceLanguageCode != "" || guide.Body == "" {
		t.Fatalf("missing default fallback %+v %v", guide, err)
	}
	ru, err := service.Guide(ctx, "ru")
	if err != nil || ru.Inherited || !strings.Contains(ru.Body, "Пишите") {
		t.Fatalf("missing seeded Russian guide %+v %v", ru, err)
	}
	presets, err := service.Presets(ctx, "de")
	if err != nil || len(presets) != 6 || presets[5].Slug != "bot-blog" {
		t.Fatalf("seeded presets %+v %v", presets, err)
	}
	for _, id := range []int64{0, editor.ID} {
		if err := service.SaveGuide(ctx, id, "", "Changed policy"); !errors.Is(err, editorial.ErrForbidden) {
			t.Fatalf("non-admin changed guidance: %v", err)
		}
	}
	if err := service.SaveGuide(ctx, admin.ID, "de", "Eine lokale Richtlinie."); err != nil {
		t.Fatal(err)
	}
	guide, err = service.Guide(ctx, "de")
	if err != nil || guide.Inherited || guide.Body != "Eine lokale Richtlinie." {
		t.Fatalf("override not used: %+v %v", guide, err)
	}
	if err := service.SaveGuide(ctx, admin.ID, "de", ""); err != nil {
		t.Fatal(err)
	}
	if guide, err = service.Guide(ctx, "de"); err != nil || !guide.Inherited {
		t.Fatalf("fallback not restored: %+v %v", guide, err)
	}
	if err := service.SavePreset(ctx, admin.ID, "de", "blog-post", "Beitrag", "Eine klare Gliederung."); err != nil {
		t.Fatal(err)
	}
	presets, err = service.Presets(ctx, "de")
	if err != nil || presets[0].Name != "Beitrag" || presets[0].SourceLanguageCode != "de" {
		t.Fatalf("preset override %+v %v", presets, err)
	}
	if err := service.SavePreset(ctx, admin.ID, "de", "blog-post", "", ""); err != nil {
		t.Fatal(err)
	}
	if presets, err = service.Presets(ctx, "de"); err != nil || presets[0].SourceLanguageCode != "" {
		t.Fatalf("preset fallback %+v %v", presets, err)
	}
	for _, tc := range []struct{ language, body string }{{"", ""}, {"", strings.Repeat("a", 8001)}, {"xx", "Unknown language"}} {
		if err := service.SaveGuide(ctx, admin.ID, tc.language, tc.body); !errors.Is(err, editorial.ErrInvalid) {
			t.Fatalf("invalid guide accepted: %v", err)
		}
	}
	if err := service.SavePreset(ctx, admin.ID, "", "unknown", "Unknown", "Instructions"); !errors.Is(err, editorial.ErrInvalid) {
		t.Fatalf("unknown preset accepted: %v", err)
	}
	if _, err = db.Exec("UPDATE users SET role='editor' WHERE id=?", admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.SavePreset(ctx, admin.ID, "", "blog-post", "Changed", "Stale permission"); !errors.Is(err, editorial.ErrForbidden) {
		t.Fatalf("stale administrator accepted: %v", err)
	}
}
