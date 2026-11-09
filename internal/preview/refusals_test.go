// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestPreviewIssuanceRefusalsIdentifyCause(t *testing.T) {
	t.Run("site URL", func(t *testing.T) {
		f := setup(t)
		principal := provenance.Principal{UserID: f.user.ID, Source: "human"}
		for _, origin := range []string{"", ":bad", "ftp://cms.test", "http://public.test", "https://cms.test/path", "https://private:secret@cms.test", "https://cms.test?secret=value", "https://cms.test?", "https://cms.test#secret"} {
			link, err := f.svc.Issue(f.ctx, principal, f.page.ID, f.page.Revision, origin, nil)
			if !errors.Is(err, preview.ErrSiteURL) || errors.Is(err, preview.ErrUnavailable) || link.URL.Reveal() != "" {
				t.Errorf("site configuration refusal: link issued=%t error=%v", link.URL.Reveal() != "", err)
			}
		}
		var count int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM preview_links").Scan(&count); err != nil || count != 0 {
			t.Fatalf("invalid URL created links: count=%d error=%v", count, err)
		}
		for _, origin := range []string{"https://cms.test", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
			link, err := f.svc.Issue(f.ctx, principal, f.page.ID, f.page.Revision, origin, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.Resolve(f.ctx, token(t, link)); err != nil {
				t.Fatal("normal share cannot be redeemed", err)
			}
		}
		if _, err := f.svc.Issue(f.ctx, principal, f.page.ID, 999, "https://cms.test", nil); !errors.Is(err, preview.ErrUnavailable) {
			t.Errorf("missing revision lost unavailable classification: %v", err)
		}
		testutil.AssertPageRevision(t, f.q, f.page.ID, f.page.Revision)
	})
	for _, scenario := range []string{"active", "hourly", "overfull hourly", "inclusive boundary"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			f.now = f.now.Add(250 * time.Millisecond)
			principal := provenance.Principal{UserID: f.user.ID, Source: "human"}
			issue := func() (preview.Link, error) {
				return f.svc.Issue(f.ctx, principal, f.page.ID, f.page.Revision, "https://cms.test", nil)
			}
			count, wantDelay := preview.MaxIssuedPerHour, int64(2401)
			switch scenario {
			case "active":
				count = preview.MaxActiveLinks
			case "overfull hourly":
				count++
			case "inclusive boundary":
				wantDelay = 1
			}
			for i := range count {
				created := f.now.Unix() - 100
				if i == 0 || scenario == "overfull hourly" && i == 1 {
					created = f.now.Unix() - 1200
				}
				if i == 0 && scenario == "overfull hourly" {
					created = f.now.Unix() - 1800
				}
				if i == 0 && scenario == "inclusive boundary" {
					created = f.now.Unix() - 3600
				}
				_, err := f.q.CreatePreviewLink(f.ctx, store.CreatePreviewLinkParams{TokenHash: fmt.Sprintf("%064d", i), PageID: f.page.ID, Revision: f.page.Revision, CreatedBy: f.user.ID, SessionVersion: f.user.SessionVersion, CreatedAt: created, ExpiresAt: f.now.Add(time.Hour).Unix()})
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "active" {
				f.exec(t, "UPDATE preview_links SET revoked_at=1")
				// Another user's recent issuance must not shift this user's
				// retry cutoff, even though it sorts ahead of every local row.
				if _, err := f.q.CreatePreviewLink(f.ctx, store.CreatePreviewLinkParams{TokenHash: "foreign-quota-fixture", PageID: f.page.ID, Revision: f.page.Revision, CreatedBy: f.foreign.ID, SessionVersion: f.foreign.SessionVersion, CreatedAt: f.now.Unix(), ExpiresAt: f.now.Add(time.Hour).Unix()}); err != nil {
					t.Fatal(err)
				}
				count++
			}
			link, err := issue()
			var limited *preview.RateLimitError
			if scenario == "active" {
				if !errors.Is(err, preview.ErrActiveLimit) || errors.As(err, &limited) {
					t.Errorf("active cap: %v", err)
				}
			} else if !errors.As(err, &limited) || limited.RetryAfterSeconds != wantDelay || errors.Is(err, preview.ErrActiveLimit) {
				t.Errorf("hourly cap: %+v, want delay %d", err, wantDelay)
			}
			if !errors.Is(err, preview.ErrLimit) || link.URL.Reveal() != "" {
				t.Errorf("limit category/empty result changed: issued=%t error=%v", link.URL.Reveal() != "", err)
			}
			var links, events int
			if err = f.db.QueryRow("SELECT (SELECT COUNT(*) FROM preview_links), (SELECT COUNT(*) FROM events WHERE message='Preview share link created')").Scan(&links, &events); err != nil || links != count || events != 0 {
				t.Fatalf("refusal created a link/event: %d/%d %v", links, events, err)
			}
			if scenario == "active" {
				f.exec(t, "UPDATE preview_links SET revoked_at=1 WHERE id=(SELECT MIN(id) FROM preview_links)")
			} else {
				// Only the cutoff query needs this column. Counts still work.
				f.exec(t, "ALTER TABLE preview_links RENAME COLUMN id TO sf13_unavailable")
				_, faultErr := issue()
				f.exec(t, "ALTER TABLE preview_links RENAME COLUMN sf13_unavailable TO id")
				if faultErr == nil || errors.Is(faultErr, preview.ErrLimit) {
					t.Errorf("retry-time lookup fault became a quota refusal: %v", faultErr)
				}
				f.now = f.now.Add(time.Duration(wantDelay-1) * time.Second)
				if _, err = issue(); !errors.As(err, &limited) || limited.RetryAfterSeconds != 1 {
					t.Errorf("issuance window opened too early: %v", err)
				}
				f.now = f.now.Add(time.Second)
			}
			link, err = issue()
			if err != nil {
				t.Fatal("issuance did not recover", err)
			}
			if _, err = f.svc.Resolve(f.ctx, token(t, link)); err != nil {
				t.Fatal("recovered link cannot be redeemed", err)
			}
			testutil.AssertPageRevision(t, f.q, f.page.ID, f.page.Revision)
		})
	}
}

// Scan all URL guards and refusal returns in Issue, rather than an inventory
// of individual input fields. This is a direct AST guard, not dataflow analysis.
func TestPreviewIssuanceRefusalsStaySpecific(t *testing.T) {
	positions := gotoken.NewFileSet()
	parsed, err := parser.ParseFile(positions, "service.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, declaration := range parsed.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "Issue" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("Issue missing; update refusal drift coverage")
	}
	guards := 0
	for node := range ast.Preorder(body) {
		if returned, ok := node.(*ast.ReturnStmt); ok {
			for _, value := range returned.Results {
				if name, named := value.(*ast.Ident); named && name.Name == "ErrLimit" {
					t.Errorf("%s: generic quota refusal loses the recovery action", positions.Position(returned.Pos()))
				}
			}
		}
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			continue
		}
		usesBase := false
		for condition := range ast.Preorder(branch.Cond) {
			if selected, ok := condition.(*ast.SelectorExpr); ok {
				if owner, named := selected.X.(*ast.Ident); named && owner.Name == "base" {
					usesBase = true
				}
			}
		}
		if !usesBase {
			continue
		}
		guards++
		for nested := range ast.Preorder(branch.Body) {
			if name, named := nested.(*ast.Ident); named && name.Name == "ErrUnavailable" {
				t.Errorf("%s: URL validation misreports a missing draft", positions.Position(name.Pos()))
			}
		}
	}
	if guards == 0 {
		t.Fatal("no URL guards found; update refusal drift coverage")
	}
}
