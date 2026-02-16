// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/demo"
	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/handler/api"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/logging"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/scheduler"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/session"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
	"github.com/olegiv/ocms-go/internal/themes"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/internal/version"
	"github.com/olegiv/ocms-go/internal/webhook"
	"github.com/olegiv/ocms-go/modules/analytics_ext"
	"github.com/olegiv/ocms-go/modules/analytics_int"
	"github.com/olegiv/ocms-go/modules/dbmanager"
	"github.com/olegiv/ocms-go/modules/developer"
	"github.com/olegiv/ocms-go/modules/embed"
	"github.com/olegiv/ocms-go/modules/example"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
	"github.com/olegiv/ocms-go/modules/informer"
	"github.com/olegiv/ocms-go/modules/migrator"
	"github.com/olegiv/ocms-go/modules/privacy"
	"github.com/olegiv/ocms-go/modules/sentinel"
	"github.com/olegiv/ocms-go/web"

	// Custom modules — loaded via init() self-registration
	_ "github.com/olegiv/ocms-go/custom/modules"
)

// Version information - injected at build time via ldflags
var (
	appVersion   = "dev"
	appGitCommit = "unknown"
	appBuildTime = "unknown"
)

// crudHandlers defines the standard CRUD handler methods.
type crudHandlers struct {
	List     http.HandlerFunc
	NewForm  http.HandlerFunc
	Create   http.HandlerFunc
	EditForm http.HandlerFunc
	Update   http.HandlerFunc
	Delete   http.HandlerFunc
}

// registerCRUD registers standard CRUD routes for a resource.
// Routes: GET /, GET /new, POST /, GET /{id}, PUT /{id}, POST /{id}, DELETE /{id}
func registerCRUD(r chi.Router, base, baseID string, h crudHandlers) {
	r.Get(base, h.List)
	r.Get(base+handler.RouteSuffixNew, h.NewForm)
	r.Post(base, h.Create)
	r.Get(baseID, h.EditForm)
	r.Put(baseID, h.Update)
	r.Post(baseID, h.Update) // HTML forms can't send PUT
	r.Delete(baseID, h.Delete)
}

// registerSettingsRoutes registers a settings page with Get, Put, and Post (for HTML forms).
func registerSettingsRoutes(r chi.Router, route string, get, update http.HandlerFunc) {
	r.Get(route, get)
	r.Put(route, update)
	r.Post(route, update) // HTML forms can't send PUT
}

// registerFrontendRoutes registers common frontend public routes on the given router.
func registerFrontendRoutes(r chi.Router, h *handler.FrontendHandler) {
	r.Get(handler.RouteRoot, h.Home)
	r.Get(handler.RouteSuffixSearch, h.Search)
	r.Get(handler.RouteBlog, h.Blog)
	r.Get(handler.RouteCategorySlug, h.Category)
	r.Get(handler.RouteTagSlug, h.Tag)
	r.Get(handler.RoutePageByID, h.PageByID)
	r.Get(handler.RouteParamSlug, h.Page)

	// Legacy blog tag URL redirect: /blog/tag/{slug} -> /tag/{slug}
	r.Get("/blog/tag/{slug}", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		// Validate slug to prevent open URL redirect (CWE-601)
		if !util.IsValidSlug(slug) {
			http.NotFound(w, req)
			return
		}

		// Build target URL
		var targetPath string
		lang := chi.URLParam(req, "lang")
		if lang != "" {
			// Validate language code to prevent open URL redirect (CWE-601)
			if !util.IsValidLangCode(lang) {
				http.NotFound(w, req)
				return
			}
			targetPath = "/" + lang + "/tag/" + slug
		} else {
			targetPath = "/tag/" + slug
		}

		// Parse and verify URL is local (no hostname) to prevent open redirect
		target, err := url.Parse(strings.ReplaceAll(targetPath, "\\", "/"))
		if err != nil || target.Hostname() != "" {
			http.NotFound(w, req)
			return
		}
		http.Redirect(w, req, target.String(), http.StatusMovedPermanently)
	})
}

func main() {
	// Parse CLI flags
	showVersion := flag.Bool("version", false, "Show version information")
	flag.BoolVar(showVersion, "v", false, "Show version information (shorthand)")
	showHelp := flag.Bool("help", false, "Show help information")
	flag.BoolVar(showHelp, "h", false, "Show help information (shorthand)")

	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "oCMS - Open Content Management System\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		_, _ = fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		_, _ = fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_SESSION_SECRET    Session encryption key (required, min 32 bytes)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_DB_PATH           SQLite database path (default: ./data/ocms.db)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_SERVER_PORT       Server port (default: 8080)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_ENV               Environment: development|production (default: development)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_FORM_CAPTCHA  Require captcha for all public forms (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_WEBHOOK_FORM_DATA_MODE  form.submitted payload mode: redacted|none|full (default: redacted)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION  Reject production startup when webhook payload mode is full (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_CUSTOM_DIR        Custom content directory (default: ./custom)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_ACTIVE_THEME      Active theme name (default: default)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REDIS_URL         Redis URL for distributed caching (optional)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_TRUSTED_PROXIES   Comma-separated trusted proxy CIDRs/IPs (optional)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_TRUSTED_PROXIES  Reject production startup when trusted proxies are not configured (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_API_ALLOWED_CIDRS Comma-separated CIDRs/IPs allowed for API key access (optional)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_API_ALLOWED_CIDRS  Reject API key auth when global source CIDRs are not configured (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_API_KEY_EXPIRY  Reject API keys without expiration (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_API_KEY_SOURCE_CIDRS  Reject API keys without per-key CIDR restrictions (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REVOKE_API_KEY_ON_SOURCE_IP_CHANGE  Deactivate API keys when source IP changes without per-key CIDRs (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_API_KEY_MAX_TTL_DAYS  Maximum API key lifetime in days (0 disables, default: 0)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_EMBED_ALLOWED_ORIGINS   Comma-separated allowed origins for embed proxy routes (optional)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS   Comma-separated allowed hosts for embed provider endpoints (optional)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_EMBED_ALLOWED_ORIGINS  Reject production startup when embed proxy is active without origin allowlist (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_EMBED_ALLOWED_UPSTREAM_HOSTS  Reject production startup when embed proxy is active without upstream host allowlist (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_EMBED_PROXY_TOKEN   Optional shared token required by embed proxy routes\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_EMBED_PROXY_TOKEN  Reject production startup when embed proxy token policy is enabled without a token (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_HTTPS_OUTBOUND  Require HTTPS for outbound integration URLs (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_SANITIZE_PAGE_HTML  Sanitize page HTML before rendering to visitors (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REQUIRE_SANITIZE_PAGE_HTML  Reject production startup when page HTML sanitization is disabled (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_BLOCK_SUSPICIOUS_PAGE_HTML  Reject page writes with suspicious HTML markup (default: false)\n")
		_, _ = fmt.Fprintf(os.Stderr, "\nFor more information, see: https://github.com/olegiv/ocms-go\n")
	}

	flag.Parse()

	// Handle -h/-help flag
	if *showHelp {
		flag.Usage()
		os.Exit(0)
	}

	// Handle -v/-version flag
	if *showVersion {
		_, _ = fmt.Printf("ocms %s (commit: %s, built: %s)\n", appVersion, appGitCommit, appBuildTime)
		os.Exit(0)
	}

	if err := run(); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}

// parseLogLevel converts a log level string to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func auditRequiredFormCaptchaPosture(ctx context.Context, db *sql.DB, hooks *module.HookRegistry, verifierEnabled bool) error {
	if hooks == nil || !hooks.HasHandlers(hcaptcha.HookFormCaptchaVerify) {
		return fmt.Errorf(
			"refusing to start in production: OCMS_REQUIRE_FORM_CAPTCHA is enabled but captcha verification is unavailable",
		)
	}
	if !verifierEnabled {
		return fmt.Errorf(
			"refusing to start in production: OCMS_REQUIRE_FORM_CAPTCHA is enabled but captcha verifier is not fully configured",
		)
	}

	const query = `
		SELECT COUNT(*)
		FROM forms f
		WHERE f.is_active = 1
		  AND NOT EXISTS (
		  	SELECT 1
		  	FROM form_fields ff
		  	WHERE ff.form_id = f.id
		  	  AND ff.type = 'captcha'
		  )
	`

	var formsMissingCaptcha int
	if err := db.QueryRowContext(ctx, query).Scan(&formsMissingCaptcha); err != nil {
		return fmt.Errorf("auditing form captcha posture: %w", err)
	}

	if formsMissingCaptcha > 0 {
		return fmt.Errorf(
			"refusing to start in production: %d active public form(s) are missing a captcha field while OCMS_REQUIRE_FORM_CAPTCHA is enabled",
			formsMissingCaptcha,
		)
	}

	return nil
}

func auditRequiredHTTPSOutboundPosture(ctx context.Context, db *sql.DB) error {
	countNonHTTPS := func(query string) (int, error) {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return 0, err
		}
		defer func() { _ = rows.Close() }()

		invalid := 0
		for rows.Next() {
			var rawURL string
			if err := rows.Scan(&rawURL); err != nil {
				return 0, err
			}
			parsed, err := url.Parse(strings.TrimSpace(rawURL))
			if err != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Host) == "" {
				invalid++
			}
		}

		if err := rows.Err(); err != nil {
			return 0, err
		}
		return invalid, nil
	}

	invalidWebhooks, err := countNonHTTPS(`SELECT url FROM webhooks WHERE is_active = 1`)
	if err != nil {
		return fmt.Errorf("auditing webhook HTTPS posture: %w", err)
	}

	invalidTasks, err := countNonHTTPS(`SELECT url FROM scheduled_tasks WHERE is_active = 1`)
	if err != nil {
		return fmt.Errorf("auditing scheduler HTTPS posture: %w", err)
	}

	if invalidWebhooks > 0 || invalidTasks > 0 {
		return fmt.Errorf(
			"refusing to start in production: OCMS_REQUIRE_HTTPS_OUTBOUND is enabled but %d active webhook(s) and %d active scheduled task(s) use non-HTTPS URLs",
			invalidWebhooks,
			invalidTasks,
		)
	}

	return nil
}

func isMissingEmbedSettingsTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") && strings.Contains(msg, "embed_settings")
}

func auditRequiredEmbedHTTPSPosture(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT provider, settings FROM embed_settings WHERE is_enabled = 1`)
	if err != nil {
		if isMissingEmbedSettingsTable(err) {
			return nil
		}
		return fmt.Errorf("auditing embed HTTPS posture: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var invalidCount int
	for rows.Next() {
		var providerID string
		var rawSettings string
		if err := rows.Scan(&providerID, &rawSettings); err != nil {
			return fmt.Errorf("auditing embed HTTPS posture: %w", err)
		}

		// Current built-in outbound embed provider is Dify.
		if providerID != "dify" {
			continue
		}

		var settings map[string]any
		if err := json.Unmarshal([]byte(rawSettings), &settings); err != nil {
			invalidCount++
			continue
		}

		apiEndpoint, _ := settings["api_endpoint"].(string)
		apiEndpoint = strings.TrimSpace(apiEndpoint)
		parsedEndpoint, err := url.Parse(apiEndpoint)
		if err != nil || !strings.EqualFold(parsedEndpoint.Scheme, "https") || strings.TrimSpace(parsedEndpoint.Host) == "" {
			invalidCount++
			continue
		}
		if err := util.ValidateWebhookURL(apiEndpoint); err != nil {
			invalidCount++
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("auditing embed HTTPS posture: %w", err)
	}

	if invalidCount > 0 {
		return fmt.Errorf(
			"refusing to start in production: OCMS_REQUIRE_HTTPS_OUTBOUND is enabled but %d active embed provider endpoint(s) are invalid or non-HTTPS",
			invalidCount,
		)
	}

	return nil
}

func parseAllowedEmbedUpstreamHosts(raw string) (map[string]struct{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	entries := strings.Split(trimmed, ",")
	allowed := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		host := strings.TrimSpace(strings.ToLower(entry))
		host = strings.Trim(host, ".")
		if host == "" {
			return nil, fmt.Errorf("embed upstream host allowlist contains empty entry")
		}
		if strings.Contains(host, "://") || strings.Contains(host, "/") {
			return nil, fmt.Errorf("invalid embed upstream host allowlist entry %q", entry)
		}
		if _, _, err := net.SplitHostPort(host); err == nil {
			return nil, fmt.Errorf("embed upstream host allowlist entry must not include port: %q", entry)
		}
		if strings.Contains(host, ":") {
			return nil, fmt.Errorf("embed upstream host allowlist entry must not include port: %q", entry)
		}

		allowed[host] = struct{}{}
	}

	return allowed, nil
}

func auditEmbedUpstreamHostPosture(ctx context.Context, db *sql.DB, allowedHosts map[string]struct{}) error {
	if len(allowedHosts) == 0 {
		return nil
	}

	rows, err := db.QueryContext(ctx, `SELECT provider, settings FROM embed_settings WHERE is_enabled = 1`)
	if err != nil {
		if isMissingEmbedSettingsTable(err) {
			return nil
		}
		return fmt.Errorf("auditing embed upstream host posture: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var invalidCount int
	for rows.Next() {
		var providerID string
		var rawSettings string
		if err := rows.Scan(&providerID, &rawSettings); err != nil {
			return fmt.Errorf("auditing embed upstream host posture: %w", err)
		}
		if providerID != "dify" {
			continue
		}

		var settings map[string]any
		if err := json.Unmarshal([]byte(rawSettings), &settings); err != nil {
			invalidCount++
			continue
		}
		apiEndpoint, _ := settings["api_endpoint"].(string)
		parsedEndpoint, err := url.Parse(strings.TrimSpace(apiEndpoint))
		if err != nil {
			invalidCount++
			continue
		}
		host := strings.Trim(strings.ToLower(parsedEndpoint.Hostname()), ".")
		if host == "" {
			invalidCount++
			continue
		}
		if _, ok := allowedHosts[host]; !ok {
			invalidCount++
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("auditing embed upstream host posture: %w", err)
	}

	if invalidCount > 0 {
		return fmt.Errorf(
			"refusing to start in production: OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS is configured but %d active embed provider endpoint(s) use non-allowlisted hosts",
			invalidCount,
		)
	}

	return nil
}

func auditRequiredEmbedUpstreamHostPolicyPosture(ctx context.Context, db *sql.DB, configuredHosts string) error {
	if strings.TrimSpace(configuredHosts) != "" {
		return nil
	}

	var activeEmbedProviders int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embed_settings WHERE is_enabled = 1`).Scan(&activeEmbedProviders); err != nil {
		return fmt.Errorf("auditing embed upstream host policy posture: %w", err)
	}
	if activeEmbedProviders > 0 {
		return fmt.Errorf(
			"refusing to start in production: embed proxy is active (%d provider(s)) but OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS is not configured",
			activeEmbedProviders,
		)
	}

	return nil
}

func auditRequiredAPIKeyExpiryPosture(ctx context.Context, db *sql.DB) error {
	var nonExpiringActiveKeys int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE is_active = 1 AND expires_at IS NULL`).Scan(&nonExpiringActiveKeys)
	if err != nil {
		return fmt.Errorf("auditing API key expiry posture: %w", err)
	}

	if nonExpiringActiveKeys > 0 {
		return fmt.Errorf(
			"refusing to start in production: OCMS_REQUIRE_API_KEY_EXPIRY is enabled but %d active API key(s) have no expiration",
			nonExpiringActiveKeys,
		)
	}

	return nil
}

func auditRequiredAPIKeyMaxTTLPosture(ctx context.Context, db *sql.DB, maxTTLDays int) error {
	if maxTTLDays <= 0 {
		return nil
	}

	var keysOutsideTTL int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM api_keys
		WHERE is_active = 1
		  AND (
		  	expires_at IS NULL
		  	OR datetime(expires_at) > datetime(created_at, '+' || ? || ' days')
		  )
	`, maxTTLDays).Scan(&keysOutsideTTL)
	if err != nil {
		return fmt.Errorf("auditing API key max TTL posture: %w", err)
	}

	if keysOutsideTTL > 0 {
		return fmt.Errorf(
			"refusing to start in production: OCMS_API_KEY_MAX_TTL_DAYS=%d but %d active API key(s) exceed this lifetime policy",
			maxTTLDays,
			keysOutsideTTL,
		)
	}

	return nil
}

func auditRequiredAPIKeySourceCIDRPosture(ctx context.Context, db *sql.DB) error {
	var keysWithoutSourceCIDRs int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM api_keys k
		WHERE k.is_active = 1
		  AND NOT EXISTS (
		  	SELECT 1
		  	FROM api_key_source_cidrs c
		  	WHERE c.api_key_id = k.id
		  )
	`).Scan(&keysWithoutSourceCIDRs)
	if err != nil {
		return fmt.Errorf("auditing API key source CIDR posture: %w", err)
	}

	if keysWithoutSourceCIDRs > 0 {
		return fmt.Errorf(
			"refusing to start in production: OCMS_REQUIRE_API_KEY_SOURCE_CIDRS is enabled but %d active API key(s) have no source CIDR restriction",
			keysWithoutSourceCIDRs,
		)
	}

	return nil
}

// initI18nFromDB updates i18n settings from database (active languages, default language).
func initI18nFromDB(ctx context.Context, queries *store.Queries) {
	if activeLanguages, err := queries.ListActiveLanguages(ctx); err == nil {
		var activeCodes []string
		for _, lang := range activeLanguages {
			activeCodes = append(activeCodes, lang.Code)
		}
		i18n.SetActiveLanguages(activeCodes)
		slog.Info("i18n active languages set from database", "languages", activeCodes)
	}

	if defaultLang, err := queries.GetDefaultLanguage(ctx); err == nil {
		if i18n.IsSupported(defaultLang.Code) {
			i18n.SetDefaultLanguage(defaultLang.Code)
			slog.Info("i18n default language set from database", "language", defaultLang.Code)
		}
	}
}

// initCacheManager creates and starts the cache manager, preloads caches.
func initCacheManager(ctx context.Context, db *sql.DB, cfg *config.Config) *cache.Manager {
	cacheConfig := cache.Config{
		Type:             "memory",
		RedisURL:         cfg.RedisURL,
		Prefix:           cfg.CachePrefix,
		DefaultTTL:       time.Duration(cfg.CacheTTL) * time.Second,
		MaxSize:          cfg.CacheMaxSize,
		CleanupInterval:  time.Minute,
		FallbackToMemory: true,
	}
	if cfg.UseRedisCache() {
		cacheConfig.Type = "redis"
	}
	cacheManager := cache.NewManagerWithConfig(store.New(db), cacheConfig)
	cacheManager.Start()

	if err := cacheManager.Preload(ctx, ""); err != nil {
		slog.Warn("failed to preload caches", "error", err)
	}
	switch {
	case cacheManager.IsRedis():
		slog.Info(handler.LogCacheManagerInit, "backend", "redis", "url", cache.SanitizeRedisURL(cfg.RedisURL))
	case cacheManager.Info().IsFallback:
		slog.Warn(handler.LogCacheManagerInit, "backend", "memory", "note", "Redis unavailable, using fallback")
	default:
		slog.Info(handler.LogCacheManagerInit, "backend", "memory")
	}

	return cacheManager
}

// registerModules registers all application modules with the registry.
func registerModules(registry *module.Registry, sentinelModule *sentinel.Module, sessionManager *scs.SessionManager, eventService *service.EventService) (*analytics_int.Module, error) {
	modules := []module.Module{
		example.New(),
		developer.New(),
		analytics_ext.New(),
		embed.New(),
		hcaptcha.New(),
		privacy.New(),
		informer.New(),
	}

	for _, mod := range modules {
		if err := registry.Register(mod); err != nil {
			return nil, fmt.Errorf("registering %s module: %w", mod.Name(), err)
		}
	}

	sentinelModule.SetSessionManager(sessionManager)
	sentinelModule.SetActiveChecker(registry)
	sentinelModule.SetEventLogger(eventService)
	if err := registry.Register(sentinelModule); err != nil {
		return nil, fmt.Errorf("registering sentinel module: %w", err)
	}

	if err := registry.Register(migrator.New()); err != nil {
		return nil, fmt.Errorf("registering migrator module: %w", err)
	}
	if err := registry.Register(dbmanager.New()); err != nil {
		return nil, fmt.Errorf("registering dbmanager module: %w", err)
	}

	internalAnalyticsModule := analytics_int.New()
	if err := registry.Register(internalAnalyticsModule); err != nil {
		return nil, fmt.Errorf("registering analytics_int module: %w", err)
	}

	// Register custom modules (self-registered via init() in custom/modules/)
	for _, mod := range module.CustomModules() {
		if err := registry.Register(mod); err != nil {
			return nil, fmt.Errorf("registering custom module %s: %w", mod.Name(), err)
		}
	}

	return internalAnalyticsModule, nil
}

// loadActiveTheme determines and activates the appropriate theme.
func loadActiveTheme(ctx context.Context, queries *store.Queries, themeManager *theme.Manager, renderer *render.Renderer, cfg *config.Config) {
	themeManager.SetFuncMap(renderer.TemplateFuncs())
	if err := themeManager.LoadThemes(); err != nil {
		slog.Warn("failed to load themes", "error", err)
	}

	activeTheme := cfg.ActiveTheme
	if dbConfig, err := queries.GetConfigByKey(ctx, "active_theme"); err == nil && dbConfig.Value != "" {
		activeTheme = dbConfig.Value
		slog.Info("active theme loaded from database", "theme", activeTheme)
	}

	if themeManager.HasTheme(activeTheme) {
		if err := themeManager.SetActiveTheme(activeTheme); err != nil {
			slog.Warn("failed to set active theme", "theme", activeTheme, "error", err)
		}
	} else if themeManager.ThemeCount() > 0 {
		availableThemes := themeManager.ListThemesWithActive()
		if len(availableThemes) > 0 {
			if err := themeManager.SetActiveTheme(availableThemes[0].Name); err != nil {
				slog.Warn("failed to set fallback theme", "error", err)
			}
		}
	}
	slog.Info("theme manager initialized", "themes", themeManager.ThemeCount())
}

func run() error {
	// Load .env files if present (development)
	_ = godotenv.Load()
	_ = godotenv.Load("modules/migrator/.env") // Migrator module config

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	allowedEmbedUpstreamHosts, err := parseAllowedEmbedUpstreamHosts(cfg.EmbedAllowedUpstreamHosts)
	if err != nil {
		return fmt.Errorf("parsing OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS: %w", err)
	}

	// Create version info from build-time injected values
	versionInfo := &version.Info{
		Version:   appVersion,
		GitCommit: appGitCommit,
		BuildTime: appBuildTime,
	}

	// Setup logger
	logLevel := parseLogLevel(cfg.LogLevel)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	if cfg.Env == "production" {
		if !cfg.RequireFormCaptcha {
			slog.Warn("production security warning: OCMS_REQUIRE_FORM_CAPTCHA is disabled")
		}
		if cfg.WebhookFormDataMode == "full" {
			slog.Warn("production security warning: OCMS_WEBHOOK_FORM_DATA_MODE=full may expose sensitive submission data to webhook endpoints")
		}
		if !cfg.RequireWebhookFormDataMinimization {
			slog.Warn("production security warning: OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION is disabled")
		}
		if !cfg.RequireTrustedProxies {
			slog.Warn("production security warning: OCMS_REQUIRE_TRUSTED_PROXIES is disabled")
		}
		if strings.TrimSpace(cfg.APIAllowedCIDRs) == "" {
			slog.Warn("production security warning: OCMS_API_ALLOWED_CIDRS is not configured")
		}
		if !cfg.RequireAPIAllowedCIDRs {
			slog.Warn("production security warning: OCMS_REQUIRE_API_ALLOWED_CIDRS is disabled")
		}
		if !cfg.RequireAPIKeyExpiry {
			slog.Warn("production security warning: OCMS_REQUIRE_API_KEY_EXPIRY is disabled")
		}
		if !cfg.RequireAPIKeySourceCIDRs {
			slog.Warn("production security warning: OCMS_REQUIRE_API_KEY_SOURCE_CIDRS is disabled")
		}
		if !cfg.RevokeAPIKeyOnSourceIPChange {
			slog.Warn("production security warning: OCMS_REVOKE_API_KEY_ON_SOURCE_IP_CHANGE is disabled")
		}
		if cfg.APIKeyMaxTTLDays <= 0 {
			slog.Warn("production security warning: OCMS_API_KEY_MAX_TTL_DAYS is not configured")
		}
		if strings.TrimSpace(cfg.EmbedAllowedOrigins) == "" {
			slog.Warn("production security warning: OCMS_EMBED_ALLOWED_ORIGINS is not configured")
		}
		if strings.TrimSpace(cfg.EmbedAllowedUpstreamHosts) == "" {
			slog.Warn("production security warning: OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS is not configured")
		}
		if !cfg.RequireEmbedAllowedOrigins {
			slog.Warn("production security warning: OCMS_REQUIRE_EMBED_ALLOWED_ORIGINS is disabled")
		}
		if !cfg.RequireEmbedAllowedUpstreamHosts {
			slog.Warn("production security warning: OCMS_REQUIRE_EMBED_ALLOWED_UPSTREAM_HOSTS is disabled")
		}
		if !cfg.RequireEmbedProxyToken {
			slog.Warn("production security warning: OCMS_REQUIRE_EMBED_PROXY_TOKEN is disabled")
		}
		if !cfg.RequireHTTPSOutbound {
			slog.Warn("production security warning: OCMS_REQUIRE_HTTPS_OUTBOUND is disabled")
		}
		if !cfg.SanitizePageHTML {
			slog.Warn("production security warning: OCMS_SANITIZE_PAGE_HTML is disabled")
		}
		if !cfg.RequireSanitizePageHTML {
			slog.Warn("production security warning: OCMS_REQUIRE_SANITIZE_PAGE_HTML is disabled")
		}
		if !cfg.BlockSuspiciousPageHTML {
			slog.Warn("production security warning: OCMS_BLOCK_SUSPICIOUS_PAGE_HTML is disabled")
		}
	}

	if err := middleware.ConfigureTrustedProxies(cfg.TrustedProxies); err != nil {
		return fmt.Errorf("configuring trusted proxies: %w", err)
	}
	if err := middleware.ConfigureAPIAllowedCIDRs(cfg.APIAllowedCIDRs); err != nil {
		return fmt.Errorf("configuring API allowed CIDRs: %w", err)
	}
	if strings.TrimSpace(cfg.APIAllowedCIDRs) != "" {
		slog.Info("API source CIDR allowlist enabled")
	}
	middleware.SetRequireAPIAllowedCIDRs(cfg.RequireAPIAllowedCIDRs)
	if cfg.RequireAPIAllowedCIDRs {
		slog.Info("API global source CIDR requirement enabled")
	}
	middleware.SetRequireAPIKeyExpiry(cfg.RequireAPIKeyExpiry)
	if cfg.RequireAPIKeyExpiry {
		slog.Info("API key expiry enforcement enabled")
	}
	middleware.SetRequireAPIKeySourceCIDRs(cfg.RequireAPIKeySourceCIDRs)
	if cfg.RequireAPIKeySourceCIDRs {
		slog.Info("API key per-key source CIDR enforcement enabled")
	}
	middleware.SetRevokeAPIKeyOnSourceIPChange(cfg.RevokeAPIKeyOnSourceIPChange)
	if cfg.RevokeAPIKeyOnSourceIPChange {
		slog.Info("API key source IP anomaly auto-revocation enabled")
	}
	middleware.SetAPIKeyMaxTTLDays(cfg.APIKeyMaxTTLDays)
	if cfg.APIKeyMaxTTLDays > 0 {
		slog.Info("API key max lifetime policy enabled", "max_ttl_days", cfg.APIKeyMaxTTLDays)
	}
	util.SetRequireHTTPSOutbound(cfg.RequireHTTPSOutbound)
	scheduler.SetRequireHTTPSOutbound(cfg.RequireHTTPSOutbound)
	if cfg.RequireHTTPSOutbound {
		slog.Info("HTTPS-only outbound URL policy enabled")
	}

	// Initialize i18n system for admin UI localization
	if err := i18n.Init(logger); err != nil {
		return fmt.Errorf("initializing i18n: %w", err)
	}
	slog.Info("i18n system initialized", "languages", i18n.SupportedLanguages)

	// Ensure data directory exists
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	// Reset demo data if overdue (only in demo mode)
	if middleware.IsDemoMode() {
		if err := demo.ResetIfNeeded(cfg.DBPath, cfg.UploadsDir, dbDir); err != nil {
			slog.Warn("demo reset check failed", "error", err)
		}
	}

	// Initialize database
	slog.Info("initializing database", "path", cfg.DBPath)
	db, err := store.NewDB(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer func(db *sql.DB) {
		err = db.Close()
		if err != nil {
			slog.Error("error closing database connection", "error", err)
		}
	}(db)

	// Run migrations
	slog.Info("running database migrations")
	if err := store.Migrate(db); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	slog.Info("database ready")

	// Upgrade logger to also write WARN and ERROR logs to the Event Log database
	textHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	eventLogHandler := logging.NewEventLogHandler(textHandler, db)
	logger = slog.New(eventLogHandler)
	slog.SetDefault(logger)
	slog.Info("event log integration enabled", "min_level", "warn")

	// Seed default data
	ctx := context.Background()
	if err := store.Seed(ctx, db, cfg.DoSeed); err != nil {
		return fmt.Errorf("seeding database: %w", err)
	}

	// Seed demo content if both base seeding and demo mode are enabled
	if cfg.DoSeed {
		if err := store.SeedDemo(ctx, db); err != nil {
			return fmt.Errorf("seeding demo content: %w", err)
		}
	}

	// Update i18n from database settings
	queries := store.New(db)
	if cfg.Env == "production" {
		hasDefaultAdminCreds, err := store.HasDefaultAdminCredentials(ctx, queries)
		if err != nil {
			return fmt.Errorf("auditing default admin credentials: %w", err)
		}
		if hasDefaultAdminCreds {
			return fmt.Errorf(
				"refusing to start in production: default seeded admin credentials are still active for %s; rotate credentials before startup",
				store.DefaultAdminEmail,
			)
		}
	}
	if cfg.Env == "production" && cfg.RequireAPIKeyExpiry {
		if err := auditRequiredAPIKeyExpiryPosture(ctx, db); err != nil {
			return err
		}
	}
	if cfg.Env == "production" && cfg.APIKeyMaxTTLDays > 0 {
		if err := auditRequiredAPIKeyMaxTTLPosture(ctx, db, cfg.APIKeyMaxTTLDays); err != nil {
			return err
		}
	}
	if cfg.Env == "production" && cfg.RequireAPIKeySourceCIDRs {
		if err := auditRequiredAPIKeySourceCIDRPosture(ctx, db); err != nil {
			return err
		}
	}
	if cfg.Env == "production" && len(allowedEmbedUpstreamHosts) > 0 {
		if err := auditEmbedUpstreamHostPosture(ctx, db, allowedEmbedUpstreamHosts); err != nil {
			return err
		}
	}
	initI18nFromDB(ctx, queries)

	// Initialize session manager
	sessionManager := session.New(db, cfg.IsDevelopment())
	middleware.SetSessionManager(sessionManager)
	slog.Info("session manager initialized")

	// Initialize language cookie security settings
	middleware.InitLanguageCookies(cfg.IsDevelopment())

	// Initialize cache manager
	cacheManager := initCacheManager(ctx, db, cfg)
	defer cacheManager.Stop()

	// Create shared MenuService using cache manager's MenuCache
	menuService := service.NewMenuService(db, cacheManager.Menus)

	// Initialize template renderer
	templatesFS, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		return fmt.Errorf("getting templates fs: %w", err)
	}

	renderer, err := render.New(render.Config{
		TemplatesFS:    templatesFS,
		SessionManager: sessionManager,
		DB:             db,
		IsDev:          cfg.IsDevelopment(),
		MenuService:    menuService,
	})
	if err != nil {
		return fmt.Errorf("initializing renderer: %w", err)
	}
	slog.Info("template renderer initialized")

	// Initialize theme manager with embedded core themes and custom directory
	themeManager := theme.NewManager(themes.FS, cfg.CustomDir, logger)

	// Add theme manager's template functions (TTheme) to renderer
	renderer.AddTemplateFuncs(themeManager.TemplateFuncs())

	// Note: Theme loading is deferred until after modules are initialized
	// so that module template functions (like analyticsHead) are available

	// Initialize scheduler registry and scheduler
	schedulerRegistry := scheduler.NewRegistry(db, logger)
	sched := scheduler.New(db, logger, schedulerRegistry)
	if err := sched.Start(); err != nil {
		return fmt.Errorf("starting scheduler: %w", err)
	}
	defer sched.Stop()

	// Initialize task executor for user-created scheduled URL tasks
	taskExecutor := scheduler.NewTaskExecutor(db, logger, schedulerRegistry, sched.Cron())

	// Schedule daily demo reset at 01:00 UTC (if demo mode)
	if middleware.IsDemoMode() {
		if err := sched.AddDemoReset(cfg.DBPath, cfg.UploadsDir, dbDir); err != nil {
			return fmt.Errorf("adding demo reset job: %w", err)
		}
		slog.Info("demo reset scheduled", "schedule", "daily at 01:00 UTC")
	}

	// Initialize and start webhook dispatcher
	webhookDispatcher := webhook.NewDispatcher(db, logger, webhook.DefaultConfig())
	webhookDispatcher.Start(ctx)
	defer webhookDispatcher.Stop()
	slog.Info("webhook dispatcher initialized")

	// Initialize hook registry
	hookRegistry := module.NewHookRegistry(logger)

	// Initialize event service for modules
	eventService := service.NewEventService(db)

	// Initialize module registry
	moduleRegistry := module.NewRegistry(logger)

	// Create module context
	moduleCtx := &module.Context{
		DB:                db,
		Store:             store.New(db),
		Logger:            logger,
		Config:            cfg,
		Render:            renderer,
		Events:            eventService,
		Hooks:             hookRegistry,
		SchedulerRegistry: schedulerRegistry,
	}

	// Register all modules
	sentinelModule := sentinel.New()
	internalAnalyticsModule, err := registerModules(moduleRegistry, sentinelModule, sessionManager, eventService)
	if err != nil {
		return err
	}

	// Initialize all registered modules
	if err := moduleRegistry.InitAll(moduleCtx); err != nil {
		return fmt.Errorf("initializing modules: %w", err)
	}
	defer func() {
		if err := moduleRegistry.ShutdownAll(); err != nil {
			slog.Error("error shutting down modules", "error", err)
		}
	}()

	// Seed demo informer settings (must run after module init creates the table)
	if cfg.DoSeed {
		if err := store.SeedDemoInformerSettings(db); err != nil {
			slog.Warn("failed to seed demo informer settings", "error", err)
		} else if mod, ok := moduleRegistry.Get("informer"); ok {
			if inf, ok := mod.(*informer.Module); ok {
				if err := inf.ReloadSettings(); err != nil {
					slog.Warn("failed to reload informer settings after demo seed", "error", err)
				}
			}
		}
	}

	// Always initialize sentinel for middleware to work even if module is inactive.
	// InitAll skips Init() for inactive modules, but sentinel middleware is always
	// registered and needs its caches loaded. The middleware checks active status at runtime.
	if !moduleRegistry.IsActive("sentinel") {
		if err := sentinelModule.Init(moduleCtx); err != nil {
			slog.Warn("failed to pre-initialize sentinel for middleware", "error", err)
		}
	}

	// Set module registry on renderer for sidebar modules
	renderer.SetSidebarModuleProvider(moduleRegistry)

	if cfg.Env == "production" && cfg.RequireEmbedAllowedOrigins && strings.TrimSpace(cfg.EmbedAllowedOrigins) == "" {
		var activeEmbedProviders int
		err := db.QueryRow(`SELECT COUNT(*) FROM embed_settings WHERE is_enabled = 1`).Scan(&activeEmbedProviders)
		if err != nil {
			return fmt.Errorf("auditing embed origin policy posture: %w", err)
		}
		if activeEmbedProviders > 0 {
			return fmt.Errorf(
				"refusing to start in production: embed proxy is active (%d provider(s)) but OCMS_EMBED_ALLOWED_ORIGINS is not configured",
				activeEmbedProviders,
			)
		}
	}
	if cfg.Env == "production" && cfg.RequireEmbedAllowedUpstreamHosts {
		if err := auditRequiredEmbedUpstreamHostPolicyPosture(ctx, db, cfg.EmbedAllowedUpstreamHosts); err != nil {
			return err
		}
	}
	if cfg.Env == "production" && cfg.RequireHTTPSOutbound {
		if err := auditRequiredHTTPSOutboundPosture(ctx, db); err != nil {
			return err
		}
		if err := auditRequiredEmbedHTTPSPosture(ctx, db); err != nil {
			return err
		}
	}
	if cfg.Env == "production" && cfg.RequireFormCaptcha {
		captchaVerifierEnabled := false
		if mod, ok := moduleRegistry.Get("hcaptcha"); ok {
			if captchaMod, ok := mod.(*hcaptcha.Module); ok {
				captchaVerifierEnabled = captchaMod.IsEnabled()
			}
		}
		if err := auditRequiredFormCaptchaPosture(ctx, db, hookRegistry, captchaVerifierEnabled); err != nil {
			return err
		}
	}

	// Set up hook registry to check module active status
	hookRegistry.SetIsModuleActive(moduleRegistry.IsActive)

	// Add module template functions to renderer
	moduleFuncs := moduleRegistry.AllTemplateFuncs()
	if len(moduleFuncs) > 0 {
		renderer.AddTemplateFuncs(moduleFuncs)
		// Reload admin templates with new module functions
		if err := renderer.ReloadTemplates(); err != nil {
			slog.Warn("failed to reload admin templates with module funcs", "error", err)
		}
	}

	slog.Info("module system initialized", "modules", moduleRegistry.Count())

	// Load user-created scheduled tasks from DB
	if err := taskExecutor.LoadAndScheduleAll(); err != nil {
		slog.Error("failed to load scheduled tasks", "error", err)
	}

	// Register daily cleanup job for old task runs (>30 days)
	taskExecutor.RegisterCleanupJob()

	// Load themes (after modules provide their template functions)
	loadActiveTheme(ctx, queries, themeManager, renderer, cfg)

	// Create router
	r := chi.NewRouter()

	// Middleware stack
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	// Sentinel IP ban check (always registered, checks active status at runtime)
	r.Use(sentinelModule.GetMiddleware())
	slog.Info("sentinel IP ban middleware registered")
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Compress(5))                    // Gzip compression with level 5
	r.Use(chimw.GetHead)                        // Handle HEAD requests for uptime monitoring
	r.Use(middleware.Timeout(30 * time.Second)) // 30 second request timeout
	r.Use(middleware.StripTrailingSlash)        // Redirect /path/ to /path (301)

	// Global redirects middleware (database-driven URL redirects)
	redirectsMiddleware := middleware.NewRedirectsMiddleware(db)
	r.Use(redirectsMiddleware.Handler)

	// Security headers middleware (CSP, HSTS, X-Frame-Options, etc.)
	securityConfig := middleware.DefaultSecurityHeadersConfig(cfg.IsDevelopment())
	r.Use(middleware.SecurityHeaders(securityConfig))
	slog.Info("security headers middleware initialized",
		"hsts", !cfg.IsDevelopment(),
		"x_frame_options", "SAMEORIGIN",
	)

	// Request path middleware for logging context
	r.Use(middleware.RequestPath)

	r.Use(sessionManager.LoadAndSave)

	// CSRF protection middleware (applied globally, API routes will be exempted)
	csrfConfig := middleware.DefaultCSRFConfig([]byte(cfg.SessionSecret), cfg.IsDevelopment())
	csrfMiddleware := middleware.CSRF(csrfConfig)
	slog.Info("CSRF protection initialized", "secure", !cfg.IsDevelopment())

	// Initialize login protection
	loginProtection := middleware.NewLoginProtection(middleware.DefaultLoginProtectionConfig())
	slog.Info("login protection initialized",
		"ip_rate_limit", "0.5 req/s",
		"max_failed_attempts", 5,
		"lockout_duration", "15m",
	)

	// Initialize public rate limiter for auth routes (defense-in-depth)
	// 10 requests per second with burst of 20 per IP
	publicRateLimiter := middleware.NewGlobalRateLimiter(10.0, 20)
	slog.Info("public rate limiter initialized", "rate", "10 req/s", "burst", 20)

	// Dedicated public form submission limiter to reduce spam/flood abuse.
	// 1 request per second with a burst of 5 per IP.
	formSubmitRateLimiter := middleware.NewGlobalRateLimiter(1.0, 5)
	slog.Info("form submission rate limiter initialized", "rate", "1 req/s", "burst", 5)

	// Initialize handlers
	authHandler := handler.NewAuthHandler(db, renderer, sessionManager, loginProtection, hookRegistry)
	adminHandler := handler.NewAdminHandler(db, renderer, sessionManager, cacheManager)
	usersHandler := handler.NewUsersHandler(db, renderer, sessionManager)
	pagesHandler := handler.NewPagesHandler(db, renderer, sessionManager)
	pagesHandler.SetBlockSuspiciousMarkup(cfg.BlockSuspiciousPageHTML)
	if cfg.BlockSuspiciousPageHTML {
		slog.Info("page suspicious HTML blocking policy enabled")
	}
	configHandler := handler.NewConfigHandler(db, renderer, sessionManager, cacheManager)
	eventsHandler := handler.NewEventsHandler(db, renderer, sessionManager)
	taxonomyHandler := handler.NewTaxonomyHandler(db, renderer, sessionManager)
	mediaHandler := handler.NewMediaHandler(db, renderer, sessionManager, cfg.UploadsDir)
	menusHandler := handler.NewMenusHandler(db, renderer, sessionManager)
	frontendHandler := handler.NewFrontendHandler(db, themeManager, cacheManager, logger, renderer.GetMenuService(), eventService)
	frontendHandler.SetSanitizePageHTML(cfg.SanitizePageHTML)
	if cfg.SanitizePageHTML {
		slog.Info("frontend page HTML sanitization enabled")
	}
	formsHandler := handler.NewFormsHandler(db, renderer, sessionManager, hookRegistry, themeManager, cacheManager, renderer.GetMenuService(), frontendHandler)
	formsHandler.SetRequireCaptcha(cfg.RequireFormCaptcha)
	if cfg.RequireFormCaptcha {
		slog.Info("public forms captcha policy enabled")
	}
	formsHandler.SetWebhookFormDataMode(cfg.WebhookFormDataMode)
	slog.Info("form webhook payload mode configured", "mode", cfg.WebhookFormDataMode)
	themesHandler := handler.NewThemesHandler(db, renderer, sessionManager, themeManager, cacheManager)
	widgetsHandler := handler.NewWidgetsHandler(db, renderer, sessionManager, themeManager)
	modulesHandler := handler.NewModulesHandler(db, renderer, sessionManager, moduleRegistry, hookRegistry)
	cacheHandler := handler.NewCacheHandler(renderer, sessionManager, cacheManager, eventService)
	schedulerHandler := handler.NewSchedulerHandler(db, renderer, sessionManager, schedulerRegistry, taskExecutor, eventService)
	languagesHandler := handler.NewLanguagesHandler(db, renderer, sessionManager)
	apiHandler := api.NewHandler(db)
	apiDocsHandler, err := api.NewDocsHandler(api.DocsConfig{
		DB:         db,
		TemplateFS: templatesFS,
		IsDev:      cfg.IsDevelopment(),
	})
	if err != nil {
		return fmt.Errorf("initializing api docs handler: %w", err)
	}
	apiKeysHandler := handler.NewAPIKeysHandler(db, renderer, sessionManager)
	apiKeysHandler.SetRequireSourceCIDRs(cfg.RequireAPIKeySourceCIDRs)
	apiKeysHandler.SetRequireExpiry(cfg.RequireAPIKeyExpiry)
	apiKeysHandler.SetMaxTTLDays(cfg.APIKeyMaxTTLDays)
	webhooksHandler := handler.NewWebhooksHandler(db, renderer, sessionManager)
	redirectsHandler := handler.NewRedirectsHandler(db, renderer, sessionManager, redirectsMiddleware)
	importExportHandler := handler.NewImportExportHandler(db, renderer, sessionManager)
	healthHandler := handler.NewHealthHandler(db, sessionManager, cfg.UploadsDir)
	docsHandler := handler.NewDocsHandler(renderer, sessionManager, cfg, moduleRegistry, healthHandler.StartTime(), versionInfo)

	// Set webhook dispatcher on handlers that dispatch events
	pagesHandler.SetDispatcher(webhookDispatcher)
	mediaHandler.SetDispatcher(webhookDispatcher)
	usersHandler.SetDispatcher(webhookDispatcher)
	formsHandler.SetDispatcher(webhookDispatcher)

	// Set cache manager on handlers that need cache invalidation
	pagesHandler.SetCacheManager(cacheManager)
	apiHandler.SetCacheManager(cacheManager)

	// Health check routes (public, returns additional details for authenticated callers)
	r.Get("/health", healthHandler.Health)
	r.Get("/health/live", healthHandler.Liveness)
	r.Get("/health/ready", healthHandler.Readiness)

	// Public frontend routes (with language detection and analytics tracking)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Language(db))
		// Optionally load user for context-aware caching (doesn't require login)
		r.Use(middleware.OptionalLoadUser(sessionManager, db))
		// Add internal analytics tracking middleware (if module is enabled)
		if internalAnalyticsModule.IsEnabled() {
			r.Use(internalAnalyticsModule.GetTrackingMiddleware())
		}

		// Default language routes (no prefix)
		// Specific routes first (before catch-all in registerFrontendRoutes)
		r.Get("/sitemap.xml", frontendHandler.Sitemap)
		r.Get("/robots.txt", frontendHandler.Robots)
		r.Get("/.well-known/security.txt", frontendHandler.Security)
		// Common frontend routes (RouteParamSlug catch-all is registered last)
		registerFrontendRoutes(r, frontendHandler)

		// Language-prefixed routes (e.g., /ru/, /ru/page-slug)
		r.Route("/{lang:[a-z]{2}}", func(r chi.Router) {
			registerFrontendRoutes(r, frontendHandler)
		})
	})

	// Auth routes (public, with CSRF and rate limiting)
	// Defense-in-depth: publicRateLimiter (10 req/s) + loginProtection (0.5 req/s on POST + account lockout)
	r.Group(func(r chi.Router) {
		r.Use(publicRateLimiter.HTMLMiddleware())
		r.Use(csrfMiddleware)
		r.Get(handler.RouteLogin, authHandler.LoginForm)
		r.With(loginProtection.Middleware()).Post(handler.RouteLogin, authHandler.Login)
		r.Post(handler.RouteLogout, authHandler.Logout)
		r.Post(handler.RouteLanguage, authHandler.SetLanguage)
	})

	// Session test routes (development only)
	if cfg.IsDevelopment() {
		r.Get("/session/set", func(w http.ResponseWriter, r *http.Request) {
			value := r.URL.Query().Get("value")
			if value == "" {
				value = "test-value"
			}
			sessionManager.Put(r.Context(), "test_key", value)
			w.Header().Set(handler.HeaderContentType, "text/plain; charset=utf-8")
			_, _ = fmt.Fprintf(w, "Session value set: %s\n", value)
		})

		r.Get("/session/get", func(w http.ResponseWriter, r *http.Request) {
			value := sessionManager.GetString(r.Context(), "test_key")
			w.Header().Set(handler.HeaderContentType, "text/plain; charset=utf-8")
			if value == "" {
				_, _ = fmt.Fprintln(w, "No session value found")
			} else {
				_, _ = fmt.Fprintf(w, "Session value: %s\n", value)
			}
		})
	}

	// Admin routes (protected with CSRF)
	r.Route("/admin", func(r chi.Router) {
		r.Use(csrfMiddleware)
		r.Use(middleware.Auth(sessionManager))
		r.Use(middleware.LoadUser(sessionManager, db))
		r.Use(middleware.LoadSiteConfig(db, nil)) // Admin: always query DB, no cache

		// Editor routes (editor + admin) - public users have no admin access
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireEditorWithEventLog(eventService))

			// Dashboard and common routes
			r.Get(handler.RouteRoot, adminHandler.Dashboard)
			r.Post("/language", adminHandler.SetLanguage)
			r.Get("/events", eventsHandler.List)

			// Page management routes
			registerCRUD(r, handler.RoutePages, handler.RoutePagesID, crudHandlers{
				List: pagesHandler.List, NewForm: pagesHandler.NewForm, Create: pagesHandler.Create,
				EditForm: pagesHandler.EditForm, Update: pagesHandler.Update, Delete: pagesHandler.Delete,
			})
			r.Post(handler.RoutePagesID+"/publish", pagesHandler.TogglePublish)
			r.Get(handler.RoutePagesID+"/versions", pagesHandler.Versions)
			r.Post(handler.RoutePagesID+"/versions/{versionId}/restore", pagesHandler.RestoreVersion)
			r.Post(handler.RoutePagesID+handler.RouteSuffixTranslate, pagesHandler.Translate)

			// Tag management routes
			registerCRUD(r, handler.RouteTags, handler.RouteTagsID, crudHandlers{
				List: taxonomyHandler.ListTags, NewForm: taxonomyHandler.NewTagForm, Create: taxonomyHandler.CreateTag,
				EditForm: taxonomyHandler.EditTagForm, Update: taxonomyHandler.UpdateTag, Delete: taxonomyHandler.DeleteTag,
			})
			r.Get(handler.RouteTags+handler.RouteSuffixSearch, taxonomyHandler.SearchTags)
			r.Post(handler.RouteTagsID+handler.RouteSuffixTranslate, taxonomyHandler.TranslateTag)

			// Category management routes
			registerCRUD(r, handler.RouteCategories, handler.RouteCategoriesID, crudHandlers{
				List: taxonomyHandler.ListCategories, NewForm: taxonomyHandler.NewCategoryForm, Create: taxonomyHandler.CreateCategory,
				EditForm: taxonomyHandler.EditCategoryForm, Update: taxonomyHandler.UpdateCategory, Delete: taxonomyHandler.DeleteCategory,
			})
			r.Get(handler.RouteCategories+handler.RouteSuffixSearch, taxonomyHandler.SearchCategories)
			r.Post(handler.RouteCategoriesID+handler.RouteSuffixTranslate, taxonomyHandler.TranslateCategory)

			// Media library routes
			r.Get(handler.RouteMedia, mediaHandler.Library)
			r.Get(handler.RouteMedia+"/api", mediaHandler.API) // JSON API for media picker
			r.Get(handler.RouteMedia+handler.RouteSuffixUpload, mediaHandler.UploadForm)
			r.Post(handler.RouteMedia+handler.RouteSuffixUpload, mediaHandler.Upload)
			r.Get(handler.RouteMediaID, mediaHandler.EditForm)
			r.Put(handler.RouteMediaID, mediaHandler.Update)
			r.Post(handler.RouteMediaID, mediaHandler.Update) // HTML forms can't send PUT
			r.Delete(handler.RouteMediaID, mediaHandler.Delete)
			r.Post(handler.RouteMediaID+handler.RouteSuffixMove, mediaHandler.MoveMedia)
			r.Post(handler.RouteMediaID+handler.RouteSuffixRegenerate, mediaHandler.RegenerateVariants)

			// Media folders
			r.Post(handler.RouteMedia+handler.RouteSuffixFolders, mediaHandler.CreateFolder)
			r.Put(handler.RouteMediaFoldersID, mediaHandler.UpdateFolder)
			r.Post(handler.RouteMediaFoldersID, mediaHandler.UpdateFolder) // HTML forms can't send PUT
			r.Delete(handler.RouteMediaFoldersID, mediaHandler.DeleteFolder)

			// Menu management routes
			registerCRUD(r, handler.RouteMenus, handler.RouteMenusID, crudHandlers{
				List: menusHandler.List, NewForm: menusHandler.NewForm, Create: menusHandler.Create,
				EditForm: menusHandler.EditForm, Update: menusHandler.Update, Delete: menusHandler.Delete,
			})
			r.Post(handler.RouteMenusID+"/items", menusHandler.AddItem)
			r.Put(handler.RouteMenusID+handler.RouteItemsItemID, menusHandler.UpdateItem)
			r.Delete(handler.RouteMenusID+handler.RouteItemsItemID, menusHandler.DeleteItem)
			r.Post(handler.RouteMenusID+handler.RouteSuffixReorder, menusHandler.Reorder)

			// Form management routes
			registerCRUD(r, handler.RouteForms, handler.RouteFormsID, crudHandlers{
				List: formsHandler.List, NewForm: formsHandler.NewForm, Create: formsHandler.Create,
				EditForm: formsHandler.EditForm, Update: formsHandler.Update, Delete: formsHandler.Delete,
			})
			r.Post(handler.RouteFormsID+"/fields", formsHandler.AddField)
			r.Put(handler.RouteFormsID+handler.RouteFieldsFieldID, formsHandler.UpdateField)
			r.Delete(handler.RouteFormsID+handler.RouteFieldsFieldID, formsHandler.DeleteField)
			r.Post(handler.RouteFormsID+"/fields/reorder", formsHandler.ReorderFields)

			// Form submissions routes
			r.Get(handler.RouteFormsID+"/submissions", formsHandler.Submissions)
			r.Get(handler.RouteFormsID+handler.RouteSubmissionsSubID, formsHandler.ViewSubmission)
			r.Delete(handler.RouteFormsID+handler.RouteSubmissionsSubID, formsHandler.DeleteSubmission)
			r.Post(handler.RouteFormsID+"/submissions/export", formsHandler.ExportSubmissions)

			// Form translation route
			r.Post(handler.RouteFormsID+handler.RouteSuffixTranslate, formsHandler.TranslateForm)

			// Theme settings (not activation - that's admin only)
			registerSettingsRoutes(r, handler.RouteThemeSettings, themesHandler.Settings, themesHandler.SaveSettings)

			// Widget management routes
			r.Get(handler.RouteWidgets, widgetsHandler.List)
			r.Post(handler.RouteWidgets, widgetsHandler.Create)
			r.Get(handler.RouteWidgetsID, widgetsHandler.GetWidget)
			r.Put(handler.RouteWidgetsID, widgetsHandler.Update)
			r.Delete(handler.RouteWidgetsID, widgetsHandler.Delete)
			r.Post(handler.RouteWidgetsID+handler.RouteSuffixMove, widgetsHandler.MoveWidget)
			r.Post(handler.RouteWidgets+handler.RouteSuffixReorder, widgetsHandler.Reorder)

			// Register module admin routes (require editor role)
			moduleRegistry.AdminRouteAll(r)
		})

		// Admin-only routes
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdminWithEventLog(eventService))

			// User management routes
			registerCRUD(r, handler.RouteUsers, handler.RouteUsersID, crudHandlers{
				List: usersHandler.List, NewForm: usersHandler.NewForm, Create: usersHandler.Create,
				EditForm: usersHandler.EditForm, Update: usersHandler.Update, Delete: usersHandler.Delete,
			})

			// Language management routes
			registerCRUD(r, handler.RouteLanguages, handler.RouteLanguagesID, crudHandlers{
				List: languagesHandler.List, NewForm: languagesHandler.NewForm, Create: languagesHandler.Create,
				EditForm: languagesHandler.EditForm, Update: languagesHandler.Update, Delete: languagesHandler.Delete,
			})
			r.Post(handler.RouteLanguagesID+"/default", languagesHandler.SetDefault)

			// Configuration routes
			registerSettingsRoutes(r, handler.RouteConfig, configHandler.List, configHandler.Update)

			// Theme management routes (activation is admin only)
			r.Get("/themes", themesHandler.List)
			r.Post("/themes/activate", themesHandler.Activate)

			// Module management routes
			r.Get("/modules", modulesHandler.List)
			r.Post("/modules/{name}/toggle", modulesHandler.ToggleActive)
			r.Post("/modules/{name}/toggle-sidebar", modulesHandler.ToggleSidebar)

			// API key management routes
			registerCRUD(r, handler.RouteAPIKeys, handler.RouteAPIKeysID, crudHandlers{
				List: apiKeysHandler.List, NewForm: apiKeysHandler.NewForm, Create: apiKeysHandler.Create,
				EditForm: apiKeysHandler.EditForm, Update: apiKeysHandler.Update, Delete: apiKeysHandler.Delete,
			})

			// Webhook management routes
			registerCRUD(r, handler.RouteWebhooks, handler.RouteWebhooksID, crudHandlers{
				List: webhooksHandler.List, NewForm: webhooksHandler.NewForm, Create: webhooksHandler.Create,
				EditForm: webhooksHandler.EditForm, Update: webhooksHandler.Update, Delete: webhooksHandler.Delete,
			})
			r.Get(handler.RouteWebhooksID+"/deliveries", webhooksHandler.Deliveries)
			r.Post(handler.RouteWebhooksID+"/test", webhooksHandler.Test)
			r.Post(handler.RouteWebhooksID+"/deliveries/{did}/retry", webhooksHandler.RetryDelivery)

			// Redirect management routes
			registerCRUD(r, handler.RouteRedirects, handler.RouteRedirectsID, crudHandlers{
				List: redirectsHandler.List, NewForm: redirectsHandler.NewForm, Create: redirectsHandler.Create,
				EditForm: redirectsHandler.EditForm, Update: redirectsHandler.Update, Delete: redirectsHandler.Delete,
			})
			r.Post(handler.RouteRedirectsID+"/toggle", redirectsHandler.Toggle)

			// Cache management routes
			r.Get("/cache", cacheHandler.Stats)
			r.Post("/cache/clear", cacheHandler.Clear)
			r.Post("/cache/clear/config", cacheHandler.ClearConfig)
			r.Post("/cache/clear/sitemap", cacheHandler.ClearSitemap)
			r.Post("/cache/clear/pages", cacheHandler.ClearPages)
			r.Post("/cache/clear/menus", cacheHandler.ClearMenus)
			r.Post("/cache/clear/languages", cacheHandler.ClearLanguages)

			// Scheduler management routes
			r.Get("/scheduler", schedulerHandler.List)
			r.Post("/scheduler/update", schedulerHandler.UpdateSchedule)
			r.Post("/scheduler/reset", schedulerHandler.ResetSchedule)
			r.Post("/scheduler/trigger/{source}/{name}", schedulerHandler.TriggerNow)
			// Scheduled task routes
			r.Get("/scheduler/tasks/new", schedulerHandler.TaskForm)
			r.Post("/scheduler/tasks", schedulerHandler.TaskCreate)
			r.Get("/scheduler/tasks/{id}/edit", schedulerHandler.TaskForm)
			r.Post("/scheduler/tasks/{id}", schedulerHandler.TaskUpdate)
			r.Post("/scheduler/tasks/{id}/toggle", schedulerHandler.TaskToggle)
			r.Post("/scheduler/tasks/{id}/delete", schedulerHandler.TaskDelete)
			r.Get("/scheduler/tasks/{id}/runs", schedulerHandler.TaskRuns)
			r.Post("/scheduler/tasks/{id}/trigger", schedulerHandler.TaskTrigger)

			// Import/Export routes
			r.Get(handler.RouteExport, importExportHandler.ExportForm)
			r.Post(handler.RouteExport, importExportHandler.Export)
			r.Get(handler.RouteImport, importExportHandler.ImportForm)
			r.Post(handler.RouteImport+"/validate", importExportHandler.ImportValidate)
			r.Post(handler.RouteImport, importExportHandler.Import)

			// Site documentation routes
			r.Get(handler.RouteDocs, docsHandler.Overview)
			r.Get(handler.RouteDocsSlug, docsHandler.Guide)
		})

	})

	// REST API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		// Global rate limiting for API (100 requests per second with burst of 200)
		apiRateLimiter := middleware.NewGlobalRateLimiter(100, 200)
		r.Use(apiRateLimiter.Middleware())

		// Public endpoints (no authentication required)
		r.Get("/status", apiHandler.Status)
		r.Get("/docs", apiDocsHandler.ServeDocs)

		// Pages - public read endpoints (optional auth for enhanced access)
		r.Group(func(r chi.Router) {
			r.Use(middleware.OptionalAPIKeyAuth(db))
			r.Get(handler.RoutePages, apiHandler.ListPages)
			r.Get(handler.RoutePagesID, apiHandler.GetPage)
			r.Get(handler.RoutePages+"/slughandler.RouteParamSlug", apiHandler.GetPageBySlug)
		})

		// Media - public read endpoints (optional auth for enhanced access)
		r.Group(func(r chi.Router) {
			r.Use(middleware.OptionalAPIKeyAuth(db))
			r.Get(handler.RouteMedia, apiHandler.ListMedia)
			r.Get(handler.RouteMediaID, apiHandler.GetMedia)
		})

		// Tags - public read endpoints
		r.Get(handler.RouteTags, apiHandler.ListTags)
		r.Get(handler.RouteTagsID, apiHandler.GetTag)

		// Categories - public read endpoints
		r.Get(handler.RouteCategories, apiHandler.ListCategories)
		r.Get(handler.RouteCategoriesID, apiHandler.GetCategory)

		// Protected endpoints (API key required)
		r.Group(func(r chi.Router) {
			r.Use(middleware.APIKeyAuth(db))
			r.Use(middleware.APIRateLimit(10, 20)) // 10 requests per second per API key

			// Auth info endpoint
			r.Get("/auth", apiHandler.AuthInfo)

			// Pages - write endpoints (requires pages:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("pages:write"))
				r.Post(handler.RoutePages, apiHandler.CreatePage)
				r.Put(handler.RoutePagesID, apiHandler.UpdatePage)
				r.Delete(handler.RoutePagesID, apiHandler.DeletePage)
			})

			// Media - write endpoints (requires media:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("media:write"))
				r.Post(handler.RouteMedia, apiHandler.UploadMedia)
				r.Put(handler.RouteMediaID, apiHandler.UpdateMedia)
				r.Delete(handler.RouteMediaID, apiHandler.DeleteMedia)
			})

			// Taxonomy - write endpoints (requires taxonomy:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("taxonomy:write"))
				r.Post(handler.RouteTags, apiHandler.CreateTag)
				r.Put(handler.RouteTagsID, apiHandler.UpdateTag)
				r.Delete(handler.RouteTagsID, apiHandler.DeleteTag)
				r.Post(handler.RouteCategories, apiHandler.CreateCategory)
				r.Put(handler.RouteCategoriesID, apiHandler.UpdateCategory)
				r.Delete(handler.RouteCategoriesID, apiHandler.DeleteCategory)
			})
		})
	})
	slog.Info("REST API v1 mounted at /api/v1")

	// Public form routes (no authentication required, with CSRF protection and language detection)
	r.Group(func(r chi.Router) {
		r.Use(csrfMiddleware)
		r.Use(middleware.Language(db))
		r.Get(handler.RouteFormsSlug, formsHandler.Show)
		r.With(formSubmitRateLimiter.HTMLMiddleware()).Post(handler.RouteFormsSlug, formsHandler.Submit)
	})

	// Favicon route - serve from theme settings or embedded default
	defaultFavicon, _ := web.Static.ReadFile("static/dist/favicon.ico")
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		frontendHandler.Favicon(w, r, defaultFavicon)
	})

	// Static file serving
	staticFS, err := fs.Sub(web.Static, "static/dist")
	if err != nil {
		return fmt.Errorf("getting static fs: %w", err)
	}
	// Static assets: cache for 1 year (31536000 seconds)
	staticHandler := middleware.StaticCache(31536000)(http.StripPrefix("/static/dist/", http.FileServer(http.FS(staticFS))))
	r.Handle("/static/dist/*", staticHandler)

	// Serve uploaded media files from uploads directory (configured via OCMS_UPLOADS_DIR)
	// Uploads: cache for 1 week (604800 seconds)
	uploadsDirFS := http.Dir(cfg.UploadsDir)
	uploadsHandler := middleware.StaticCache(604800)(http.StripPrefix("/uploads/", middleware.SecureUploads(http.FileServer(uploadsDirFS))))
	r.Handle("/uploads/*", uploadsHandler)

	// Serve theme static files with caching (1 month = 2592000 seconds)
	// Supports both embedded themes (from binary) and external themes (from filesystem)
	r.Get("/themes/{themeName}/static/*", func(w http.ResponseWriter, r *http.Request) {
		themeName := chi.URLParam(r, "themeName")
		thm, err := themeManager.GetTheme(themeName)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Security headers for static files
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Cache-Control", "public, max-age=2592000")

		// Strip the prefix to get the requested file path
		reqPath := r.URL.Path
		prefix := fmt.Sprintf("/themes/%s/static/", themeName)
		requestedPath := reqPath[len(prefix):]

		// Clean the path to resolve any .. sequences
		// Use path.Clean (not filepath.Clean) for URL paths - filepath.Clean uses
		// OS-specific separators (backslashes on Windows), which breaks embed.FS
		// lookups that require forward slashes.
		cleanPath := path.Clean(requestedPath)

		// Reject paths that try to escape (start with .. or are absolute)
		if strings.HasPrefix(cleanPath, "..") || path.IsAbs(cleanPath) {
			http.NotFound(w, r)
			return
		}

		// Handle embedded vs external themes
		if thm.IsEmbedded && thm.EmbeddedFS != nil {
			// Serve from embedded filesystem
			embeddedPath := "static/" + cleanPath
			data, err := fs.ReadFile(thm.EmbeddedFS, embeddedPath)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			// Set content type based on file extension
			contentType := handler.ContentTypeByExtension(cleanPath)
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			_, _ = w.Write(data)
			return
		}

		// Serve from filesystem for external themes
		filePath := filepath.Join(thm.StaticPath, cleanPath)

		// Verify the resolved path is within the static directory
		absStaticPath, err := filepath.Abs(thm.StaticPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		absFilePath, err := filepath.Abs(filePath)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Check that file is within static directory
		if !strings.HasPrefix(absFilePath, absStaticPath+string(filepath.Separator)) && absFilePath != absStaticPath {
			http.NotFound(w, r)
			return
		}

		// Verify containment using filepath.Rel (CodeQL-recognized pattern)
		rel, err := filepath.Rel(absStaticPath, absFilePath)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			http.NotFound(w, r)
			return
		}

		http.ServeFile(w, r, absFilePath)
	})

	// Register module public routes
	moduleRegistry.RouteAll(r)

	// 404 Not Found handler - use frontend theme's 404
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		frontendHandler.NotFound(w, req)
	})

	// Create server with appropriate timeouts
	srv := &http.Server{
		Addr:              cfg.ServerAddr(),
		Handler:           r,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second, // Longer to allow for large uploads and slow connections
		IdleTimeout:       60 * time.Second, // Reduced from 120s to mitigate slowloris attacks
		MaxHeaderBytes:    1 << 20,          // 1MB max header size
	}

	// Start server in goroutine
	go func() {
		slog.Info("starting server", "addr", cfg.ServerAddr(), "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	slog.Info("server stopped")
	return nil
}
