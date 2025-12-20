package main

import (
	"context"
	"database/sql"
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

func main() {
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
	cacheConfig := cache.CacheConfig{
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
		slog.Info("cache manager initialized", "backend", "redis", "url", cfg.RedisURL)
	} else if cacheManager.Info().IsFallback {
		slog.Warn("cache manager initialized", "backend", "memory", "note", "Redis unavailable, using fallback")
	} else {
		slog.Info("cache manager initialized", "backend", "memory")
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
	moduleCtx := &module.ModuleContext{
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
	mediaHandler := handler.NewMediaHandler(db, renderer, sessionManager, "./uploads")
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
	healthHandler := handler.NewHealthHandler(db, "./uploads")

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
		r.Get("/", frontendHandler.Home)
		r.Get("/sitemap.xml", frontendHandler.Sitemap)
		r.Get("/robots.txt", frontendHandler.Robots)
		r.Get("/search", frontendHandler.Search)
		r.Get("/blog", frontendHandler.Blog)
		r.Get("/category/{slug}", frontendHandler.Category)
		r.Get("/tag/{slug}", frontendHandler.Tag)
		r.Get("/{slug}", frontendHandler.Page) // Must be last - catch-all for page slugs

		// Language-prefixed routes (e.g., /ru/, /ru/page-slug)
		r.Route("/{lang:[a-z]{2}}", func(r chi.Router) {
			r.Get("/", frontendHandler.Home)
			r.Get("/search", frontendHandler.Search)
			r.Get("/blog", frontendHandler.Blog)
			r.Get("/category/{slug}", frontendHandler.Category)
			r.Get("/tag/{slug}", frontendHandler.Tag)
			r.Get("/{slug}", frontendHandler.Page)
		})
	})

	// Auth routes (public, with CSRF and rate limiting)
	// Defense-in-depth: publicRateLimiter (10 req/s) + loginProtection (0.5 req/s on POST + account lockout)
	r.Group(func(r chi.Router) {
		r.Use(publicRateLimiter.HTMLMiddleware())
		r.Use(csrfMiddleware)
		r.Get("/login", authHandler.LoginForm)
		r.With(loginProtection.Middleware()).Post("/login", authHandler.Login)
		r.Get("/logout", authHandler.Logout)
		r.Post("/logout", authHandler.Logout)
	})

	// Session test routes (development only)
	if cfg.IsDevelopment() {
		r.Get("/session/set", func(w http.ResponseWriter, r *http.Request) {
			value := r.URL.Query().Get("value")
			if value == "" {
				value = "test-value"
			}
			sessionManager.Put(r.Context(), "test_key", value)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprintf(w, "Session value set: %s\n", value)
		})

		r.Get("/session/get", func(w http.ResponseWriter, r *http.Request) {
			value := sessionManager.GetString(r.Context(), "test_key")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
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

		r.Get("/", adminHandler.Dashboard)
		r.Post("/language", adminHandler.SetLanguage)

		// User management routes
		r.Get("/users", usersHandler.List)
		r.Get("/users/new", usersHandler.NewForm)
		r.Post("/users", usersHandler.Create)
		r.Get("/users/{id}", usersHandler.EditForm)
		r.Put("/users/{id}", usersHandler.Update)
		r.Post("/users/{id}", usersHandler.Update) // HTML forms can't send PUT
		r.Delete("/users/{id}", usersHandler.Delete)

		// Language management routes
		r.Get("/languages", languagesHandler.List)
		r.Get("/languages/new", languagesHandler.NewForm)
		r.Post("/languages", languagesHandler.Create)
		r.Get("/languages/{id}", languagesHandler.EditForm)
		r.Put("/languages/{id}", languagesHandler.Update)
		r.Post("/languages/{id}", languagesHandler.Update) // HTML forms can't send PUT
		r.Delete("/languages/{id}", languagesHandler.Delete)
		r.Post("/languages/{id}/default", languagesHandler.SetDefault)

		// Page management routes
		r.Get("/pages", pagesHandler.List)
		r.Get("/pages/new", pagesHandler.NewForm)
		r.Post("/pages", pagesHandler.Create)
		r.Get("/pages/{id}", pagesHandler.EditForm)
		r.Put("/pages/{id}", pagesHandler.Update)
		r.Post("/pages/{id}", pagesHandler.Update) // HTML forms can't send PUT
		r.Delete("/pages/{id}", pagesHandler.Delete)
		r.Post("/pages/{id}/publish", pagesHandler.TogglePublish)
		r.Get("/pages/{id}/versions", pagesHandler.Versions)
		r.Post("/pages/{id}/versions/{versionId}/restore", pagesHandler.RestoreVersion)
		r.Post("/pages/{id}/translate/{langCode}", pagesHandler.Translate)

		// Configuration routes
		r.Get("/config", configHandler.List)
		r.Put("/config", configHandler.Update)
		r.Post("/config", configHandler.Update) // HTML forms can't send PUT

		// Events log route
		r.Get("/events", eventsHandler.List)

		// Tag management routes
		r.Get("/tags", taxonomyHandler.ListTags)
		r.Get("/tags/new", taxonomyHandler.NewTagForm)
		r.Post("/tags", taxonomyHandler.CreateTag)
		r.Get("/tags/search", taxonomyHandler.SearchTags)
		r.Get("/tags/{id}", taxonomyHandler.EditTagForm)
		r.Put("/tags/{id}", taxonomyHandler.UpdateTag)
		r.Post("/tags/{id}", taxonomyHandler.UpdateTag) // HTML forms can't send PUT
		r.Delete("/tags/{id}", taxonomyHandler.DeleteTag)
		r.Post("/tags/{id}/translate/{langCode}", taxonomyHandler.TranslateTag)

		// Category management routes
		r.Get("/categories", taxonomyHandler.ListCategories)
		r.Get("/categories/new", taxonomyHandler.NewCategoryForm)
		r.Post("/categories", taxonomyHandler.CreateCategory)
		r.Get("/categories/search", taxonomyHandler.SearchCategories)
		r.Get("/categories/{id}", taxonomyHandler.EditCategoryForm)
		r.Put("/categories/{id}", taxonomyHandler.UpdateCategory)
		r.Post("/categories/{id}", taxonomyHandler.UpdateCategory) // HTML forms can't send PUT
		r.Delete("/categories/{id}", taxonomyHandler.DeleteCategory)
		r.Post("/categories/{id}/translate/{langCode}", taxonomyHandler.TranslateCategory)

		// Media library routes
		r.Get("/media", mediaHandler.Library)
		r.Get("/media/api", mediaHandler.API) // JSON API for media picker
		r.Get("/media/upload", mediaHandler.UploadForm)
		r.Post("/media/upload", mediaHandler.Upload)
		r.Get("/media/{id}", mediaHandler.EditForm)
		r.Put("/media/{id}", mediaHandler.Update)
		r.Post("/media/{id}", mediaHandler.Update) // HTML forms can't send PUT
		r.Delete("/media/{id}", mediaHandler.Delete)
		r.Post("/media/{id}/move", mediaHandler.MoveMedia)

		// Media folders
		r.Post("/media/folders", mediaHandler.CreateFolder)
		r.Put("/media/folders/{id}", mediaHandler.UpdateFolder)
		r.Post("/media/folders/{id}", mediaHandler.UpdateFolder) // HTML forms can't send PUT
		r.Delete("/media/folders/{id}", mediaHandler.DeleteFolder)

		// Menu management routes
		r.Get("/menus", menusHandler.List)
		r.Get("/menus/new", menusHandler.NewForm)
		r.Post("/menus", menusHandler.Create)
		r.Get("/menus/{id}", menusHandler.EditForm)
		r.Put("/menus/{id}", menusHandler.Update)
		r.Post("/menus/{id}", menusHandler.Update) // HTML forms can't send PUT
		r.Delete("/menus/{id}", menusHandler.Delete)
		r.Post("/menus/{id}/items", menusHandler.AddItem)
		r.Put("/menus/{id}/items/{itemId}", menusHandler.UpdateItem)
		r.Delete("/menus/{id}/items/{itemId}", menusHandler.DeleteItem)
		r.Post("/menus/{id}/reorder", menusHandler.Reorder)

		// Form management routes
		r.Get("/forms", formsHandler.List)
		r.Get("/forms/new", formsHandler.NewForm)
		r.Post("/forms", formsHandler.Create)
		r.Get("/forms/{id}", formsHandler.EditForm)
		r.Put("/forms/{id}", formsHandler.Update)
		r.Post("/forms/{id}", formsHandler.Update) // HTML forms can't send PUT
		r.Delete("/forms/{id}", formsHandler.Delete)
		r.Post("/forms/{id}/fields", formsHandler.AddField)
		r.Put("/forms/{id}/fields/{fieldId}", formsHandler.UpdateField)
		r.Delete("/forms/{id}/fields/{fieldId}", formsHandler.DeleteField)
		r.Post("/forms/{id}/fields/reorder", formsHandler.ReorderFields)

		// Form submissions routes
		r.Get("/forms/{id}/submissions", formsHandler.Submissions)
		r.Get("/forms/{id}/submissions/{subId}", formsHandler.ViewSubmission)
		r.Delete("/forms/{id}/submissions/{subId}", formsHandler.DeleteSubmission)
		r.Post("/forms/{id}/submissions/export", formsHandler.ExportSubmissions)

		// Theme management routes
		r.Get("/themes", themesHandler.List)
		r.Post("/themes/activate", themesHandler.Activate)
		r.Get("/themes/{name}/settings", themesHandler.Settings)
		r.Put("/themes/{name}/settings", themesHandler.SaveSettings)
		r.Post("/themes/{name}/settings", themesHandler.SaveSettings) // HTML forms can't send PUT

		// Widget management routes
		r.Get("/widgets", widgetsHandler.List)
		r.Post("/widgets", widgetsHandler.Create)
		r.Get("/widgets/{id}", widgetsHandler.GetWidget)
		r.Put("/widgets/{id}", widgetsHandler.Update)
		r.Delete("/widgets/{id}", widgetsHandler.Delete)
		r.Post("/widgets/{id}/move", widgetsHandler.MoveWidget)
		r.Post("/widgets/reorder", widgetsHandler.Reorder)

		// Module management routes
		r.Get("/modules", modulesHandler.List)
		r.Post("/modules/{name}/toggle", modulesHandler.ToggleActive)

		// API key management routes
		r.Get("/api-keys", apiKeysHandler.List)
		r.Get("/api-keys/new", apiKeysHandler.NewForm)
		r.Post("/api-keys", apiKeysHandler.Create)
		r.Get("/api-keys/{id}", apiKeysHandler.EditForm)
		r.Put("/api-keys/{id}", apiKeysHandler.Update)
		r.Post("/api-keys/{id}", apiKeysHandler.Update) // HTML forms can't send PUT
		r.Delete("/api-keys/{id}", apiKeysHandler.Delete)

		// Webhook management routes
		r.Get("/webhooks", webhooksHandler.List)
		r.Get("/webhooks/new", webhooksHandler.NewForm)
		r.Post("/webhooks", webhooksHandler.Create)
		r.Get("/webhooks/{id}", webhooksHandler.EditForm)
		r.Put("/webhooks/{id}", webhooksHandler.Update)
		r.Post("/webhooks/{id}", webhooksHandler.Update) // HTML forms can't send PUT
		r.Delete("/webhooks/{id}", webhooksHandler.Delete)
		r.Get("/webhooks/{id}/deliveries", webhooksHandler.Deliveries)
		r.Post("/webhooks/{id}/test", webhooksHandler.Test)
		r.Post("/webhooks/{id}/deliveries/{did}/retry", webhooksHandler.RetryDelivery)

		// Cache management routes
		r.Get("/cache", cacheHandler.Stats)
		r.Post("/cache/clear", cacheHandler.Clear)
		r.Post("/cache/clear/config", cacheHandler.ClearConfig)
		r.Post("/cache/clear/sitemap", cacheHandler.ClearSitemap)

		// Import/Export routes
		r.Get("/export", importExportHandler.ExportForm)
		r.Post("/export", importExportHandler.Export)
		r.Get("/import", importExportHandler.ImportForm)
		r.Post("/import/validate", importExportHandler.ImportValidate)
		r.Post("/import", importExportHandler.Import)

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
			r.Get("/pages", apiHandler.ListPages)
			r.Get("/pages/{id}", apiHandler.GetPage)
			r.Get("/pages/slug/{slug}", apiHandler.GetPageBySlug)
		})

		// Media - public read endpoints (optional auth for enhanced access)
		r.Group(func(r chi.Router) {
			r.Use(middleware.OptionalAPIKeyAuth(db))
			r.Get("/media", apiHandler.ListMedia)
			r.Get("/media/{id}", apiHandler.GetMedia)
		})

		// Tags - public read endpoints
		r.Get("/tags", apiHandler.ListTags)
		r.Get("/tags/{id}", apiHandler.GetTag)

		// Categories - public read endpoints
		r.Get("/categories", apiHandler.ListCategories)
		r.Get("/categories/{id}", apiHandler.GetCategory)

		// Protected endpoints (API key required)
		r.Group(func(r chi.Router) {
			r.Use(middleware.APIKeyAuth(db))
			r.Use(middleware.APIRateLimit(10, 20)) // 10 requests per second per API key

			// Auth info endpoint
			r.Get("/auth", apiHandler.AuthInfo)

			// Pages - write endpoints (requires pages:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("pages:write"))
				r.Post("/pages", apiHandler.CreatePage)
				r.Put("/pages/{id}", apiHandler.UpdatePage)
				r.Delete("/pages/{id}", apiHandler.DeletePage)
			})

			// Media - write endpoints (requires media:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("media:write"))
				r.Post("/media", apiHandler.UploadMedia)
				r.Put("/media/{id}", apiHandler.UpdateMedia)
				r.Delete("/media/{id}", apiHandler.DeleteMedia)
			})

			// Taxonomy - write endpoints (requires taxonomy:write permission)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequirePermission("taxonomy:write"))
				r.Post("/tags", apiHandler.CreateTag)
				r.Put("/tags/{id}", apiHandler.UpdateTag)
				r.Delete("/tags/{id}", apiHandler.DeleteTag)
				r.Post("/categories", apiHandler.CreateCategory)
				r.Put("/categories/{id}", apiHandler.UpdateCategory)
				r.Delete("/categories/{id}", apiHandler.DeleteCategory)
			})
		})
	})
	slog.Info("REST API v1 mounted at /api/v1")

	// Public form routes (no authentication required, with CSRF protection)
	r.Group(func(r chi.Router) {
		r.Use(csrfMiddleware)
		r.Get("/forms/{slug}", formsHandler.Show)
		r.Post("/forms/{slug}", formsHandler.Submit)
	})

	// Favicon route - serve from embedded static files
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := web.Static.ReadFile("static/dist/favicon.svg")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
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
	uploadsDir := http.Dir("./uploads")
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
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
