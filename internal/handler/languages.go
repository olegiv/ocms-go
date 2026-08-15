// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"

	"github.com/alexedwards/scs/v2"
)

// LanguagesHandler handles language management in admin.
type LanguagesHandler struct {
	db             *sql.DB
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	cacheManager   *cache.Manager
}

const languageStateRefreshTimeout = 5 * time.Second

func detachedLanguageStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), languageStateRefreshTimeout)
}

// NewLanguagesHandler creates a new LanguagesHandler.
func NewLanguagesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager,
	cacheManagers ...*cache.Manager) *LanguagesHandler {
	h := &LanguagesHandler{
		db:             db,
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
	if len(cacheManagers) > 0 {
		h.cacheManager = cacheManagers[0]
	}
	return h
}

// updateLanguage atomically propagates a legacy code rename through every
// table that denormalizes languages.code into a language_code column. These
// columns intentionally have no foreign key, so updating only languages would
// make the existing content, menus, taxonomy, media and forms unreachable.
//
// Every update runs in a transaction, rename or not, because the page-prefix
// check belongs inside it. Deciding outside the write leaves room for a page
// saved at the same first segment to commit in between, after which the
// language middleware consumes that page's URL with neither request having
// seen a conflict.
func (h *LanguagesHandler) updateLanguage(ctx context.Context, params store.UpdateLanguageParams,
	previousCode string) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin language update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := store.New(h.db).WithTx(tx)

	conflict, err := validateLanguagePrefixAgainstPages(ctx, queries, params.Code, params.IsActive)
	if err != nil {
		return err
	}
	if conflict != "" {
		return errLanguagePrefixTaken
	}

	if params.Code == previousCode {
		if _, err := queries.UpdateLanguage(ctx, params); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit language update: %w", err)
		}
		return nil
	}

	for _, query := range []string{
		`UPDATE pages SET language_code = ? WHERE language_code = ?`,
		`UPDATE tags SET language_code = ? WHERE language_code = ?`,
		`UPDATE categories SET language_code = ? WHERE language_code = ?`,
		`UPDATE menus SET language_code = ? WHERE language_code = ?`,
		`UPDATE forms SET language_code = ? WHERE language_code = ?`,
		`UPDATE form_fields SET language_code = ? WHERE language_code = ?`,
		`UPDATE form_submissions SET language_code = ? WHERE language_code = ?`,
		`UPDATE widgets SET language_code = ? WHERE language_code = ?`,
		`UPDATE media SET language_code = ? WHERE language_code = ?`,
		`UPDATE config SET language_code = ? WHERE language_code = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, params.Code, previousCode); err != nil {
			return fmt.Errorf("propagate language code %q to %q: %w",
				previousCode, params.Code, err)
		}
	}

	if _, err := queries.UpdateLanguage(ctx, params); err != nil {
		return fmt.Errorf("update renamed language: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit language rename: %w", err)
	}
	return nil
}

func refreshI18nLanguageState(ctx context.Context, queries *store.Queries) error {
	activeLanguages, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		return fmt.Errorf("list active languages: %w", err)
	}
	defaultLanguage, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return fmt.Errorf("get default language: %w", err)
	}
	activeCodes := make([]string, 0, len(activeLanguages))
	for _, language := range activeLanguages {
		activeCodes = append(activeCodes, language.Code)
	}
	i18n.ConfigureLanguages(activeCodes, defaultLanguage.Code)
	return nil
}

func (h *LanguagesHandler) invalidateLanguageCaches(ctx context.Context) {
	refreshCtx, cancel := detachedLanguageStateContext(ctx)
	defer cancel()
	if err := refreshI18nLanguageState(refreshCtx, h.queries); err != nil {
		slog.Error("failed to refresh runtime language state", "error", err)
	}
	if h.cacheManager == nil {
		return
	}
	h.cacheManager.InvalidateLanguages()
	h.cacheManager.InvalidateTranslations()
	h.cacheManager.InvalidateContent()
	h.cacheManager.InvalidateMenus()
	h.cacheManager.InvalidateConfig()
	h.cacheManager.General.Clear()
}

func (h *LanguagesHandler) setDefaultLanguage(ctx context.Context, id int64) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin default-language switch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := store.New(h.db).WithTx(tx)
	target, err := queries.GetLanguageByID(ctx, id)
	if err != nil {
		return fmt.Errorf("re-read default-language target: %w", err)
	}
	if !target.IsActive {
		return errors.New("cannot set an inactive language as default")
	}
	if validationError := validateLanguageCodeForSave(target.Code, true, target.Code); validationError != "" {
		return fmt.Errorf("cannot set language %q as default: %s", target.Code, validationError)
	}
	// Read through the transaction: a page created between this check and the
	// commit would otherwise become unreachable behind the new default's prefix.
	conflict, err := validateLanguagePrefixAgainstPages(ctx, queries, target.Code, true)
	if err != nil {
		return err
	}
	if conflict != "" {
		return fmt.Errorf("cannot set language %q as default: %s", target.Code, conflict)
	}
	if err := queries.ClearDefaultLanguage(ctx); err != nil {
		return fmt.Errorf("clear previous default language: %w", err)
	}
	if err := queries.SetDefaultLanguage(ctx, store.SetDefaultLanguageParams{
		ID: id, UpdatedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("set new default language: %w", err)
	}
	var defaults int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM languages WHERE is_default = 1 AND is_active = 1`).Scan(&defaults); err != nil {
		return fmt.Errorf("verify default language: %w", err)
	}
	if defaults != 1 {
		return fmt.Errorf("default-language switch produced %d active defaults", defaults)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit default-language switch: %w", err)
	}
	h.invalidateLanguageCaches(ctx)
	return nil
}

type languageReferenceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func languageReferenceCount(ctx context.Context, queryer languageReferenceQueryer, language store.Language) (int64, error) {
	var count int64
	err := queryer.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM pages WHERE language_code = ?) +
			(SELECT COUNT(*) FROM tags WHERE language_code = ?) +
			(SELECT COUNT(*) FROM categories WHERE language_code = ?) +
			(SELECT COUNT(*) FROM menus WHERE language_code = ?) +
			(SELECT COUNT(*) FROM forms WHERE language_code = ?) +
			(SELECT COUNT(*) FROM form_fields WHERE language_code = ?) +
			(SELECT COUNT(*) FROM form_submissions WHERE language_code = ?) +
			(SELECT COUNT(*) FROM widgets WHERE language_code = ?) +
			(SELECT COUNT(*) FROM media WHERE language_code = ?) +
			(SELECT COUNT(*) FROM config WHERE language_code = ?) +
			(SELECT COUNT(*) FROM translations WHERE language_id = ?) +
			(SELECT COUNT(*) FROM config_translations WHERE language_id = ?) +
			(SELECT COUNT(*) FROM media_translations WHERE language_id = ?)`,
		language.Code, language.Code, language.Code, language.Code, language.Code,
		language.Code, language.Code, language.Code, language.Code, language.Code,
		language.ID, language.ID, language.ID).Scan(&count)
	return count, err
}

type languageInUseError struct{ count int64 }

func (e *languageInUseError) Error() string {
	return fmt.Sprintf("cannot delete language: %d record(s) are using this language", e.count)
}

func (h *LanguagesHandler) deleteLanguageIfUnused(ctx context.Context, language store.Language) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin language deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	usageCount, err := languageReferenceCount(ctx, tx, language)
	if err != nil {
		return fmt.Errorf("check language usage: %w", err)
	}
	if usageCount > 0 {
		return &languageInUseError{count: usageCount}
	}
	if err := store.New(h.db).WithTx(tx).DeleteLanguage(ctx, language.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit language deletion: %w", err)
	}
	h.invalidateLanguageCaches(ctx)
	return nil
}

// languageFormInput holds parsed form values for language create/update.
type languageFormInput struct {
	Code       string
	Name       string
	NativeName string
	Direction  string
	IsActive   bool
	Position   string
}

// parseLanguageFormInput extracts language form field values from the request.
func parseLanguageFormInput(r *http.Request) languageFormInput {
	isActiveStr := r.FormValue("is_active")
	return languageFormInput{
		Code:       strings.TrimSpace(r.FormValue("code")),
		Name:       strings.TrimSpace(r.FormValue("name")),
		NativeName: strings.TrimSpace(r.FormValue("native_name")),
		Direction:  strings.TrimSpace(r.FormValue("direction")),
		IsActive:   isActiveStr == "1" || isActiveStr == "on" || isActiveStr == "true",
		Position:   strings.TrimSpace(r.FormValue("position")),
	}
}

// toFormValues converts the input to a map for template re-rendering.
func (input languageFormInput) toFormValues() map[string]string {
	fv := map[string]string{
		"code":        input.Code,
		"name":        input.Name,
		"native_name": input.NativeName,
		"direction":   input.Direction,
		"position":    input.Position,
	}
	if input.IsActive {
		fv["is_active"] = "1"
	}
	return fv
}

// validateLanguageCodeForSave validates codes used by the public language
// router. An unchanged legacy code may be saved only while deactivating it, so
// administrators can remediate rows that predate the routing restrictions.
func validateLanguageCodeForSave(code string, isActive bool, existingCode string) string {
	if code == "" {
		return "languages.error_code_required"
	}

	isLegacyDeactivation := existingCode != "" && code == existingCode && !isActive
	if !util.IsValidLangCode(code) && !isLegacyDeactivation {
		return "languages.error_code_format"
	}
	if util.IsReservedLanguageCode(code) && !isLegacyDeactivation {
		return "languages.error_code_reserved"
	}

	return ""
}

// errLanguagePrefixTaken reports that a page route already owns the prefix a
// language write is claiming, discovered inside the write's own transaction.
var errLanguagePrefixTaken = errors.New("language prefix is taken by a page route")

// createLanguageGuarded writes a language only if no page route holds its
// prefix, deciding both inside one transaction.
//
// The form validation ahead of this checks the same thing, but a check outside
// the write is advisory: two admins saving at once — one creating the language
// "eng", one creating a page at "eng" — each see a clear namespace and both
// writes land, after which the language middleware eats the page's URL.
// Reading the pages through this transaction ties the answer to the write, so
// SQLite refuses the second writer instead of interleaving them.
func (h *LanguagesHandler) createLanguageGuarded(
	ctx context.Context, params store.CreateLanguageParams,
) (store.Language, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Language{}, fmt.Errorf("begin language create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	queries := store.New(h.db).WithTx(tx)
	conflict, err := validateLanguagePrefixAgainstPages(ctx, queries, params.Code, params.IsActive)
	if err != nil {
		return store.Language{}, err
	}
	if conflict != "" {
		return store.Language{}, errLanguagePrefixTaken
	}
	language, err := queries.CreateLanguage(ctx, params)
	if err != nil {
		return store.Language{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.Language{}, fmt.Errorf("commit language create: %w", err)
	}
	return language, nil
}

// addPagePrefixConflictError records the page-collision error, if any, under
// the form's code field. A lookup failure is logged and treated as no
// conflict: the collision is rare, and refusing every save because one query
// failed would be worse than the shadowing it prevents.
func (h *LanguagesHandler) addPagePrefixConflictError(
	r *http.Request, validationErrors map[string]string, code string, isActive bool,
) {
	conflict, err := validateLanguagePrefixAgainstPages(r.Context(), h.queries, code, isActive)
	if err != nil {
		slog.Error("database error checking page routes for language prefix", "error", err, "code", code)
		return
	}
	if conflict != "" {
		validationErrors["code"] = i18n.T(h.renderer.GetAdminLang(r), conflict)
	}
}

// pageRouteQueryer reads whether a page already answers at the first path
// segment a language code would take over.
type pageRouteQueryer interface {
	PageRouteExistsUnderPrefix(ctx context.Context, prefix string) (int64, error)
}

// validateLanguagePrefixAgainstPages guards the boundary between two
// namespaces that are validated separately and can therefore collide.
//
// A page slug is checked against pages and aliases; a language code is checked
// against the application's own reserved routes. Neither looks at the other, so
// activating a language whose code matches an existing page — /eng, say — sends
// the language middleware in first: it strips the segment, the language
// homepage answers, and the page becomes unreachable with nothing reporting a
// conflict. Only an active, routable code takes a prefix, so an inactive or
// reserved one is left alone.
func validateLanguagePrefixAgainstPages(
	ctx context.Context, queries pageRouteQueryer, code string, isActive bool,
) (string, error) {
	if queries == nil || !isActive || !util.IsRoutableLanguageCode(code) {
		return "", nil
	}
	exists, err := queries.PageRouteExistsUnderPrefix(ctx, code)
	if err != nil {
		return "", fmt.Errorf("check page routes under language prefix %q: %w", code, err)
	}
	if exists != 0 {
		return "languages.error_code_page_conflict", nil
	}
	return "", nil
}

// getLanguageByIDParam parses the language ID from URL and fetches the language.
// Returns nil and sends an error response if the language is not found.
func (h *LanguagesHandler) getLanguageByIDParam(w http.ResponseWriter, r *http.Request) *store.Language {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return nil
	}

	lang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			logAndInternalError(w, "failed to get language", "error", err)
		}
		return nil
	}
	return &lang
}

// renderLanguageForm renders the language form using templ.
func (h *LanguagesHandler) renderLanguageForm(w http.ResponseWriter, r *http.Request, lang *store.Language, errs map[string]string, formValues map[string]string, isEdit bool) {
	adminLang := h.renderer.GetAdminLang(r)

	viewData := adminviews.LanguageFormData{
		IsEdit:          isEdit,
		Language:        convertLanguageInfo(lang),
		CommonLanguages: convertCommonLanguages(),
		Errors:          errs,
		FormValues:      formValues,
	}

	var title string
	var breadcrumbs []render.Breadcrumb
	if isEdit && lang != nil {
		title = i18n.T(adminLang, "languages.edit")
		breadcrumbs = languageEditBreadcrumbs(adminLang, lang.Name, lang.ID)
	} else {
		title = i18n.T(adminLang, "languages.new")
		breadcrumbs = languageFormBreadcrumbs(adminLang, false)
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, title, breadcrumbs)
	renderTempl(w, r, adminviews.LanguageFormPage(pc, viewData))
}

// List displays all languages.
func (h *LanguagesHandler) List(w http.ResponseWriter, r *http.Request) {
	adminLang := h.renderer.GetAdminLang(r)

	languages, err := h.queries.ListLanguages(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to list languages", "error", err)
		return
	}

	totalLanguages, err := h.queries.CountLanguages(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count languages", "error", err)
		return
	}

	viewData := adminviews.LanguagesListData{
		Languages:      convertLanguageListItems(languages),
		TotalLanguages: totalLanguages,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(adminLang, "nav.languages"), languagesBreadcrumbs(adminLang))
	renderTempl(w, r, adminviews.LanguagesListPage(pc, viewData))
}

// NewForm displays the form to create a new language.
func (h *LanguagesHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	h.renderLanguageForm(w, r, nil, make(map[string]string), make(map[string]string), false)
}

// Create handles creating a new language.
func (h *LanguagesHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionEditLanguages, redirectAdminLanguages) {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminLanguagesNew) {
		return
	}

	input := parseLanguageFormInput(r)

	position := int64(0)
	if input.Position != "" {
		if p, err := strconv.ParseInt(input.Position, 10, 64); err == nil {
			position = p
		}
	} else {
		// Get max position and add 1
		maxPos, err := h.queries.GetMaxLanguagePosition(r.Context())
		if err == nil && maxPos != nil {
			switch v := maxPos.(type) {
			case int64:
				position = v + 1
			case int:
				position = int64(v) + 1
			case float64:
				position = int64(v) + 1
			}
		}
	}

	// Default direction to ltr
	direction := input.Direction
	if direction == "" {
		direction = model.DirectionLTR
	}

	// Update input with computed values for form re-rendering
	input.Direction = direction
	input.Position = strconv.FormatInt(position, 10)

	validationErrors := make(map[string]string)

	// Validate code
	if validationError := validateLanguageCodeForSave(input.Code, input.IsActive, ""); validationError != "" {
		validationErrors["code"] = i18n.T(h.renderer.GetAdminLang(r), validationError)
	} else {
		exists, err := h.queries.LanguageCodeExists(r.Context(), input.Code)
		if err != nil {
			slog.Error("database error checking language code", "error", err)
		} else if exists != 0 {
			validationErrors["code"] = "Language code already exists"
		}
		if validationErrors["code"] == "" {
			h.addPagePrefixConflictError(r, validationErrors, input.Code, input.IsActive)
		}
	}

	// Validate name
	if input.Name == "" {
		validationErrors["name"] = "Name is required"
	}

	// Validate native name
	if input.NativeName == "" {
		validationErrors["native_name"] = "Native name is required"
	}

	// Validate direction
	if direction != model.DirectionLTR && direction != model.DirectionRTL {
		validationErrors["direction"] = "Direction must be ltr or rtl"
	}

	if len(validationErrors) > 0 {
		h.renderLanguageForm(w, r, nil, validationErrors, input.toFormValues(), false)
		return
	}

	now := time.Now()
	newLang, err := h.createLanguageGuarded(r.Context(), store.CreateLanguageParams{
		Code:       input.Code,
		Name:       input.Name,
		NativeName: input.NativeName,
		IsDefault:  false,
		IsActive:   input.IsActive,
		Direction:  direction,
		Position:   position,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if errors.Is(err, errLanguagePrefixTaken) {
		// A page claimed the prefix between validation and this write.
		validationErrors["code"] = i18n.T(h.renderer.GetAdminLang(r), "languages.error_code_page_conflict")
		h.renderLanguageForm(w, r, nil, validationErrors, input.toFormValues(), false)
		return
	}
	if err != nil {
		slog.Error("failed to create language", "error", err)
		flashError(w, r, h.renderer, redirectAdminLanguagesNew, "Error creating language")
		return
	}

	h.invalidateLanguageCaches(r.Context())
	slog.Info("language created", "language_id", newLang.ID, "code", newLang.Code)
	flashSuccess(w, r, h.renderer, redirectAdminLanguages, "Language created successfully")
}

// EditForm displays the form to edit an existing language.
func (h *LanguagesHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	lang := h.getLanguageByIDParam(w, r)
	if lang == nil {
		return
	}

	h.renderLanguageForm(w, r, lang, make(map[string]string), languageToFormValues(lang), true)
}

// languageToFormValues converts a store.Language to form values map.
func languageToFormValues(lang *store.Language) map[string]string {
	fv := map[string]string{
		"code":        lang.Code,
		"name":        lang.Name,
		"native_name": lang.NativeName,
		"direction":   lang.Direction,
		"position":    strconv.FormatInt(lang.Position, 10),
	}
	if lang.IsActive {
		fv["is_active"] = "1"
	}
	return fv
}

// Update handles updating an existing language.
func (h *LanguagesHandler) Update(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionEditLanguages, redirectAdminLanguages) {
		return
	}

	existingLang := h.getLanguageByIDParam(w, r)
	if existingLang == nil {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminLanguagesID, existingLang.ID)) {
		return
	}

	input := parseLanguageFormInput(r)

	position := existingLang.Position
	if input.Position != "" {
		if p, err := strconv.ParseInt(input.Position, 10, 64); err == nil {
			position = p
		}
	}

	// Default direction to ltr
	direction := input.Direction
	if direction == "" {
		direction = model.DirectionLTR
	}

	// Update input with computed values for form re-rendering
	input.Direction = direction
	input.Position = strconv.FormatInt(position, 10)

	validationErrors := make(map[string]string)

	// Validate code
	if validationError := validateLanguageCodeForSave(input.Code, input.IsActive, existingLang.Code); validationError != "" {
		validationErrors["code"] = i18n.T(h.renderer.GetAdminLang(r), validationError)
	} else {
		exists, err := h.queries.LanguageCodeExistsExcluding(r.Context(), store.LanguageCodeExistsExcludingParams{
			Code: input.Code,
			ID:   existingLang.ID,
		})
		if err != nil {
			slog.Error("database error checking language code", "error", err)
		} else if exists != 0 {
			validationErrors["code"] = "Language code already exists"
		}
		if validationErrors["code"] == "" {
			h.addPagePrefixConflictError(r, validationErrors, input.Code, input.IsActive)
		}
	}

	// Validate name
	if input.Name == "" {
		validationErrors["name"] = "Name is required"
	}

	// Validate native name
	if input.NativeName == "" {
		validationErrors["native_name"] = "Native name is required"
	}

	// Validate direction
	if direction != model.DirectionLTR && direction != model.DirectionRTL {
		validationErrors["direction"] = "Direction must be ltr or rtl"
	}

	// Cannot deactivate default language
	if existingLang.IsDefault && !input.IsActive {
		validationErrors["is_active"] = "Cannot deactivate the default language"
	}

	if len(validationErrors) > 0 {
		h.renderLanguageForm(w, r, existingLang, validationErrors, input.toFormValues(), true)
		return
	}

	now := time.Now()
	err := h.updateLanguage(r.Context(), store.UpdateLanguageParams{
		ID:         existingLang.ID,
		Code:       input.Code,
		Name:       input.Name,
		NativeName: input.NativeName,
		IsDefault:  existingLang.IsDefault, // Keep existing default status
		IsActive:   input.IsActive,
		Direction:  direction,
		Position:   position,
		UpdatedAt:  now,
	}, existingLang.Code)
	if errors.Is(err, errLanguagePrefixTaken) {
		// A page claimed the prefix between validation and this write.
		validationErrors["code"] = i18n.T(h.renderer.GetAdminLang(r), "languages.error_code_page_conflict")
		h.renderLanguageForm(w, r, existingLang, validationErrors, input.toFormValues(), true)
		return
	}
	if err != nil {
		slog.Error("failed to update language", "error", err)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminLanguagesID, existingLang.ID), "Error updating language")
		return
	}

	h.invalidateLanguageCaches(r.Context())
	slog.Info("language updated", "language_id", existingLang.ID, "code", input.Code)
	flashSuccess(w, r, h.renderer, redirectAdminLanguages, "Language updated successfully")
}

// Delete handles deleting a language.
func (h *LanguagesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		msg := middleware.DemoModeMessageDetailed(middleware.RestrictionEditLanguages)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, msg, http.StatusForbidden)
			return
		}
		http.Error(w, msg, http.StatusForbidden)
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	lang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Reswap", "none")
				http.Error(w, "Language not found", http.StatusNotFound)
				return
			}
			http.NotFound(w, r)
		} else {
			slog.Error("failed to get language", "error", err, "language_id", id)
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Reswap", "none")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Cannot delete default language
	if lang.IsDefault {
		errMsg := "Cannot delete the default language"
		slog.Warn("attempted to delete default language", "language_id", id, "code", lang.Code)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, errMsg)
		return
	}

	err = h.deleteLanguageIfUnused(r.Context(), lang)
	var inUse *languageInUseError
	if errors.As(err, &inUse) {
		errMsg := inUse.Error()
		slog.Warn("attempted to delete language with linked records",
			"language_id", id, "code", lang.Code, "reference_count", inUse.count)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, errMsg, http.StatusConflict)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, errMsg)
		return
	}
	if err != nil {
		slog.Error("failed to delete language", "error", err, "language_id", id, "code", lang.Code)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, "Error deleting language", http.StatusInternalServerError)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, "Error deleting language")
		return
	}
	slog.Info("language deleted", "language_id", id, "code", lang.Code)
	h.invalidateLanguageCaches(r.Context())

	// For HTMX requests, return empty response to remove the row
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminLanguages, "Language deleted successfully")
}

// SetDefault handles setting a language as the default.
func (h *LanguagesHandler) SetDefault(w http.ResponseWriter, r *http.Request) {
	lang := h.getLanguageByIDParam(w, r)
	if lang == nil {
		return
	}

	// Cannot set inactive language as default
	if !lang.IsActive {
		flashError(w, r, h.renderer, redirectAdminLanguages, "Cannot set an inactive language as default")
		return
	}
	if validationError := validateLanguageCodeForSave(lang.Code, true, lang.Code); validationError != "" {
		flashError(w, r, h.renderer, redirectAdminLanguages,
			i18n.T(h.renderer.GetAdminLang(r), validationError))
		return
	}
	// setDefaultLanguage repeats this inside its transaction, which is what
	// actually holds the line. Checking here too turns the generic failure
	// flash into one that names the conflict the administrator can fix.
	conflict, err := validateLanguagePrefixAgainstPages(r.Context(), h.queries, lang.Code, true)
	if err != nil {
		logAndInternalError(w, "failed to check page routes for language prefix", "error", err, "code", lang.Code)
		return
	}
	if conflict != "" {
		flashError(w, r, h.renderer, redirectAdminLanguages, i18n.T(h.renderer.GetAdminLang(r), conflict))
		return
	}

	if err := h.setDefaultLanguage(r.Context(), lang.ID); err != nil {
		slog.Error("failed to set default language", "error", err)
		flashError(w, r, h.renderer, redirectAdminLanguages, "Error setting default language")
		return
	}

	slog.Info("default language set", "language_id", lang.ID, "code", lang.Code)
	flashSuccess(w, r, h.renderer, redirectAdminLanguages, fmt.Sprintf("%s set as default language", lang.Name))
}

// FindDefaultLanguage returns a pointer to the default language from a slice.
// Returns nil unless the slice contains exactly one default language.
func FindDefaultLanguage(languages []store.Language) *store.Language {
	var found *store.Language
	for i := range languages {
		if languages[i].IsDefault {
			if found != nil {
				return nil
			}
			found = &languages[i]
		}
	}
	return found
}

// ListActiveLanguagesWithFallback returns all active languages, or an empty slice on error.
// This is useful when the languages list is needed for display but not critical for the operation.
func ListActiveLanguagesWithFallback(ctx context.Context, queries *store.Queries) []store.Language {
	languages, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		return []store.Language{}
	}
	routable := make([]store.Language, 0, len(languages))
	for _, language := range languages {
		if isRoutableContentLanguage(language) {
			routable = append(routable, language)
		}
	}
	return routable
}

func isRoutableContentLanguage(language store.Language) bool {
	return language.IsActive && util.IsValidLangCode(language.Code) && !util.IsReservedLanguageCode(language.Code)
}

func getRoutableContentLanguage(ctx context.Context, queries *store.Queries, code string) (store.Language, error) {
	language, err := queries.GetLanguageByCode(ctx, strings.TrimSpace(code))
	if err != nil {
		return store.Language{}, err
	}
	if !isRoutableContentLanguage(language) {
		return store.Language{}, fmt.Errorf("language %q is not active and routable", language.Code)
	}
	return language, nil
}
