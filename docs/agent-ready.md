# Agent-Ready Discovery

oCMS exposes a set of standards-based discovery surfaces so AI agents can
find the programmable interface of any oCMS-powered site from a single
request to the homepage. The checks are those exercised by
[isitagentready.com](https://isitagentready.com).

## What is published

| Surface | Path | Spec |
|---|---|---|
| Sitemap | `/sitemap.xml` | sitemaps.org |
| Robots | `/robots.txt` | RFC 9309 |
| Content preferences | `Content-Signal:` directive inside `/robots.txt` | [contentsignals.org](https://contentsignals.org/), draft-romm-aipref-contentsignals |
| Homepage link header | `Link:` response header on `GET /` | RFC 8288 |
| API Catalog | `/.well-known/api-catalog` | RFC 9727 (linkset format RFC 9264) |
| Agent Skills index | `/.well-known/agent-skills/index.json` | [Cloudflare Agent Skills Discovery RFC v0.2.0](https://github.com/cloudflare/agent-skills-discovery-rfc) |
| MCP Server Card | `/.well-known/mcp/server-card.json` | draft SEP-1649 |
| Security contact | `/.well-known/security.txt` | RFC 9116 (unrelated, included for completeness) |

The Link header advertises three relations:

- `rel="api-catalog"` → `/.well-known/api-catalog`
- `rel="service-desc"` → `/api/v2/openapi.json`
- `rel="service-doc"` → `/api/v2/docs` (Swagger UI)

## Configuration

| Config key | Default | Purpose |
|---|---|---|
| `robots_content_signal` | `search=yes, ai-train=no, ai-input=yes` | Value emitted as `Content-Signal: ...` line in `robots.txt`. Set to `off`, `none`, or `disabled` to suppress the directive. |
| `mcp_server_version` | `0.0.0` | Value emitted as `serverInfo.version` in the MCP Server Card. |

Both keys live in the admin `Config` table and can be edited via
`/admin/config`.

## MCP transport status

The MCP Server Card is published with `"transport": null` — oCMS does
not currently run an MCP transport (stdio or streaming HTTP). The card
declares a REST fallback via `capabilities.rest.openapi` so agents that
accept REST-described servers (Claude, Cursor) can still bind. When a
real MCP transport ships (tracked as Phase 2), update
`seo.BuildMCPServerCard` to emit the transport endpoint.

## Agent Skills SHA-256

The `sha256` field of the `ocms-rest-api` skill currently renders as an
empty string. Computing the digest of the live OpenAPI bytes at request
time would require cross-package access to the huma registry. A
follow-up (Phase 2) will inject a precomputed digest from `main.go`
after the API is built; the exported helper `seo.ComputeSHA256Hex` is
already in place.

## Local verification

```sh
# Boot
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! make dev &
sleep 3

# Link header on homepage
curl -sI http://localhost:8080/ | grep -i '^link:'

# Content-Signal directive in robots.txt
curl -s http://localhost:8080/robots.txt | grep -i '^content-signal:'

# RFC 9727 catalog (expects application/linkset+json)
curl -sS -D - http://localhost:8080/.well-known/api-catalog

# Agent Skills v0.2.0 index
curl -s http://localhost:8080/.well-known/agent-skills/index.json | jq .

# MCP Server Card (SEP-1649)
curl -s http://localhost:8080/.well-known/mcp/server-card.json | jq .
```

## Re-scanning

After deploying changes to a public site, re-run the scanner:
<https://isitagentready.com/www.example.com>. The categories that
should flip to pass after this change are: Link headers, Content
Signals, API Catalog, MCP Server Card, and Agent Skills.

## Out of scope (tracked follow-ups)

- **Markdown for Agents** — serve `Content-Type: text/markdown` when the
  request carries `Accept: text/markdown`. Requires an HTML-to-Markdown
  converter and cache layer; not shipped in this change.
- **WebMCP** — `navigator.modelContext.provideContext()` calls on the
  admin dashboard to expose oCMS actions as in-browser tools. Phase 2.
- **OAuth 2.0 / OIDC discovery** — currently oCMS authenticates via
  static API keys. Publishing `/.well-known/oauth-authorization-server`
  and `/.well-known/oauth-protected-resource` requires a real
  authorization server. Phase 3.
- **Web Bot Auth** — informational only in the scan; requires a JWKS at
  `/.well-known/http-message-signatures-directory` and signed outbound
  requests. Phase 3.
