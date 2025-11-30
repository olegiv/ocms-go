package main

import (
	"context"
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

	"ocms-go/internal/config"
	"ocms-go/internal/handler"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/session"
	"ocms-go/internal/store"
	"ocms-go/internal/theme"
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
	defer db.Close()

	// Run migrations
	slog.Info("running database migrations")
	if err := store.Migrate(db); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	slog.Info("database ready")

	// Seed default data
	ctx := context.Background()
	if err := store.Seed(ctx, db); err != nil {
		return fmt.Errorf("seeding database: %w", err)
	}

	// Initialize session manager
	sessionManager := session.New(db, cfg.IsDevelopment())
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

	// Middleware
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(sessionManager.LoadAndSave)

	// Initialize handlers
	authHandler := handler.NewAuthHandler(db, renderer, sessionManager)
	adminHandler := handler.NewAdminHandler(db, renderer, sessionManager)
	usersHandler := handler.NewUsersHandler(db, renderer, sessionManager)
	pagesHandler := handler.NewPagesHandler(db, renderer, sessionManager)
	configHandler := handler.NewConfigHandler(db, renderer, sessionManager)
	eventsHandler := handler.NewEventsHandler(db, renderer, sessionManager)
	taxonomyHandler := handler.NewTaxonomyHandler(db, renderer, sessionManager)
	mediaHandler := handler.NewMediaHandler(db, renderer, sessionManager, "./uploads")
	menusHandler := handler.NewMenusHandler(db, renderer, sessionManager)
	formsHandler := handler.NewFormsHandler(db, renderer, sessionManager)
	themesHandler := handler.NewThemesHandler(db, renderer, sessionManager, themeManager)
	widgetsHandler := handler.NewWidgetsHandler(db, renderer, sessionManager, themeManager)
	frontendHandler := handler.NewFrontendHandler(db, themeManager, logger)

	// Public frontend routes
	r.Get("/", frontendHandler.Home)
	r.Get("/search", frontendHandler.Search)
	r.Get("/category/{slug}", frontendHandler.Category)
	r.Get("/tag/{slug}", frontendHandler.Tag)

	// Auth routes (public)
	r.Get("/login", authHandler.LoginForm)
	r.Post("/login", authHandler.Login)
	r.Get("/logout", authHandler.Logout)
	r.Post("/logout", authHandler.Logout)

	// Session test routes (development only)
	if cfg.IsDevelopment() {
		r.Get("/session/set", func(w http.ResponseWriter, r *http.Request) {
			value := r.URL.Query().Get("value")
			if value == "" {
				value = "test-value"
			}
			sessionManager.Put(r.Context(), "test_key", value)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "Session value set: %s\n", value)
		})

		r.Get("/session/get", func(w http.ResponseWriter, r *http.Request) {
			value := sessionManager.GetString(r.Context(), "test_key")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if value == "" {
				fmt.Fprintln(w, "No session value found")
			} else {
				fmt.Fprintf(w, "Session value: %s\n", value)
			}
		})
	}

	// Admin routes (protected)
	r.Route("/admin", func(r chi.Router) {
		r.Use(middleware.Auth(sessionManager))
		r.Use(middleware.LoadUser(sessionManager, db))
		r.Use(middleware.LoadSiteConfig(db))

		r.Get("/", adminHandler.Dashboard)

		// User management routes
		r.Get("/users", usersHandler.List)
		r.Get("/users/new", usersHandler.NewForm)
		r.Post("/users", usersHandler.Create)
		r.Get("/users/{id}", usersHandler.EditForm)
		r.Put("/users/{id}", usersHandler.Update)
		r.Post("/users/{id}", usersHandler.Update) // HTML forms can't send PUT
		r.Delete("/users/{id}", usersHandler.Delete)

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

		// Category management routes
		r.Get("/categories", taxonomyHandler.ListCategories)
		r.Get("/categories/new", taxonomyHandler.NewCategoryForm)
		r.Post("/categories", taxonomyHandler.CreateCategory)
		r.Get("/categories/search", taxonomyHandler.SearchCategories)
		r.Get("/categories/{id}", taxonomyHandler.EditCategoryForm)
		r.Put("/categories/{id}", taxonomyHandler.UpdateCategory)
		r.Post("/categories/{id}", taxonomyHandler.UpdateCategory) // HTML forms can't send PUT
		r.Delete("/categories/{id}", taxonomyHandler.DeleteCategory)

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
	})

	// Public form routes (no authentication required, but session needed for CSRF)
	r.Get("/forms/{slug}", formsHandler.Show)
	r.Post("/forms/{slug}", formsHandler.Submit)

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

	// Serve theme static files
	r.Get("/themes/{themeName}/static/*", func(w http.ResponseWriter, r *http.Request) {
		themeName := chi.URLParam(r, "themeName")
		thm, err := themeManager.GetTheme(themeName)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Strip the prefix to get the file path
		path := r.URL.Path
		prefix := fmt.Sprintf("/themes/%s/static/", themeName)
		filePath := filepath.Join(thm.StaticPath, path[len(prefix):])
		http.ServeFile(w, r, filePath)
	})

	// Page by slug route (catches all unmatched single-level paths)
	// This should be registered last to catch pages by slug
	r.Get("/{slug}", frontendHandler.Page)

	// 404 Not Found handler - use frontend theme's 404
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		frontendHandler.NotFound(w, req)
	})

	// Create server
	srv := &http.Server{
		Addr:         cfg.ServerAddr(),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
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
