// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/custom/modules/bookmarks"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/analytics_int"
	"github.com/olegiv/ocms-go/modules/embed"
	"github.com/olegiv/ocms-go/modules/example"
)

func TestRegistryOwnsConcreteModulePublicRoutes(t *testing.T) {
	registry := module.NewRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, mod := range []module.Module{
		bookmarks.New(),
		example.New(),
		analytics_int.New(),
		embed.New(),
	} {
		if err := registry.Register(mod); err != nil {
			t.Fatalf("Register(%s): %v", mod.Name(), err)
		}
	}
	registry.RouteAll(chi.NewRouter())

	for _, path := range []string{
		"/bookmarks",
		"/example",
		"/analytics/read",
		"/embed/dify/token",
		"/embed/dify/chat-messages",
		"/embed/dify/messages/message-id/suggested",
	} {
		if !registry.OwnsPublicPath(path) {
			t.Errorf("OwnsPublicPath(%q) = false, want true", path)
		}
	}

	for _, path := range []string{
		"/ordinary-taxonomy-alias",
		"/topics/go",
		"/embed/not-a-registered-route",
	} {
		if registry.OwnsPublicPath(path) {
			t.Errorf("OwnsPublicPath(%q) = true, want false", path)
		}
	}
}
