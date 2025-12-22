package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ocms-go/internal/store"
)

func TestMatchAcceptLanguage(t *testing.T) {
	langMap := map[string]store.Language{
		"en": {ID: 1, Code: "en", Name: "English", IsDefault: true},
		"ru": {ID: 2, Code: "ru", Name: "Russian"},
		"de": {ID: 3, Code: "de", Name: "German"},
	}

	tests := []struct {
		name       string
		acceptLang string
		wantCode   string
		wantNil    bool
	}{
		{
			name:       "exact match",
			acceptLang: "en",
			wantCode:   "en",
		},
		{
			name:       "exact match with quality",
			acceptLang: "ru;q=0.9",
			wantCode:   "ru",
		},
		{
			name:       "first match wins",
			acceptLang: "de,en;q=0.9,ru;q=0.8",
			wantCode:   "de",
		},
		{
			name:       "primary code match",
			acceptLang: "en-US",
			wantCode:   "en",
		},
		{
			name:       "primary code match with region",
			acceptLang: "de-DE,en;q=0.9",
			wantCode:   "de",
		},
		{
			name:       "no match",
			acceptLang: "fr,es",
			wantNil:    true,
		},
		{
			name:       "case insensitive",
			acceptLang: "EN-US",
			wantCode:   "en",
		},
		{
			name:       "multiple with spaces",
			acceptLang: " ru , en;q=0.8 ",
			wantCode:   "ru",
		},
		{
			name:       "empty string",
			acceptLang: "",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchAcceptLanguage(tt.acceptLang, langMap)

			if tt.wantNil {
				if got != nil {
					t.Errorf("matchAcceptLanguage() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("matchAcceptLanguage() = nil, want language")
			}
			if got.Code != tt.wantCode {
				t.Errorf("matchAcceptLanguage().Code = %q, want %q", got.Code, tt.wantCode)
			}
		})
	}
}

func TestSetLanguageContext(t *testing.T) {
	lang := store.Language{
		ID:         1,
		Code:       "en",
		Name:       "English",
		NativeName: "English",
		Direction:  "ltr",
		IsDefault:  true,
	}

	ctx := context.Background()
	newCtx := setLanguageContext(ctx, lang)

	// Check LanguageInfo
	info, ok := newCtx.Value(ContextKeyLanguage).(LanguageInfo)
	if !ok {
		t.Fatal("ContextKeyLanguage not set")
	}
	if info.ID != 1 {
		t.Errorf("LanguageInfo.ID = %d, want 1", info.ID)
	}
	if info.Code != "en" {
		t.Errorf("LanguageInfo.Code = %q, want %q", info.Code, "en")
	}
	if info.Name != "English" {
		t.Errorf("LanguageInfo.Name = %q, want %q", info.Name, "English")
	}
	if info.Direction != "ltr" {
		t.Errorf("LanguageInfo.Direction = %q, want %q", info.Direction, "ltr")
	}
	if !info.IsDefault {
		t.Error("LanguageInfo.IsDefault = false, want true")
	}

	// Check language code
	code, ok := newCtx.Value(ContextKeyLanguageCode).(string)
	if !ok {
		t.Fatal("ContextKeyLanguageCode not set")
	}
	if code != "en" {
		t.Errorf("ContextKeyLanguageCode = %q, want %q", code, "en")
	}
}

func TestGetLanguage(t *testing.T) {
	t.Run("no language in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		lang := GetLanguage(req)
		if lang != nil {
			t.Errorf("GetLanguage() = %v, want nil", lang)
		}
	})

	t.Run("language in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		langInfo := LanguageInfo{
			ID:         2,
			Code:       "ru",
			Name:       "Russian",
			NativeName: "Русский",
			Direction:  "ltr",
			IsDefault:  false,
		}
		ctx := context.WithValue(req.Context(), ContextKeyLanguage, langInfo)
		req = req.WithContext(ctx)

		lang := GetLanguage(req)
		if lang == nil {
			t.Fatal("GetLanguage() = nil, want language")
		}
		if lang.ID != 2 {
			t.Errorf("GetLanguage().ID = %d, want 2", lang.ID)
		}
		if lang.Code != "ru" {
			t.Errorf("GetLanguage().Code = %q, want %q", lang.Code, "ru")
		}
		if lang.NativeName != "Русский" {
			t.Errorf("GetLanguage().NativeName = %q, want %q", lang.NativeName, "Русский")
		}
	})
}

func TestSetLanguageCookie(t *testing.T) {
	rr := httptest.NewRecorder()

	SetLanguageCookie(rr, "ru")

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Expected 1 cookie, got %d", len(cookies))
	}

	cookie := cookies[0]
	if cookie.Name != LanguageCookieName {
		t.Errorf("Cookie name = %q, want %q", cookie.Name, LanguageCookieName)
	}
	if cookie.Value != "ru" {
		t.Errorf("Cookie value = %q, want %q", cookie.Value, "ru")
	}
	if cookie.Path != "/" {
		t.Errorf("Cookie path = %q, want %q", cookie.Path, "/")
	}
	if !cookie.HttpOnly {
		t.Error("Cookie should be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("Cookie SameSite = %v, want %v", cookie.SameSite, http.SameSiteLaxMode)
	}
	if cookie.MaxAge <= 0 {
		t.Error("Cookie MaxAge should be positive (1 year)")
	}
}
