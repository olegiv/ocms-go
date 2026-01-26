// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
)

func TestNewModulesHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewModulesHandler(db, nil, sm, nil, nil)
	if h == nil {
		t.Fatal("NewModulesHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestModulesListData(t *testing.T) {
	data := ModulesListData{
		Modules: []module.Info{
			{Name: "analytics", Version: "1.0.0", Active: true},
			{Name: "comments", Version: "2.0.0", Active: false},
		},
		Hooks: []module.HookInfo{
			{Name: "page.created", Handlers: []module.HookHandlerInfo{{Name: "handler1", Module: "mod1", Priority: 1}, {Name: "handler2", Module: "mod2", Priority: 2}}},
			{Name: "page.updated", Handlers: []module.HookHandlerInfo{{Name: "handler3", Module: "mod1", Priority: 1}}},
		},
	}

	if len(data.Modules) != 2 {
		t.Errorf("got %d modules, want 2", len(data.Modules))
	}
	if len(data.Hooks) != 2 {
		t.Errorf("got %d hooks, want 2", len(data.Hooks))
	}
}

func TestModulesListDataEmpty(t *testing.T) {
	data := ModulesListData{
		Modules: []module.Info{},
		Hooks:   []module.HookInfo{},
	}

	if len(data.Modules) != 0 {
		t.Errorf("got %d modules, want 0", len(data.Modules))
	}
	if len(data.Hooks) != 0 {
		t.Errorf("got %d hooks, want 0", len(data.Hooks))
	}
}

func TestToggleActiveRequest(t *testing.T) {
	req := ToggleActiveRequest{
		Active: true,
	}

	if !req.Active {
		t.Error("Active should be true")
	}

	req2 := ToggleActiveRequest{
		Active: false,
	}

	if req2.Active {
		t.Error("Active should be false")
	}
}

func TestToggleActiveResponse(t *testing.T) {
	resp := ToggleActiveResponse{
		Success: true,
		Active:  true,
		Message: "",
	}

	if !resp.Success {
		t.Error("Success should be true")
	}
	if !resp.Active {
		t.Error("Active should be true")
	}
	if resp.Message != "" {
		t.Error("Message should be empty")
	}
}

func TestToggleActiveResponseWithMessage(t *testing.T) {
	resp := ToggleActiveResponse{
		Success: false,
		Active:  false,
		Message: "Module not found",
	}

	if resp.Success {
		t.Error("Success should be false")
	}
	if resp.Message != "Module not found" {
		t.Errorf("Message = %q, want %q", resp.Message, "Module not found")
	}
}

func TestToggleSidebarRequest(t *testing.T) {
	req := ToggleSidebarRequest{
		Show: true,
	}

	if !req.Show {
		t.Error("Show should be true")
	}

	req2 := ToggleSidebarRequest{
		Show: false,
	}

	if req2.Show {
		t.Error("Show should be false")
	}
}

func TestToggleSidebarResponse(t *testing.T) {
	resp := ToggleSidebarResponse{
		Success: true,
		Show:    true,
		Message: "",
	}

	if !resp.Success {
		t.Error("Success should be true")
	}
	if !resp.Show {
		t.Error("Show should be true")
	}
	if resp.Message != "" {
		t.Error("Message should be empty")
	}
}

func TestToggleSidebarResponseWithMessage(t *testing.T) {
	resp := ToggleSidebarResponse{
		Success: false,
		Show:    false,
		Message: "Failed to update sidebar visibility",
	}

	if resp.Success {
		t.Error("Success should be false")
	}
	if resp.Message != "Failed to update sidebar visibility" {
		t.Errorf("Message = %q, want %q", resp.Message, "Failed to update sidebar visibility")
	}
}

func TestModuleInfo(t *testing.T) {
	info := module.Info{
		Name:          "test-module",
		Version:       "1.2.3",
		Description:   "A test module",
		Active:        true,
		ShowInSidebar: true,
	}

	if info.Name != "test-module" {
		t.Errorf("Name = %q, want %q", info.Name, "test-module")
	}
	if info.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", info.Version, "1.2.3")
	}
	if !info.Active {
		t.Error("Active should be true")
	}
	if !info.ShowInSidebar {
		t.Error("ShowInSidebar should be true")
	}
}

func TestModuleHookInfo(t *testing.T) {
	info := module.HookInfo{
		Name: "page.created",
		Handlers: []module.HookHandlerInfo{
			{Name: "handler1", Module: "mod1", Priority: 1},
			{Name: "handler2", Module: "mod2", Priority: 2},
			{Name: "handler3", Module: "mod3", Priority: 3},
		},
	}

	if info.Name != "page.created" {
		t.Errorf("Name = %q, want %q", info.Name, "page.created")
	}
	if len(info.Handlers) != 3 {
		t.Errorf("Handlers count = %d, want 3", len(info.Handlers))
	}
}
