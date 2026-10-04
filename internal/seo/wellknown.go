// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package seo wellknown.go builds JSON bodies for Agent-Ready discovery
// endpoints served under /.well-known/. All builders accept a normalized
// siteURL (scheme + host, no trailing slash) and return valid JSON ready
// to write directly to the response body.
package seo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// normalizeSiteURL strips a trailing slash so builders can concatenate
// paths without producing "//".
func normalizeSiteURL(siteURL string) string {
	return strings.TrimSuffix(siteURL, "/")
}

// APICatalogLink is one entry inside a linkset array.
//
// Shape follows RFC 9727 (API Catalog) which uses the linkset format
// from RFC 9264. Keys whose name contains a hyphen are emitted with
// json tags explicitly.
type APICatalogLink struct {
	Anchor      string             `json:"anchor"`
	ServiceDesc []APICatalogTarget `json:"service-desc,omitempty"`
	ServiceDoc  []APICatalogTarget `json:"service-doc,omitempty"`
	Status      []APICatalogTarget `json:"status,omitempty"`
	Describedby []APICatalogTarget `json:"describedby,omitempty"`
}

// APICatalogTarget describes one link target inside a relation array.
type APICatalogTarget struct {
	Href string `json:"href"`
	Type string `json:"type,omitempty"`
}

// APICatalog is the top-level RFC 9727 document.
type APICatalog struct {
	Linkset []APICatalogLink `json:"linkset"`
}

// BuildAPICatalog returns an RFC 9727 api-catalog document as JSON bytes,
// pointing at the oCMS v2 REST API (OpenAPI spec, Swagger UI, and health
// endpoint). Content-Type for the response should be
// application/linkset+json.
func BuildAPICatalog(siteURL string) []byte {
	base := normalizeSiteURL(siteURL)
	doc := APICatalog{
		Linkset: []APICatalogLink{
			{
				Anchor: base + "/api/v2",
				ServiceDesc: []APICatalogTarget{
					{Href: base + "/api/v2/openapi.json", Type: "application/json"},
				},
				ServiceDoc: []APICatalogTarget{
					{Href: base + "/api/v2/docs", Type: "text/html"},
				},
				Status: []APICatalogTarget{
					{Href: base + "/health", Type: "application/json"},
				},
			},
		},
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return out
}

// AgentSkill is one entry in the Agent Skills Discovery index (v0.2.0).
// sha256 MUST be a hex digest of the referenced document bytes.
type AgentSkill struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
}

// AgentSkillsIndex matches the v0.2.0 schema at agentskills.io.
type AgentSkillsIndex struct {
	Schema string       `json:"$schema"`
	Skills []AgentSkill `json:"skills"`
}

// agentSkillsSchemaURL is the canonical schema reference for v0.2.0.
const agentSkillsSchemaURL = "https://agentskills.io/schemas/v0.2.0/index.json"

// BuildAgentSkillsIndex returns a v0.2.0 skills index as JSON bytes.
//
// The only skill declared by default is ocms-rest-api, referencing the
// live OpenAPI document at /api/v2/openapi.json. openapiSHA256 must be
// a 64-char hex digest computed from the served OpenAPI bytes by the
// caller — see ComputeSHA256Hex. When the caller cannot compute the
// digest (startup race, spec not yet rendered), pass an empty string;
// the field will still be present but empty, which keeps the document
// valid JSON even if scanners may mark the sha256 as unverified.
func BuildAgentSkillsIndex(siteURL, openapiSHA256 string) []byte {
	base := normalizeSiteURL(siteURL)
	doc := AgentSkillsIndex{
		Schema: agentSkillsSchemaURL,
		Skills: []AgentSkill{
			{
				Name:        "ocms-rest-api",
				Type:        "openapi",
				Description: "Read and write pages, media, tags, and categories on this oCMS site via the REST API.",
				URL:         base + "/api/v2/openapi.json",
				SHA256:      openapiSHA256,
			},
		},
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return out
}

// MCPServerInfo is the serverInfo object for SEP-1649.
type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPRESTCapability advertises the REST API alongside (or, when no MCP
// transport is live, instead of) the MCP endpoint. It is not part of the
// formal SEP-1649 spec but is accepted in the "capabilities" free-form object.
type MCPRESTCapability struct {
	OpenAPI string `json:"openapi"`
}

// MCPToolsCapability declares that the live MCP endpoint serves tools. It
// marshals as an empty object, matching the MCP capabilities shape.
type MCPToolsCapability struct{}

// MCPCapabilities is the capabilities object declared by the server.
type MCPCapabilities struct {
	Tools *MCPToolsCapability `json:"tools,omitempty"`
	REST  *MCPRESTCapability  `json:"rest,omitempty"`
}

// MCPRemote is one remote endpoint in the SEP-2127 card shape (the successor
// of SEP-1649, aligned with the MCP registry server.json "remotes").
type MCPRemote struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// MCPEndpoint describes a live MCP transport. Callers pass it only while an
// MCP server is actually reachable, so the card never advertises an endpoint
// that would answer 404.
type MCPEndpoint struct {
	Path             string   // site-relative endpoint path, e.g. "/api/mcp"
	Version          string   // server implementation version
	ProtocolVersions []string // MCP protocol revisions the endpoint negotiates
}

// MCPServerCard follows the draft SEP-1649 shape
// (github.com/modelcontextprotocol/modelcontextprotocol PR #2127), plus the
// SEP-2127 "remotes" and "supportedProtocolVersions" fields when a transport
// is live, so scanners of either draft find the endpoint.
type MCPServerCard struct {
	ServerInfo                MCPServerInfo   `json:"serverInfo"`
	Transport                 *string         `json:"transport"` // nil => null in JSON
	Capabilities              MCPCapabilities `json:"capabilities"`
	Remotes                   []MCPRemote     `json:"remotes,omitempty"`
	SupportedProtocolVersions []string        `json:"supportedProtocolVersions,omitempty"`
}

// mcpStreamableHTTP is the transport type for MCP's Streamable HTTP transport.
const mcpStreamableHTTP = "streamable-http"

// BuildMCPServerCard returns the MCP Server Card.
//
// Without an endpoint the card is intentionally honest: transport is null and
// only the REST fallback is declared, because publishing a transport that is
// not running would be worse than declaring its absence. With an endpoint it
// names the Streamable HTTP URL in both draft shapes and still links the REST
// API. version (the admin-editable mcp_server_version setting) wins over the
// endpoint's own version; both empty falls back to "0.0.0".
func BuildMCPServerCard(siteURL, version string, endpoint *MCPEndpoint) []byte {
	base := normalizeSiteURL(siteURL)
	if version == "" && endpoint != nil {
		version = endpoint.Version
	}
	if version == "" {
		version = "0.0.0"
	}
	card := MCPServerCard{
		ServerInfo: MCPServerInfo{
			Name:    "oCMS REST bridge",
			Version: version,
		},
		Transport: nil,
		Capabilities: MCPCapabilities{
			REST: &MCPRESTCapability{
				OpenAPI: base + "/api/v2/openapi.json",
			},
		},
	}
	if endpoint != nil {
		endpointURL := base + endpoint.Path
		card.ServerInfo.Name = "oCMS"
		card.Transport = &endpointURL
		card.Capabilities.Tools = &MCPToolsCapability{}
		card.Remotes = []MCPRemote{{Type: mcpStreamableHTTP, URL: endpointURL}}
		card.SupportedProtocolVersions = endpoint.ProtocolVersions
	}
	out, _ := json.MarshalIndent(card, "", "  ")
	return out
}

// ComputeSHA256Hex returns a lowercase hex SHA-256 digest of b. Used
// to fill AgentSkill.SHA256 for the OpenAPI spec reference.
func ComputeSHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// LinkHeaderHomepage returns a single RFC 8288 Link header value
// advertising the api-catalog, service-desc (OpenAPI) and service-doc
// (Swagger UI) relations. Paths are relative (path-only) which is
// explicitly permitted by RFC 8288 §3.
const LinkHeaderHomepage = `</.well-known/api-catalog>; rel="api-catalog"; type="application/linkset+json", ` +
	`</api/v2/openapi.json>; rel="service-desc"; type="application/json", ` +
	`</api/v2/docs>; rel="service-doc"; type="text/html"`
