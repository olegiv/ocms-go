// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/olegiv/ocms-go/internal/util"
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
	if user == nil {
		http.NotFound(w, r)
		return
	}
	if err := pagewrite.RequireEditor(r.Context(), h.queries, user.ID); err != nil {
		previewFailure(w, r, err)
		return
	}
	if _, err := h.queries.GetPageByID(r.Context(), id); err != nil {
		previewFailure(w, r, err)
		return
	}
	snapshot, err := preview.Load(r.Context(), h.queries, id, revision)
	if err != nil {
		previewFailure(w, r, err)
		return
	}
	h.renderCapturedPreview(w, r, snapshot, true)
}

// previewFailure answers a preview that cannot be shown. A link or revision
// that is missing, revoked or not the viewer's to see is a 404; anything else
// is a fault, logged and answered 503, so a valid link never looks expired.
// The share token is in the request body, never in the logged path.
func previewFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, preview.ErrUnavailable) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, pagewrite.ErrEditorRequired) {
		http.NotFound(w, r)
		return
	}
	slog.Error("preview unavailable", "error", err, "path", util.BoundedLogField(r.URL.Path))
	http.Error(w, i18n.T(i18n.MatchLanguage(r.Header.Get("Accept-Language")), "preview.temporarily_unavailable"), http.StatusServiceUnavailable)
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

// SetAgentSharePolicy wires the live policy for credential-bound share links.
func (h *FrontendHandler) SetAgentSharePolicy(allowed preview.AgentSharePolicy) {
	h.agentShareAllowed = allowed
}

// PreviewShare resolves only a bounded POST body, without session credentials,
// redirects, bearer cookies or a stored redemption that could bypass revocation.
func (h *FrontendHandler) PreviewShare(w http.ResponseWriter, r *http.Request) {
	preview.Headers(w.Header())
	shares := preview.Service{Q: h.queries, AgentShareAllowed: h.agentShareAllowed}
	snapshot, issuer, err := shares.ResolveWithIssuer(r.Context(), postedPreviewToken(w, r))
	if err != nil {
		previewFailure(w, r, err)
		return
	}
	// The recorded revision can predate an authorship reassignment. Only the
	// person who issued the link answers for the profile it shows to anyone
	// holding it, so another recorded author's name, avatar, bio and links
	// stay out of the page (security audit 2026-11-03, C-8).
	h.renderCapturedPreview(w, r, snapshot, snapshot.Page.AuthorID == issuer)
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

// renderCapturedPreview renders a recorded snapshot. showAuthor says whether
// the recorded author's profile box may appear: signed-in editors always see
// it, an anonymous share only when the author is the user who issued the link.
func (h *FrontendHandler) renderCapturedPreview(w http.ResponseWriter, r *http.Request, snapshot preview.Snapshot, showAuthor bool) {
	preview.Headers(w.Header())
	page := snapshot.Page
	language, err := contentpath.RouteLanguage(r.Context(), h.queries, page.LanguageCode)
	if err != nil {
		previewFailure(w, r, err)
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
		slog.Error("failed to prepare preview body", "error", err, "page_id", page.ID, "revision", page.Revision)
		http.Error(w, i18n.T(language.Code, "preview.render_error"), http.StatusServiceUnavailable)
		return
	}
	page.Body = security.SanitizePageHTML(page.Body)
	view := h.pageToView(clean.Context(), page, base.LangCode, base.LangPrefix)
	// Relations deleted since the revision are skipped; a lookup fault fails
	// the preview instead of silently showing the revision without them.
	view.Author = nil
	if showAuthor {
		author, lookupErr := h.queries.GetUserByID(ctx, page.AuthorID)
		switch {
		case lookupErr == nil:
			view.Author = &AuthorView{ID: author.ID, Name: author.Name, Avatar: author.Avatar, Bio: author.Bio, WebsiteURL: author.WebsiteUrl, LinkedInURL: author.LinkedinUrl, GitHubURL: author.GithubUrl, TelegramURL: author.TelegramUrl}
		case !errors.Is(lookupErr, sql.ErrNoRows):
			previewFailure(w, r, lookupErr)
			return
		}
	}
	view.Categories = nil
	view.Category = nil
	view.Tags = nil
	for _, id := range snapshot.CategoryIDs {
		category, lookupErr := h.queries.GetCategoryByID(ctx, id)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			continue
		}
		if lookupErr != nil {
			previewFailure(w, r, lookupErr)
			return
		}
		view.Categories = append(view.Categories, CategoryView{ID: category.ID, Name: category.Name, Slug: category.Slug, URL: languagePrefixedURL(base.LangPrefix, "/category/"+category.Slug)})
		if view.Category == nil {
			view.Category = &view.Categories[0]
		}
	}
	for _, id := range snapshot.TagIDs {
		tag, lookupErr := h.queries.GetTagByID(ctx, id)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			continue
		}
		if lookupErr != nil {
			previewFailure(w, r, lookupErr)
			return
		}
		view.Tags = append(view.Tags, TagView{ID: tag.ID, Name: tag.Name, Slug: tag.Slug, URL: languagePrefixedURL(base.LangPrefix, "/tag/"+tag.Slug)})
	}
	if view.FeaturedImageLarge != "" {
		view.FeaturedImage = view.FeaturedImageLarge
	}
	base.Page = &view
	base.FeaturedImage = view.FeaturedImage
	base.BodyClass = "single-page preview-page"
	data := PageData{BaseTemplateData: base, Page: &view, ShowAuthorBox: showAuthor}
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
		slog.Error("failed to render preview", "error", err, "page_id", page.ID, "revision", page.Revision)
		http.Error(w, i18n.T(language.Code, "preview.render_error"), http.StatusServiceUnavailable)
		return
	}
	body, err := preview.SanitizeTheme(rendered.Bytes(), fmt.Sprintf(i18n.T(language.Code, "preview.banner"), page.ID, page.Revision), i18n.T(language.Code, "preview.placeholder"))
	if err != nil {
		slog.Error("failed to sanitize preview", "error", err, "page_id", page.ID, "revision", page.Revision)
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
