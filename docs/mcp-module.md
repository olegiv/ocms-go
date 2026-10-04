# MCP Server Module

The MCP module lets AI agents (Claude Code, Cursor, VS Code, Codex and any
other client of the [Model Context Protocol](https://modelcontextprotocol.io))
read site content: they can search, list and read pages, media, tags and
categories through typed tools.

This first release is **read-only**. Every tool is marked read-only, and a
drift test fails if a tool without that mark is registered.

## Features

- **Streamable HTTP transport** at `POST /api/mcp`. It runs stateless with
  JSON responses, so it works behind reverse proxies, needs no session
  storage and fits multi-instance deployments.
- **Built on the official Go SDK**
  ([`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
  v1.8.0). It speaks protocol versions `2026-07-28`, `2025-11-25`,
  `2025-06-18`, `2025-03-26` and `2024-11-05`.
- **10 read-only tools** that wrap the REST v2 services. REST and MCP share one
  set of visibility rules, validation and caching, and the tool input schemas
  come from the same struct tags as the REST parameters.
- **API key authentication.** Keys need the `mcp:access` permission and are
  subject to every API key policy, including CIDR allowlists, expiry, maximum
  lifetime and IP-change revocation.
- **Drafts are hidden by default.** An agent can see a draft only if an admin
  turns the drafts policy on **and** the agent's key holds `pages:read`.
- **Admin page** at `/admin/mcp`. It shows the endpoint and ready-made client
  configuration, and holds the drafts policy, instructions for agents, the tool
  catalog and the keys that have MCP access.
- **Server card.** `/.well-known/mcp/server-card.json` advertises the endpoint
  while the module is active.
- **Structured logs:** one line per tool call, including calls the SDK rejects.
  Keys and arguments are never logged.

## Enabling the module

The module is **opt-in**. When it is first registered it starts inactive, so an
upgrade never opens a new remote endpoint on its own.

1. Go to **Admin → Modules** and turn on **MCP Server**. Until you do,
   `/api/mcp` returns 404.
2. Go to **Admin → API Keys** and create a key with the **MCP** permission
   (`mcp:access`). That alone lets agents read published content. Add
   **Pages → Read pages, including unpublished drafts** (`pages:read`) only if
   agents should see drafts (see below).
3. Configure the client (see [Connecting clients](#connecting-clients)). The
   admin page at **Admin → Modules → MCP Server** (`/admin/mcp`) shows ready-made
   snippets for the endpoint.

Set the **Site URL** (`OCMS_SITE_URL` or **Admin → Config**) to an absolute
`http(s)` URL. Without a usable one, the admin page shows the endpoint as a bare
path and tool results leave out page URLs. The admin page tells a missing value
apart from an unusable one, such as `example.com` without a scheme.

### Production API key policies apply

MCP requests go through the same `middleware.APIKeyAuth` as REST v2. In
production, each of these defaults to on:

| Variable | Effect on MCP clients |
|---|---|
| `OCMS_REQUIRE_API_ALLOWED_CIDRS` | `OCMS_API_ALLOWED_CIDRS` must list the networks agents connect from |
| `OCMS_REQUIRE_API_KEY_EXPIRY` | The key needs an expiration date |
| `OCMS_REQUIRE_API_KEY_SOURCE_CIDRS` | The key needs its own source CIDRs |
| `OCMS_REVOKE_API_KEY_ON_SOURCE_IP_CHANGE` | A key without source CIDRs is deactivated when its source IP changes |
| `OCMS_API_KEY_MAX_TTL_DAYS` | The key's lifetime is capped (90 days by default) |

An agent on a laptop whose address changes therefore needs a key whose source
CIDRs cover every network it uses. Behind a reverse proxy, also set
`OCMS_TRUSTED_PROXIES` so the client IP is resolved correctly (see
[reverse-proxy.md](reverse-proxy.md)).

## Connecting clients

The endpoint takes a static bearer token, which is the API key. Every client
that supports remote HTTP MCP servers with custom headers can connect.

**Claude Code:**

```bash
claude mcp add --transport http ocms https://example.com/api/mcp \
  --header "Authorization: Bearer <YOUR_API_KEY>"
```

**Cursor, VS Code and other clients that read an `mcpServers` JSON file:**

```json
{
  "mcpServers": {
    "ocms": {
      "type": "http",
      "url": "https://example.com/api/mcp",
      "headers": {
        "Authorization": "Bearer <YOUR_API_KEY>"
      }
    }
  }
}
```

Keep keys out of configuration files that are shared or committed. Most clients
can read headers from environment variables.

The custom connectors of claude.ai and Claude Desktop need OAuth 2.1, and this
release does not support it (see [Follow-ups](#follow-ups)).

## Tools

| Tool | Arguments | REST v2 counterpart | Returns |
|---|---|---|---|
| `get_site_info` | — | `GET /api/v2/auth` | Site name, description, URL and default language; active languages; the key's name, prefix, permissions and expiry; whether drafts are visible; server details |
| `search_pages` | `query` (1–200 chars), `page`, `per_page` (≤ 50) | — (MCP only) | Pages matching the words, each with a plain-text excerpt |
| `list_pages` | `status`, `category_id`, `tag_id`, `page`, `per_page` (≤ 100), `include_taxonomy` | `GET /pages` | Page summaries without bodies, newest first |
| `get_page` | `id` **or** `slug`, `body_format` (`html` \| `markdown`) | `GET /pages/{id}`, `GET /pages/slug/{slug}` | Page with body, SEO metadata, author name, categories and tags |
| `list_media` | `type`, `folder_id`, `search`, `page`, `per_page` (≤ 100) | `GET /media` | Media items, newest first, without variants |
| `get_media` | `id` | `GET /media/{id}` | One media item with variants, folder and translations |
| `list_tags` | `page`, `per_page` (≤ 100) | `GET /tags` | Tags with usage counts |
| `get_tag` | `id` | `GET /tags/{id}` | One tag |
| `list_categories` | `flat` | `GET /categories` | Category tree, or a flat list |
| `get_category` | `id` | `GET /categories/{id}` | One category with its direct children |

Notes:

- **Search:** `search_pages` uses the site's FTS5 index, which covers
  published pages, the same index that powers the public site search. When
  drafts are visible to the key, it matches titles and bodies across all pages
  instead, as the admin search does.
- **Markdown bodies:** `body_format: "markdown"` converts the stored HTML with
  the same converter as [Markdown for Agents](agent-ready.md#markdown-negotiation)
  (2 MB input cap). The result is compact, but embeds and styling are dropped.
- **URLs:** page results carry a public `url` for published pages once the site
  URL is set, with a language prefix for non-default languages. Media URLs are
  site-relative (`/uploads/...`).
- **Privacy:** author email addresses are never returned, only the author's id
  and name.
- **Pagination:** list results include `total`, `page`, `per_page` and
  `total_pages`.
- **Tool annotations:** every tool is marked `readOnlyHint: true`,
  `idempotentHint: true` and `openWorldHint: false`. Results the SDK builds
  itself, such as `tools/list` and discovery, carry `cacheScope: private`,
  because each response is produced for one API key.

REST operations without a tool are listed in `restOperationsNotExposed`
(`modules/mcpserver/catalog.go`), each with its reason: write operations are
deferred to a later release, and `status` is covered by MCP discovery.
`TestEveryRESTOperationHasMCPDecision` fails when REST gains an operation that
has neither a tool nor an entry there.

### Errors

Tool failures come back as tool results with `isError: true`. Their text is
the REST v2 error envelope, so an agent can correct its own call:

```json
{"error":{"code":"validation_error","message":"Validation failed","details":{"id":"Give either id or slug, not both"}}}
```

Codes match REST v2: `not_found`, `validation_error`, `forbidden`, `conflict`,
`unauthorized` and `internal_error`. One code is MCP-only: `timeout`, for a
call cut short because the client went away or the call hit the 25-second
limit. Internal failures never expose details; the cause is logged on the
server.

Arguments that break the input schema, such as an unknown property or a value
out of range, are rejected before the tool runs. They use the same envelope,
with the SDK's validation message as the `arguments` detail:

```json
{"error":{"code":"validation_error","message":"Validation failed","details":{"arguments":"validating \"arguments\": validating root: validating /properties/per_page: maximum: 500/1 is greater than 100.000000"}}}
```

Protocol-level failures are JSON-RPC errors instead. An unknown tool is one.
So is a tool result that fails its own output schema, which is a server bug:
that one returns a generic `internal error`, never the SDK's message, because
the message can quote server data.

## Visibility and permissions

| Content | Visible over MCP |
|---|---|
| Published pages | Always, to any key with `mcp:access` |
| Drafts (and scheduled, unpublished pages) | Only if **Expose drafts to AI agents** is on **and** the key holds `pages:read` |
| Media, tags, categories | Always; they are public reads in REST v2 too |
| Users, forms, submissions, settings, API keys | Never; no tool exposes them |

When drafts are not visible, the server removes `pages:read` from the key's
effective permissions before calling the page services. The services then apply
their published-only rule, so a draft is reported as "not found", and search
and listings skip it. A `list_pages` request with `status: "draft"` is refused
with `forbidden`. Tag and category page counts still include drafts (see
[Known limitations](#known-limitations)).

`get_site_info` reports `drafts_visible`, so agents know which rule applies.

## Admin settings

**Admin → Modules → MCP Server** (`/admin/mcp`) is for admins only, and saving
it is blocked in demo mode.

| Setting | Default | Description |
|---|---|---|
| Expose drafts to AI agents | Off | Lets keys that hold `pages:read` read unpublished drafts |
| Instructions for AI agents | Empty | Up to 4000 characters of guidance sent to every client in the `initialize` / `server/discover` response, after the built-in orientation |

Saving rebuilds the MCP server first, stores the settings second and swaps the
new server in last, one save at a time. A failure therefore never leaves the
stored settings and the running server out of step. Every change is logged and
recorded in the event log as `MCP settings updated`.

The page always shows the stored settings. If they differ from the running
ones, for example because another instance saved them, opening the page applies
them. If the stored settings cannot be read, the page says so and disables the
form. The server keeps the safe defaults (drafts hidden, no instructions) until
they can be read; the form is disabled so those defaults cannot be saved over
the stored values.

The page also shows:

- the endpoint URL;
- the supported protocol versions;
- snippets for Claude Code and JSON configuration, with a placeholder instead
  of a key;
- the tool catalog;
- every API key that holds `mcp:access`, with name, prefix, permissions, last
  use, expiry and status. Key material is never shown.

## Server card

`/.well-known/mcp/server-card.json` follows SEP-1649 and SEP-2127. While the
module is active, it carries:

```json
{
  "serverInfo": {"name": "ocms", "title": "oCMS", "version": "1.0.0"},
  "transport": "https://example.com/api/mcp",
  "capabilities": {"tools": {}, "rest": {"openapi": "https://example.com/api/v2/openapi.json"}},
  "remotes": [{"type": "streamable-http", "url": "https://example.com/api/mcp"}],
  "supportedProtocolVersions": ["2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"]
}
```

`serverInfo` is exactly what the endpoint reports at `initialize`;
`TestServerCardMatchesInitialize` keeps the two in step. While the module is
inactive, the card falls back to `"transport": null` with only the REST
capability, labelled with the `mcp_server_version` config key (`0.0.0` when
empty). That key does not apply to a live card.

The card is served with `Cache-Control: no-cache`, because toggling the module
changes it.

## Security

The endpoint runs this chain, outermost first. The global middleware chain
(trusted-proxy client IP, Sentinel bans, request logging, 30-second timeout,
security headers) runs before all of it.

| Step | Rejects with |
|---|---|
| `Cache-Control: no-store` on every response | — |
| Method check (POST only) | 405 + `Allow: POST` |
| Per-IP rate limit, applied before key verification (Argon2) | 429 |
| In-flight cap | 503 + `Retry-After` |
| Cross-origin browser check (`Sec-Fetch-Site` / `Origin`) | 403 |
| API key authentication (`middleware.APIKeyAuth`) | 401 + `WWW-Authenticate: Bearer realm="oCMS MCP"` / 403 / 429 |
| `mcp:access` permission | 403 |
| Per-key rate limit | 429 |
| Request body cap | 413 |
| JSON-RPC batch | 400 `batch_not_supported` |

| Limit | Value |
|---|---|
| Request body | 256 KiB |
| Concurrent requests | 32 |
| Per-IP rate | 20 requests/s, burst 40 |
| Per-key rate | 10 requests/s, burst 30 |
| Tool call time | 25 s per call. A client disconnect or the 30 s global timeout also cancels the call, and cancellation reaches its database queries on every protocol version. |

Other controls:

- **DNS rebinding:** the SDK's own localhost guard is disabled. It rejects a
  same-host reverse proxy that forwards `Host`, which is the setup in
  [reverse-proxy.md](reverse-proxy.md). It is replaced by the controls the MCP
  specification requires: Origin validation, through Go's
  `http.CrossOriginProtection`, and mandatory authentication on every request.
  Non-browser clients send neither `Origin` nor `Sec-Fetch-Site` and pass; a
  web page cannot reach the endpoint.
- **JSON-RPC batches:** rejected. For requests that declare protocol
  `2025-03-26` or older, or no protocol, go-sdk runs every call of a batch
  concurrently. One request could then carry thousands of tool calls past the
  per-key rate limit and the in-flight cap, which both count requests.
  Protocol `2025-06-18` and later forbid batches, and mainstream clients do not
  send them. This deliberately deviates from `2025-03-26`, which says servers
  must accept batches.
- **Cancellation:** go-sdk gives handlers of pre-`2026-07-28` requests a
  context that never reports cancellation. The module re-attaches the HTTP
  request's cancellation and adds the 25-second cap, so abandoned calls stop
  their database work.
- **Panics:** the SDK has no panic recovery and runs handlers on its own
  goroutines. The global timeout middleware runs every handler on another
  goroutine of its own, out of reach of chi's recoverer. The module therefore
  recovers panics itself at three levels: the endpoint, every MCP method and
  every tool. A panic becomes an internal error and is logged with its stack; it
  does not crash the process.
- **`"arguments": null`:** in go-sdk v1.8.0, a `tools/call` whose arguments
  are JSON `null` panics while applying schema defaults. The module turns that
  into absent arguments before validation. A regression test covers it.
- **Prompt injection:** pages are written by site authors, not by the person
  driving the agent. A compromised author account could plant instructions in
  page bodies. The built-in server instructions tell agents that everything the
  tools return is website data, not instructions. Agents should still be run
  with the usual care for untrusted input, and drafts should stay hidden unless
  you need them.
- **Read-only scope:** no tool writes data. Write tools will come with their
  own policy layer.

## Logging

The module logs through `slog` with `module=mcp`:

| Event | Level | Fields |
|---|---|---|
| Tool call, one line per `tools/call` | Info; Warn for `cancelled`; Error for `internal_error` | `tool`, `outcome`, `duration_ms`, `api_key_id`, `api_key_prefix`, `client_name`, `client_version`, `protocol_version`, `error_code`, plus `error` (the cause) for internal and cancelled calls |
| Panic in the endpoint, an MCP method or a tool | Error | `panic`, `stack`, plus `tool`/`method`/`ip` |
| Key without `mcp:access`, cross-origin request, in-flight cap reached | Warn | `api_key_id`/`api_key_prefix` or `ip`, `origin` |
| Unusable site URL (once until the value changes) | Warn | `site_url` |
| Settings saved | Info | `user_id`, `allow_drafts`, `previous_allow_drafts`, `instructions_length` (also recorded in the event log) |
| Settings reloaded from the database | Info | `allow_drafts`, `instructions_length` |
| Settings unreadable at startup | Error | `error` |
| Module initialized | Info | endpoint, tool count, drafts policy, protocol versions, limits |
| go-sdk messages | Warn and above only | `component` (`mcp-server`, `mcp-transport`) |

Tool call outcomes:

| Outcome | Meaning |
|---|---|
| `ok` | The call succeeded. |
| `tool_error` | A domain error, such as not found or forbidden. |
| `invalid_arguments` | The arguments failed the input schema, so the tool never ran. |
| `rejected` | A JSON-RPC error, such as an unknown tool. |
| `cancelled` | The client disconnected, the request timed out, or the call hit the 25-second cap. |
| `internal_error` | A server-side failure. |
| `unauthenticated` | The call had no authenticated key. |

API keys and tool arguments are never logged, and neither is returned content,
with one exception: an internal failure's cause is logged as-is. When the SDK
rejects a tool's own output (a server bug), that message can quote the
offending value.

## Testing with curl

```bash
KEY='<YOUR_API_KEY>'   # an API key holding mcp:access

# Method and authentication checks
curl -si http://localhost:8080/api/mcp | head -1                  # 405
curl -si -X POST http://localhost:8080/api/mcp | grep -i '^www-authenticate'

# List the tools
curl -s http://localhost:8080/api/mcp \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | jq '.result.tools[].name'

# Call a tool
curl -s http://localhost:8080/api/mcp \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_page","arguments":{"slug":"about","body_format":"markdown"}}}' \
  | jq -r '.result.content[0].text'
```

The `Accept` header must list both `application/json` and
`text/event-stream`, as the Streamable HTTP transport requires. Responses are
always plain JSON.

## Technical details

- **Package:** `modules/mcpserver`; module name `mcp`; translation prefix `mcp.`
- **Database table:** `mcp_settings` (single row, `id = 1`)
- **Endpoint:** `/api/mcp`. It sits under the reserved `api` prefix, so it can
  never shadow a page slug.
- **Admin URL:** `/admin/mcp`
- **Permission:** `mcp:access` (`model.PermissionMCPAccess`)
- **Opt-in:** the module implements `module.ActivationDefaulter`
  (`ActiveByDefault() == false`).
- **Schemas:** tool input and output schemas are generated by huma's schema
  registry from the same struct tags REST v2 uses, then converted to
  `jsonschema-go` schemas for the SDK. Recursive types such as the category tree
  use `$ref`/`$defs`. `TestToolInputConstraintsMatchREST` fails when an MCP
  argument and its REST parameter disagree on type, bounds or enum values.

Drift tests in `modules/mcpserver/drift_test.go`:

| Test | Fails when |
|---|---|
| `TestEveryRESTOperationHasMCPDecision` | A REST v2 operation has neither a tool nor a documented reason |
| `TestToolInputConstraintsMatchREST` | MCP argument constraints diverge from the REST parameters |
| `TestEveryRegisteredToolIsReadOnly` | A registered tool lacks the read-only annotations |
| `TestCatalogSpecsWellFormed` | A catalog entry has an invalid or duplicate name, lacks a title or description, or declares both or neither of a REST mapping and an MCP-only reason |
| `TestTranslationsCompleteAndUsedKeysExist` | en/ru keys diverge, or code uses an `mcp.*` key that does not exist |
| `TestServerCardMatchesInitialize` | The server card's `serverInfo` differs from what `initialize` reports |

`internal/api/v2/drift_test.go` adds `TestInternalErrorsKeepTheirCause`, which
fails when a v2 service builds an internal error without its cause. Without the
cause, the MCP log could not explain internal failures or recognize
cancellations.

## Known limitations

These come from the REST v2 services the tools share and will be fixed there,
for REST and MCP together:

- **Tag and category page counts include drafts.** `page_count` in `list_tags`,
  `get_tag`, `list_categories` and `get_category` (and REST v2) counts
  unpublished pages too, which reveals that drafts exist. Their content is not
  revealed.
- **Media search paging.** With `search`, `list_media` returns only the first
  page of matches, and `total` counts only the returned items.
- **Status with category or tag.** When drafts are visible, `list_pages`
  ignores `status` if `category_id` or `tag_id` is also given.

## Follow-ups

- Write tools for pages, taxonomy and media upload, with an editorial policy
  layer (drafts-only and no-deletes defaults) and `transport=mcp` audit
  metadata.
- OAuth 2.1 authorization, for the custom connectors of claude.ai and Claude
  Desktop. The bearer-token verifier is the seam it plugs into.
- MCP resources and prompts.
- A language filter for list and search tools (needs v2 service support).
