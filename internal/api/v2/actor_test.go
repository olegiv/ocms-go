// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestActorPrincipal(t *testing.T) {
	key := &store.ApiKey{ID: 7, CreatedBy: 11}
	for _, tt := range []struct {
		name      string
		actor     Actor
		transport string
		want      provenance.Principal
	}{
		{
			name: "anonymous does not acquire identity",
			want: provenance.Principal{Source: "human"},
		},
		{
			name: "direct user", actor: Actor{UserID: 3}, transport: "admin",
			want: provenance.Principal{UserID: 3, Source: "human", Transport: "admin"},
		},
		{
			name: "key owner", actor: Actor{UserID: 99, APIKey: key}, transport: "rest",
			want: provenance.Principal{UserID: 11, Source: "api", APIKeyID: 7, Transport: "rest"},
		},
		{
			name: "grant", actor: Actor{UserID: 3, OAuthGrantID: "grant", OAuthClientID: "client"}, transport: "mcp",
			want: provenance.Principal{UserID: 3, Source: "oauth", OAuthGrantID: "grant", OAuthClientID: "client", Transport: "mcp"},
		},
		{
			name: "key precedence matches authorization", actor: Actor{UserID: 99, APIKey: key, OAuthGrantID: "grant", OAuthClientID: "client"}, transport: "mcp",
			want: provenance.Principal{UserID: 11, Source: "api", APIKeyID: 7, Transport: "mcp"},
		},
		{
			name: "client alone is not a credential", actor: Actor{UserID: 3, OAuthClientID: "client"}, transport: "admin",
			want: provenance.Principal{UserID: 3, Source: "human", Transport: "admin"},
		},
		{
			name: "adapter selects transport", actor: Actor{APIKey: key, Transport: "mcp"}, transport: "rest",
			want: provenance.Principal{UserID: 11, Source: "api", APIKeyID: 7, Transport: "rest"},
		},
		{
			name: "empty transport is explicit", actor: Actor{APIKey: key, Transport: "rest"},
			want: provenance.Principal{UserID: 11, Source: "api", APIKeyID: 7},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.actor.Principal(tt.transport); got != tt.want {
				t.Fatalf("principal = %+v, want %+v", got, tt.want)
			}
		})
	}
}
