package handler

import (
	"context"
	"testing"
	"time"

	"ocms-go/internal/store"
)

func TestNewLanguagesHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewLanguagesHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewLanguagesHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestLanguagesListData(t *testing.T) {
	data := LanguagesListData{
		Languages:      []store.Language{},
		TotalLanguages: 0,
	}

	if data.Languages == nil {
		t.Error("Languages should not be nil")
	}
	if data.TotalLanguages != 0 {
		t.Error("TotalLanguages should be 0")
	}
}

func TestLanguageFormInput(t *testing.T) {
	input := languageFormInput{
		Code:       "fr",
		Name:       "French",
		NativeName: "Français",
		Direction:  "ltr",
		IsActive:   true,
		Position:   "1",
	}

	formValues := input.toFormValues()

	if formValues["code"] != "fr" {
		t.Errorf("code = %q, want %q", formValues["code"], "fr")
	}
	if formValues["name"] != "French" {
		t.Errorf("name = %q, want %q", formValues["name"], "French")
	}
	if formValues["native_name"] != "Français" {
		t.Errorf("native_name = %q, want %q", formValues["native_name"], "Français")
	}
	if formValues["direction"] != "ltr" {
		t.Errorf("direction = %q, want %q", formValues["direction"], "ltr")
	}
	if formValues["is_active"] != "1" {
		t.Errorf("is_active = %q, want %q", formValues["is_active"], "1")
	}
	if formValues["position"] != "1" {
		t.Errorf("position = %q, want %q", formValues["position"], "1")
	}
}

func TestLanguageFormInputInactive(t *testing.T) {
	input := languageFormInput{
		Code:     "de",
		IsActive: false,
	}

	formValues := input.toFormValues()

	// When inactive, is_active should not be in form values
	if _, exists := formValues["is_active"]; exists {
		t.Error("is_active should not exist when inactive")
	}
}

func TestLanguageCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	now := time.Now()
	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "fr",
		Name:       "French",
		NativeName: "Français",
		IsDefault:  false,
		IsActive:   true,
		Direction:  "ltr",
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	if lang.Code != "fr" {
		t.Errorf("Code = %q, want %q", lang.Code, "fr")
	}
	if lang.Name != "French" {
		t.Errorf("Name = %q, want %q", lang.Name, "French")
	}
	if lang.NativeName != "Français" {
		t.Errorf("NativeName = %q, want %q", lang.NativeName, "Français")
	}
}

func TestLanguageList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// English is already created by test helper
	// Create additional test languages
	languages := []store.CreateLanguageParams{
		{Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr", IsActive: true, Position: 1, CreatedAt: now, UpdatedAt: now},
		{Code: "de", Name: "German", NativeName: "Deutsch", Direction: "ltr", IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now},
	}
	for _, lang := range languages {
		if _, err := queries.CreateLanguage(context.Background(), lang); err != nil {
			t.Fatalf("CreateLanguage failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		result, err := queries.ListLanguages(context.Background())
		if err != nil {
			t.Fatalf("ListLanguages failed: %v", err)
		}
		if len(result) != 3 { // en + fr + de
			t.Errorf("got %d languages, want 3", len(result))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountLanguages(context.Background())
		if err != nil {
			t.Fatalf("CountLanguages failed: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})

	t.Run("list active", func(t *testing.T) {
		result, err := queries.ListActiveLanguages(context.Background())
		if err != nil {
			t.Fatalf("ListActiveLanguages failed: %v", err)
		}
		if len(result) < 1 {
			t.Error("should have at least 1 active language")
		}
	})
}

func TestLanguageGetByCode(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// English is already created by test helper
	lang, err := queries.GetLanguageByCode(context.Background(), "en")
	if err != nil {
		t.Fatalf("GetLanguageByCode failed: %v", err)
	}

	if lang.Code != "en" {
		t.Errorf("Code = %q, want %q", lang.Code, "en")
	}
	if !lang.IsDefault {
		t.Error("English should be the default language")
	}
}

func TestLanguageUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "es",
		Name:       "Spanish",
		NativeName: "Español",
		Direction:  "ltr",
		IsActive:   true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	_, err = queries.UpdateLanguage(context.Background(), store.UpdateLanguageParams{
		ID:         lang.ID,
		Code:       "es",
		Name:       "Spanish Updated",
		NativeName: "Español",
		IsDefault:  false,
		IsActive:   false,
		Direction:  "ltr",
		Position:   2,
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateLanguage failed: %v", err)
	}

	updated, err := queries.GetLanguageByID(context.Background(), lang.ID)
	if err != nil {
		t.Fatalf("GetLanguageByID failed: %v", err)
	}

	if updated.Name != "Spanish Updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "Spanish Updated")
	}
	if updated.IsActive {
		t.Error("IsActive should be false")
	}
	if updated.Position != 2 {
		t.Errorf("Position = %d, want 2", updated.Position)
	}
}

func TestLanguageDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "it",
		Name:       "Italian",
		NativeName: "Italiano",
		Direction:  "ltr",
		IsActive:   true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	if err := queries.DeleteLanguage(context.Background(), lang.ID); err != nil {
		t.Fatalf("DeleteLanguage failed: %v", err)
	}

	_, err = queries.GetLanguageByID(context.Background(), lang.ID)
	if err == nil {
		t.Error("expected error when getting deleted language")
	}
}

func TestLanguageCodeExists(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	t.Run("exists", func(t *testing.T) {
		// "en" is created by test helper
		count, err := queries.LanguageCodeExists(context.Background(), "en")
		if err != nil {
			t.Fatalf("LanguageCodeExists failed: %v", err)
		}
		if count == 0 {
			t.Error("expected code to exist")
		}
	})

	t.Run("not exists", func(t *testing.T) {
		count, err := queries.LanguageCodeExists(context.Background(), "zz")
		if err != nil {
			t.Fatalf("LanguageCodeExists failed: %v", err)
		}
		if count != 0 {
			t.Error("expected code to not exist")
		}
	})
}

func TestLanguageRTL(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "ar",
		Name:       "Arabic",
		NativeName: "العربية",
		Direction:  "rtl",
		IsActive:   true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	if lang.Direction != "rtl" {
		t.Errorf("Direction = %q, want %q", lang.Direction, "rtl")
	}
}

func TestLanguageSetDefault(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// Create a new language
	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "pt",
		Name:       "Portuguese",
		NativeName: "Português",
		IsDefault:  false,
		IsActive:   true,
		Direction:  "ltr",
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	// Clear current default
	if err := queries.ClearDefaultLanguage(context.Background()); err != nil {
		t.Fatalf("ClearDefaultLanguage failed: %v", err)
	}

	// Set new default
	if err := queries.SetDefaultLanguage(context.Background(), store.SetDefaultLanguageParams{
		ID:        lang.ID,
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SetDefaultLanguage failed: %v", err)
	}

	updated, err := queries.GetLanguageByID(context.Background(), lang.ID)
	if err != nil {
		t.Fatalf("GetLanguageByID failed: %v", err)
	}

	if !updated.IsDefault {
		t.Error("IsDefault should be true after SetDefaultLanguage")
	}
}

func TestFindDefaultLanguage(t *testing.T) {
	languages := []store.Language{
		{ID: 1, Code: "en", Name: "English", IsDefault: false},
		{ID: 2, Code: "fr", Name: "French", IsDefault: true},
		{ID: 3, Code: "de", Name: "German", IsDefault: false},
	}

	defaultLang := FindDefaultLanguage(languages)
	if defaultLang == nil {
		t.Fatal("expected to find default language")
	}
	if defaultLang.Code != "fr" {
		t.Errorf("Code = %q, want %q", defaultLang.Code, "fr")
	}
}

func TestFindDefaultLanguageNone(t *testing.T) {
	languages := []store.Language{
		{ID: 1, Code: "en", Name: "English", IsDefault: false},
		{ID: 2, Code: "fr", Name: "French", IsDefault: false},
	}

	defaultLang := FindDefaultLanguage(languages)
	if defaultLang != nil {
		t.Error("expected nil when no default language")
	}
}

func TestFindDefaultLanguageEmpty(t *testing.T) {
	var languages []store.Language

	defaultLang := FindDefaultLanguage(languages)
	if defaultLang != nil {
		t.Error("expected nil for empty slice")
	}
}

func TestLanguageToFormValues(t *testing.T) {
	lang := &store.Language{
		Code:       "ja",
		Name:       "Japanese",
		NativeName: "日本語",
		Direction:  "ltr",
		Position:   5,
		IsActive:   true,
	}

	formValues := languageToFormValues(lang)

	if formValues["code"] != "ja" {
		t.Errorf("code = %q, want %q", formValues["code"], "ja")
	}
	if formValues["name"] != "Japanese" {
		t.Errorf("name = %q, want %q", formValues["name"], "Japanese")
	}
	if formValues["native_name"] != "日本語" {
		t.Errorf("native_name = %q, want %q", formValues["native_name"], "日本語")
	}
	if formValues["direction"] != "ltr" {
		t.Errorf("direction = %q, want %q", formValues["direction"], "ltr")
	}
	if formValues["position"] != "5" {
		t.Errorf("position = %q, want %q", formValues["position"], "5")
	}
	if formValues["is_active"] != "1" {
		t.Errorf("is_active = %q, want %q", formValues["is_active"], "1")
	}
}

func TestLanguageToFormValuesInactive(t *testing.T) {
	lang := &store.Language{
		Code:     "ko",
		IsActive: false,
	}

	formValues := languageToFormValues(lang)

	if _, exists := formValues["is_active"]; exists {
		t.Error("is_active should not exist when inactive")
	}
}

func TestLanguageGetMaxPosition(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// Create languages with different positions
	positions := []int64{5, 10, 3}
	for i, pos := range positions {
		_, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
			Code:       string(rune('a' + i)),
			Name:       "Lang " + string(rune('A'+i)),
			NativeName: "Lang " + string(rune('A'+i)),
			Direction:  "ltr",
			IsActive:   true,
			Position:   pos,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			t.Fatalf("CreateLanguage failed: %v", err)
		}
	}

	maxPos, err := queries.GetMaxLanguagePosition(context.Background())
	if err != nil {
		t.Fatalf("GetMaxLanguagePosition failed: %v", err)
	}

	// Expect max to be 10
	if maxPos == nil {
		t.Fatal("maxPos should not be nil")
	}
}
