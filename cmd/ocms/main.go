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
		IsDev:          cfg.IsDevelopment(),
	})
	if err != nil {
		return fmt.Errorf("initializing renderer: %w", err)
	}
	slog.Info("template renderer initialized")

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

	// Routes
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "oCMS is running")
	})

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

		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			user := middleware.GetUser(req)
			if err := renderer.Render(w, req, "admin/dashboard", render.TemplateData{
				Title: "Dashboard",
				Data:  user,
			}); err != nil {
				slog.Error("render error", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		})
	})

	// Static file serving
	staticFS, err := fs.Sub(web.Static, "static/dist")
	if err != nil {
		return fmt.Errorf("getting static fs: %w", err)
	}
	r.Handle("/static/dist/*", http.StripPrefix("/static/dist/", http.FileServer(http.FS(staticFS))))

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
