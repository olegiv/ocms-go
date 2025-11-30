// Package i18n provides internationalization support for the admin UI.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/text/language"
)

//go:embed locales
var localesFS embed.FS

// Message represents a single translatable message.
type Message struct {
	ID          string `json:"id"`
	Message     string `json:"message"`
	Translation string `json:"translation"`
}

// MessageFile represents the structure of a messages JSON file.
type MessageFile struct {
	Language string    `json:"language"`
	Messages []Message `json:"messages"`
}

// Catalog holds all translations for all supported languages.
type Catalog struct {
	mu           sync.RWMutex
	translations map[string]map[string]string // lang -> key -> translation
	matcher      language.Matcher
	supported    []language.Tag
	defaultLang  string
	logger       *slog.Logger
}

// catalog is the global catalog instance.
var catalog *Catalog

// SupportedLanguages lists the admin UI languages we support.
var SupportedLanguages = []string{"en", "ru"}

// Init initializes the i18n system with the given logger.
func Init(logger *slog.Logger) error {
	catalog = &Catalog{
		translations: make(map[string]map[string]string),
		defaultLang:  "en",
		logger:       logger,
	}

	// Build supported language tags
	tags := make([]language.Tag, 0, len(SupportedLanguages))
	for _, lang := range SupportedLanguages {
		tags = append(tags, language.MustParse(lang))
	}
	catalog.supported = tags
	catalog.matcher = language.NewMatcher(tags)

	// Load translations from embedded filesystem
	for _, lang := range SupportedLanguages {
		if err := catalog.loadLanguage(lang); err != nil {
			return fmt.Errorf("failed to load language %s: %w", lang, err)
		}
	}

	if logger != nil {
		logger.Info("i18n initialized", "languages", SupportedLanguages)
	}

	return nil
}

// loadLanguage loads translations for a specific language.
func (c *Catalog) loadLanguage(lang string) error {
	path := fmt.Sprintf("locales/%s/messages.json", lang)
	data, err := localesFS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	var msgFile MessageFile
	if err := json.Unmarshal(data, &msgFile); err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.translations[lang] = make(map[string]string)
	for _, msg := range msgFile.Messages {
		c.translations[lang][msg.ID] = msg.Translation
	}

	if c.logger != nil {
		c.logger.Debug("loaded translations", "language", lang, "count", len(msgFile.Messages))
	}

	return nil
}

// T translates a message key to the specified language.
// If the key is not found, it returns the key itself.
// Supports optional arguments for string formatting.
func T(lang, key string, args ...any) string {
	if catalog == nil {
		return key
	}

	catalog.mu.RLock()
	defer catalog.mu.RUnlock()

	// Get translations for the requested language
	langTranslations, ok := catalog.translations[lang]
	if !ok {
		// Fall back to default language
		langTranslations, ok = catalog.translations[catalog.defaultLang]
		if !ok {
			return key
		}
	}

	// Get the translation
	translation, ok := langTranslations[key]
	if !ok {
		// Try default language as fallback
		if lang != catalog.defaultLang {
			if defaultTranslations, ok := catalog.translations[catalog.defaultLang]; ok {
				if translation, ok = defaultTranslations[key]; ok {
					// Log missing translation
					if catalog.logger != nil {
						catalog.logger.Debug("missing translation, using default", "key", key, "lang", lang)
					}
					if len(args) > 0 {
						return fmt.Sprintf(translation, args...)
					}
					return translation
				}
			}
		}
		return key
	}

	// Format with arguments if provided
	if len(args) > 0 {
		return fmt.Sprintf(translation, args...)
	}

	return translation
}

// GetSupportedLanguages returns the list of supported admin UI languages.
func GetSupportedLanguages() []string {
	return SupportedLanguages
}

// MatchLanguage finds the best matching supported language for the given string.
// Returns the language code (e.g., "en", "ru").
func MatchLanguage(acceptLang string) string {
	if catalog == nil {
		return "en"
	}

	// Try to parse the Accept-Language header or language code
	tags, _, err := language.ParseAcceptLanguage(acceptLang)
	if err != nil || len(tags) == 0 {
		// Try as a single language code
		tag, err := language.Parse(acceptLang)
		if err != nil {
			return catalog.defaultLang
		}
		tags = []language.Tag{tag}
	}

	// Match against supported languages
	_, idx, _ := catalog.matcher.Match(tags...)
	if idx >= 0 && idx < len(catalog.supported) {
		return catalog.supported[idx].String()
	}

	return catalog.defaultLang
}

// IsSupported checks if a language code is supported for the admin UI.
func IsSupported(lang string) bool {
	lang = strings.ToLower(lang)
	for _, supported := range SupportedLanguages {
		if supported == lang {
			return true
		}
	}
	return false
}

// GetCatalog returns the global catalog instance (for testing).
func GetCatalog() *Catalog {
	return catalog
}

// TranslationCount returns the number of translations loaded for a language.
func TranslationCount(lang string) int {
	if catalog == nil {
		return 0
	}

	catalog.mu.RLock()
	defer catalog.mu.RUnlock()

	if translations, ok := catalog.translations[lang]; ok {
		return len(translations)
	}
	return 0
}
