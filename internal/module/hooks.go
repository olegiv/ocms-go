package module

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Predefined hook names for common events.
const (
	// Page hooks
	HookPageBeforeCreate = "page.before_create"
	HookPageAfterCreate  = "page.after_create"
	HookPageBeforeSave   = "page.before_save"
	HookPageAfterSave    = "page.after_save"
	HookPageBeforeDelete = "page.before_delete"
	HookPageAfterDelete  = "page.after_delete"
	HookPageBeforeRender = "page.before_render"

	// User hooks
	HookUserBeforeCreate = "user.before_create"
	HookUserAfterCreate  = "user.after_create"
	HookUserBeforeSave   = "user.before_save"
	HookUserAfterSave    = "user.after_save"
	HookUserBeforeDelete = "user.before_delete"
	HookUserAfterDelete  = "user.after_delete"

	// Media hooks
	HookMediaBeforeUpload = "media.before_upload"
	HookMediaAfterUpload  = "media.after_upload"
	HookMediaBeforeDelete = "media.before_delete"
	HookMediaAfterDelete  = "media.after_delete"

	// Auth hooks
	HookAuthBeforeLogin  = "auth.before_login"
	HookAuthAfterLogin   = "auth.after_login"
	HookAuthBeforeLogout = "auth.before_logout"
	HookAuthAfterLogout  = "auth.after_logout"

	// Render hooks
	HookRenderPage     = "render.page"
	HookRenderTemplate = "render.template"

	// Config hooks
	HookConfigChanged = "config.changed"

	// Module hooks
	HookModuleInit     = "module.init"
	HookModuleShutdown = "module.shutdown"
)

// HookFunc is a function that can be registered as a hook handler.
// It receives a context and data, and returns modified data and an error.
// If the hook returns an error, subsequent hooks are not called.
type HookFunc func(ctx context.Context, data any) (any, error)

// HookHandler wraps a HookFunc with metadata.
type HookHandler struct {
	Name     string   // Name of the handler for debugging
	Module   string   // Module that registered the handler
	Priority int      // Lower priority runs first (default: 0)
	Fn       HookFunc // The actual handler function
}

// HookRegistry manages hook registration and execution.
type HookRegistry struct {
	hooks  map[string][]HookHandler
	logger *slog.Logger
	mu     sync.RWMutex
}

// NewHookRegistry creates a new hook registry.
func NewHookRegistry(logger *slog.Logger) *HookRegistry {
	return &HookRegistry{
		hooks:  make(map[string][]HookHandler),
		logger: logger,
	}
}

// Register adds a hook handler for the given hook name.
func (h *HookRegistry) Register(hookName string, handler HookHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()

	handlers := h.hooks[hookName]
	handlers = append(handlers, handler)

	// Sort by priority (lower priority runs first)
	for i := len(handlers) - 1; i > 0; i-- {
		if handlers[i].Priority < handlers[i-1].Priority {
			handlers[i], handlers[i-1] = handlers[i-1], handlers[i]
		}
	}

	h.hooks[hookName] = handlers

	h.logger.Debug("hook registered",
		"hook", hookName,
		"handler", handler.Name,
		"module", handler.Module,
		"priority", handler.Priority,
	)
}

// RegisterFunc is a convenience method to register a simple hook function.
func (h *HookRegistry) RegisterFunc(hookName, handlerName, moduleName string, fn HookFunc) {
	h.Register(hookName, HookHandler{
		Name:     handlerName,
		Module:   moduleName,
		Priority: 0,
		Fn:       fn,
	})
}

// Call executes all handlers for the given hook name.
// Handlers are executed in priority order (lower first).
// The data is passed through each handler, allowing modification.
// If any handler returns an error, execution stops and the error is returned.
func (h *HookRegistry) Call(ctx context.Context, hookName string, data any) (any, error) {
	h.mu.RLock()
	handlers, exists := h.hooks[hookName]
	h.mu.RUnlock()

	if !exists || len(handlers) == 0 {
		return data, nil
	}

	h.logger.Debug("calling hooks", "hook", hookName, "handlers", len(handlers))

	currentData := data
	for _, handler := range handlers {
		result, err := handler.Fn(ctx, currentData)
		if err != nil {
			h.logger.Error("hook handler error",
				"hook", hookName,
				"handler", handler.Name,
				"module", handler.Module,
				"error", err,
			)
			return nil, fmt.Errorf("hook %s handler %s: %w", hookName, handler.Name, err)
		}
		currentData = result
	}

	return currentData, nil
}

// CallNoResult executes hooks without expecting a modified result.
// This is useful for "after" hooks that just need to be notified.
func (h *HookRegistry) CallNoResult(ctx context.Context, hookName string, data any) error {
	_, err := h.Call(ctx, hookName, data)
	return err
}

// HasHandlers returns true if there are handlers registered for the hook.
func (h *HookRegistry) HasHandlers(hookName string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	handlers, exists := h.hooks[hookName]
	return exists && len(handlers) > 0
}

// HandlerCount returns the number of handlers registered for a hook.
func (h *HookRegistry) HandlerCount(hookName string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.hooks[hookName])
}

// ListHooks returns all registered hook names.
func (h *HookRegistry) ListHooks() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	names := make([]string, 0, len(h.hooks))
	for name := range h.hooks {
		names = append(names, name)
	}
	return names
}

// HookInfo contains information about a registered hook.
type HookInfo struct {
	Name     string
	Handlers []HookHandlerInfo
}

// HookHandlerInfo contains information about a hook handler.
type HookHandlerInfo struct {
	Name     string
	Module   string
	Priority int
}

// ListHookInfo returns detailed information about all registered hooks.
func (h *HookRegistry) ListHookInfo() []HookInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()

	infos := make([]HookInfo, 0, len(h.hooks))
	for name, handlers := range h.hooks {
		handlerInfos := make([]HookHandlerInfo, len(handlers))
		for i, handler := range handlers {
			handlerInfos[i] = HookHandlerInfo{
				Name:     handler.Name,
				Module:   handler.Module,
				Priority: handler.Priority,
			}
		}
		infos = append(infos, HookInfo{
			Name:     name,
			Handlers: handlerInfos,
		})
	}
	return infos
}

// Unregister removes all handlers for a hook from a specific module.
func (h *HookRegistry) Unregister(hookName, moduleName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	handlers := h.hooks[hookName]
	newHandlers := make([]HookHandler, 0, len(handlers))
	for _, handler := range handlers {
		if handler.Module != moduleName {
			newHandlers = append(newHandlers, handler)
		}
	}
	h.hooks[hookName] = newHandlers

	h.logger.Debug("hooks unregistered",
		"hook", hookName,
		"module", moduleName,
		"remaining", len(newHandlers),
	)
}

// UnregisterAll removes all handlers registered by a module.
func (h *HookRegistry) UnregisterAll(moduleName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for hookName, handlers := range h.hooks {
		newHandlers := make([]HookHandler, 0, len(handlers))
		for _, handler := range handlers {
			if handler.Module != moduleName {
				newHandlers = append(newHandlers, handler)
			}
		}
		h.hooks[hookName] = newHandlers
	}

	h.logger.Debug("all hooks unregistered for module", "module", moduleName)
}

// Clear removes all registered hooks.
func (h *HookRegistry) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.hooks = make(map[string][]HookHandler)
	h.logger.Debug("all hooks cleared")
}
