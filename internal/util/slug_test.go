// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple title",
			input:    "Hello World",
			expected: "hello-world",
		},
		{
			name:     "with special characters",
			input:    "Hello, World!",
			expected: "hello-world",
		},
		{
			name:     "with numbers",
			input:    "Page 123",
			expected: "page-123",
		},
		{
			name:     "with accents",
			input:    "Café résumé",
			expected: "cafe-resume",
		},
		{
			name:     "with multiple spaces",
			input:    "Hello   World",
			expected: "hello-world",
		},
		{
			name:     "with hyphens",
			input:    "Hello - World",
			expected: "hello-world",
		},
		{
			name:     "with leading/trailing spaces",
			input:    "  Hello World  ",
			expected: "hello-world",
		},
		{
			name:     "all special characters",
			input:    "!@#$%^&*()",
			expected: "",
		},
		{
			name:     "japanese characters",
			input:    "日本語タイトル",
			expected: "ri-ben-yu-taitoru",
		},
		{
			name:     "german umlauts",
			input:    "Über München",
			expected: "uber-munchen",
		},
		{
			name:     "cyrillic russian",
			input:    "Привет мир",
			expected: "privet-mir",
		},
		{
			name:     "cyrillic russian article",
			input:    "статья о программировании",
			expected: "statia-o-programmirovanii",
		},
		{
			name:     "chinese",
			input:    "北京欢迎你",
			expected: "bei-jing-huan-ying-ni",
		},
		{
			name:     "mixed latin and cyrillic",
			input:    "Hello Мир",
			expected: "hello-mir",
		},
		{
			name:     "ukrainian",
			input:    "Київ столиця",
			expected: "kiyiv-stolitsia",
		},
		// Additional Slavic languages
		{
			name:     "serbian cyrillic",
			input:    "Београд Србија",
			expected: "beograd-srbija",
		},
		{
			name:     "bulgarian",
			input:    "София България",
			expected: "sofiia-blgariia",
		},
		{
			name:     "belarusian",
			input:    "Мінск Беларусь",
			expected: "minsk-belarus",
		},
		{
			name:     "polish",
			input:    "Żółć gęślą",
			expected: "zolc-gesla",
		},
		{
			name:     "czech",
			input:    "Příliš žluťoučký",
			expected: "prilis-zlutoucky",
		},
		// Greek
		{
			name:     "greek",
			input:    "Αθήνα Ελλάδα",
			expected: "athena-ellada",
		},
		// Asian languages
		{
			name:     "korean",
			input:    "서울 한국",
			expected: "seoul-hangug",
		},
		{
			name:     "vietnamese",
			input:    "Xin chào thế giới",
			expected: "xin-chao-the-gioi",
		},
		{
			name:     "hindi",
			input:    "नमस्ते दुनिया",
			expected: "nmste-duniyaa",
		},
		{
			name:     "thai",
			input:    "สวัสดีโลก",
			expected: "swasdiiolk",
		},
		// Middle Eastern
		{
			name:     "arabic",
			input:    "مرحبا بالعالم",
			expected: "mrhb-bllm",
		},
		{
			name:     "hebrew",
			input:    "שלום עולם",
			expected: "shlvm-vlm",
		},
		// Turkish
		{
			name:     "turkish",
			input:    "İstanbul Türkiye",
			expected: "istanbul-turkiye",
		},
		// Romance languages
		{
			name:     "spanish",
			input:    "España año",
			expected: "espana-ano",
		},
		{
			name:     "portuguese",
			input:    "São Paulo coração",
			expected: "sao-paulo-coracao",
		},
		{
			name:     "french",
			input:    "Français être naïf",
			expected: "francais-etre-naif",
		},
		{
			name:     "romanian",
			input:    "București România",
			expected: "bucuresti-romania",
		},
		// Nordic/Scandinavian
		{
			name:     "swedish",
			input:    "Göteborg Malmö",
			expected: "goteborg-malmo",
		},
		{
			name:     "norwegian",
			input:    "Trondheim Ålesund",
			expected: "trondheim-alesund",
		},
		{
			name:     "danish",
			input:    "København Århus",
			expected: "kobenhavn-arhus",
		},
		{
			name:     "finnish",
			input:    "Hämeenlinna Jyväskylä",
			expected: "hameenlinna-jyvaskyla",
		},
		{
			name:     "icelandic",
			input:    "Reykjavík Ísland",
			expected: "reykjavik-island",
		},
		// Baltic
		{
			name:     "latvian",
			input:    "Rīga Latvija",
			expected: "riga-latvija",
		},
		{
			name:     "lithuanian",
			input:    "Vilnius Lietuva",
			expected: "vilnius-lietuva",
		},
		// Hungarian
		{
			name:     "hungarian",
			input:    "Magyarország főváros",
			expected: "magyarorszag-fovaros",
		},
		// Edge cases
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single word",
			input:    "Hello",
			expected: "hello",
		},
		{
			name:     "mixed case",
			input:    "HeLLo WoRLd",
			expected: "hello-world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Slugify(tt.input)
			if result != tt.expected {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsValidSlug(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid simple slug",
			input:    "hello-world",
			expected: true,
		},
		{
			name:     "valid slug with numbers",
			input:    "page-123",
			expected: true,
		},
		{
			name:     "valid single word",
			input:    "hello",
			expected: true,
		},
		{
			name:     "valid numbers only",
			input:    "123",
			expected: true,
		},
		{
			name:     "invalid - empty",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid - uppercase",
			input:    "Hello-World",
			expected: false,
		},
		{
			name:     "invalid - spaces",
			input:    "hello world",
			expected: false,
		},
		{
			name:     "invalid - special chars",
			input:    "hello!world",
			expected: false,
		},
		{
			name:     "invalid - starts with hyphen",
			input:    "-hello",
			expected: false,
		},
		{
			name:     "invalid - ends with hyphen",
			input:    "hello-",
			expected: false,
		},
		{
			name:     "invalid - consecutive hyphens",
			input:    "hello--world",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidSlug(tt.input)
			if result != tt.expected {
				t.Errorf("IsValidSlug(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsValidAlias(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Valid aliases - simple slugs (same as IsValidSlug)
		{
			name:     "valid simple slug",
			input:    "hello-world",
			expected: true,
		},
		{
			name:     "valid slug with numbers",
			input:    "page-123",
			expected: true,
		},
		// Valid aliases - path-like with slashes
		{
			name:     "valid path - blog post",
			input:    "blog/post/55",
			expected: true,
		},
		{
			name:     "valid path - two segments",
			input:    "category/subcategory",
			expected: true,
		},
		{
			name:     "valid path - deep nesting",
			input:    "a/b/c/d/e",
			expected: true,
		},
		{
			name:     "valid path - with hyphens",
			input:    "blog/my-post/section-1",
			expected: true,
		},
		{
			name:     "valid path - numbers in segments",
			input:    "2024/01/my-article",
			expected: true,
		},
		// Invalid aliases - empty
		{
			name:     "invalid - empty",
			input:    "",
			expected: false,
		},
		// Invalid aliases - leading/trailing slashes
		{
			name:     "invalid - leading slash",
			input:    "/blog/post",
			expected: false,
		},
		{
			name:     "invalid - trailing slash",
			input:    "blog/post/",
			expected: false,
		},
		{
			name:     "invalid - only slash",
			input:    "/",
			expected: false,
		},
		// Invalid aliases - consecutive slashes
		{
			name:     "invalid - double slash",
			input:    "blog//post",
			expected: false,
		},
		// Invalid aliases - invalid segment (uppercase)
		{
			name:     "invalid - uppercase in segment",
			input:    "blog/Post",
			expected: false,
		},
		// Invalid aliases - invalid segment (special chars)
		{
			name:     "invalid - special chars in segment",
			input:    "blog/post!",
			expected: false,
		},
		// Invalid aliases - invalid segment (spaces)
		{
			name:     "invalid - space in segment",
			input:    "blog/my post",
			expected: false,
		},
		// Invalid aliases - segment starts/ends with hyphen
		{
			name:     "invalid - segment starts with hyphen",
			input:    "blog/-post",
			expected: false,
		},
		{
			name:     "invalid - segment ends with hyphen",
			input:    "blog/post-",
			expected: false,
		},
		// Invalid aliases - empty segment
		{
			name:     "invalid - empty segment at start",
			input:    "/post",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidAlias(tt.input)
			if result != tt.expected {
				t.Errorf("IsValidAlias(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsValidLangCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Valid language codes
		{
			name:     "valid 2-letter code",
			input:    "en",
			expected: true,
		},
		{
			name:     "valid 2-letter code de",
			input:    "de",
			expected: true,
		},
		{
			name:     "valid 3-letter code",
			input:    "fra",
			expected: true,
		},
		{
			name:     "valid code with region",
			input:    "zh-cn",
			expected: true,
		},
		{
			name:     "valid code with region pt-br",
			input:    "pt-br",
			expected: true,
		},
		{
			name:     "valid code with region en-us",
			input:    "en-us",
			expected: true,
		},
		{
			name:     "valid code with script zh-hans",
			input:    "zh-hans",
			expected: true,
		},
		// Invalid language codes
		{
			name:     "invalid - empty",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid - too long",
			input:    "verylongcode",
			expected: false,
		},
		{
			name:     "invalid - uppercase",
			input:    "EN",
			expected: false,
		},
		{
			name:     "invalid - mixed case",
			input:    "En-US",
			expected: false,
		},
		{
			name:     "invalid - spaces",
			input:    "en us",
			expected: false,
		},
		{
			name:     "invalid - special chars",
			input:    "en!us",
			expected: false,
		},
		{
			name:     "invalid - slash (path traversal)",
			input:    "en/us",
			expected: false,
		},
		{
			name:     "invalid - backslash",
			input:    "en\\us",
			expected: false,
		},
		{
			name:     "invalid - starts with hyphen",
			input:    "-en",
			expected: false,
		},
		{
			name:     "invalid - ends with hyphen",
			input:    "en-",
			expected: false,
		},
		{
			name:     "invalid - consecutive hyphens",
			input:    "en--us",
			expected: false,
		},
		{
			name:     "invalid - dot (path traversal)",
			input:    "..",
			expected: false,
		},
		{
			name:     "invalid - protocol attempt",
			input:    "http:",
			expected: false,
		},
		{
			name:     "invalid - URL characters",
			input:    "evil.com",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidLangCode(tt.input)
			if result != tt.expected {
				t.Errorf("IsValidLangCode(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
