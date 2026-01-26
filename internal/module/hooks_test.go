// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewHookRegistry(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	if registry == nil {
		t.Fatal("NewHookRegistry() returned nil")
	}
	if registry.hooks == nil {
		t.Error("hooks map should be initialized")
	}
	if registry.isModuleActive == nil {
		t.Error("isModuleActive should have default function")
	}
}

func TestHookRegistryRegister(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	handler := HookHandler{
		Name:     "test_handler",
		Module:   "test_module",
		Priority: 0,
		Fn:       func(ctx context.Context, data any) (any, error) { return data, nil },
	}

	registry.Register("test.hook", handler)

	if !registry.HasHandlers("test.hook") {
		t.Error("HasHandlers() = false, want true")
	}
	if count := registry.HandlerCount("test.hook"); count != 1 {
		t.Errorf("HandlerCount() = %d, want 1", count)
	}
}

func TestHookRegistryRegisterFunc(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	fn := func(ctx context.Context, data any) (any, error) { return data, nil }
	registry.RegisterFunc("test.hook", "handler_name", "module_name", fn)

	if count := registry.HandlerCount("test.hook"); count != 1 {
		t.Errorf("HandlerCount() = %d, want 1", count)
	}
}

func TestHookRegistryPrioritySorting(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	var callOrder []string

	// Register handlers in non-priority order
	registry.Register("test.hook", HookHandler{
		Name:     "last",
		Module:   "test",
		Priority: 100,
		Fn: func(ctx context.Context, data any) (any, error) {
			callOrder = append(callOrder, "last")
			return data, nil
		},
	})
	registry.Register("test.hook", HookHandler{
		Name:     "first",
		Module:   "test",
		Priority: -10,
		Fn: func(ctx context.Context, data any) (any, error) {
			callOrder = append(callOrder, "first")
			return data, nil
		},
	})
	registry.Register("test.hook", HookHandler{
		Name:     "middle",
		Module:   "test",
		Priority: 50,
		Fn: func(ctx context.Context, data any) (any, error) {
			callOrder = append(callOrder, "middle")
			return data, nil
		},
	})

	_, err := registry.Call(context.Background(), "test.hook", nil)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	expected := []string{"first", "middle", "last"}
	if len(callOrder) != len(expected) {
		t.Fatalf("callOrder = %v, want %v", callOrder, expected)
	}
	for i, name := range callOrder {
		if name != expected[i] {
			t.Errorf("callOrder[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestHookRegistryCall(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	registry.RegisterFunc("test.hook", "double", "test", func(ctx context.Context, data any) (any, error) {
		n := data.(int)
		return n * 2, nil
	})

	result, err := registry.Call(context.Background(), "test.hook", 5)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result != 10 {
		t.Errorf("Call() = %v, want 10", result)
	}
}

func TestHookRegistryCallChain(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	// Each handler modifies the data
	registry.Register("test.hook", HookHandler{
		Name:     "add1",
		Module:   "test",
		Priority: 0,
		Fn:       func(ctx context.Context, data any) (any, error) { return data.(int) + 1, nil },
	})
	registry.Register("test.hook", HookHandler{
		Name:     "double",
		Module:   "test",
		Priority: 10,
		Fn:       func(ctx context.Context, data any) (any, error) { return data.(int) * 2, nil },
	})
	registry.Register("test.hook", HookHandler{
		Name:     "add3",
		Module:   "test",
		Priority: 20,
		Fn:       func(ctx context.Context, data any) (any, error) { return data.(int) + 3, nil },
	})

	// Start with 5: 5+1=6, 6*2=12, 12+3=15
	result, err := registry.Call(context.Background(), "test.hook", 5)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result != 15 {
		t.Errorf("Call() = %v, want 15", result)
	}
}

func TestHookRegistryCallError(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	expectedErr := errors.New("test error")
	registry.RegisterFunc("test.hook", "failing", "test", func(ctx context.Context, data any) (any, error) {
		return nil, expectedErr
	})

	_, err := registry.Call(context.Background(), "test.hook", nil)
	if err == nil {
		t.Fatal("Call() should return error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("Call() error = %v, should wrap %v", err, expectedErr)
	}
}

func TestHookRegistryCallNoHandlers(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	result, err := registry.Call(context.Background(), "nonexistent.hook", "original")
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result != "original" {
		t.Errorf("Call() = %v, want %q", result, "original")
	}
}

func TestHookRegistryCallNoResult(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	called := false
	registry.RegisterFunc("test.hook", "notify", "test", func(ctx context.Context, data any) (any, error) {
		called = true
		return data, nil
	})

	err := registry.CallNoResult(context.Background(), "test.hook", nil)
	if err != nil {
		t.Fatalf("CallNoResult() error = %v", err)
	}
	if !called {
		t.Error("Handler should have been called")
	}
}

func TestHookRegistrySetIsModuleActive(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	called := false
	registry.RegisterFunc("test.hook", "handler", "inactive_module", func(ctx context.Context, data any) (any, error) {
		called = true
		return data, nil
	})

	// Set module as inactive
	registry.SetIsModuleActive(func(moduleName string) bool {
		return moduleName != "inactive_module"
	})

	_, err := registry.Call(context.Background(), "test.hook", nil)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if called {
		t.Error("Handler from inactive module should not be called")
	}
}

func TestHookRegistryHasHandlers(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	if registry.HasHandlers("test.hook") {
		t.Error("HasHandlers() = true for unregistered hook")
	}

	registry.RegisterFunc("test.hook", "handler", "test", func(ctx context.Context, data any) (any, error) {
		return data, nil
	})

	if !registry.HasHandlers("test.hook") {
		t.Error("HasHandlers() = false after registration")
	}
}

func TestHookRegistryHandlerCount(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	if count := registry.HandlerCount("test.hook"); count != 0 {
		t.Errorf("HandlerCount() = %d, want 0", count)
	}

	registry.RegisterFunc("test.hook", "handler1", "test", func(ctx context.Context, data any) (any, error) {
		return data, nil
	})
	registry.RegisterFunc("test.hook", "handler2", "test", func(ctx context.Context, data any) (any, error) {
		return data, nil
	})

	if count := registry.HandlerCount("test.hook"); count != 2 {
		t.Errorf("HandlerCount() = %d, want 2", count)
	}
}

func TestHookRegistryListHooks(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	hooks := registry.ListHooks()
	if len(hooks) != 0 {
		t.Errorf("ListHooks() = %v, want empty", hooks)
	}

	registry.RegisterFunc("hook1", "h", "m", func(ctx context.Context, data any) (any, error) { return data, nil })
	registry.RegisterFunc("hook2", "h", "m", func(ctx context.Context, data any) (any, error) { return data, nil })

	hooks = registry.ListHooks()
	if len(hooks) != 2 {
		t.Errorf("ListHooks() length = %d, want 2", len(hooks))
	}
}

func TestHookRegistryListHookInfo(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	registry.Register("test.hook", HookHandler{
		Name:     "handler1",
		Module:   "module1",
		Priority: 10,
		Fn:       func(ctx context.Context, data any) (any, error) { return data, nil },
	})
	registry.Register("test.hook", HookHandler{
		Name:     "handler2",
		Module:   "module2",
		Priority: 5,
		Fn:       func(ctx context.Context, data any) (any, error) { return data, nil },
	})

	infos := registry.ListHookInfo()
	if len(infos) != 1 {
		t.Fatalf("ListHookInfo() length = %d, want 1", len(infos))
	}

	info := infos[0]
	if info.Name != "test.hook" {
		t.Errorf("HookInfo.Name = %q, want %q", info.Name, "test.hook")
	}
	if len(info.Handlers) != 2 {
		t.Errorf("HookInfo.Handlers length = %d, want 2", len(info.Handlers))
	}
}

func TestHookRegistryUnregister(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	registry.RegisterFunc("test.hook", "handler1", "module1", func(ctx context.Context, data any) (any, error) { return data, nil })
	registry.RegisterFunc("test.hook", "handler2", "module2", func(ctx context.Context, data any) (any, error) { return data, nil })

	registry.Unregister("test.hook", "module1")

	if count := registry.HandlerCount("test.hook"); count != 1 {
		t.Errorf("HandlerCount() = %d, want 1 after unregister", count)
	}
}

func TestHookRegistryUnregisterAll(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	registry.RegisterFunc("hook1", "handler", "module1", func(ctx context.Context, data any) (any, error) { return data, nil })
	registry.RegisterFunc("hook2", "handler", "module1", func(ctx context.Context, data any) (any, error) { return data, nil })
	registry.RegisterFunc("hook1", "handler", "module2", func(ctx context.Context, data any) (any, error) { return data, nil })

	registry.UnregisterAll("module1")

	if count := registry.HandlerCount("hook1"); count != 1 {
		t.Errorf("HandlerCount(hook1) = %d, want 1", count)
	}
	if count := registry.HandlerCount("hook2"); count != 0 {
		t.Errorf("HandlerCount(hook2) = %d, want 0", count)
	}
}

func TestHookRegistryClear(t *testing.T) {
	logger := newTestLogger()
	registry := NewHookRegistry(logger)

	registry.RegisterFunc("hook1", "h", "m", func(ctx context.Context, data any) (any, error) { return data, nil })
	registry.RegisterFunc("hook2", "h", "m", func(ctx context.Context, data any) (any, error) { return data, nil })

	registry.Clear()

	hooks := registry.ListHooks()
	if len(hooks) != 0 {
		t.Errorf("ListHooks() after Clear() = %v, want empty", hooks)
	}
}

func TestFilterHandlersByModule(t *testing.T) {
	handlers := []HookHandler{
		{Name: "h1", Module: "m1"},
		{Name: "h2", Module: "m2"},
		{Name: "h3", Module: "m1"},
		{Name: "h4", Module: "m3"},
	}

	filtered := filterHandlersByModule(handlers, "m1")

	if len(filtered) != 2 {
		t.Errorf("filterHandlersByModule() length = %d, want 2", len(filtered))
	}
	for _, h := range filtered {
		if h.Module == "m1" {
			t.Errorf("filterHandlersByModule() should not contain handlers from m1")
		}
	}
}
