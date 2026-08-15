// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

// secretConfigFields are the config.Config fields whose values are credentials.
// A log record carrying one of these writes the secret in clear text to stdout,
// to OCMS_ERROR_LOG_PATH, and to every log shipper downstream of them.
var secretConfigFields = map[string]bool{
	"SessionSecret":     true,
	"HCaptchaSecretKey": true,
	"EmbedProxyToken":   true,
}

// nonSecretConfigFields are fields whose names match secretNamePattern but hold
// no credential, so TestSecretConfigFieldsAreClassified accepts them unguarded.
// RequireEmbedProxyToken is a bool policy switch, not the token itself.
var nonSecretConfigFields = map[string]bool{
	"RequireEmbedProxyToken": true,
}

// secretNamePattern matches field names that read as carrying a credential.
// "key" is deliberately absent: it matches policy knobs such as the former
// APIKeyMaxTTLDays (an int day count), which is exactly the over-match that
// made CodeQL raise false-positive alerts 40 and 41 against this repository.
var secretNamePattern = regexp.MustCompile(`(?i)(secret|password|passwd|token|credential|privatekey)`)

// slogVerbs are the slog call names that write their arguments into a record.
// Matched on the method name alone so both the package-level slog.Info and a
// *slog.Logger receiver are covered.
var slogVerbs = map[string]bool{
	"Debug":        true,
	"DebugContext": true,
	"Error":        true,
	"ErrorContext": true,
	"Info":         true,
	"InfoContext":  true,
	"Warn":         true,
	"WarnContext":  true,
	"Log":          true,
	"LogAttrs":     true,
	"With":         true,
}

// slogAttrConstructors are the slog.Attr builders. Their names are too generic
// to match on their own, so isLogSink only accepts them on the slog package.
var slogAttrConstructors = map[string]bool{
	"Any":      true,
	"Bool":     true,
	"Duration": true,
	"Float64":  true,
	"Group":    true,
	"Int":      true,
	"Int64":    true,
	"String":   true,
	"Time":     true,
	"Uint64":   true,
}

// TestSecretConfigFieldsNeverReachALogCall fails when runtime code passes a
// credential-bearing config.Config field to a logging call.
//
// The invariant is expressed over the source tree rather than at any one call
// site because the risk is not that a specific line regresses, it is that the
// next line written anywhere reaches for cfg.SessionSecret to explain what went
// wrong. Only non-test files are scanned: config_test.go legitimately asserts
// on secret values, and a test's output is not a production log.
//
// Bug state: add slog.Info("x", "secret", cfg.SessionSecret) to any non-test
// file and this names the file and line.
func TestSecretConfigFieldsNeverReachALogCall(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "wiki", "bin", "data", "uploads":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // unparseable files are the compiler's problem
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isLogSink(call) {
				return true
			}
			for _, arg := range call.Args {
				sel, ok := arg.(*ast.SelectorExpr)
				if !ok || !secretConfigFields[sel.Sel.Name] {
					continue
				}
				pos := fset.Position(sel.Pos())
				rel, rerr := filepath.Rel(repoRoot, pos.Filename)
				if rerr != nil {
					rel = pos.Filename
				}
				offenders = append(offenders,
					rel+":"+strconv.Itoa(pos.Line)+" logs "+sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these logging calls put a credential in clear text into the "+
			"log stream; log a derived fact instead (whether it is set, its "+
			"length, a hash) and never the value:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestSecretConfigFieldsAreClassified fails when a config.Config field whose
// name reads as a credential is neither guarded by secretConfigFields nor
// declared harmless by nonSecretConfigFields, and when a guarded name no longer
// exists on the struct.
//
// Without the first half, adding an OCMS_SMTP_PASSWORD field would silently
// fall outside the guard above. Without the second, renaming SessionSecret
// would leave a stale list and let the guard pass vacuously.
func TestSecretConfigFieldsAreClassified(t *testing.T) {
	typ := reflect.TypeOf(config.Config{})

	matched := 0
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if !secretNamePattern.MatchString(name) {
			continue
		}
		matched++
		if secretConfigFields[name] || nonSecretConfigFields[name] {
			continue
		}
		t.Errorf("config.Config.%s reads as a credential but is classified in "+
			"neither secretConfigFields nor nonSecretConfigFields; add it to "+
			"one so TestSecretConfigFieldsNeverReachALogCall covers it", name)
	}
	if matched == 0 {
		t.Fatal("no config.Config field matched secretNamePattern; the pattern " +
			"or the struct changed and this test would pass vacuously")
	}

	for name := range secretConfigFields {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("secretConfigFields guards %q but config.Config has no such "+
				"field; update the list to the new name", name)
		}
	}
}

// isLogSink reports whether call writes its arguments into a log record, either
// as an slog logging verb or as an slog attribute constructor.
func isLogSink(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if slogVerbs[sel.Sel.Name] {
		return true
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "slog" && slogAttrConstructors[sel.Sel.Name]
}
