// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/ai/draft"
)

// batchResolver is a resolver that can prepare its link facts once per run.
type batchResolver struct {
	*fixtureResolver
	factoryCalls int
	checked      []string
}

func (b *batchResolver) LinkChecker(context.Context) (func(context.Context, string) (bool, error), error) {
	b.factoryCalls++
	return func(_ context.Context, path string) (bool, error) {
		b.checked = append(b.checked, path)
		return path == "/existing", nil
	}, nil
}

// Fix invariant: the per-run link facts are prepared once and lazily, never per
// link. Bug state: calling resolver.InternalLink for every internal link.
func TestInternalLinksUseOneCheckerPerRunAndOnlyWhenNeeded(t *testing.T) {
	opt, fixture := options()
	resolver := &batchResolver{fixtureResolver: fixture}
	opt.Resolver = resolver
	var body strings.Builder
	for i := range 40 {
		fmt.Fprintf(&body, "[Link %d](/page-%d) ", i, i)
	}
	body.WriteString("[Known](/existing)")
	result, err := draft.RenderMarkdown(context.Background(), body.String(), opt)
	if err != nil || len(result.Links) != 41 {
		t.Fatalf("render: %d links, %v", len(result.Links), err)
	}
	if resolver.factoryCalls != 1 || len(resolver.checked) != 41 || len(fixture.linkCalls) != 0 {
		t.Fatalf("factory calls %d, checker calls %d, per-link resolver calls %d", resolver.factoryCalls, len(resolver.checked), len(fixture.linkCalls))
	}
	if !has(result.Issues, "internal_link_broken") || result.Links[40].Status != "existing" || result.Links[0].Status != "unresolved" {
		t.Fatalf("checker verdicts were not applied: %+v", result.Links[40])
	}

	resolver.factoryCalls = 0
	if _, err = draft.RenderMarkdown(context.Background(), "[External](https://other.test/x) and [anchor](#top)", opt); err != nil || resolver.factoryCalls != 0 {
		t.Fatalf("a body without internal links prepared a checker: %d, %v", resolver.factoryCalls, err)
	}
}

type failingBatchResolver struct{ *fixtureResolver }

func (failingBatchResolver) LinkChecker(context.Context) (func(context.Context, string) (bool, error), error) {
	return nil, errors.New("facts unavailable")
}

func TestInternalLinkCheckerFailureIsReported(t *testing.T) {
	opt, fixture := options()
	opt.Resolver = failingBatchResolver{fixtureResolver: fixture}
	if _, err := draft.RenderMarkdown(context.Background(), "[Link](/page)", opt); err == nil || !strings.Contains(err.Error(), "check internal draft link") {
		t.Fatalf("checker preparation failure was swallowed: %v", err)
	}
}
