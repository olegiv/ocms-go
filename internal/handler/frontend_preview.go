// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
	previewviews "github.com/olegiv/ocms-go/internal/views/preview"
)

// PreviewSession renders exactly the recorded revision, without going through page caches.
func (h *FrontendHandler) PreviewSession(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	revision, _ := strconv.ParseInt(chi.URLParam(r, "revision"), 10, 64)
	h.renderRevisionPreview(w, r, id, revision)
}

func (h *FrontendHandler) renderRevisionPreview(w http.ResponseWriter, r *http.Request, id, revision int64) {
	preview.Headers(w.Header())
	user := middleware.GetUser(r)
	if user == nil || pagewrite.RequireEditor(r.Context(), h.queries, user.ID) != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.queries.GetPageByID(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	snapshot, err := preview.Load(r.Context(), h.queries, id, revision)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.renderCapturedPreview(w, r, snapshot)
}

// PreviewShareLanding is deliberately independent of themes and integrations:
// only its nonce-authorized bootstrap may see the browser-only fragment.
func (h *FrontendHandler) PreviewShareLanding(w http.ResponseWriter, r *http.Request) {
	preview.Headers(w.Header())
	if !preview.ShareRequestAllowed(r) {
		http.NotFound(w, r)
		return
	}
	nonce := middleware.GetCSPNonce(r)
	if nonce == "" {
		http.Error(w, "Preview unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	renderTempl(w, r, previewviews.ShareLanding(i18n.MatchLanguage(r.Header.Get("Accept-Language"))))
}

// PreviewShare resolves only a bounded POST body, without session credentials,
// redirects, bearer cookies or a stored redemption that could bypass revocation.
func (h *FrontendHandler) PreviewShare(w http.ResponseWriter, r *http.Request) {
	preview.Headers(w.Header())
	snapshot, err := (preview.Service{Q: h.queries}).Resolve(r.Context(), postedPreviewToken(w, r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.renderCapturedPreview(w, r, snapshot)
}

func postedPreviewToken(w http.ResponseWriter, r *http.Request) string {
	if r.Method != http.MethodPost || !preview.ShareRequestAllowed(r) {
		return ""
	}
	body := http.MaxBytesReader(w, r.Body, 256)
	r.Body = body
	r.Form, r.PostForm = nil, nil
	defer func() {
		_ = body.Close()
		r.Body, r.ContentLength = http.NoBody, 0
		r.Form, r.PostForm = nil, nil
	}()
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" || r.ParseForm() != nil || len(r.PostForm) != 1 || len(r.PostForm["token"]) != 1 {
		return ""
	}
	return r.PostForm["token"][0]
}

func (h *FrontendHandler) renderCapturedPreview(w http.ResponseWriter, r *http.Request, snapshot preview.Snapshot) {
	preview.Headers(w.Header())
	page := snapshot.Page
	language, err := contentpath.RouteLanguage(r.Context(), h.queries, page.LanguageCode)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if requested := chi.URLParam(r, "lang"); requested != "" && requested != language.Code {
		http.NotFound(w, r)
		return
	}
	// Public-only context for every auxiliary query, even for an authenticated editor.
	ctx := context.WithValue(preview.Context(r.Context()), middleware.ContextKeyUser, (*store.User)(nil))
	clean := r.Clone(ctx)
	cleanURL := *r.URL
	cleanURL.Path = "/" + language.Code + "/" + page.Slug
	cleanURL.RawPath = ""
	cleanURL.RawQuery = ""
	cleanURL.Fragment = ""
	clean.URL = &cleanURL
	clean.RequestURI = cleanURL.EscapedPath()
	clean = requestWithFrontendLanguage(clean, language)
	base := h.getBaseTemplateData(clean, page.Title, page.Summary)
	base.MainMenu = menuItemsToView(h.previewMenu(clean.Context(), "main", language.Code), clean.URL.Path)
	base.FooterMenu = menuItemsToView(h.previewMenu(clean.Context(), "footer", language.Code), clean.URL.Path)
	base.Navigation = base.MainMenu
	base.FooterNav = base.FooterMenu
	base.RequestURI = clean.RequestURI
	base.Robots = "noindex, nofollow"
	base.Canonical = ""
	base.OGURL = ""
	base.JSONLD = ""
	base.FeedLinks = nil
	base.Translations = nil
	base.HrefLangs = nil
	base.ShowLanguagePicker = false
	base.Widgets = nil
	base.FooterWidgets = nil
	base.ShowSearch = false
	base.ModuleHeadHTML = ""
	base.ModuleBodyTopHTML = ""
	base.ModuleBodyEndHTML = ""
	page.Body, err = preview.PrepareBody(page.Body, i18n.T(language.Code, "preview.placeholder"))
	if err != nil {
		http.Error(w, i18n.T(language.Code, "preview.render_error"), http.StatusServiceUnavailable)
		return
	}
	page.Body = security.SanitizePageHTML(page.Body)
	view := h.pageToView(clean.Context(), page, base.LangCode, base.LangPrefix)
	view.Author = nil
	if author, e := h.queries.GetUserByID(ctx, page.AuthorID); e == nil {
		view.Author = &AuthorView{ID: author.ID, Name: author.Name, Avatar: author.Avatar, Bio: author.Bio, WebsiteURL: author.WebsiteUrl, LinkedInURL: author.LinkedinUrl, GitHubURL: author.GithubUrl, TelegramURL: author.TelegramUrl}
	}
	view.Categories = nil
	view.Category = nil
	view.Tags = nil
	for _, id := range snapshot.CategoryIDs {
		if category, e := h.queries.GetCategoryByID(ctx, id); e == nil {
			item := CategoryView{ID: category.ID, Name: category.Name, Slug: category.Slug, URL: languagePrefixedURL(base.LangPrefix, "/category/"+category.Slug)}
			view.Categories = append(view.Categories, item)
			if view.Category == nil {
				view.Category = &view.Categories[0]
			}
		}
	}
	for _, id := range snapshot.TagIDs {
		if tag, e := h.queries.GetTagByID(ctx, id); e == nil {
			view.Tags = append(view.Tags, TagView{ID: tag.ID, Name: tag.Name, Slug: tag.Slug, URL: languagePrefixedURL(base.LangPrefix, "/tag/"+tag.Slug)})
		}
	}
	if view.FeaturedImageLarge != "" {
		view.FeaturedImage = view.FeaturedImageLarge
	}
	base.Page = &view
	base.FeaturedImage = view.FeaturedImage
	base.BodyClass = "single-page preview-page"
	data := PageData{BaseTemplateData: base, Page: &view, ShowAuthorBox: true}
	var rendered bytes.Buffer
	active := h.themeManager.GetActiveTheme()
	if active != nil && active.RenderEngine() == theme.EngineHTML {
		isolated := *active
		isolated.Templates, err = active.Templates.Clone()
		if err == nil {
			isolated.Templates = isolated.Templates.Funcs(h.previewTemplateFuncs(clean.Context(), language.Code))
			err = isolated.RenderPage(&rendered, "page", data)
		}
	} else if component := templComponent("page", data); component != nil {
		err = component.Render(clean.Context(), &rendered)
	} else {
		err = fmt.Errorf("preview theme has no page layout")
	}
	if err != nil {
		http.Error(w, i18n.T(language.Code, "preview.render_error"), http.StatusServiceUnavailable)
		return
	}
	body, err := preview.SanitizeTheme(rendered.Bytes(), fmt.Sprintf(i18n.T(language.Code, "preview.banner"), page.ID, page.Revision), i18n.T(language.Code, "preview.placeholder"))
	if err != nil {
		http.Error(w, i18n.T(language.Code, "preview.render_error"), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

// sessionPreviewPath is intentionally independent of current slug routing.
func sessionPreviewPath(page store.Page) string {
	return preview.SessionURL("", page.LanguageCode, page.ID, page.Revision)
}
