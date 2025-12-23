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
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"ocms-go/internal/cache"
	"ocms-go/internal/config"
	"ocms-go/internal/handler"
	"ocms-go/internal/handler/api"
	"ocms-go/internal/i18n"
	"ocms-go/internal/logging"
	"ocms-go/internal/middleware"
	"ocms-go/internal/module"
	"ocms-go/internal/render"
	"ocms-go/internal/scheduler"
	"ocms-go/internal/service"
	"ocms-go/internal/session"
	"ocms-go/internal/store"
	"ocms-go/internal/theme"
	"ocms-go/internal/webhook"
	"ocms-go/modules/analytics_ext"
	"ocms-go/modules/analytics_int"
	"ocms-go/modules/developer"
	"ocms-go/modules/example"
	"ocms-go/modules/hcaptcha"
	"ocms-go/web"
)

// Version information - injected at build time via ldflags
var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

// Constants for repeated literals
const (
	// Log messages
	logCacheManagerInit = "cache manager initialized"

	// Paths
	uploadsDirPath = "./uploads"

	// HTTP headers
	headerContentType = "Content-Type"

	// Common route suffixes
	routeRoot            = "/"
	routeSuffixNew       = "/new"
	routeSuffixSearch    = "/search"
	routeSuffixUpload    = "/upload"
	routeSuffixReorder   = "/reorder"
	routeSuffixMove      = "/move"
	routeSuffixTranslate = "/translate/{langCode}"

	// Route parameter patterns
	routeParamID          = "/{id}"
	routeParamSlug        = "/{slug}"
	routeTagSlug          = "/tag/{slug}"
	routeCategorySlug     = "/category/{slug}"
	routeFormsSlug        = "/forms/{slug}"
	routeSubmissionsSubID = "/submissions/{subId}"
	routeItemsItemID      = "/items/{itemId}"
	routeFieldsFieldID    = "/fields/{fieldId}"
	routeSuffixFolders    = "/folders"

	// Public route patterns
	routeLogin  = "/login"
	routeLogout = "/logout"
	routeBlog   = "/blog"

	// Admin route patterns - base routes
	routeUsers      = "/users"
	routeLanguages  = "/languages"
	routePages      = "/pages"
	routeTags       = "/tags"
	routeCategories = "/categories"
	routeMedia      = "/media"
	routeMenus      = "/menus"
	routeForms      = "/forms"
	routeWidgets    = "/widgets"
	routeAPIKeys    = "/api-keys"
	routeWebhooks   = "/webhooks"
	routeExport     = "/export"
	routeImport     = "/import"

	// Admin route patterns - with ID parameter
	routeUsersID        = routeUsers + routeParamID
	routeLanguagesID    = routeLanguages + routeParamID
	routePagesID        = routePages + routeParamID
	routeConfig         = "/config"
	routeTagsID         = routeTags + routeParamID
	routeCategoriesID   = routeCategories + routeParamID
	routeMediaID        = routeMedia + routeParamID
	routeMediaFoldersID = routeMedia + routeSuffixFolders + routeParamID
	routeMenusID        = routeMenus + routeParamID
	routeFormsID        = routeForms + routeParamID
	routeThemeSettings  = "/themes/{name}/settings"
	routeWidgetsID      = routeWidgets + routeParamID
	routeAPIKeysID      = routeAPIKeys + routeParamID
	routeWebhooksID     = routeWebhooks + routeParamID
)

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
		_, _ = fmt.Fprintf(os.Stderr, "  OCMS_THEMES_DIR        Themes directory (default: ./themes)\n")
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
		_, _ = fmt.Printf("ocms %s (commit: %s, built: %s)\n", version, gitCommit, buildTime)
		os.Exit(0)
	}

	if err := run(); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Load .env file if present (development)
	_ = godotenv.Load()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
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
	if err := store.Seed(ctx, db); err != nil {
		return fmt.Errorf("seeding database: %w", err)
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
	})
	if err != nil {
		return fmt.Errorf("initializing renderer: %w", err)
	}
	slog.Info("template renderer initialized")

	// Initialize theme manager
	themeManager := theme.NewManager(cfg.ThemesDir, logger)

	// Add theme manager's template functions (TTheme) to renderer
	renderer.AddTemplateFuncs(themeManager.TemplateFuncs())

	// Note: Theme loading is deferred until after modules are initialized
	// so that module template functions (like analyticsHead) are available

	// Initialize cache manager with config
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

	// Preload caches
	if err := cacheManager.Config.Preload(ctx); err != nil {
		slog.Warn("failed to preload config cache", "error", err)
	}
	if cacheManager.IsRedis() {
		slog.Info(logCacheManagerInit, "backend", "redis", "url", cfg.RedisURL)
	} else if cacheManager.Info().IsFallback {
		slog.Warn(logCacheManagerInit, "backend", "memory", "note", "Redis unavailable, using fallback")
	} else {
		slog.Info(logCacheManagerInit, "backend", "memory")
	}

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
	if err := moduleRegistry.Register(hcaptcha.New()); err != nil {
		return fmt.Errorf("registering hcaptcha module: %w", err)
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

	// Set active theme
	if themeManager.HasTheme(cfg.ActiveTheme) {
		if err := themeManager.SetActiveTheme(cfg.ActiveTheme); err != nil {
			slog.Warn("failed to set active theme", "theme", cfg.ActiveTheme, "error", err)
		}
	} else if themeManager.ThemeCount() > 0 {
		// Fall back to first available theme
		themes := themeManager.ListThemesWithActive()
		if len(themes) > 0 {
			if err := themeManager.SetActiveTheme(themes[0].Name); err != nil {
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
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Compress(5))                    // Gzip compression with level 5
	r.Use(middleware.Timeout(30 * time.Second)) // 30 second request timeout

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
	mediaHandler := handler.NewMediaHandler(db, renderer, sessionManager, uploadsDirPath)
	menusHandler := handler.NewMenusHandler(db, renderer, sessionManager)
	formsHandler := handler.NewFormsHandler(db, renderer, sessionManager)
	themesHandler := handler.NewThemesHandler(db, renderer, sessionManager, themeManager, cacheManager)
	widgetsHandler := handler.NewWidgetsHandler(db, renderer, sessionManager, themeManager)
	modulesHandler := handler.NewModulesHandler(db, renderer, sessionManager, moduleRegistry, hookRegistry)
	frontendHandler := handler.NewFrontendHandler(db, themeManager, cacheManager, logger, renderer.GetMenuService())
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
	importExportHandler := handler.NewImportExportHandler(db, renderer, sessionManager)
	healthHandler := handler.NewHealthHandler(db, uploadsDirPath)

	// Set webhook dispatcher on handlers that dispatch events
	pagesHandler.SetDispatcher(webhookDispatcher)
	mediaHandler.SetDispatcher(webhookDispatcher)
	usersHandler.SetDispatcher(webhookDispatcher)
	formsHandler.SetDispatcher(webhookDispatcher)

	// Health check routes (should be early, before session middleware for some endpoints)
	r.Get("/health", healthHandler.Health)
	r.Get("/health/live", healthHandler.Liveness)
	r.Get("/health/ready", healthHandler.Readiness)

	// Public frontend routes (with language detection and analytics tracking)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Language(db))
		// Add internal analytics tracking middleware (if module is enabled)
		if internalAnalyticsModule.IsEnabled() {
			r.Use(internalAnalyticsModule.GetTrackingMiddleware())
		}

		// Default language routes (no prefix)
		r.Get(routeRoot, frontendHandler.Home)
		r.Get("/sitemap.xml", frontendHandler.Sitemap)
		r.Get("/robots.txt", frontendHandler.Robots)
		r.Get(routeSuffixSearch, frontendHandler.Search)
		r.Get(routeBlog, frontendHandler.Blog)
		r.Get(routeCategorySlug, frontendHandler.Category)
		r.Get(routeTagSlug, frontendHandler.Tag)
		r.Get(routeParamSlug, frontendHandler.Page) // Must be last - catch-all for page slugs

		// Language-prefixed routes (e.g., /ru/, /ru/page-slug)
		r.Route("/{lang:[a-z]{2}}", func(r chi.Router) {
			r.Get(routeRoot, frontendHandler.Home)
			r.Get(routeSuffixSearch, frontendHandler.Search)
			r.Get(routeBlog, frontendHandler.Blog)
			r.Get(routeCategorySlug, frontendHandler.Category)
			r.Get(routeTagSlug, frontendHandler.Tag)
			r.Get(routeParamSlug, frontendHandler.Page)
		})
	})

	// Auth routes (public, with CSRF and rate limiting)
	// Defense-in-depth: publicRateLimiter (10 req/s) + loginProtection (0.5 req/s on POST + account lockout)
	r.Group(func(r chi.Router) {
		r.Use(publicRateLimiter.HTMLMiddleware())
		r.Use(csrfMiddleware)
		r.Get(routeLogin, authHandler.LoginForm)
		r.With(loginProtection.Middleware()).Post(routeLogin, authHandler.Login)
		r.Get(routeLogout, authHandler.Logout)
		r.Post(routeLogout, authHandler.Logout)
	})

	// Session test routes (development only)
	if cfg.IsDevelopment() {
		r.Get("/session/set", func(w http.ResponseWriter, r *http.Request) {
			value := r.URL.Query().Get("value")
			if value == "" {
				value = "test-value"
			}
			sessionManager.Put(r.Context(), "test_key", value)
			w.Header().Set(headerContentType, "text/plain; charset=utf-8")
			_, _ = fmt.Fprintf(w, "Session value set: %s\n", value)
		})

		r.Get("/session/get", func(w http.ResponseWriter, r *http.Request) {
			value := sessionManager.GetString(r.Context(), "test_key")
			w.Header().Set(headerContentType, "text/plain; charset=utf-8")
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
		r.Use(middleware.LoadSiteConfig(db, cacheManager))

		r.Get(routeRoot, adminHandler.Dashboard)
		r.Post("/language", adminHandler.SetLanguage)

		// User management routes
		r.Get(routeUsers, usersHandler.List)
		r.Get(routeUsers+routeSuffixNew, usersHandler.NewForm)
		r.Post(routeUsers, usersHandler.Create)
		r.Get(routeUsersID, usersHandler.EditForm)
		r.Put(routeUsersID, usersHandler.Update)
		r.Post(routeUsersID, usersHandler.Update) // HTML forms can't send PUT
		r.Delete(routeUsersID, usersHandler.Delete)

		// Language management routes
		r.Get(routeLanguages, languagesHandler.List)
		r.Get(routeLanguages+routeSuffixNew, languagesHandler.NewForm)
		r.Post(routeLanguages, languagesHandler.Create)
		r.Get(routeLanguagesID, languagesHandler.EditForm)
		r.Put(routeLanguagesID, languagesHandler.Update)
		r.Post(routeLanguagesID, languagesHandler.Update) // HTML forms can't send PUT
		r.Delete(routeLanguagesID, languagesHandler.Delete)
		r.Post(routeLanguagesID+"/default", languagesHandler.SetDefault)

		// Page management routes
		r.Get(routePages, pagesHandler.List)
		r.Get(routePages+routeSuffixNew, pagesHandler.NewForm)
		r.Post(routePages, pagesHandler.Create)
		r.Get(routePagesID, pagesHandler.EditForm)
		r.Put(routePagesID, pagesHandler.Update)
		r.Post(routePagesID, pagesHandler.Update) // HTML forms can't send PUT
		r.Delete(routePagesID, pagesHandler.Delete)
		r.Post(routePagesID+"/publish", pagesHandler.TogglePublish)
		r.Get(routePagesID+"/versions", pagesHandler.Versions)
		r.Post(routePagesID+"/versions/{versionId}/restore", pagesHandler.RestoreVersion)
		r.Post(routePagesID+routeSuffixTranslate, pagesHandler.Translate)

		// Configuration routes
		r.Get(routeConfig, configHandler.List)
		r.Put(routeConfig, configHandler.Update)
		r.Post(routeConfig, configHandler.Update) // HTML forms can't send PUT

		// Events log route
		r.Get("/events", eventsHandler.List)

		// Tag management routes
		r.Get(routeTags, taxonomyHandler.ListTags)
		r.Get(routeTags+routeSuffixNew, taxonomyHandler.NewTagForm)
		r.Post(routeTags, taxonomyHandler.CreateTag)
		r.Get(routeTags+routeSuffixSearch, taxonomyHandler.SearchTags)
		r.Get(routeTagsID, taxonomyHandler.EditTagForm)
		r.Put(routeTagsID, taxonomyHandler.UpdateTag)
		r.Post(routeTagsID, taxonomyHandler.UpdateTag) // HTML forms can't send PUT
		r.Delete(routeTagsID, taxonomyHandler.DeleteTag)
		r.Post(routeTagsID+routeSuffixTranslate, taxonomyHandler.TranslateTag)

		// Category management routes
		r.Get(routeCategories, taxonomyHandler.ListCategories)
		r.Get(routeCategories+routeSuffixNew, taxonomyHandler.NewCategoryForm)
		r.Post(routeCategories, taxonomyHandler.CreateCategory)
		r.Get(routeCategories+routeSuffixSearch, taxonomyHandler.SearchCategories)
		r.Get(routeCategoriesID, taxonomyHandler.EditCategoryForm)
		r.Put(routeCategoriesID, taxonomyHandler.UpdateCategory)
		r.Post(routeCategoriesID, taxonomyHandler.UpdateCategory) // HTML forms can't send PUT
		r.Delete(routeCategoriesID, taxonomyHandler.DeleteCategory)
		r.Post(routeCategoriesID+routeSuffixTranslate, taxonomyHandler.TranslateCategory)

		// Media library routes
		r.Get(routeMedia, mediaHandler.Library)
		r.Get(routeMedia+"/api", mediaHandler.API) // JSON API for media picker
		r.Get(routeMedia+routeSuffixUpload, mediaHandler.UploadForm)
		r.Post(routeMedia+routeSuffixUpload, mediaHandler.Upload)
		r.Get(routeMediaID, mediaHandler.EditForm)
		r.Put(routeMediaID, mediaHandler.Update)
		r.Post(routeMediaID, mediaHandler.Update) // HTML forms can't send PUT
		r.Delete(routeMediaID, mediaHandler.Delete)
		r.Post(routeMediaID+routeSuffixMove, mediaHandler.MoveMedia)

		// Media folders
		r.Post(routeMedia+routeSuffixFolders, mediaHandler.CreateFolder)
		r.Put(routeMediaFoldersID, mediaHandler.UpdateFolder)
		r.Post(routeMediaFoldersID, mediaHandler.UpdateFolder) // HTML forms can't send PUT
		r.Delete(routeMediaFoldersID, mediaHandler.DeleteFolder)

		// Menu management routes
		r.Get(routeMenus, menusHandler.List)
		r.Get(routeMenus+routeSuffixNew, menusHandler.NewForm)
		r.Post(routeMenus, menusHandler.Create)
		r.Get(routeMenusID, menusHandler.EditForm)
		r.Put(routeMenusID, menusHandler.Update)
		r.Post(routeMenusID, menusHandler.Update) // HTML forms can't send PUT
		r.Delete(routeMenusID, menusHandler.Delete)
		r.Post(routeMenusID+"/items", menusHandler.AddItem)
		r.Put(routeMenusID+routeItemsItemID, menusHandler.UpdateItem)
		r.Delete(routeMenusID+routeItemsItemID, menusHandler.DeleteItem)
		r.Post(routeMenusID+routeSuffixReorder, menusHandler.Reorder)

		// Form management routes
		r.Get(routeForms, formsHandler.List)
		r.Get(routeForms+routeSuffixNew, formsHandler.NewForm)
		r.Post(routeForms, formsHandler.Create)
		r.Get(routeFormsID, formsHandler.EditForm)
		r.Put(routeFormsID, formsHandler.Update)
		r.Post(routeFormsID, formsHandler.Update) // HTML forms can't send PUT
		r.Delete(routeFormsID, formsHandler.Delete)
		r.Post(routeFormsID+"/fields", formsHandler.AddField)
		r.Put(routeFormsID+routeFieldsFieldID, formsHandler.UpdateField)
		r.Delete(routeFormsID+routeFieldsFieldID, formsHandler.DeleteField)
		r.Post(routeFormsID+"/fields/reorder", formsHandler.ReorderFields)

		// Form submissions routes
		r.Get(routeFormsID+"/submissions", formsHandler.Submissions)
		r.Get(routeFormsID+routeSubmissionsSubID, formsHandler.ViewSubmission)
		r.Delete(routeFormsID+routeSubmissionsSubID, formsHandler.DeleteSubmission)
		r.Post(routeFormsID+"/submissions/export", formsHandler.ExportSubmissions)

		// Theme management routes
		r.Get("/themes", themesHandler.List)
		r.Post("/themes/activate", themesHandler.Activate)
		r.Get(routeThemeSettings, themesHandler.Settings)
		r.Put(routeThemeSettings, themesHandler.SaveSettings)
		r.Post(routeThemeSettings, themesHandler.SaveSettings) // HTML forms can't send PUT

		// Widget management routes
		r.Get(routeWidgets, widgetsHandler.List)
		r.Post(routeWidgets, widgetsHandler.Create)
		r.Get(routeWidgetsID, widgetsHandler.GetWidget)
		r.Put(routeWidgetsID, widgetsHandler.Update)
		r.Delete(routeWidgetsID, widgetsHandler.Delete)
		r.Post(routeWidgetsID+routeSuffixMove, widgetsHandler.MoveWidget)
		r.Post(routeWidgets+routeSuffixReorder, widgetsHandler.Reorder)

		// Module management routes
		r.Get("/modules", modulesHandler.List)
		r.Post("/modules/{name}/toggle", modulesHandler.ToggleActive)
		r.Post("/modules/{name}/toggle-sidebar", modulesHandler.ToggleSidebar)

		// API key management routes
		r.Get(routeAPIKeys, apiKeysHandler.List)
		r.Get(routeAPIKeys+routeSuffixNew, apiKeysHandler.NewForm)
		r.Post(routeAPIKeys, apiKeysHandler.Create)
		r.Get(routeAPIKeysID, apiKeysHandler.EditForm)
		r.Put(routeAPIKeysID, apiKeysHandler.Update)
		r.Post(routeAPIKeysID, apiKeysHandler.Update) // HTML forms can't send PUT
		r.Delete(routeAPIKeysID, apiKeysHandler.Delete)

		// Webhook management routes
		r.Get(routeWebhooks, webhooksHandler.List)
		r.Get(routeWebhooks+routeSuffixNew, webhooksHandler.NewForm)
		r.Post(routeWebhooks, webhooksHandler.Create)
		r.Get(routeWebhooksID, webhooksHandler.EditForm)
		r.Put(routeWebhooksID, webhooksHandler.Update)
		r.Post(routeWebhooksID, webhooksHandler.Update) // HTML forms can't send PUT
		r.Delete(routeWebhooksID, webhooksHandler.Delete)
		r.Get(routeWebhooksID+"/deliveries", webhooksHandler.Deliveries)
		r.Post(routeWebhooksID+"/test", webhooksHandler.Test)
		r.Post(routeWebhooksID+"/deliveries/{did}/retry", webhooksHandler.RetryDelivery)

		// Cache management routes
		r.Get("/cache", cacheHandler.Stats)
		r.Post("/cache/clear", cacheHandler.Clear)
		r.Post("/cache/clear/config", cacheHandler.ClearConfig)
		r.Post("/cache/clear/sitemap", cacheHandler.ClearSitemap)

		// Import/Export routes
		r.Get(routeExport, importExportHandler.ExportForm)
		r.Post(routeExport, importExportHandler.Export)
		r.Get(routeImport, importExportHandler.ImportForm)
		r.Post(routeImport+"/validate", importExportHandler.ImportValidate)
		r.Post(routeImport, importExportHandler.Import)

		// Register module admin routes
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
			r.Get(routePages, apiHandler.ListPages)
			r.Get(routePagesID, apiHandler.GetPage)
			r.Get(routePages+"/slugrouteParamSlug", apiHandler.GetPageBySlug)
		})

		// Media - public read endpoints (optional auth for enhanced access)
		r.Group(func(r chi.Router) {
			r.Use(middleware.OptionalAPIKeyAuth(db))
			r.Get(routeMedia, apiHandler.ListMedia)
			r.Get(routeMediaID, apiHandler.GetMedia)
		})

		// Tags - public read endpoints
		r.Get(routeTags, apiHandler.ListTags)
		r.Get(routeTagsID, apiHandler.GetTag)

		// Categories - public read endpoints
		r.Get(routeCategories, apiHandler.ListCategories)
		r.Get(routeCategoriesID, apiHandler.GetCategory)

		// Protected endpoints (API key required)
		r.Group(func(r chi.Router) {
			r.Use(middleware.APIKeyAuth(db))
			r.Use(middleware.APIRateLimit(10, 20)) // 10 requests per second per API key

			// Auth info endpoint
			r.Get("/auth", apiHandler.AuthInfo)

			// Pages - write endpoints (requires pages:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("pages:write"))
				r.Post(routePages, apiHandler.CreatePage)
				r.Put(routePagesID, apiHandler.UpdatePage)
				r.Delete(routePagesID, apiHandler.DeletePage)
			})

			// Media - write endpoints (requires media:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("media:write"))
				r.Post(routeMedia, apiHandler.UploadMedia)
				r.Put(routeMediaID, apiHandler.UpdateMedia)
				r.Delete(routeMediaID, apiHandler.DeleteMedia)
			})

			// Taxonomy - write endpoints (requires taxonomy:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("taxonomy:write"))
				r.Post(routeTags, apiHandler.CreateTag)
				r.Put(routeTagsID, apiHandler.UpdateTag)
				r.Delete(routeTagsID, apiHandler.DeleteTag)
				r.Post(routeCategories, apiHandler.CreateCategory)
				r.Put(routeCategoriesID, apiHandler.UpdateCategory)
				r.Delete(routeCategoriesID, apiHandler.DeleteCategory)
			})
		})
	})
	slog.Info("REST API v1 mounted at /api/v1")

	// Public form routes (no authentication required, with CSRF protection)
	r.Group(func(r chi.Router) {
		r.Use(csrfMiddleware)
		r.Get(routeFormsSlug, formsHandler.Show)
		r.Post(routeFormsSlug, formsHandler.Submit)
	})

	// Favicon route - serve from embedded static files
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := web.Static.ReadFile("static/dist/favicon.svg")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set(headerContentType, "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		_, _ = w.Write(data)
	})

	// Static file serving
	staticFS, err := fs.Sub(web.Static, "static/dist")
	if err != nil {
		return fmt.Errorf("getting static fs: %w", err)
	}
	// Static assets: cache for 1 year (31536000 seconds)
	staticHandler := middleware.StaticCache(31536000)(http.StripPrefix("/static/dist/", http.FileServer(http.FS(staticFS))))
	r.Handle("/static/dist/*", staticHandler)

	// Serve uploaded media files from ./uploads directory
	// Uploads: cache for 1 week (604800 seconds)
	uploadsDir := http.Dir(uploadsDirPath)
	uploadsHandler := middleware.StaticCache(604800)(http.StripPrefix("/uploads/", http.FileServer(uploadsDir)))
	r.Handle("/uploads/*", uploadsHandler)

	// Serve theme static files with caching (1 month = 2592000 seconds)
	r.Get("/themes/{themeName}/static/*", func(w http.ResponseWriter, r *http.Request) {
		themeName := chi.URLParam(r, "themeName")
		thm, err := themeManager.GetTheme(themeName)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Add cache headers for theme static files
		w.Header().Set("Cache-Control", "public, max-age=2592000")
		// Strip the prefix to get the file path
		path := r.URL.Path
		prefix := fmt.Sprintf("/themes/%s/static/", themeName)
		filePath := filepath.Join(thm.StaticPath, path[len(prefix):])
		http.ServeFile(w, r, filePath)
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
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB max header size
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
