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
| Markdown for Agents | `Accept: text/markdown` on `GET /` and `GET /{slug}` | [Cloudflare Markdown for Agents](https://developers.cloudflare.com/fundamentals/reference/markdown-for-agents/) |
| API Catalog | `/.well-known/api-catalog` | RFC 9727 (linkset format RFC 9264) |
| Agent Skills index | `/.well-known/agent-skills/index.json` | [Cloudflare Agent Skills Discovery RFC v0.2.0](https://github.com/cloudflare/agent-skills-discovery-rfc) |
| MCP Server Card | `/.well-known/mcp/server-card.json` | draft SEP-1649 while the MCP module is active |
| Security contact | `/.well-known/security.txt` | RFC 9116 (unrelated, included for completeness) |

The Link header advertises three relations:

- `rel="api-catalog"` → `/.well-known/api-catalog`
- `rel="service-desc"` → `/api/v2/openapi.json`
- `rel="service-doc"` → `/api/v2/docs` (Swagger UI)

## Configuration

| Config key | Default | Purpose |
|---|---|---|
| `robots_content_signal` | `search=yes, ai-train=no, ai-input=yes` | Value emitted as `Content-Signal: ...` line in `robots.txt`. Set to `off`, `none`, or `disabled` to suppress the directive. |
| `mcp_server_version` | empty | `serverInfo.version` of the REST-bridge MCP Server Card served while the MCP module is off (empty → `0.0.0`). While the module is on, the card reports the running MCP server's own version. |

Both keys live in the admin `Config` table and can be edited via
`/admin/config`.

## MCP transport status

oCMS ships an MCP transport as the opt-in **MCP Server** module: Streamable
HTTP at `POST /api/mcp` with read-only tools (see
[mcp-module.md](mcp-module.md)). The server card follows the module's state:

- **Module active:** the card names the endpoint in the SEP-1649 `transport`
  object (`"type": "streamable-http"`, `"endpoint": "https://…/api/mcp"`). It
  declares the draft's `$schema`, card format `version`, preferred supported
  `protocolVersion`, and `capabilities.tools`, and still links the
  REST API under `capabilities.rest.openapi`. Its `serverInfo` (`ocms`,
  title `oCMS`, the module version) is exactly what the endpoint reports at
  `initialize`.
- **Module inactive** (the default): the card omits the schema and protocol
  fields, keeps `"transport": null`, and declares only the REST fallback,
  so it never advertises an endpoint that
  would answer 404.

`seo.BuildMCPServerCard` receives the endpoint from
`FrontendHandler.SetMCPEndpointProvider`, which `cmd/ocms/main.go` wires to
the module registry's active status. Because toggling the module changes the
card, it is served with `Cache-Control: no-cache`.

## Markdown negotiation

The homepage (`/`) and page routes (`/{slug}`, and their language-prefixed
variants) honor `Accept: text/markdown`. When the header signals a
markdown preference, the handler returns a plain-text Markdown
representation in place of the HTML theme output:

- `Content-Type: text/markdown; charset=utf-8`
- `Vary: Accept` (also set on the HTML response so reverse-proxy caches
  keep the two representations keyed separately)
- `X-Markdown-Tokens: <n>` — coarse whitespace-token count for agents
  that estimate context budget
- HTML remains the default for any `Accept` value that does not prefer
  `text/markdown` (browsers with `*/*`, `text/html`, or no header)

The page body is converted from stored HTML (TinyMCE output) using
[`JohannesKaufmann/html-to-markdown/v2`](https://github.com/JohannesKaufmann/html-to-markdown).
`<script>` and `<iframe>` are dropped by construction since CommonMark
has no analogue; see the drift test
`TestFrontendHandler_Page_Markdown_ScriptsStripped`.

A 2 MB size cap on the input HTML guards against CPU DoS from a single
oversized page; on cap overflow the handler logs the error and falls
back to the HTML representation so callers never see a blank response.
Authentication parity with the HTML path is enforced by running the
negotiation branch **after** the draft-preview guard — drafts remain
admin/editor-only in both representations.

Implementation lives in `internal/seo/markdown/negotiate.go`; the
wiring is in `internal/handler/frontend.go` (`Home` and `Page`).

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

# Markdown for Agents on homepage and single page
curl -sD- -H 'Accept: text/markdown' http://localhost:8080/ | head -20
curl -sD- -H 'Accept: text/markdown' http://localhost:8080/<slug> | head -20

# Content-Signal directive in robots.txt
curl -s http://localhost:8080/robots.txt | grep -i '^content-signal:'

# RFC 9727 catalog (expects application/linkset+json)
curl -sS -D - http://localhost:8080/.well-known/api-catalog

# Agent Skills v0.2.0 index
curl -s http://localhost:8080/.well-known/agent-skills/index.json | jq .

# MCP Server Card (SEP-1649); shows a transport object once the MCP module is active
curl -s http://localhost:8080/.well-known/mcp/server-card.json | jq .

# MCP endpoint (module active): 405 for GET, 401 with a Bearer challenge for POST
curl -si http://localhost:8080/api/mcp | head -1
curl -si -X POST http://localhost:8080/api/mcp | grep -i '^www-authenticate'
```

## Re-scanning

After deploying changes to a public site, re-run the scanner:
<https://isitagentready.com/www.example.com>. The categories that
should flip to pass after this change are: Link headers, Content
Signals, API Catalog, MCP Server Card, Agent Skills, and Markdown for
Agents (which promotes the site to Level 3 — Agent-Readable).

## Out of scope (tracked follow-ups)

- **WebMCP** — `navigator.modelContext.provideContext()` calls on the
  admin dashboard to expose oCMS actions as in-browser tools. Phase 2.
- **OAuth 2.0 / OIDC discovery** — currently oCMS authenticates REST and
  MCP clients via static API keys (MCP keys need the `mcp:access`
  permission). Publishing `/.well-known/oauth-authorization-server`
  and `/.well-known/oauth-protected-resource` requires a real
  authorization server. Phase 3.
- **Web Bot Auth** — informational only in the scan; requires a JWKS at
  `/.well-known/http-message-signatures-directory` and signed outbound
  requests. Phase 3.
