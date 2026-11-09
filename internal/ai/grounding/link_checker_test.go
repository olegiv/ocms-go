// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package grounding_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/store"
)

// queryCounter counts the statements a resolver sends, by table.
type queryCounter struct {
	store.DBTX
	mu     sync.Mutex
	counts map[string]int
}

func (c *queryCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.mu.Lock()
	for _, table := range []string{"redirects", "languages"} {
		if strings.Contains(query, "FROM "+table+" ") {
			c.counts[table]++
		}
	}
	c.mu.Unlock()
	return c.DBTX.QueryContext(ctx, query, args...)
}

func (c *queryCounter) count(table string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[table]
}

func (c *queryCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.counts)
}

func renderLinks(t *testing.T, f fixture, counter *queryCounter, links int) draft.Rendered {
	t.Helper()
	r, err := grounding.NewResolver(store.New(counter), "https://example.test", grounding.Access{UserID: f.owner, OwnDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := range links {
		fmt.Fprintf(&body, "[Link %d](/missing-%d) ", i, i)
	}
	body.WriteString("[Published](/live) [Moved](/old-live)")
	counter.reset()
	result, err := draft.RenderMarkdown(f.ctx, body.String(), draft.Options{Resolver: r.DraftResolver(), DefaultLanguage: "en", UILanguage: "en", SiteURL: "https://example.test", LinkPolicy: draft.Keep})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Fix invariant: loading redirects and active languages costs one query each
// per draft run, however many internal links the body has. Bug state:
// resolvePath loaded both again for every link.
func TestLinkChecksLoadRedirectsAndLanguagesOncePerRun(t *testing.T) {
	f := seedFixture(t)
	f.page(t, "live", "en", "published", f.owner)
	f.redirect(t, "/old-live", "/live", false)
	counter := &queryCounter{DBTX: f.db, counts: map[string]int{}}

	few := renderLinks(t, f, counter, 3)
	fewRedirects, fewLanguages := counter.count("redirects"), counter.count("languages")
	many := renderLinks(t, f, counter, 120)
	manyRedirects, manyLanguages := counter.count("redirects"), counter.count("languages")

	if fewRedirects != 1 || fewLanguages != 1 || manyRedirects != 1 || manyLanguages != 1 {
		t.Fatalf("site facts loaded per link: 3 links -> redirects %d, languages %d; 120 links -> redirects %d, languages %d", fewRedirects, fewLanguages, manyRedirects, manyLanguages)
	}
	// The checker must keep the per-link verdicts, including through a redirect.
	for _, rendered := range []draft.Rendered{few, many} {
		last := len(rendered.Links)
		if rendered.Links[last-2].Status != "existing" || rendered.Links[last-1].Status != "existing" || rendered.Links[0].Status != "unresolved" {
			t.Fatalf("link verdicts changed: %+v", rendered.Links)
		}
	}
}

func TestLinkCheckerMatchesInternalLink(t *testing.T) {
	f := seedFixture(t)
	f.page(t, "live", "en", "published", f.owner)
	f.redirect(t, "/old-live", "/live", false)
	r := f.resolver(t, grounding.Access{UserID: f.owner, OwnDrafts: true})
	check, err := r.LinkChecker(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/live", "/old-live", "/missing", "/ru/live", "/ru", "/search", "/admin/pages"} {
		want, wantErr := r.InternalLink(f.ctx, path)
		got, gotErr := check(f.ctx, path)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Errorf("%s: checker %v %v, InternalLink %v %v", path, got, gotErr, want, wantErr)
		}
	}
}
