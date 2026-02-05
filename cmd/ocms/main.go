// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/config"
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
	"github.com/olegiv/ocms-go/modules/migrator"
	"github.com/olegiv/ocms-go/modules/privacy"
	"github.com/olegiv/ocms-go/modules/sentinel"
	"github.com/olegiv/ocms-go/web"
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
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_CUSTOM_DIR        Custom content directory (default: ./custom)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_ACTIVE_THEME      Active theme name (default: default)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_REDIS_URL         Redis URL for distributed caching (optional)\n")
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

func run() error {
	// Load .env files if present (development)
	_ = godotenv.Load()
	_ = godotenv.Load("modules/migrator/.env") // Migrator module config

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Create version info from build-time injected values
	versionInfo := &version.Info{
		Version:   appVersion,
		GitCommit: appGitCommit,
		BuildTime: appBuildTime,
	}

	// Setup logger
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

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

	// Seed demo content if demo mode is enabled
	if err := store.SeedDemo(ctx, db); err != nil {
		return fmt.Errorf("seeding demo content: %w", err)
	}

	// Update i18n from database settings
	queries := store.New(db)

	// Set active languages (only match against languages that are active in DB)
	if activeLanguages, err := queries.ListActiveLanguages(ctx); err == nil {
		var activeCodes []string
		for _, lang := range activeLanguages {
			activeCodes = append(activeCodes, lang.Code)
		}
		i18n.SetActiveLanguages(activeCodes)
		slog.Info("i18n active languages set from database", "languages", activeCodes)
	}

	// Set default language
	if defaultLang, err := queries.GetDefaultLanguage(ctx); err == nil {
		if i18n.IsSupported(defaultLang.Code) {
			i18n.SetDefaultLanguage(defaultLang.Code)
			slog.Info("i18n default language set from database", "language", defaultLang.Code)
		}
	}

	// Initialize session manager
	sessionManager := session.New(db, cfg.IsDevelopment())
	middleware.SetSessionManager(sessionManager)
	slog.Info("session manager initialized")

	// Initialize language cookie security settings
	middleware.InitLanguageCookies(cfg.IsDevelopment())

	// Initialize cache manager with config (must be before renderer for shared MenuService)
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
	defer cacheManager.Stop()

	// Preload caches (config, menus, languages)
	if err := cacheManager.Preload(ctx, ""); err != nil {
		slog.Warn("failed to preload caches", "error", err)
	}
	switch {
	case cacheManager.IsRedis():
		slog.Info(handler.LogCacheManagerInit, "backend", "redis", "url", cfg.RedisURL)
	case cacheManager.Info().IsFallback:
		slog.Warn(handler.LogCacheManagerInit, "backend", "memory", "note", "Redis unavailable, using fallback")
	default:
		slog.Info(handler.LogCacheManagerInit, "backend", "memory")
	}

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

	// Initialize and start scheduler
	sched := scheduler.New(db, logger)
	if err := sched.Start(); err != nil {
		return fmt.Errorf("starting scheduler: %w", err)
	}
	defer sched.Stop()

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
		DB:     db,
		Store:  store.New(db),
		Logger: logger,
		Config: cfg,
		Render: renderer,
		Events: eventService,
		Hooks:  hookRegistry,
	}

	// Register modules
	// Modules should be registered before InitAll is called
	if err := moduleRegistry.Register(example.New()); err != nil {
		return fmt.Errorf("registering example module: %w", err)
	}
	if err := moduleRegistry.Register(developer.New()); err != nil {
		return fmt.Errorf("registering developer module: %w", err)
	}
	if err := moduleRegistry.Register(analytics_ext.New()); err != nil {
		return fmt.Errorf("registering analytics_ext module: %w", err)
	}
	if err := moduleRegistry.Register(embed.New()); err != nil {
		return fmt.Errorf("registering embed module: %w", err)
	}
	if err := moduleRegistry.Register(hcaptcha.New()); err != nil {
		return fmt.Errorf("registering hcaptcha module: %w", err)
	}
	if err := moduleRegistry.Register(privacy.New()); err != nil {
		return fmt.Errorf("registering privacy module: %w", err)
	}
	sentinelModule := sentinel.New()
	sentinelModule.SetSessionManager(sessionManager)
	if err := moduleRegistry.Register(sentinelModule); err != nil {
		return fmt.Errorf("registering sentinel module: %w", err)
	}
	if err := moduleRegistry.Register(migrator.New()); err != nil {
		return fmt.Errorf("registering migrator module: %w", err)
	}
	if err := moduleRegistry.Register(dbmanager.New()); err != nil {
		return fmt.Errorf("registering dbmanager module: %w", err)
	}

	// Register internal analytics module (built-in analytics tracking)
	internalAnalyticsModule := analytics_int.New()
	if err := moduleRegistry.Register(internalAnalyticsModule); err != nil {
		return fmt.Errorf("registering analytics_int module: %w", err)
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

	// Set module registry on renderer for sidebar modules
	renderer.SetSidebarModuleProvider(moduleRegistry)

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

	// Load themes (after modules provide their template functions)
	themeManager.SetFuncMap(renderer.TemplateFuncs())
	if err := themeManager.LoadThemes(); err != nil {
		slog.Warn("failed to load themes", "error", err)
	}

	// Determine active theme: database config takes priority over environment variable
	activeTheme := cfg.ActiveTheme
	if dbConfig, err := queries.GetConfigByKey(ctx, "active_theme"); err == nil && dbConfig.Value != "" {
		activeTheme = dbConfig.Value
		slog.Info("active theme loaded from database", "theme", activeTheme)
	}

	// Set active theme
	if themeManager.HasTheme(activeTheme) {
		if err := themeManager.SetActiveTheme(activeTheme); err != nil {
			slog.Warn("failed to set active theme", "theme", activeTheme, "error", err)
		}
	} else if themeManager.ThemeCount() > 0 {
		// Fall back to first available theme
		availableThemes := themeManager.ListThemesWithActive()
		if len(availableThemes) > 0 {
			if err := themeManager.SetActiveTheme(availableThemes[0].Name); err != nil {
				slog.Warn("failed to set fallback theme", "error", err)
			}
		}
	}
	slog.Info("theme manager initialized", "themes", themeManager.ThemeCount())

	// Create router
	r := chi.NewRouter()

	// Middleware stack
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	// Sentinel IP ban check (applied early, after RealIP)
	if moduleRegistry.IsActive("sentinel") {
		r.Use(sentinelModule.GetMiddleware())
		slog.Info("sentinel IP ban middleware enabled")
	}
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

	// Initialize handlers
	authHandler := handler.NewAuthHandler(db, renderer, sessionManager, loginProtection, hookRegistry)
	adminHandler := handler.NewAdminHandler(db, renderer, sessionManager, cacheManager)
	usersHandler := handler.NewUsersHandler(db, renderer, sessionManager)
	pagesHandler := handler.NewPagesHandler(db, renderer, sessionManager)
	configHandler := handler.NewConfigHandler(db, renderer, sessionManager, cacheManager)
	eventsHandler := handler.NewEventsHandler(db, renderer, sessionManager)
	taxonomyHandler := handler.NewTaxonomyHandler(db, renderer, sessionManager)
	mediaHandler := handler.NewMediaHandler(db, renderer, sessionManager, cfg.UploadsDir)
	menusHandler := handler.NewMenusHandler(db, renderer, sessionManager)
	frontendHandler := handler.NewFrontendHandler(db, themeManager, cacheManager, logger, renderer.GetMenuService(), eventService)
	formsHandler := handler.NewFormsHandler(db, renderer, sessionManager, hookRegistry, themeManager, cacheManager, renderer.GetMenuService(), frontendHandler)
	themesHandler := handler.NewThemesHandler(db, renderer, sessionManager, themeManager, cacheManager)
	widgetsHandler := handler.NewWidgetsHandler(db, renderer, sessionManager, themeManager)
	modulesHandler := handler.NewModulesHandler(db, renderer, sessionManager, moduleRegistry, hookRegistry)
	cacheHandler := handler.NewCacheHandler(renderer, sessionManager, cacheManager, eventService)
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
	webhooksHandler := handler.NewWebhooksHandler(db, renderer, sessionManager)
	redirectsHandler := handler.NewRedirectsHandler(db, renderer, sessionManager, redirectsMiddleware)
	importExportHandler := handler.NewImportExportHandler(db, renderer, sessionManager)
	healthHandler := handler.NewHealthHandler(db, sessionManager, cfg.UploadsDir)
	docsHandler := handler.NewDocsHandler(renderer, cfg, moduleRegistry, healthHandler.StartTime(), versionInfo)

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
		r.Get(handler.RouteLogout, authHandler.Logout)
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

		// Register module admin routes (module-specific permissions)
		moduleRegistry.AdminRouteAll(r)
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

	// Public form routes (no authentication required, with CSRF protection)
	r.Group(func(r chi.Router) {
		r.Use(csrfMiddleware)
		r.Get(handler.RouteFormsSlug, formsHandler.Show)
		r.Post(handler.RouteFormsSlug, formsHandler.Submit)
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
	uploadsHandler := middleware.StaticCache(604800)(http.StripPrefix("/uploads/", http.FileServer(uploadsDirFS)))
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
