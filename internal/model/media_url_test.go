// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaURLEncodesFilename(t *testing.T) {
	tests := []struct {
		name     string
		variant  string
		filename string
		want     string
	}{
		{"spaces", VariantThumbnail, "a photo.jpg", "/uploads/thumbnail/u1/a%20photo.jpg"},
		{"comma", VariantSmall, "New Year, 2021.jpg", "/uploads/small/u1/New%20Year%2C%202021.jpg"},
		{"cyrillic", VariantMedium, "фото.jpg", "/uploads/medium/u1/%D1%84%D0%BE%D1%82%D0%BE.jpg"},
		{"plain", VariantLarge, "plain.jpg", "/uploads/large/u1/plain.jpg"},
		{"original maps to originals", VariantOriginal, "a.jpg", "/uploads/originals/u1/a.jpg"},
		{"empty variant maps to originals", "", "a.jpg", "/uploads/originals/u1/a.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MediaURL(tt.variant, "u1", tt.filename); got != tt.want {
				t.Errorf("MediaURL(%q, u1, %q) = %q, want %q", tt.variant, tt.filename, got, tt.want)
			}
		})
	}
}

// TestMediaURLSurvivesSrcsetEscaping is the test that matters here: it renders
// the URL through html/template in a srcset attribute, exactly as the themes do.
//
// html/template escapes a URL differently per attribute. In src it encodes a
// space for you; in srcset a space terminates the URL candidate, so the entry
// is replaced with "#ZgotmplZ". Browsers prefer srcset over src, so the result
// is a broken image on every listing page — while the file itself is present
// and the src URL alone would have worked.
//
// Bug state: drop url.PathEscape from MediaURL and the spaced and Cyrillic
// cases render "#ZgotmplZ".
func TestMediaURLSurvivesSrcsetEscaping(t *testing.T) {
	tpl := template.Must(template.New("t").Parse(
		`<img src="{{.U}}" srcset="{{.U}} 150w, {{.U}} 400w">`))

	for _, filename := range []string{
		"a photo.jpg",
		"View of seaside resort Sidi Bou Said Tunisia North Africa.jpg",
		"Happy New Year 2021, lettering on the beach.jpg",
		"фото с пробелом.jpg",
		"plain.jpg",
	} {
		t.Run(filename, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tpl.Execute(&buf, map[string]string{
				"U": MediaURL(VariantThumbnail, "u1", filename),
			}); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if strings.Contains(buf.String(), "ZgotmplZ") {
				t.Errorf("srcset was rejected by html/template for %q:\n  %s", filename, buf.String())
			}
		})
	}
}

// TestUploadURLsAreBuiltByHelper stops the pattern from returning through a new
// code path.
//
// Every /uploads/ URL has to go through MediaURL, because the encoding is not
// optional: a hand-rolled fmt.Sprintf with a raw filename produces a URL that
// works in src, silently breaks in srcset, and is only visible as a broken
// image in a browser — never in a test that checks the response body.
//
// Bug state: rebuild any of these with fmt.Sprintf("/uploads/...%s/%s", …) and
// this names the file and line.
func TestUploadURLsAreBuiltByHelper(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	var offenders []string
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "bin", "data", "uploads", "wiki":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The helper itself is the one place allowed to spell the path out.
		if filepath.Base(path) == "media.go" && strings.Contains(path, "internal/model") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil //nolint:nilerr // unparseable files are the compiler's problem
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if !strings.Contains(lit.Value, "/uploads/") {
				return true
			}
			// Two ways to hand-roll the path, both of which skip the encoding:
			//   fmt.Sprintf("/uploads/%s/%s/%s", …)   — a format string, and
			//   "/uploads/" + variant + "/" + uuid    — plain concatenation.
			// Matching only the first is what let the v2 media service drift.
			// A bare "/uploads/" that is not being joined to anything is fine.
			formatted := strings.Contains(lit.Value, "%s/%s")
			concatenated := isOperandOfStringConcat(file, lit)
			if formatted || concatenated {
				offenders = append(offenders, rel+":"+
					itoa(fset.Position(lit.Pos()).Line)+"  "+lit.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("media URLs are built without model.MediaURL, so the filename is not "+
			"percent-encoded and srcset will break:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// isOperandOfStringConcat reports whether lit appears as an operand of a `+`
// expression, i.e. a path being assembled by concatenation rather than by
// model.MediaURL.
func isOperandOfStringConcat(file *ast.File, lit *ast.BasicLit) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || bin.Op != token.ADD {
			return true
		}
		// Walk the whole `+` chain: the literal may sit at any depth.
		ast.Inspect(bin, func(inner ast.Node) bool {
			if inner == lit {
				found = true
			}
			return !found
		})
		return !found
	})
	return found
}

// itoa avoids pulling strconv in for a single conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
