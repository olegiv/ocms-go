// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"bytes"
	"encoding/json"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	templparser "github.com/a-h/templ/parser/v2"
	"github.com/olegiv/ocms-go/internal/testutil/repotree"
	"golang.org/x/net/html"
)

type alpineElement struct {
	Attrs    map[string]string `json:"attrs,omitempty"`
	Children []alpineElement   `json:"children,omitempty"`
	Template string            `json:"template,omitempty"`
}

type alpineSource struct {
	Path    string          `json:"path"`
	Roots   []alpineElement `json:"roots,omitempty"`
	Scripts []string        `json:"scripts,omitempty"`
}

// TestAlpineCSPTemplateScopes checks first-party templ and HTML directives
// against the parser shipped by @alpinejs/csp. The snapshot gate installs
// that package before running the test.
// It checks static directives, inline attribute maps and simple formatted
// values. Arbitrary generated attribute expressions, runtime property values
// and third-party markup need separate coverage.
func TestAlpineCSPTemplateScopes(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	node, err := exec.LookPath("node")
	_, parserErr := os.Stat(filepath.Join(root, filepath.FromSlash("node_modules/@alpinejs/csp/src/parser.js")))
	if err != nil || parserErr != nil {
		if os.Getenv("OCMS_REQUIRE_SHARED_TOOLING") == "1" {
			t.Fatalf("Alpine scope check requires Node and built assets: node=%v parser=%v", err, parserErr)
		}
		t.Skip("install Node and build assets to check Alpine scopes")
	}

	var sources []alpineSource
	for _, dir := range []string{"internal", "modules", "web", "custom/themes"} {
		err = filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if repotree.Skip(root, path, entry) || entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			source := alpineSource{Path: filepath.ToSlash(rel)}
			if readErr := source.read(path); readErr != nil {
				return readErr
			}
			if len(source.Roots)+len(source.Scripts) > 0 {
				sources = append(sources, source)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	input, err := json.Marshal(sources)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, "internal/views/admin/testdata/check-alpine-csp.mjs")
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Alpine CSP scope check: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

var goTemplateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
var alpineFormatValue = strings.NewReplacer("%d", "1", "%t", "false", "%s", "sample", "%q", "'sample'")

func (s *alpineSource) read(path string) error {
	ext := filepath.Ext(path)
	if ext != ".templ" && ext != ".html" && ext != ".js" || strings.HasSuffix(path, ".min.js") {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch ext {
	case ".js":
		s.addScript(string(body))
	case ".templ":
		file, parseErr := templparser.ParseString(string(body))
		if parseErr != nil {
			return parseErr
		}
		for _, node := range file.Nodes {
			if template, ok := node.(*templparser.HTMLTemplate); ok {
				if children := s.templElements(template.Children); len(children) != 0 {
					name, _, _ := strings.Cut(template.Expression.Value, "(")
					s.Roots = append(s.Roots, alpineElement{Template: name, Children: children})
				}
			}
		}
	case ".html":
		doc, parseErr := html.Parse(bytes.NewReader(goTemplateAction.ReplaceAll(body, []byte("1"))))
		if parseErr != nil {
			return parseErr
		}
		s.Roots = append(s.Roots, s.htmlElement(doc))
	}
	return nil
}

func (s *alpineSource) addScript(body string) {
	if strings.Contains(body, "Alpine.data(") {
		s.Scripts = append(s.Scripts, body)
	}
}

func alpineAttribute(name string) bool {
	return strings.HasPrefix(name, "x-") || strings.HasPrefix(name, "@") || strings.HasPrefix(name, ":")
}

func (s *alpineSource) htmlElement(node *html.Node) alpineElement {
	var result alpineElement
	if node.Type == html.ElementNode && node.Data == "script" && node.FirstChild != nil {
		s.addScript(node.FirstChild.Data)
		return result
	}
	for _, attr := range node.Attr {
		if alpineAttribute(attr.Key) {
			if result.Attrs == nil {
				result.Attrs = make(map[string]string)
			}
			result.Attrs[attr.Key] = attr.Val
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		element := s.htmlElement(child)
		if len(element.Attrs)+len(element.Children) > 0 {
			result.Children = append(result.Children, element)
		}
	}
	return result
}

func (s *alpineSource) templElements(nodes []templparser.Node) []alpineElement {
	var elements []alpineElement
	for _, node := range nodes {
		var element alpineElement
		switch item := node.(type) {
		case *templparser.Element:
			element.Attrs = templAlpineAttributes(item.Attributes)
		case *templparser.TemplElementExpression:
			element.Attrs = alpineComponentAttributes(item.Expression.Value)
		case *templparser.ScriptElement:
			var body strings.Builder
			for _, part := range item.Contents {
				switch {
				case part.Value != nil:
					body.WriteString(*part.Value)
				case !part.InsideStringLiteral:
					body.WriteString("null")
				}
			}
			s.addScript(body.String())
		}
		if composite, ok := node.(templparser.CompositeNode); ok {
			element.Children = s.templElements(composite.ChildNodes())
		}
		if len(element.Attrs)+len(element.Children) > 0 {
			elements = append(elements, element)
		}
	}
	return elements
}

func templAlpineAttributes(attributes []templparser.Attribute) map[string]string {
	result := make(map[string]string)
	for _, attr := range attributes {
		switch item := attr.(type) {
		case *templparser.ConstantAttribute:
			if name := item.Key.String(); alpineAttribute(name) {
				result[name] = html.UnescapeString(item.Value)
			}
		case *templparser.ExpressionAttribute:
			if name := item.Key.String(); alpineAttribute(name) {
				expr, err := goparser.ParseExpr(item.Expression.Value)
				if err == nil {
					result[name] = alpineExpression(expr)
				}
			}
		case *templparser.ConditionalAttribute:
			for name, value := range templAlpineAttributes(append(append([]templparser.Attribute{}, item.Then...), item.Else...)) {
				result[name] = value
			}
		}
	}
	return result
}

// Inline templ.Attributes literals are rendered on the element owned by the
// component call. Inspect those literals in the caller's Alpine scope.
func alpineComponentAttributes(source string) map[string]string {
	result := make(map[string]string)
	expr, err := goparser.ParseExpr(source)
	if err != nil {
		return result
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		item, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		name := goString(item.Key)
		if alpineAttribute(name) {
			result[name] = alpineExpression(item.Value)
		}
		return true
	})
	return result
}

func goString(expr ast.Expr) string {
	if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		if value, err := strconv.Unquote(literal.Value); err == nil {
			return value
		}
	}
	return ""
}

func alpineExpression(expr ast.Expr) string {
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) > 0 {
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Sprintf" {
			return alpineFormatValue.Replace(goString(call.Args[0]))
		}
	}
	return goString(expr)
}
