// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	templparser "github.com/a-h/templ/parser/v2"
	"github.com/a-h/templ/parser/v2/visitor"
	"golang.org/x/net/html"
)

// TestTemplatesHaveNoInlineEventHandlers covers static on* attributes in
// templ source (including templ.Attributes literals) and HTML theme
// templates. Nonce CSP blocks them even on elements with a nonce. Use
// addEventListener or a CSP-compatible Alpine directive instead.
func TestTemplatesHaveNoInlineEventHandlers(t *testing.T) {
	root := filepath.Join("..", "..")
	scanned := map[string]int{}
	err := walkRepoFiles(root, func(rel string) bool {
		if strings.Contains("/"+rel, "/testdata/") {
			return false
		}
		switch filepath.Ext(rel) {
		case ".templ":
			return true
		case ".html", ".gohtml", ".tmpl":
			return strings.HasPrefix(rel, "internal/themes/") ||
				strings.HasPrefix(rel, "custom/themes/") ||
				strings.HasPrefix(rel, "web/templates/")
		}
		return false
	}, func(path, rel string) error {
		source, readErr := os.ReadFile(path) // #nosec G304 -- paths come from the repository walker
		if readErr != nil {
			return readErr
		}
		check := checkHTMLInlineHandlers
		kind := "HTML"
		if filepath.Ext(rel) == ".templ" {
			check, kind = checkTemplInlineHandlers, "templ"
		}
		scanned[kind]++
		if checkErr := check(source); checkErr != nil {
			t.Errorf("%s: %v", rel, checkErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned["templ"] == 0 || scanned["HTML"] == 0 {
		t.Fatalf("inline handler sweep missed a template type: %v", scanned)
	}
}

func checkInlineHandler(name string) error {
	if len(name) > 2 && strings.EqualFold(name[:2], "on") {
		return fmt.Errorf("inline event attribute %q is blocked by CSP", name)
	}
	return nil
}

var htmlTemplateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

func checkHTMLInlineHandlers(source []byte) error {
	z := html.NewTokenizer(bytes.NewReader(htmlTemplateAction.ReplaceAll(source, []byte("x"))))
	for {
		switch z.Next() {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return nil
			}
			return z.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			for _, attr := range z.Token().Attr {
				if err := checkInlineHandler(attr.Key); err != nil {
					return err
				}
			}
		}
	}
}

func checkTemplInlineHandlers(source []byte) error {
	file, err := templparser.ParseString(string(source))
	if err != nil {
		return err
	}
	checkKey := func(key templparser.AttributeKey) error {
		if dynamic, ok := key.(templparser.ExpressionAttributeKey); ok {
			name, _ := strconv.Unquote(strings.TrimSpace(dynamic.Expression.Value))
			return checkInlineHandler(name)
		}
		return checkInlineHandler(key.String())
	}
	checkExpression := func(source string) error {
		expr, parseErr := goparser.ParseExpr(source)
		if parseErr != nil {
			return parseErr
		}
		return checkInlineAttributeMaps(expr)
	}

	v := visitor.New()
	v.BoolConstantAttribute = func(attr *templparser.BoolConstantAttribute) error { return checkKey(attr.Key) }
	v.ConstantAttribute = func(attr *templparser.ConstantAttribute) error { return checkKey(attr.Key) }
	v.BoolExpressionAttribute = func(attr *templparser.BoolExpressionAttribute) error { return checkKey(attr.Key) }
	v.ExpressionAttribute = func(attr *templparser.ExpressionAttribute) error { return checkKey(attr.Key) }
	v.SpreadAttributes = func(attr *templparser.SpreadAttributes) error { return checkExpression(attr.Expression.Value) }
	v.CallTemplateExpression = func(node *templparser.CallTemplateExpression) error {
		return checkExpression(node.Expression.Value)
	}
	visitComponent := v.TemplElementExpression
	v.TemplElementExpression = func(node *templparser.TemplElementExpression) error {
		if err := checkExpression(node.Expression.Value); err != nil {
			return err
		}
		return visitComponent(node)
	}
	v.GoCode = func(node *templparser.GoCode) error {
		return checkInlineAttributeCode("func _() {\n" + node.Expression.Value + "\n}")
	}
	v.TemplateFileGoExpression = func(node *templparser.TemplateFileGoExpression) error {
		return checkInlineAttributeCode(node.Expression.Value)
	}
	return file.Visit(v)
}

func checkInlineAttributeCode(source string) error {
	file, err := goparser.ParseFile(token.NewFileSet(), "", "package check\n"+source, 0)
	if err != nil {
		return err
	}
	return checkInlineAttributeMaps(file)
}

func checkInlineAttributeMaps(root ast.Node) error {
	var result error
	ast.Inspect(root, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || result != nil {
			return result == nil
		}
		selector, ok := literal.Type.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Attributes" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "templ" {
			return true
		}
		for _, item := range literal.Elts {
			if pair, ok := item.(*ast.KeyValueExpr); ok {
				if key, ok := pair.Key.(*ast.BasicLit); ok && key.Kind == token.STRING {
					name, _ := strconv.Unquote(key.Value)
					if result = checkInlineHandler(name); result != nil {
						return false
					}
				}
			}
		}
		return true
	})
	return result
}

func TestInlineHandlerSweepSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		templ        bool
		handler      string
	}{
		{"templ literal", `<button onClick="go()">Click</button>`, true, "onClick"},
		{"templ expression", `<button onfocus={ action }>Click</button>`, true, "onfocus"},
		{"templ attribute map", `@Button(templ.Attributes{"onclick": "go()"})`, true, "onclick"},
		{"templ spread", `<button { templ.Attributes{"onfocus": "go()"}... }>Click</button>`, true, "onfocus"},
		{"theme markup", `{{if .Show}}<img src="{{.URL}}" ONERROR="go()">{{end}}`, false, "onerror"},
		{"theme comment and directives", `<!-- <a onclick="go()"> --> <button data-onclick="text" x-on:click="go()">Click</button>`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check, source := checkHTMLInlineHandlers, tc.source
			if tc.templ {
				check, source = checkTemplInlineHandlers, "package fixture\n\ntempl Fixture() {\n\t"+tc.source+"\n}\n"
			}
			err := check([]byte(source))
			if tc.handler == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "inline event attribute "+strconv.Quote(tc.handler)) {
				t.Fatalf("want inline handler %q, got %v", tc.handler, err)
			}
		})
	}
}
