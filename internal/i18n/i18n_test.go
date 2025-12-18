package i18n

import (
	"encoding/json"
	"fmt"
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
