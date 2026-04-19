// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestOpenAPISurface asserts every expected /api/v2 path is registered. If a
// domain operation is dropped or its path renamed, this test breaks — clients
// consuming the OpenAPI doc get the same visibility we do.
func TestOpenAPISurface(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	r := chi.NewRouter()
	queries := store.New(db)
	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})

	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries))

	want := []string{
		"/auth",
		"/categories",
		"/categories/{id}",
		"/media",
		"/media/batch",
		"/media/{id}",
		"/pages",
		"/pages/slug/{slug}",
		"/pages/{id}",
		"/status",
		"/tags",
		"/tags/{id}",
	}

	got := make([]string, 0, len(h.OpenAPI().Paths))
	for p := range h.OpenAPI().Paths {
		got = append(got, p)
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OpenAPI paths drifted:\n  want: %v\n  got:  %v", want, got)
	}
}

// TestV2DoesNotImportV1 enforces the architectural rule that /api/v2 is a fresh
// build. Re-introducing the deleted internal/handler/api package or importing
// any of its types anywhere under internal/api/v2 would be a silent regression
// that this test catches early. Parses real import statements so comments and
// test strings that mention the v1 path are ignored.
func TestV2DoesNotImportV1(t *testing.T) {
	pkg, err := build.Default.Import("github.com/olegiv/ocms-go/internal/api/v2", "", build.FindOnly)
	if err != nil {
		t.Fatalf("locate v2 package: %v", err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(pkg.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "internal/handler/api") {
				rel, _ := filepath.Rel(pkg.Dir, path)
				t.Errorf("%s imports forbidden v1 package %s", rel, imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk v2 tree: %v", err)
	}
}
