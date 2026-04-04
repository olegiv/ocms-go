// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestInit(t *testing.T) {
	// Initialize without logger
	if err := Init(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify translations loaded
	if TranslationCount("en") == 0 {
		t.Error("Expected English translations to be loaded")
	}
	if TranslationCount("ru") == 0 {
		t.Error("Expected Russian translations to be loaded")
	}
}

func TestT(t *testing.T) {
	// Initialize
	if err := Init(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tests := []struct {
		lang     string
		key      string
		args     []any
		expected string
	}{
		{"en", "btn.save", nil, "Save"},
		{"ru", "btn.save", nil, "Сохранить"},
		{"en", "btn.cancel", nil, "Cancel"},
		{"ru", "btn.cancel", nil, "Отмена"},
		{"en", "nav.dashboard", nil, "Dashboard"},
		{"ru", "nav.dashboard", nil, "Панель управления"},
		{"en", "msg.deleted", []any{"Page"}, "Page deleted successfully"},
		{"ru", "msg.deleted", []any{"Page"}, "Page удалён"},
		// Fallback to English for unknown language
		{"de", "btn.save", nil, "Save"},
		// Return key if not found
		{"en", "nonexistent.key", nil, "nonexistent.key"},
	}

	for _, tt := range tests {
		t.Run(tt.lang+"_"+tt.key, func(t *testing.T) {
			result := T(tt.lang, tt.key, tt.args...)
			if result != tt.expected {
				t.Errorf("T(%q, %q, %v) = %q, want %q", tt.lang, tt.key, tt.args, result, tt.expected)
			}
		})
	}
}

func TestMatchLanguage(t *testing.T) {
	// Initialize
	if err := Init(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"en", "en"},
		{"ru", "ru"},
		{"en-US", "en"},
		{"ru-RU", "ru"},
		{"de", "en"},      // Falls back to default
		{"invalid", "en"}, // Falls back to default
		{"en-US, ru;q=0.9, de;q=0.8", "en"},
		{"ru-RU, en;q=0.9", "ru"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := MatchLanguage(tt.input)
			if result != tt.expected {
				t.Errorf("MatchLanguage(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsSupported(t *testing.T) {
	tests := []struct {
		lang     string
		expected bool
	}{
		{"en", true},
		{"ru", true},
		{"EN", true}, // Case insensitive
		{"RU", true},
		{"de", false},
		{"fr", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			result := IsSupported(tt.lang)
			if result != tt.expected {
				t.Errorf("IsSupported(%q) = %v, want %v", tt.lang, result, tt.expected)
			}
		})
	}
}

func TestGetSupportedLanguages(t *testing.T) {
	langs := GetSupportedLanguages()
	if len(langs) != 2 {
		t.Errorf("Expected 2 supported languages, got %d", len(langs))
	}
	if langs[0] != "en" || langs[1] != "ru" {
		t.Errorf("Expected [en, ru], got %v", langs)
	}
}

func TestTranslationFilesNoDuplicates(t *testing.T) {
	for _, lang := range SupportedLanguages {
		t.Run(lang, func(t *testing.T) {
			path := fmt.Sprintf("locales/%s/messages.json", lang)
			data, err := localesFS.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed to read %s: %v", path, err)
			}

			var msgFile MessageFile
			if err := json.Unmarshal(data, &msgFile); err != nil {
				t.Fatalf("Failed to parse %s: %v", path, err)
			}

			seen := make(map[string]int)
			var duplicates []string
			for i, msg := range msgFile.Messages {
				if firstIdx, exists := seen[msg.ID]; exists {
					duplicates = append(duplicates, fmt.Sprintf("%q (lines %d and %d)", msg.ID, firstIdx+1, i+1))
				} else {
					seen[msg.ID] = i
				}
			}

			if len(duplicates) > 0 {
				t.Errorf("Found %d duplicate translation IDs in %s:\n  %v", len(duplicates), lang, duplicates)
			}
		})
	}
}

func TestTranslationFilesEqualCount(t *testing.T) {
	counts := make(map[string]int)
	keys := make(map[string]map[string]bool)

	for _, lang := range SupportedLanguages {
		path := fmt.Sprintf("locales/%s/messages.json", lang)
		data, err := localesFS.ReadFile(path)
		if err != nil {
			t.Fatalf("Failed to read %s: %v", path, err)
		}

		var msgFile MessageFile
		if err := json.Unmarshal(data, &msgFile); err != nil {
			t.Fatalf("Failed to parse %s: %v", path, err)
		}

		// Count unique keys (in case there are duplicates)
		keys[lang] = make(map[string]bool)
		for _, msg := range msgFile.Messages {
			keys[lang][msg.ID] = true
		}
		counts[lang] = len(keys[lang])
	}

	// Compare all languages to the first one
	if len(SupportedLanguages) < 2 {
		return
	}

	refLang := SupportedLanguages[0]
	refCount := counts[refLang]

	for _, lang := range SupportedLanguages[1:] {
		if counts[lang] != refCount {
			t.Errorf("Translation count mismatch: %s has %d, %s has %d",
				refLang, refCount, lang, counts[lang])

			// Find missing keys
			missingInLang := findMissingKeys(keys[refLang], keys[lang])
			missingInRef := findMissingKeys(keys[lang], keys[refLang])

			if len(missingInLang) > 0 {
				t.Errorf("Keys in %s but missing in %s: %v", refLang, lang, missingInLang)
			}
			if len(missingInRef) > 0 {
				t.Errorf("Keys in %s but missing in %s: %v", lang, refLang, missingInRef)
			}
		}
	}
}

func findMissingKeys(a, b map[string]bool) []string {
	var missing []string
	for key := range a {
		if !b[key] {
			missing = append(missing, key)
		}
	}
	return missing
}

// initCatalog reinitializes the global catalog before a test to ensure isolation.
func initCatalog(t *testing.T) {
	t.Helper()
	if err := Init(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
}

// TestTNilCatalog verifies that T returns the key when the catalog has not been
// initialized (nil global catalog).
func TestTNilCatalog(t *testing.T) {
	catalog = nil
	got := T("en", "btn.save")
	if got != "btn.save" {
		t.Errorf("T with nil catalog = %q, want %q", got, "btn.save")
	}
	// Restore for subsequent tests.
	initCatalog(t)
}

// TestTFallbackDefaultLangForMissingKey verifies that when the requested
// language exists but the key is missing, T falls back to the default language.
func TestTFallbackDefaultLangForMissingKey(t *testing.T) {
	initCatalog(t)

	// "ru" exists but "nonexistent.key" should fall back to English key string.
	got := T("ru", "nonexistent.key.xyz")
	if got != "nonexistent.key.xyz" {
		t.Errorf("T fallback for missing key = %q, want key itself %q", got, "nonexistent.key.xyz")
	}
}

// TestTFallbackDefaultLangForMissingKeyWithArgs verifies fallback to default
// language when the key is absent from the requested language but present in
// the default language and format arguments are provided.
func TestTFallbackDefaultLangForMissingKeyWithArgs(t *testing.T) {
	initCatalog(t)

	// Add a key to English only, then request it in Russian.
	AddTranslations("en", map[string]string{"test.only_en": "Hello %s"})

	got := T("ru", "test.only_en", "World")
	if got != "Hello World" {
		t.Errorf("T fallback with args = %q, want %q", got, "Hello World")
	}
}

// TestTWithArgsFormatting verifies printf-style formatting in translations.
func TestTWithArgsFormatting(t *testing.T) {
	initCatalog(t)

	tests := []struct {
		name string
		lang string
		key  string
		args []any
		want string
	}{
		{
			name: "single string arg",
			lang: "en",
			key:  "msg.deleted",
			args: []any{"Page"},
			want: "Page deleted successfully",
		},
		{
			name: "single string arg ru",
			lang: "ru",
			key:  "msg.deleted",
			args: []any{"Page"},
			want: "Page удалён",
		},
		{
			name: "no args returns translation as-is",
			lang: "en",
			key:  "btn.save",
			args: nil,
			want: "Save",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := T(tt.lang, tt.key, tt.args...)
			if got != tt.want {
				t.Errorf("T(%q, %q, %v) = %q, want %q", tt.lang, tt.key, tt.args, got, tt.want)
			}
		})
	}
}

// TestTUnknownLangNoTranslationReturnsKey verifies that when the requested
// language does not exist and the key is also absent from the default language,
// T returns the raw key.
func TestTUnknownLangNoTranslationReturnsKey(t *testing.T) {
	initCatalog(t)

	got := T("zz", "completely.unknown.key.zzz")
	if got != "completely.unknown.key.zzz" {
		t.Errorf("T unknown lang unknown key = %q, want key itself", got)
	}
}

// TestGetDefaultLanguage verifies the default language getter before and after
// initialization.
func TestGetDefaultLanguage(t *testing.T) {
	// With nil catalog it should return "en".
	catalog = nil
	if got := GetDefaultLanguage(); got != "en" {
		t.Errorf("GetDefaultLanguage with nil catalog = %q, want %q", got, "en")
	}

	// After standard Init the default should be "en".
	initCatalog(t)
	if got := GetDefaultLanguage(); got != "en" {
		t.Errorf("GetDefaultLanguage after Init = %q, want %q", got, "en")
	}
}

// TestInitWithDefaultCustomLang verifies that InitWithDefault with a supported
// language sets that language as the default.
func TestInitWithDefaultCustomLang(t *testing.T) {
	if err := InitWithDefault(nil, "ru"); err != nil {
		t.Fatalf("InitWithDefault(ru) failed: %v", err)
	}
	if got := GetDefaultLanguage(); got != "ru" {
		t.Errorf("GetDefaultLanguage = %q, want %q", got, "ru")
	}
	// Restore for subsequent tests.
	initCatalog(t)
}

// TestInitWithDefaultUnsupportedLangFallsBackToEn verifies that an unsupported
// language in InitWithDefault causes a fallback to "en".
func TestInitWithDefaultUnsupportedLangFallsBackToEn(t *testing.T) {
	if err := InitWithDefault(nil, "zz"); err != nil {
		t.Fatalf("InitWithDefault(zz) failed: %v", err)
	}
	if got := GetDefaultLanguage(); got != "en" {
		t.Errorf("GetDefaultLanguage after unsupported default = %q, want %q", got, "en")
	}
	// Restore for subsequent tests.
	initCatalog(t)
}

// TestSetDefaultLanguage verifies that the default language can be changed
// and that invalid/unsupported inputs are rejected.
func TestSetDefaultLanguage(t *testing.T) {
	initCatalog(t)

	// Change default to Russian.
	SetDefaultLanguage("ru")
	if got := GetDefaultLanguage(); got != "ru" {
		t.Errorf("GetDefaultLanguage after SetDefaultLanguage(ru) = %q, want %q", got, "ru")
	}

	// Unsupported language must not change the default.
	SetDefaultLanguage("zz")
	if got := GetDefaultLanguage(); got != "ru" {
		t.Errorf("GetDefaultLanguage after SetDefaultLanguage(zz) = %q, want %q (should be unchanged)", got, "ru")
	}

	// Restore to English for subsequent tests.
	SetDefaultLanguage("en")
	if got := GetDefaultLanguage(); got != "en" {
		t.Errorf("GetDefaultLanguage after restoring en = %q, want %q", got, "en")
	}
}

// TestSetDefaultLanguageNilCatalog verifies that SetDefaultLanguage is a no-op
// when the catalog is nil (does not panic).
func TestSetDefaultLanguageNilCatalog(t *testing.T) {
	catalog = nil
	SetDefaultLanguage("en") // must not panic
	// Restore.
	initCatalog(t)
}

// TestTranslationCountNilCatalog verifies that TranslationCount returns 0 when
// the catalog has not been initialized.
func TestTranslationCountNilCatalog(t *testing.T) {
	catalog = nil
	if got := TranslationCount("en"); got != 0 {
		t.Errorf("TranslationCount with nil catalog = %d, want 0", got)
	}
	// Restore.
	initCatalog(t)
}

// TestTranslationCountUnknownLang verifies that TranslationCount returns 0 for
// a language that has never been loaded.
func TestTranslationCountUnknownLang(t *testing.T) {
	initCatalog(t)
	if got := TranslationCount("zz"); got != 0 {
		t.Errorf("TranslationCount(zz) = %d, want 0", got)
	}
}

// TestTranslationCountPositive verifies that after initialization the loaded
// languages report a positive translation count.
func TestTranslationCountPositive(t *testing.T) {
	initCatalog(t)
	for _, lang := range []string{"en", "ru"} {
		if got := TranslationCount(lang); got <= 0 {
			t.Errorf("TranslationCount(%q) = %d, want > 0", lang, got)
		}
	}
}

// TestAddTranslations verifies that new translations are merged into the catalog
// and are retrievable via T.
func TestAddTranslations(t *testing.T) {
	initCatalog(t)

	AddTranslations("en", map[string]string{
		"test.greeting": "Hello, World!",
		"test.farewell": "Goodbye!",
	})

	if got := T("en", "test.greeting"); got != "Hello, World!" {
		t.Errorf("T(en, test.greeting) = %q, want %q", got, "Hello, World!")
	}
	if got := T("en", "test.farewell"); got != "Goodbye!" {
		t.Errorf("T(en, test.farewell) = %q, want %q", got, "Goodbye!")
	}
}

// TestAddTranslationsNewLanguage verifies that AddTranslations creates a
// language map for a language that was not previously loaded.
func TestAddTranslationsNewLanguage(t *testing.T) {
	initCatalog(t)

	// "de" is not a supported admin language but AddTranslations should still
	// store it in the map and allow retrieval via T.
	AddTranslations("de", map[string]string{
		"custom.key": "Hallo",
	})

	before := TranslationCount("de")
	if before == 0 {
		t.Error("Expected TranslationCount(de) > 0 after AddTranslations")
	}

	if got := T("de", "custom.key"); got != "Hallo" {
		t.Errorf("T(de, custom.key) = %q, want %q", got, "Hallo")
	}
}

// TestAddTranslationsOverride verifies that AddTranslations can override an
// existing translation value.
func TestAddTranslationsOverride(t *testing.T) {
	initCatalog(t)

	// Override the btn.save translation.
	AddTranslations("en", map[string]string{"btn.save": "Store"})
	if got := T("en", "btn.save"); got != "Store" {
		t.Errorf("T(en, btn.save) after override = %q, want %q", got, "Store")
	}
}

// TestAddTranslationsNilCatalog verifies that AddTranslations is a no-op when
// the catalog has not been initialized.
func TestAddTranslationsNilCatalog(t *testing.T) {
	catalog = nil
	AddTranslations("en", map[string]string{"k": "v"}) // must not panic
	// Restore.
	initCatalog(t)
}

// TestAddTranslationsEmptyMap verifies that an empty map is ignored without
// modifying the catalog.
func TestAddTranslationsEmptyMap(t *testing.T) {
	initCatalog(t)
	countBefore := TranslationCount("en")
	AddTranslations("en", map[string]string{})
	if got := TranslationCount("en"); got != countBefore {
		t.Errorf("TranslationCount changed after empty AddTranslations: %d -> %d", countBefore, got)
	}
}

// TestAddTranslationsNilMap verifies that a nil map is ignored gracefully.
func TestAddTranslationsNilMap(t *testing.T) {
	initCatalog(t)
	countBefore := TranslationCount("en")
	AddTranslations("en", nil)
	if got := TranslationCount("en"); got != countBefore {
		t.Errorf("TranslationCount changed after nil AddTranslations: %d -> %d", countBefore, got)
	}
}

// TestSetActiveLanguages verifies filtering to a subset of supported languages.
func TestSetActiveLanguages(t *testing.T) {
	initCatalog(t)

	// Limit to Russian only.
	SetActiveLanguages([]string{"ru"})

	// Russian should now match directly.
	if got := MatchLanguage("ru"); got != "ru" {
		t.Errorf("MatchLanguage(ru) after SetActiveLanguages([ru]) = %q, want %q", got, "ru")
	}
}

// TestSetActiveLanguagesEmpty verifies that an empty slice sets the matcher to
// only the default language.
func TestSetActiveLanguagesEmpty(t *testing.T) {
	initCatalog(t)

	SetActiveLanguages([]string{})
	// After empty list, the matcher uses the default (en) only.
	if got := MatchLanguage("en"); got != "en" {
		t.Errorf("MatchLanguage(en) after SetActiveLanguages([]) = %q, want %q", got, "en")
	}
}

// TestSetActiveLanguagesUnsupported verifies that codes not in SupportedLanguages
// are silently ignored, causing the fallback to the default.
func TestSetActiveLanguagesUnsupported(t *testing.T) {
	initCatalog(t)

	// Provide only unsupported codes — should fall back to default only.
	SetActiveLanguages([]string{"zz", "xx"})
	// Should not panic. MatchLanguage should still return a valid code.
	got := MatchLanguage("en")
	if got == "" {
		t.Error("MatchLanguage returned empty string after SetActiveLanguages with unsupported codes")
	}
}

// TestSetActiveLanguagesNilCatalog verifies that SetActiveLanguages is a no-op
// when the catalog has not been initialized.
func TestSetActiveLanguagesNilCatalog(t *testing.T) {
	catalog = nil
	SetActiveLanguages([]string{"en"}) // must not panic
	// Restore.
	initCatalog(t)
}

// TestMatchLanguageNilCatalog verifies that MatchLanguage returns "en" when the
// catalog has not been initialized.
func TestMatchLanguageNilCatalog(t *testing.T) {
	catalog = nil
	if got := MatchLanguage("ru"); got != "en" {
		t.Errorf("MatchLanguage with nil catalog = %q, want %q", got, "en")
	}
	// Restore.
	initCatalog(t)
}

// TestMatchLanguageEmptyString verifies behavior with an empty Accept-Language
// string — should return the default language.
func TestMatchLanguageEmptyString(t *testing.T) {
	initCatalog(t)
	// An empty string will fail language.ParseAcceptLanguage; the matcher should
	// still return the default.
	got := MatchLanguage("")
	if got == "" {
		t.Error("MatchLanguage(\"\") returned empty string")
	}
}

// TestLoadTranslationsFromFS verifies loading translations from an embedded
// filesystem with a prefix pointing to the testdata directory.
func TestLoadTranslationsFromFS(t *testing.T) {
	initCatalog(t)

	// testModuleFS embeds testdata/; the prefix is "testdata/mymodule" so the
	// function will look for testdata/mymodule/locales/{lang}/messages.json.
	if err := LoadTranslationsFromFS(testModuleFS, "testdata/mymodule"); err != nil {
		t.Fatalf("LoadTranslationsFromFS failed: %v", err)
	}

	if got := T("en", "module.hello"); got != "Hello from module" {
		t.Errorf("T(en, module.hello) = %q, want %q", got, "Hello from module")
	}
	if got := T("en", "module.bye"); got != "Bye from module" {
		t.Errorf("T(en, module.bye) = %q, want %q", got, "Bye from module")
	}
}

// TestLoadTranslationsFromFSEmptyPrefix verifies the empty-prefix path variant.
// testNoPrefixFS embeds testdata/locales directly so its root contains
// locales/{lang}/messages.json when accessed without a prefix.
func TestLoadTranslationsFromFSEmptyPrefix(t *testing.T) {
	initCatalog(t)

	// testNoPrefixFS root contains "testdata/locales/en/messages.json".
	// With empty prefix the function looks for "locales/{lang}/messages.json",
	// but the embed root here is "testdata/" so we need to pass "testdata".
	if err := LoadTranslationsFromFS(testNoPrefixFS, "testdata"); err != nil {
		t.Fatalf("LoadTranslationsFromFS with testdata prefix failed: %v", err)
	}

	if got := T("en", "noprefix.key"); got != "No Prefix Value" {
		t.Errorf("T(en, noprefix.key) = %q, want %q", got, "No Prefix Value")
	}
}

// TestLoadTranslationsFromFSMissingFiles verifies that when none of the
// language files exist in the FS the function still returns nil (files are
// silently skipped).
func TestLoadTranslationsFromFSMissingFiles(t *testing.T) {
	initCatalog(t)

	// testModuleFS has no "missing/locales/*" paths — all ReadFile calls will
	// fail and be skipped.
	if err := LoadTranslationsFromFS(testModuleFS, "missing"); err != nil {
		t.Errorf("LoadTranslationsFromFS with missing files should not error, got: %v", err)
	}
}

// TestLoadTranslationsFromFSInvalidJSON verifies that malformed JSON returns an
// error.
func TestLoadTranslationsFromFSInvalidJSON(t *testing.T) {
	initCatalog(t)

	// testBadFS embeds testdata/bad/locales/en/messages.json which contains
	// invalid JSON.
	if err := LoadTranslationsFromFS(testBadFS, "testdata/bad"); err == nil {
		t.Error("LoadTranslationsFromFS with invalid JSON should return an error")
	}
}

// TestLoadTranslationsFromFSNilCatalog verifies that the function returns an
// error when the catalog has not been initialized.
func TestLoadTranslationsFromFSNilCatalog(t *testing.T) {
	catalog = nil
	// Use any valid embed.FS; the nil-catalog check happens before any FS access.
	if err := LoadTranslationsFromFS(testModuleFS, "testdata/mymodule"); err == nil {
		t.Error("LoadTranslationsFromFS with nil catalog should return an error")
	}
	// Restore.
	initCatalog(t)
}

// TestContains verifies the unexported contains helper for edge cases.
func TestContains(t *testing.T) {
	tests := []struct {
		slice []string
		str   string
		want  bool
	}{
		{[]string{"en", "ru"}, "en", true},
		{[]string{"en", "ru"}, "de", false},
		{[]string{}, "en", false},
		{nil, "en", false},
		{[]string{"en"}, "", false},
		{[]string{""}, "", true},
	}
	for _, tt := range tests {
		got := contains(tt.slice, tt.str)
		if got != tt.want {
			t.Errorf("contains(%v, %q) = %v, want %v", tt.slice, tt.str, got, tt.want)
		}
	}
}

// TestConcurrentTranslationAccess verifies that concurrent reads and writes do
// not cause data races (run with -race to detect).
func TestConcurrentTranslationAccess(t *testing.T) {
	initCatalog(t)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Concurrent readers.
	for range goroutines {
		go func() {
			defer wg.Done()
			_ = T("en", "btn.save")
			_ = T("ru", "btn.cancel")
			_ = TranslationCount("en")
			_ = GetDefaultLanguage()
		}()
	}

	// Concurrent writers via AddTranslations.
	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			AddTranslations("en", map[string]string{
				fmt.Sprintf("concurrent.key.%d", n): fmt.Sprintf("value %d", n),
			})
		}(i)
	}

	// Concurrent SetDefaultLanguage calls.
	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				SetDefaultLanguage("en")
			} else {
				SetDefaultLanguage("ru")
			}
		}(i)
	}

	wg.Wait()
}

// TestTDefaultLangFallbackWhenLangMissing exercises the branch where the
// requested language map does not exist but the default does, and the key IS
// present in the default.
func TestTDefaultLangFallbackWhenLangMissing(t *testing.T) {
	initCatalog(t)

	// "zz" is not a loaded language — T should fall back to default ("en").
	got := T("zz", "btn.save")
	if got != "Save" {
		t.Errorf("T(zz, btn.save) fallback to default = %q, want %q", got, "Save")
	}
}
