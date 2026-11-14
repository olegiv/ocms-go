// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestLogsHonorsJournalWithStaleFile(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, site.Dir, map[string]string{"logs/app.log": "old startup output"})
	original := env.Exec
	env.Exec = func(ctx context.Context, c Cmd) error {
		if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "show" {
			_, err := fmt.Fprintln(c.Stdout, "journal")
			return err
		}
		return original(ctx, c)
	}
	if err := env.Logs(context.Background(), site, 100, false, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env.host.calls, "journalctl -u app@a -n 100 --no-pager") {
		t.Fatalf("journal-configured unit read stale log instead: %v", env.host.calls)
	}
}

func TestLogsUsesConfiguredFileDestination(t *testing.T) {
	for _, kind := range []string{"append", "file", "truncate"} {
		t.Run(kind, func(t *testing.T) {
			env := newTestEnv(t)
			site, _ := env.addSite(t, "a")
			writeFiles(t, site.Dir, map[string]string{"logs/custom.log": "current", "logs/app.log": "stale"})
			path := filepath.Join(site.Dir, "logs", "custom.log")
			env.host.stdout = kind + ":" + path
			if err := env.Logs(context.Background(), site, 5, true, false); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(env.host.calls, "tail -n 5 "+path+" -f") {
				t.Fatalf("wrong backend: %v", env.host.calls)
			}
		})
	}
}
