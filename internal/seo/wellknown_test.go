// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAPICatalog(t *testing.T) {
	for _, site := range []string{"https://example.com", "https://example.com/"} {
		t.Run(site, func(t *testing.T) {
			raw := BuildAPICatalog(site)

			var got APICatalog
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("BuildAPICatalog() produced invalid JSON: %v\n%s", err, raw)
			}
			if len(got.Linkset) != 1 {
				t.Fatalf("linkset length = %d, want 1", len(got.Linkset))
			}

			entry := got.Linkset[0]
			if entry.Anchor != "https://example.com/api/v2" {
				t.Errorf("anchor = %q, want %q", entry.Anchor, "https://example.com/api/v2")
			}
			if len(entry.ServiceDesc) == 0 || entry.ServiceDesc[0].Href != "https://example.com/api/v2/openapi.json" {
				t.Errorf("service-desc = %+v, want OpenAPI URL", entry.ServiceDesc)
			}
			if entry.ServiceDesc[0].Type != "application/json" {
				t.Errorf("service-desc type = %q, want application/json", entry.ServiceDesc[0].Type)
			}
			if len(entry.ServiceDoc) == 0 || entry.ServiceDoc[0].Href != "https://example.com/api/v2/docs" {
				t.Errorf("service-doc = %+v, want Swagger UI URL", entry.ServiceDoc)
			}
			if len(entry.Status) == 0 || entry.Status[0].Href != "https://example.com/health" {
				t.Errorf("status = %+v, want /health", entry.Status)
			}
			if strings.Contains(string(raw), "//api/v2") {
				t.Errorf("trailing-slash site URL produced double slash:\n%s", raw)
			}
		})
	}
}

func TestBuildAgentSkillsIndex(t *testing.T) {
	raw := BuildAgentSkillsIndex("https://example.com/", "deadbeef")

	var got AgentSkillsIndex
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	if got.Schema != agentSkillsSchemaURL {
		t.Errorf("$schema = %q, want %q", got.Schema, agentSkillsSchemaURL)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("skills length = %d, want 1", len(got.Skills))
	}
	skill := got.Skills[0]
	if skill.Name != "ocms-rest-api" {
		t.Errorf("skill name = %q", skill.Name)
	}
	if skill.Type != "openapi" {
		t.Errorf("skill type = %q, want openapi", skill.Type)
	}
	if skill.URL != "https://example.com/api/v2/openapi.json" {
		t.Errorf("skill url = %q", skill.URL)
	}
	if skill.SHA256 != "deadbeef" {
		t.Errorf("skill sha256 = %q, want passed-through value", skill.SHA256)
	}
	if skill.Description == "" {
		t.Error("skill description must not be empty")
	}
}

func TestBuildMCPServerCard(t *testing.T) {
	raw := BuildMCPServerCard("https://example.com", "1.2.3", nil)

	var got MCPServerCard
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	if got.ServerInfo.Name == "" {
		t.Error("serverInfo.name must be set")
	}
	if got.ServerInfo.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", got.ServerInfo.Version)
	}
	if got.Transport != nil {
		t.Errorf("transport = %v, want nil (no MCP transport yet)", *got.Transport)
	}
	if got.Capabilities.REST == nil || got.Capabilities.REST.OpenAPI != "https://example.com/api/v2/openapi.json" {
		t.Errorf("capabilities.rest.openapi not set correctly: %+v", got.Capabilities)
	}
	// Must contain literal "null" for the transport field (not omitted).
	if !strings.Contains(string(raw), `"transport": null`) {
		t.Errorf("expected transport to render as literal null in JSON:\n%s", raw)
	}
}

func TestBuildMCPServerCardDefaultsVersion(t *testing.T) {
	raw := BuildMCPServerCard("https://example.com", "", nil)
	var got MCPServerCard
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.ServerInfo.Version == "" {
		t.Error("empty version should fall back to a non-empty default")
	}
}

func TestComputeSHA256Hex(t *testing.T) {
	// Known digest: sha256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	if got := ComputeSHA256Hex(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("sha256(empty) = %s, want e3b0c442...b855", got)
	}
	if got := ComputeSHA256Hex([]byte{}); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("sha256([]byte{}) should match empty string digest; got %s", got)
	}
	if len(ComputeSHA256Hex([]byte("oCMS"))) != 64 {
		t.Error("digest must be 64 hex chars")
	}
}

func TestLinkHeaderHomepage(t *testing.T) {
	h := LinkHeaderHomepage
	for _, want := range []string{
		`rel="api-catalog"`,
		`rel="service-desc"`,
		`rel="service-doc"`,
		`</.well-known/api-catalog>`,
		`</api/v2/openapi.json>`,
		`</api/v2/docs>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("LinkHeaderHomepage missing %q\nfull value: %s", want, h)
		}
	}
}

func TestBuildMCPServerCardWithEndpoint(t *testing.T) {
	endpoint := &MCPEndpoint{
		Path:             "/api/mcp",
		Name:             "ocms",
		Title:            "oCMS",
		Version:          "1.0.0",
		ProtocolVersions: []string{"2026-07-28", "2025-11-25"},
	}
	raw := BuildMCPServerCard("https://example.com/", "", endpoint)

	var got MCPServerCard
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	const wantURL = "https://example.com/api/mcp"
	if got.Transport == nil || *got.Transport != wantURL {
		t.Errorf("transport = %v, want %q", got.Transport, wantURL)
	}
	if len(got.Remotes) != 1 || got.Remotes[0].Type != "streamable-http" || got.Remotes[0].URL != wantURL {
		t.Errorf("remotes = %+v, want one streamable-http remote at %s", got.Remotes, wantURL)
	}
	if strings.Join(got.SupportedProtocolVersions, ",") != "2026-07-28,2025-11-25" {
		t.Errorf("supportedProtocolVersions = %v", got.SupportedProtocolVersions)
	}
	if got.Capabilities.Tools == nil {
		t.Error("capabilities.tools must be declared when a transport is live")
	}
	if got.Capabilities.REST == nil {
		t.Error("capabilities.rest must still point at the REST API")
	}
	if got.ServerInfo != (MCPServerInfo{Name: "ocms", Title: "oCMS", Version: "1.0.0"}) {
		t.Errorf("serverInfo = %+v, want the identity the endpoint reports at initialize", got.ServerInfo)
	}
}

// TestBuildMCPServerCardLiveIdentityIgnoresOverride fails when the card of a
// live endpoint reports a different server than initialize does: the
// mcp_server_version setting labels only the REST-bridge card.
func TestBuildMCPServerCardLiveIdentityIgnoresOverride(t *testing.T) {
	raw := BuildMCPServerCard("https://example.com", "2.5.0", &MCPEndpoint{Path: "/api/mcp", Name: "ocms", Title: "oCMS", Version: "1.0.0"})
	var got MCPServerCard
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.ServerInfo.Version != "1.0.0" || got.ServerInfo.Name != "ocms" {
		t.Errorf("serverInfo = %+v, want the live server's name and version", got.ServerInfo)
	}
}

// TestBuildMCPServerCardRESTBridgeUnchanged pins the card served while no
// transport is live, byte for byte: adding the live-endpoint fields must not
// change what the REST-bridge card looks like.
func TestBuildMCPServerCardRESTBridgeUnchanged(t *testing.T) {
	const want = `{
  "serverInfo": {
    "name": "oCMS REST bridge",
    "version": "2.5.0"
  },
  "transport": null,
  "capabilities": {
    "rest": {
      "openapi": "https://example.com/api/v2/openapi.json"
    }
  }
}`
	if got := string(BuildMCPServerCard("https://example.com", "2.5.0", nil)); got != want {
		t.Errorf("REST-bridge card changed:\n%s\nwant:\n%s", got, want)
	}
}
