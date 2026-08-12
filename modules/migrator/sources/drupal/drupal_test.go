// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

func TestSourceMetadata(t *testing.T) {
	s := NewSource()
	if s.Name() != "drupal" {
		t.Errorf("Name() = %q, want %q", s.Name(), "drupal")
	}
	if s.DisplayName() != "Drupal" {
		t.Errorf("DisplayName() = %q, want %q", s.DisplayName(), "Drupal")
	}
	if s.Description() != "drupal.description" {
		t.Errorf("Description() = %q, want an i18n key", s.Description())
	}
}

func TestConfigFieldsShape(t *testing.T) {
	s := NewSource()
	fields := s.ConfigFields()

	required := map[string]bool{
		"mysql_host": false, "mysql_port": false, "mysql_user": false,
		"mysql_password": false, "mysql_database": false,
	}
	byName := make(map[string]bool)

	for _, f := range fields {
		byName[f.Name] = true
		if _, ok := required[f.Name]; ok {
			required[f.Name] = f.Required
		}
		if !strings.HasPrefix(f.Label, "drupal.") {
			t.Errorf("field %q label %q should be a drupal.* i18n key", f.Name, f.Label)
		}
	}

	for name, isRequired := range required {
		if !isRequired {
			t.Errorf("field %q should be required", name)
		}
	}
	for _, name := range []string{"table_prefix", "files_path", "type_map", "tag_vocabularies"} {
		if !byName[name] {
			t.Errorf("missing config field %q", name)
		}
	}

	// The password field must be typed so applySafeDefaults never renders its
	// environment default into the HTML form.
	for _, f := range fields {
		if f.Name == "mysql_password" && f.Type != "password" {
			t.Errorf("mysql_password type = %q, want %q", f.Type, "password")
		}
	}
}

// TestBuildDSNEscapesCredentials is the reason this source builds its DSN with
// mysql.Config rather than fmt.Sprintf: a password containing '@', '/', ':' or
// '?' silently produces a malformed DSN under string formatting, which either
// fails to connect or — worse — connects somewhere unintended.
func TestBuildDSNEscapesCredentials(t *testing.T) {
	cfg := map[string]string{
		"mysql_host":     "db.example.com",
		"mysql_port":     "3307",
		"mysql_user":     "drupal@user",
		"mysql_password": "p@ss/w:rd?x#y",
		"mysql_database": "drupal_db",
	}

	dsn, err := BuildDSN(cfg)
	if err != nil {
		t.Fatalf("BuildDSN() error = %v", err)
	}

	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("generated DSN does not round-trip through mysql.ParseDSN: %v", err)
	}
	if parsed.User != cfg["mysql_user"] {
		t.Errorf("User = %q, want %q", parsed.User, cfg["mysql_user"])
	}
	if parsed.Passwd != cfg["mysql_password"] {
		t.Errorf("Passwd = %q, want %q", parsed.Passwd, cfg["mysql_password"])
	}
	if parsed.Addr != "db.example.com:3307" {
		t.Errorf("Addr = %q, want %q", parsed.Addr, "db.example.com:3307")
	}
	if parsed.DBName != "drupal_db" {
		t.Errorf("DBName = %q, want %q", parsed.DBName, "drupal_db")
	}
	if !parsed.ParseTime {
		t.Error("ParseTime should be enabled so Drupal timestamps decode")
	}
	if parsed.Timeout == 0 || parsed.ReadTimeout == 0 {
		t.Error("connect and read timeouts must be set so a hung source cannot stall an import")
	}
}

func TestBuildDSNRejectsBadInput(t *testing.T) {
	base := map[string]string{
		"mysql_host": "localhost", "mysql_port": "3306",
		"mysql_user": "u", "mysql_password": "p", "mysql_database": "d",
	}

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{"empty host", func(c map[string]string) { c["mysql_host"] = "" }},
		{"empty database", func(c map[string]string) { c["mysql_database"] = "" }},
		{"non-numeric port", func(c map[string]string) { c["mysql_port"] = "abc" }},
		{"port zero", func(c map[string]string) { c["mysql_port"] = "0" }},
		{"port too large", func(c map[string]string) { c["mysql_port"] = "70000" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := make(map[string]string, len(base))
			for k, v := range base {
				cfg[k] = v
			}
			tt.mutate(cfg)
			if _, err := BuildDSN(cfg); err == nil {
				t.Error("BuildDSN() should have returned an error")
			}
		})
	}
}

func TestBuildDSNHonoursHostAllowlist(t *testing.T) {
	t.Setenv(shared.EnvAllowedDBHosts, "allowed.example.com")

	cfg := map[string]string{
		"mysql_host": "denied.example.com", "mysql_port": "3306",
		"mysql_user": "u", "mysql_password": "p", "mysql_database": "d",
	}
	if _, err := BuildDSN(cfg); err == nil {
		t.Error("BuildDSN() should reject a host outside the allowlist")
	}

	cfg["mysql_host"] = "allowed.example.com"
	if _, err := BuildDSN(cfg); err != nil {
		t.Errorf("BuildDSN() rejected an allowlisted host: %v", err)
	}
}

func TestParseTypeMap(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		bundle string
		want   string
	}{
		{"default mapping, article", "", "article", pageTypePost},
		{"default mapping, page", "", "page", pageTypePage},
		{"unlisted bundle falls back to page", "", "recipe", pageTypePage},
		{"custom mapping", "news:post,recipe:page", "news", pageTypePost},
		{"case insensitive bundle", "News:post", "news", pageTypePost},
		{"whitespace tolerated", " news : post ", "news", pageTypePost},
		{"invalid page type ignored", "news:widget", "news", pageTypePage},
		{"malformed pair ignored", "news", "news", pageTypePage},
		{"empty bundle ignored", ":post", "", pageTypePage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PageTypeFor(ParseTypeMap(tt.raw), tt.bundle)
			if got != tt.want {
				t.Errorf("PageTypeFor(ParseTypeMap(%q), %q) = %q, want %q", tt.raw, tt.bundle, got, tt.want)
			}
		})
	}
}

func TestParseVocabularyList(t *testing.T) {
	got := parseVocabularyList("Tags, Topics ,")
	if !got["tags"] || !got["topics"] {
		t.Errorf("parseVocabularyList() = %v, want tags and topics", got)
	}
	if got[""] {
		t.Error("parseVocabularyList() should drop empty entries")
	}
	if fallback := parseVocabularyList(""); !fallback["tags"] {
		t.Error("an empty list should fall back to the stock 'tags' vocabulary")
	}
}

func TestFileSchemeAndRelPath(t *testing.T) {
	tests := []struct {
		uri        string
		wantScheme string
		wantRel    string
	}{
		{"public://2026-01/photo.jpg", "public", "2026-01/photo.jpg"},
		{"private://secret.pdf", "private", "secret.pdf"},
		{"temporary://tmp.bin", "temporary", "tmp.bin"},
		{"photo.jpg", "", "photo.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			f := File{URI: tt.uri}
			if got := f.Scheme(); got != tt.wantScheme {
				t.Errorf("Scheme() = %q, want %q", got, tt.wantScheme)
			}
			if got := f.RelPath(); got != tt.wantRel {
				t.Errorf("RelPath() = %q, want %q", got, tt.wantRel)
			}
		})
	}
}

func TestResolveFilePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "2026-01"), 0o755); err != nil {
		t.Fatalf("failed to create fixture dir: %v", err)
	}
	target := filepath.Join(root, "2026-01", "photo.jpg")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	cleanRoot, realRoot, err := shared.ResolveMediaRoot(root)
	if err != nil {
		t.Fatalf("ResolveMediaRoot() error = %v", err)
	}

	t.Run("public file resolves", func(t *testing.T) {
		got, err := resolveFilePath(File{URI: "public://2026-01/photo.jpg"}, cleanRoot, realRoot)
		if err != nil {
			t.Fatalf("resolveFilePath() error = %v", err)
		}
		if filepath.Base(got) != "photo.jpg" {
			t.Errorf("resolved to %q, want the fixture file", got)
		}
	})

	for _, tt := range []struct{ name, uri string }{
		{"private stream rejected", "private://secret.pdf"},
		{"temporary stream rejected", "temporary://tmp.bin"},
		{"unknown stream rejected", "s3://bucket/key.jpg"},
		{"traversal rejected", "public://../../etc/passwd"},
		{"empty path rejected", "public://"},
		{"missing file rejected", "public://nope.jpg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolveFilePath(File{URI: tt.uri}, cleanRoot, realRoot); err == nil {
				t.Errorf("resolveFilePath(%q) should have returned an error", tt.uri)
			}
		})
	}
}

func TestPathAliasNodeID(t *testing.T) {
	tests := []struct {
		path   string
		wantID int64
		wantOK bool
	}{
		{"/node/12", 12, true},
		{"node/7", 7, true},
		{"/node/33/", 33, true},
		{"/taxonomy/term/4", 0, false},
		{"/user/1", 0, false},
		{"/node/abc", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			a := PathAlias{Path: tt.path}
			gotID, gotOK := a.NodeID()
			if gotID != tt.wantID || gotOK != tt.wantOK {
				t.Errorf("NodeID() = (%d, %v), want (%d, %v)", gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestMenuLinkParentUUID(t *testing.T) {
	tests := []struct {
		name   string
		parent sql.NullString
		want   string
	}{
		{"root item", sql.NullString{}, ""},
		{"empty string", sql.NullString{String: "", Valid: true}, ""},
		{"menu link parent", sql.NullString{String: "menu_link_content:abc-123", Valid: true}, "abc-123"},
		{"unknown plugin", sql.NullString{String: "views_view:frontpage", Valid: true}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := MenuLink{Parent: tt.parent}
			if got := m.ParentUUID(); got != tt.want {
				t.Errorf("ParentUUID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLinkURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantNode int64
		wantURL  string
		wantErr  bool
	}{
		{"entity node", "entity:node/12", 12, "", false},
		{"internal node", "internal:/node/7", 7, "", false},
		{"internal path", "internal:/about-us", 0, "/about-us", false},
		{"internal path without slash", "internal:contact", 0, "/contact", false},
		{"internal front page", "internal:/", 0, "/", false},
		{"external https", "https://example.com/x", 0, "https://example.com/x", false},
		{"external http", "http://example.com", 0, "http://example.com", false},
		{"base path", "base:sitemap.xml", 0, "/sitemap.xml", false},
		{"route has no equivalent", "route:<front>", 0, "", true},
		{"unsupported entity", "entity:taxonomy_term/3", 0, "", true},
		{"empty", "", 0, "", true},
		{"unknown scheme", "weird:thing", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNode, gotURL, err := ResolveLinkURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveLinkURI(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if gotNode != tt.wantNode {
				t.Errorf("node = %d, want %d", gotNode, tt.wantNode)
			}
			if gotURL != tt.wantURL {
				t.Errorf("url = %q, want %q", gotURL, tt.wantURL)
			}
		})
	}
}

func TestRewriteBodyFileURLs(t *testing.T) {
	refs := NewMediaRefs()
	refs.ByPath["2026-01/photo.jpg"] = "/uploads/originals/uuid-1/photo.jpg"
	refs.ByPath["doc.pdf"] = "/uploads/originals/uuid-2/doc.pdf"

	body := `<p><img src="/sites/default/files/2026-01/photo.jpg" alt="x"></p>` +
		`<a href="/system/files/doc.pdf">doc</a>` +
		`<img src="/sites/default/files/unknown.png">`

	got := RewriteBody(body, refs)

	if !strings.Contains(got, "/uploads/originals/uuid-1/photo.jpg") {
		t.Errorf("image URL was not rewritten: %s", got)
	}
	if !strings.Contains(got, "/uploads/originals/uuid-2/doc.pdf") {
		t.Errorf("document URL was not rewritten: %s", got)
	}
	if !strings.Contains(got, "/sites/default/files/unknown.png") {
		t.Errorf("an unmapped file should be left alone, got: %s", got)
	}
}

// TestRewriteBodyLongestPathWins guards the reason rewriting parses the URL
// rather than doing a blind ReplaceAll over every map key: a short filename
// that is a substring of a longer path must not corrupt the longer one.
func TestRewriteBodyLongestPathWins(t *testing.T) {
	refs := NewMediaRefs()
	refs.ByPath["a.jpg"] = "/uploads/originals/short/a.jpg"
	refs.ByPath["nested/a.jpg"] = "/uploads/originals/long/a.jpg"

	got := RewriteBody(`<img src="/sites/default/files/nested/a.jpg">`, refs)

	if !strings.Contains(got, "/uploads/originals/long/a.jpg") {
		t.Errorf("nested path should map to its own media, got: %s", got)
	}
	if strings.Contains(got, "short") {
		t.Errorf("nested path was corrupted by the shorter key, got: %s", got)
	}
}

func TestRewriteBodyDrupalMediaEmbeds(t *testing.T) {
	refs := NewMediaRefs()
	refs.ByUUID["uuid-img"] = "/uploads/originals/m1/photo.jpg"
	refs.ByUUID["uuid-doc"] = "/uploads/originals/m2/report.pdf"
	refs.IsImg["/uploads/originals/m1/photo.jpg"] = true
	refs.AltMap["/uploads/originals/m1/photo.jpg"] = "A photo"

	tests := []struct {
		name        string
		body        string
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "image embed becomes img",
			body:        `<drupal-media data-entity-type="media" data-entity-uuid="uuid-img"></drupal-media>`,
			wantContain: `<img src="/uploads/originals/m1/photo.jpg" alt="A photo">`,
			wantAbsent:  "drupal-media",
		},
		{
			name:        "non-image embed becomes link",
			body:        `<drupal-media data-entity-uuid="uuid-doc" />`,
			wantContain: `<a href="/uploads/originals/m2/report.pdf">`,
			wantAbsent:  "drupal-media",
		},
		{
			name:        "explicit alt wins",
			body:        `<drupal-media data-entity-uuid="uuid-img" alt="Custom"></drupal-media>`,
			wantContain: `alt="Custom"`,
			wantAbsent:  "drupal-media",
		},
		{
			name:        "unresolvable embed is dropped",
			body:        `before<drupal-media data-entity-uuid="missing"></drupal-media>after`,
			wantContain: "beforeafter",
			wantAbsent:  "drupal-media",
		},
		{
			name:        "drupal-entity is handled too",
			body:        `<drupal-entity data-entity-uuid="uuid-img"></drupal-entity>`,
			wantContain: `<img src=`,
			wantAbsent:  "drupal-entity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RewriteBody(tt.body, refs)
			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("got %q, want it to contain %q", got, tt.wantContain)
			}
			if tt.wantAbsent != "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("got %q, want it to no longer contain %q", got, tt.wantAbsent)
			}
		})
	}
}

func TestRewriteBodyEmptyAndNilSafe(t *testing.T) {
	if got := RewriteBody("", NewMediaRefs()); got != "" {
		t.Errorf("RewriteBody(\"\") = %q, want empty", got)
	}
	body := `<p>hello</p>`
	if got := RewriteBody(body, nil); got != body {
		t.Errorf("RewriteBody with nil refs = %q, want it unchanged", got)
	}
}

func TestSchemaMissingOptional(t *testing.T) {
	full := Schema{
		HasNodeImage: true, HasNodeTags: true, HasTermData: true, HasTermPar: true,
		HasFiles: true, HasMediaImg: true, HasAliases: true, HasMenuLinks: true,
	}
	if missing := full.MissingOptional(); len(missing) != 0 {
		t.Errorf("MissingOptional() = %v, want none", missing)
	}

	partial := Schema{HasFiles: true}
	missing := partial.MissingOptional()
	if len(missing) != 7 {
		t.Errorf("MissingOptional() reported %d tables, want 7: %v", len(missing), missing)
	}
	for _, name := range missing {
		if name == tableFileManaged {
			t.Errorf("%s is present and should not be reported missing", tableFileManaged)
		}
	}
}

func TestUnixOrNow(t *testing.T) {
	now := timeFixture()
	if got := unixOrNow(0, now); !got.Equal(now) {
		t.Errorf("unixOrNow(0) = %v, want the fallback %v", got, now)
	}
	if got := unixOrNow(-1, now); !got.Equal(now) {
		t.Errorf("unixOrNow(-1) = %v, want the fallback %v", got, now)
	}
	if got := unixOrNow(1700000000, now); got.Unix() != 1700000000 {
		t.Errorf("unixOrNow(1700000000).Unix() = %d, want 1700000000", got.Unix())
	}
}

// timeFixture returns a stable timestamp for tests.
func timeFixture() time.Time {
	return time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
}
