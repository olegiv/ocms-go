package seo

import (
	"strings"
	"testing"
)

func TestNewRobotsBuilder(t *testing.T) {
	config := RobotsConfig{
		SiteURL: "https://example.com",
	}
	builder := NewRobotsBuilder(config)
	if builder == nil {
		t.Fatal("NewRobotsBuilder() returned nil")
	}
}

func TestRobotsBuilderBuildDefault(t *testing.T) {
	config := RobotsConfig{
		SiteURL: "https://example.com",
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Check user-agent
	if !strings.Contains(content, "User-agent: *") {
		t.Error("Build() should contain 'User-agent: *'")
	}

	// Check default disallow paths
	defaultPaths := []string{"/admin", "/login", "/logout", "/session"}
	for _, path := range defaultPaths {
		if !strings.Contains(content, "Disallow: "+path) {
			t.Errorf("Build() should disallow %q", path)
		}
	}

	// Check Allow directive
	if !strings.Contains(content, "Allow: /") {
		t.Error("Build() should contain 'Allow: /'")
	}

	// Check sitemap reference
	if !strings.Contains(content, "Sitemap: https://example.com/sitemap.xml") {
		t.Error("Build() should contain sitemap reference")
	}
}

func TestRobotsBuilderBuildDisallowAll(t *testing.T) {
	config := RobotsConfig{
		SiteURL:     "https://staging.example.com",
		DisallowAll: true,
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Check disallow all
	if !strings.Contains(content, "Disallow: /") {
		t.Error("Build() with DisallowAll should contain 'Disallow: /'")
	}

	// Should not contain sitemap when disallowing all
	if strings.Contains(content, "Sitemap:") {
		t.Error("Build() with DisallowAll should not contain sitemap reference")
	}

	// Should not contain Allow when disallowing all
	if strings.Contains(content, "Allow: /") {
		t.Error("Build() with DisallowAll should not contain 'Allow: /'")
	}
}

func TestRobotsBuilderBuildWithExtraRules(t *testing.T) {
	config := RobotsConfig{
		SiteURL:    "https://example.com",
		ExtraRules: "Crawl-delay: 10",
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	if !strings.Contains(content, "Crawl-delay: 10") {
		t.Error("Build() should contain extra rules")
	}
}

func TestRobotsBuilderBuildExtraRulesWithoutNewline(t *testing.T) {
	config := RobotsConfig{
		SiteURL:    "https://example.com",
		ExtraRules: "Crawl-delay: 10",
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Extra rules should have newline appended if missing
	if !strings.Contains(content, "Crawl-delay: 10\n") {
		t.Error("Build() should append newline to extra rules")
	}
}

func TestRobotsBuilderBuildExtraRulesWithNewline(t *testing.T) {
	config := RobotsConfig{
		SiteURL:    "https://example.com",
		ExtraRules: "Crawl-delay: 10\n",
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Should not double newlines
	if strings.Contains(content, "Crawl-delay: 10\n\n\n") {
		t.Error("Build() should not add extra newlines")
	}
}

func TestRobotsBuilderBuildWithCustomDisallowPaths(t *testing.T) {
	config := RobotsConfig{
		SiteURL:       "https://example.com",
		DisallowPaths: []string{"/api", "/private"},
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Check custom disallow paths
	if !strings.Contains(content, "Disallow: /api") {
		t.Error("Build() should disallow /api")
	}
	if !strings.Contains(content, "Disallow: /private") {
		t.Error("Build() should disallow /private")
	}

	// Should still include default paths
	if !strings.Contains(content, "Disallow: /admin") {
		t.Error("Build() should still disallow default paths")
	}
}

func TestRobotsBuilderBuildNoSiteURL(t *testing.T) {
	config := RobotsConfig{}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Should not contain sitemap when no site URL
	if strings.Contains(content, "Sitemap:") {
		t.Error("Build() without SiteURL should not contain sitemap reference")
	}
}

func TestRobotsBuilderBuildSiteURLWithTrailingSlash(t *testing.T) {
	config := RobotsConfig{
		SiteURL: "https://example.com/",
	}
	builder := NewRobotsBuilder(config)
	content := builder.Build()

	// Should normalize URL (no double slash)
	if strings.Contains(content, "https://example.com//sitemap.xml") {
		t.Error("Build() should normalize trailing slash in site URL")
	}
	if !strings.Contains(content, "https://example.com/sitemap.xml") {
		t.Error("Build() should contain correctly formatted sitemap URL")
	}
}

func TestGenerateRobots(t *testing.T) {
	tests := []struct {
		name        string
		siteURL     string
		disallowAll bool
		extraRules  string
		wantContain []string
		wantExclude []string
	}{
		{
			name:        "basic production",
			siteURL:     "https://example.com",
			disallowAll: false,
			extraRules:  "",
			wantContain: []string{"User-agent: *", "Disallow: /admin", "Sitemap:"},
			wantExclude: []string{},
		},
		{
			name:        "staging site",
			siteURL:     "https://staging.example.com",
			disallowAll: true,
			extraRules:  "",
			wantContain: []string{"User-agent: *", "Disallow: /"},
			wantExclude: []string{"Sitemap:", "Allow: /"},
		},
		{
			name:        "with extra rules",
			siteURL:     "https://example.com",
			disallowAll: false,
			extraRules:  "Crawl-delay: 5",
			wantContain: []string{"Crawl-delay: 5", "Sitemap:"},
			wantExclude: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := GenerateRobots(tt.siteURL, tt.disallowAll, tt.extraRules)

			for _, want := range tt.wantContain {
				if !strings.Contains(content, want) {
					t.Errorf("GenerateRobots() should contain %q", want)
				}
			}

			for _, exclude := range tt.wantExclude {
				if strings.Contains(content, exclude) {
					t.Errorf("GenerateRobots() should not contain %q", exclude)
				}
			}
		})
	}
}
