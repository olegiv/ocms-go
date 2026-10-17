// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestCheckFieldLock checks the rule AssertFieldAccessHoldsLock enforces on
// small sources: an access must follow a Lock or RLock of the mutex in the
// same function, and a closure must take the lock itself.
func TestCheckFieldLock(t *testing.T) {
	for name, tc := range map[string]struct {
		body      string
		unguarded int
	}{
		"lock first": {`m.settingsMu.RLock()
			defer m.settingsMu.RUnlock()
			return m.settings`, 0},
		"no lock": {`return m.settings`, 1},
		"access before the lock": {`s := m.settings
			m.settingsMu.RLock()
			defer m.settingsMu.RUnlock()
			return s`, 1},
		"another mutex": {`m.otherMu.Lock()
			return m.settings`, 1},
		"closure without its own lock": {`m.settingsMu.Lock()
			go func() { _ = m.settings }()
			return nil`, 1},
		"closure with its own lock": {`go func() {
				m.settingsMu.Lock()
				defer m.settingsMu.Unlock()
				_ = m.settings
			}()
			return nil`, 0},
	} {
		src := "package p\nfunc (m *M) f() any {\n" + tc.body + "\n}\n"
		file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
		if err != nil {
			t.Fatalf("%s: parsing: %v", name, err)
		}
		accesses, unguarded := 0, 0
		checkFieldLock(file.Decls[0].(*ast.FuncDecl).Body, "settings", "settingsMu", &accesses,
			func(token.Pos) { unguarded++ })
		if accesses != 1 || unguarded != tc.unguarded {
			t.Errorf("%s: accesses=%d unguarded=%d, want 1 and %d", name, accesses, unguarded, tc.unguarded)
		}
	}
}
