// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcpserver exposes site content to AI agents over the Model Context
// Protocol (MCP).
//
// It serves a stateless Streamable HTTP endpoint at EndpointPath built on the
// official Go SDK (github.com/modelcontextprotocol/go-sdk). The tools are thin
// read-only adapters over the REST v2 domain services, so both transports
// share validation, visibility rules and caching; tool schemas are generated
// from the same huma struct tags REST v2 validates with. Access requires an
// API key holding the mcp:access permission.
package mcpserver

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

//go:embed locales
var localesFS embed.FS

const (
	// ModuleName is the registry name of the MCP module.
	ModuleName    = "mcp"
	moduleVersion = "1.0.0"

	// serverName and serverTitle are the serverInfo the endpoint reports at
	// initialize. The server card reports the same values (ServerCardEndpoint),
	// so a client comparing the two sees one server.
	serverName  = "ocms"
	serverTitle = "oCMS"

	// EndpointPath is the public MCP endpoint. It sits under the reserved
	// "api" prefix (util.IsReservedLanguageCode), so it can never shadow a
	// page: page slugs are single path segments and are not reserved, which a
	// bare /mcp would have to rely on.
	EndpointPath = "/api/mcp"

	adminPath = "/admin/mcp"
)

// Request limits for the MCP endpoint. Every call is authenticated, but the
// limits still bound what one key, or one address probing for keys, can cost.
//
// The read-only tools take a handful of scalar arguments, so 256 KiB leaves
// generous room for JSON-RPC framing and the per-request _meta of protocol
// 2026-07-28 while keeping the body the SDK buffers in memory small. Agents
// call tools sequentially and rarely exceed a few calls per second; the
// per-key limit allows bursts of 30 and refills at 10 per second, and the
// per-IP limit sits in front of API key verification (Argon2) to blunt key
// guessing. The in-flight cap stops a burst from queueing unbounded work
// behind SQLite.
const (
	maxRequestBodyBytes = 256 << 10
	maxInFlightRequests = 32
	ipRateLimitRPS      = 20.0
	ipRateLimitBurst    = 40
	keyRateLimitRPS     = 10.0
	keyRateLimitBurst   = 30
)

// services are the domain services the tools adapt: the same types REST v2
// serves, so both transports apply identical rules.
type services struct {
	queries  *store.Queries
	pages    *pages.Service
	media    *media.Service
	taxonomy *taxonomy.Service
	search   *service.SearchService
	cache    *cache.Manager
}

// endpointHandler boxes the HTTP chain so it can live in an atomic.Pointer.
type endpointHandler struct {
	http.Handler
}

// serverState pairs the active settings with the server built from them, so
// a request never sees the settings of one save with the server of another.
type serverState struct {
	settings Settings
	server   *mcp.Server
}

// Module implements module.Module for the MCP server.
type Module struct {
	module.BaseModule
	ctx    *module.Context
	logger *slog.Logger
	svc    *services

	state atomic.Pointer[serverState]
	// settingsMu serializes settings changes (build, save, swap), so two
	// concurrent saves cannot leave the database and the running server
	// with different settings.
	settingsMu sync.Mutex
	// handler is the full HTTP chain, built in Init. Routes are registered for
	// inactive modules too, while Init runs only on activation, so the route
	// dispatches through this pointer instead of capturing middleware at
	// registration time.
	handler atomic.Pointer[endpointHandler]
	// invalidSiteURL is the last unusable site URL logged, so a bad value is
	// reported once rather than on every tool call.
	invalidSiteURL atomic.Pointer[string]

	schemaCache *mcp.SchemaCache
}

// New creates the MCP module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			ModuleName,
			moduleVersion,
			"MCP Server",
		),
		schemaCache: mcp.NewSchemaCache(),
	}
}

// ActiveByDefault keeps the module off until an administrator enables it:
// it opens a new remote surface, which an upgrade must not do unasked.
func (m *Module) ActiveByDefault() bool { return false }

// Init builds the services, server and HTTP chain from the module context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx
	m.logger = ctx.Logger
	if m.logger == nil {
		m.logger = slog.Default()
	}
	m.logger = m.logger.With("module", ModuleName)
	m.svc = newServices(ctx)

	settings, err := loadSettings(context.Background(), ctx.DB)
	if err != nil {
		// The defaults are the safe choice (drafts hidden, no administrator
		// instructions); the admin page reports the failure and refuses to
		// save over settings it could not read.
		m.logger.Error("failed to load MCP settings; serving defaults (drafts hidden, no instructions)", "error", err)
		settings = Settings{}
	}
	srv, err := m.buildServer(settings)
	if err != nil {
		return fmt.Errorf("building MCP server: %w", err)
	}
	m.state.Store(&serverState{settings: settings, server: srv})
	m.handler.Store(&endpointHandler{Handler: m.buildHTTPHandler(ctx.DB)})

	m.logger.Info("MCP module initialized",
		"endpoint", EndpointPath,
		"tools", len(toolCatalog()),
		"read_only", true,
		"allow_drafts", settings.AllowDrafts,
		"protocol_versions", mcp.SupportedProtocolVersions(),
		"max_request_bytes", maxRequestBodyBytes,
		"max_in_flight", maxInFlightRequests,
		"ip_rate_limit_rps", ipRateLimitRPS,
		"key_rate_limit_rps", keyRateLimitRPS,
	)
	return nil
}

// newServices constructs the REST v2 domain services from the module context.
// The page policy only gates writes, but is wired from config so the services
// behave exactly as REST's do.
func newServices(ctx *module.Context) *services {
	queries := ctx.Store
	if queries == nil {
		queries = store.New(ctx.DB)
	}
	var (
		policy     pages.Policy
		uploadsDir string
	)
	if ctx.Config != nil {
		policy = pages.Policy{
			BlockSuspiciousMarkup: ctx.Config.BlockSuspiciousPageHTML,
			SanitizeHTML:          ctx.Config.SanitizePageHTML,
		}
		uploadsDir = ctx.Config.UploadsDir
	}
	return &services{
		queries:  queries,
		pages:    pages.NewService(ctx.DB, queries, ctx.Cache, ctx.Events, policy),
		media:    media.NewService(ctx.DB, queries, ctx.Events, uploadsDir),
		taxonomy: taxonomy.NewService(ctx.DB, queries, ctx.Events),
		search:   service.NewSearchService(ctx.DB),
		cache:    ctx.Cache,
	}
}

// Shutdown performs cleanup when the module is shutting down. The endpoint
// is stateless, so there are no sessions to drain.
func (m *Module) Shutdown() error {
	if m.logger != nil {
		m.logger.Info("MCP module shutting down")
	}
	return nil
}

// RegisterRoutes registers the MCP endpoint. Every method is routed to the
// chain, which answers anything but POST with 405 before touching API keys.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Handle(EndpointPath, http.HandlerFunc(m.serveEndpoint))
}

// serveEndpoint dispatches to the chain built by Init.
//
// It recovers panics itself. The global middleware.Timeout runs handlers on a
// goroutine of its own, out of reach of chi's Recoverer, so without this a
// panic anywhere in the chain or in the SDK's request handling would end the
// process.
func (m *Module) serveEndpoint(w http.ResponseWriter, r *http.Request) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
			// A deliberate abort; re-panicking here would crash the process.
			return
		}
		m.logger.Error("MCP endpoint panicked",
			"panic", fmt.Sprint(p),
			"stack", string(debug.Stack()),
			"ip", middleware.GetClientIP(r))
		middleware.WriteAPIError(w, http.StatusInternalServerError, codeInternal, msgInternal, nil)
	}()
	h := m.handler.Load()
	if h == nil {
		// Registered but never initialized; the registry normally 404s first.
		middleware.WriteAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "MCP server is not initialized", nil)
		return
	}
	h.ServeHTTP(w, r)
}

// RegisterAdminRoutes registers the admin settings page (admins only).
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdmin())
		r.Get("/mcp", m.handleDashboard)
		r.Post("/mcp", m.handleSaveSettings)
	})
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string { return adminPath }

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string { return "MCP Server" }

// TranslationsFS returns the embedded filesystem containing module translations.
func (m *Module) TranslationsFS() embed.FS { return localesFS }

// ServerCardEndpoint describes the endpoint for the MCP server card
// (/.well-known/mcp/server-card.json). Callers advertise it only while the
// module is active.
func ServerCardEndpoint() *seo.MCPEndpoint {
	return &seo.MCPEndpoint{
		Path:             EndpointPath,
		Name:             serverName,
		Title:            serverTitle,
		Version:          moduleVersion,
		ProtocolVersions: mcp.SupportedProtocolVersions(),
	}
}

// currentSettings returns the active settings snapshot.
func (m *Module) currentSettings() Settings {
	if st := m.state.Load(); st != nil {
		return st.settings
	}
	return Settings{}
}

// currentServer returns the active MCP server, or nil before Init.
func (m *Module) currentServer() *mcp.Server {
	if st := m.state.Load(); st != nil {
		return st.server
	}
	return nil
}

// Migrations returns database migrations for the module.
func (m *Module) Migrations() []module.Migration {
	return []module.Migration{
		{
			Version:     1,
			Description: "Create mcp_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS mcp_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						allow_drafts INTEGER NOT NULL DEFAULT 0,
						instructions TEXT NOT NULL DEFAULT '',
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO mcp_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS mcp_settings`)
				return err
			},
		},
	}
}
