// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/alexedwards/scs/v2"
	"github.com/olegiv/ocms-go/internal/editorial"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

const RouteEditorial = "/editorial"
const editorialPath = "/admin/editorial"

// EditorialHandler manages trusted site guidance behind the existing administrator/CSRF chain.
type EditorialHandler struct {
	queries  *store.Queries
	service  *editorial.Service
	renderer *render.Renderer
	sessions *scs.SessionManager
}

func NewEditorialHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *EditorialHandler {
	q := store.New(db)
	return &EditorialHandler{queries: q, service: editorial.New(q), renderer: renderer, sessions: sm}
}

func editorialLocation(language, preset string) string {
	query := url.Values{}
	if language != "" {
		query.Set("language_code", language)
	}
	if preset != "" {
		query.Set("preset", preset)
	}
	if len(query) == 0 {
		return editorialPath
	}
	return editorialPath + "?" + query.Encode()
}

func (h *EditorialHandler) view(r *http.Request, language, preset string) (adminviews.EditorialData, error) {
	data := adminviews.EditorialData{LanguageCode: language, PresetSlug: preset, IsDemoMode: middleware.IsDemoMode()}
	guide, err := h.service.Guide(r.Context(), language)
	if err != nil {
		return data, err
	}
	data.Guide, data.Inherited = guide.Body, guide.Inherited
	presets, err := h.service.Presets(r.Context(), language)
	if err != nil {
		return data, err
	}
	if data.PresetSlug == "" {
		data.PresetSlug = "blog-post"
	}
	for _, p := range presets {
		data.Presets = append(data.Presets, adminviews.EditorialOption{Code: p.Slug, Name: p.Name, URL: editorialLocation(language, p.Slug)})
		if p.Slug == data.PresetSlug {
			data.PresetName, data.PresetInstructions = p.Name, p.Instructions
		}
	}
	if data.PresetName == "" {
		return data, editorial.ErrInvalid
	}
	languages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		return data, err
	}
	for _, lang := range languages {
		if util.IsRoutableLanguageCode(lang.Code) {
			data.Languages = append(data.Languages, adminviews.EditorialOption{Code: lang.Code, Name: lang.NativeName, URL: editorialLocation(lang.Code, data.PresetSlug)})
		}
	}
	data.DefaultURL = editorialLocation("", data.PresetSlug)
	return data, nil
}

func (h *EditorialHandler) render(w http.ResponseWriter, r *http.Request, data adminviews.EditorialData, status int) {
	pc := buildPageContext(r, h.sessions, h.renderer, i18n.T(middleware.GetAdminLang(r), "editorial.title"), nil)
	if status != http.StatusOK {
		w.Header().Set(HeaderContentType, "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	renderTempl(w, r, adminviews.EditorialPage(pc, data))
}

// Get renders default guidance or a per-language override.
func (h *EditorialHandler) Get(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || user.Role != model.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	data, err := h.view(r, r.URL.Query().Get("language_code"), r.URL.Query().Get("preset"))
	if errors.Is(err, editorial.ErrInvalid) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("load editorial settings", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, data, http.StatusOK)
}

// Update retains submitted text on validation failure and never accepts an actor from the form.
func (h *EditorialHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || user.Role != model.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if demoGuard(w, r, h.renderer, middleware.RestrictionEditConfig, editorialPath) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(middleware.GetAdminLang(r), "editorial.invalid"), http.StatusBadRequest)
		return
	}
	language, preset := r.PostForm.Get("language_code"), r.PostForm.Get("preset")
	data, err := h.view(r, language, preset)
	if err != nil {
		if errors.Is(err, editorial.ErrInvalid) {
			http.Error(w, i18n.T(middleware.GetAdminLang(r), "editorial.invalid"), http.StatusBadRequest)
		} else {
			slog.Error("load editorial save form", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	switch r.PostForm.Get("kind") {
	case "guide":
		data.Guide = r.PostForm.Get("guide")
		err = h.service.SaveGuide(r.Context(), user.ID, language, data.Guide)
	case "preset":
		data.PresetName, data.PresetInstructions = r.PostForm.Get("name"), r.PostForm.Get("instructions")
		err = h.service.SavePreset(r.Context(), user.ID, language, preset, data.PresetName, data.PresetInstructions)
	default:
		err = editorial.ErrInvalid
	}
	if errors.Is(err, editorial.ErrForbidden) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, editorial.ErrInvalid) {
		data.Error = i18n.T(middleware.GetAdminLang(r), "editorial.invalid")
		h.render(w, r, data, http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		slog.Error("save editorial settings", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	flashSuccess(w, r, h.renderer, editorialLocation(language, data.PresetSlug), i18n.T(middleware.GetAdminLang(r), "editorial.saved"))
}
