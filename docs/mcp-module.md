# MCP Server Module

The MCP module lets AI agents (Claude Code, Cursor, VS Code, Codex and any
other client of the [Model Context Protocol](https://modelcontextprotocol.io))
read site content: they can search, list and read pages, media, tags and
categories through typed tools.

Content reads remain available under their existing policy. R1.5 adds opt-in
owned draft creation/update/translation with `mcp:drafts`, retained retries,
CAS, provenance and existing taxonomy/media. Publishing and deletion remain
human actions. See [Draft authoring](draft-authoring.md) for exact contracts;
[OAuth](oauth.md) records the completed R1.1 authentication acceptance and
PHASE5 notes retain R1.5/R1.6 results. R1.7 is deployed at `ba044434`
(2026-11-04), with fresh Claude web/ChatGPT web OAuth/MFA reconnect,
draft replay/conflict, share-policy and anonymous preview/revocation checks.
The [upgrade record](launch/r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04)
records exact identity and limits; native/CLI modes were not rerun on this SHA.
The [current acceptance record](launch/r17-acceptance.md) retains earlier web
sharing/revocation and native/CLI multilingual draft results on `39d82c94`.
The inline alt-text repair passed both web clients' resaves/readbacks and
rendered previews on `16ac7161`. Fresh editorial/stale-translation review,
[release decisions](launch/release-decisions.md) and human publication remain.

## Features

- **Streamable HTTP transport** at `POST /api/mcp`. It runs stateless with
  JSON responses, so it works behind reverse proxies, needs no session
  storage and fits multi-instance deployments.
- **Built on the official Go SDK**
  ([`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
  v1.8.0). It speaks protocol versions `2026-07-28`, `2025-11-25`,
  `2025-06-18`, `2025-03-26` and `2024-11-05`.
- **21 registrations** over core authoring and REST v2 services (14 existing reads, six draft operations and explicit share issuance). Existing content tools share one
  set of visibility rules, validation and caching, and the tool input schemas
  come from the same struct tags as the REST parameters.
- **API key authentication.** Keys need the `mcp:access` permission and are
  subject to every API key policy, including CIDR allowlists, expiry, maximum
  lifetime and IP-change revocation.
- **Optional OAuth.** `OCMS_OAUTH_ENABLED=true` enables browser authorization,
  fresh password/MFA sign-in, scoped consent, rotating refresh and account
  revocation. The default remains off. The key or grant owner must currently
  be an editor or admin; OAuth permissions also follow current grant scopes
  and account credential version.
- **Drafts are hidden by default.** An agent can see a draft only if an admin
  turns the drafts policy on **and** the agent's key holds `pages:read`.
- **Admin page** at `/admin/mcp`. It shows the endpoint and ready-made client
  configuration, and holds the drafts policy, instructions for agents, the tool
  catalog and the connected-apps overview (OAuth connections and the API keys
  that have MCP access, paginated; there is no separate API key table).
- **Server card.** `/.well-known/mcp/server-card.json` advertises the endpoint
  while the module is active.
- **Structured logs:** one line per dispatched tool call, including calls the
  SDK rejects after dispatch: invalid arguments, unknown tools and invalid
  output. Keys and arguments are never logged.

## Enabling the module

The module is **opt-in**. When it is first registered it starts inactive, so an
upgrade never opens a new remote endpoint on its own.

1. Go to **Admin → Modules** and turn on **MCP Server**. Until you do,
   `/api/mcp` returns 404.
2. Go to **Admin → API Keys** and create a key with the **MCP** permission
   (`mcp:access`). That alone lets agents read published content. Add
   **Pages → Read pages, including unpublished drafts** (`pages:read`) only if
   agents should see drafts (see below).
3. Configure the client (see [Connecting clients](#connecting-clients)), or
   enable [browser OAuth](oauth.md). The
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

For Claude's remote connectors, requests originate from Anthropic's cloud
rather than the desktop computer. Anthropic currently publishes
`160.79.104.0/21` as its outbound IPv4 range; check the
[official IP addresses](https://platform.claude.com/docs/en/api/ip-addresses)
before configuring it. Add this range to the key's **Source CIDR allowlist**
and to `OCMS_API_ALLOWED_CIDRS` when the global policy requires it.

For local clients, such as Claude Code or a desktop HTTP bridge, add the
client's public IP as a `/32` entry. A key used by both kinds of client can
have one CIDR per line, for example:

```text
160.79.104.0/21
203.0.113.42/32
```

Replace the documentation address `203.0.113.42` with your current public
IPv4 address, and update it if your ISP or VPN changes that address. For an
individual IPv6 address, use `/128`.

If an IP change has already revoked the key, edit it in **Admin → API Keys**:
save the source CIDRs while **Active** remains unchecked, then edit it again
and check **Active**. This order prevents a retry from revoking the key between
activation and saving its CIDRs. The token stays the same and no server
restart is needed. Once source CIDRs are configured, changes between allowed
IPs may still log an `observed` warning, but they do not revoke the key.
Requests from outside the allowlist are rejected.

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

**Claude Desktop and claude.ai remote connectors:**

Request header authentication is a **limited beta**. If **Request headers**
does not appear in the connector dialog, your organization does not yet have
access. See Anthropic's
[custom connector instructions](https://claude.com/docs/connectors/custom/add-unlisted).

1. Open **Customize → Connectors → Add custom connector**. For an organization
   connector, an owner uses **Organization settings → Connectors**.
2. Enter the public HTTPS endpoint, such as `https://example.com/api/mcp`.
3. Choose **No sign-in** for authentication.
4. Under **Request headers**, add a required `Authorization` header with the
   complete value `Bearer <YOUR_API_KEY>`, including the `Bearer ` prefix.
5. Save the connector and enable it through **+ → Connectors** in the chat.

Configure the source CIDRs described above before connecting. OAuth is not
implemented by this release; accounts without request header access need a
local stdio-to-HTTP bridge in Claude Desktop or another client that supports
bearer headers. OAuth connections reserve the `Authorization` header, so use
**No sign-in** for this API key setup.

## Tools

| Tool | Arguments | REST v2 counterpart | Returns |
|---|---|---|---|
| `get_authoring_context` | `brief`, `references`, `language_code`, `preset`, `task`, paging offsets | — | Bounded references with revisions, guidance and existing taxonomy/images/link choices |
| `get_page_draft_schema` | — | — | Strict draft schemas and localized issue catalog |
| `get_style_guide` | `language_code` | — | Editorial guide with fallback identity |
| `list_presets` | `language_code` | — | Seeded article presets and language overrides |
| `get_site_info` | — | `GET /api/v2/auth` | Site name, description, URL and default language; active languages; the key's name, prefix, permissions and expiry; whether drafts are visible; server details |
| `search_pages` | `query` (1–200 chars), `language_code`, `page`, `per_page` (≤ 50) | — (MCP only) | Pages matching the words, each with a plain-text excerpt |
| `list_pages` | `status`, `language_code`, `category_id`, `tag_id`, `page`, `per_page` (≤ 100), `include_taxonomy` | `GET /pages` | Page summaries without bodies, newest first |
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
  drafts are visible to the key, it matches titles and body text (never the
  HTML markup) across all pages instead, as the admin search does.
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
(`modules/mcpserver/catalog.go`), each with its reason: direct REST mutations are covered by draft tools with a different contract,
and `status` is covered by MCP discovery.
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
| Own AI-editable unscheduled drafts in authoring context | Also available with `mcp:drafts`; this exception does not broaden generic page reads |
| Media, tags, categories | Always; they are public reads in REST v2 too |
| Editorial guides/presets | Read-only guidance with `mcp:access` |
| User directories, forms/submissions, general configuration, API key directories | No corresponding tool |

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
| Instructions for AI agents | Empty | Up to 2400 characters of guidance sent to every client in the `initialize` / `server/discover` response, after the built-in orientation |

Saving rebuilds the MCP server first, stores the settings second and swaps the
new server in last, one save at a time. A failure therefore never leaves the
stored settings and the running server out of step. Every change is logged and
recorded in the event log as `MCP settings updated`.

The page always shows the stored settings. Each authenticated MCP request also
reloads the settings and checks module activation in the shared database before
dispatch. Saving settings or disabling MCP on one instance therefore applies to
subsequent requests on other running instances. Requests already admitted may
finish with their earlier settings. Enable the module on every instance, or
restart instances after enabling it, so their routes are registered.

If the stored settings cannot be read, the page says so and disables the form,
so nothing is saved over values it could not read. MCP requests return HTTP 503
if activation or settings cannot be read, or the server cannot apply updated
settings; they retry on the next request. A stored inactive module returns
HTTP 404. This prevents a replica from serving drafts under a stale policy.

The page also shows:

- the endpoint URL;
- the supported protocol versions;
- snippets for Claude Code and JSON configuration, with a placeholder instead
  of a key;
- the tool catalog;
- every API key that holds `mcp:access`, with name, prefix, permissions, last
  use, expiry and status. Key material is never shown.

## Server card

`/.well-known/mcp/server-card.json` follows the [SEP-1649 draft](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/1649)
while the module is active. It carries:

```json
{
  "$schema": "https://static.modelcontextprotocol.io/schemas/mcp-server-card/v1.json",
  "version": "1.0",
  "protocolVersion": "2026-07-28",
  "serverInfo": {"name": "ocms", "title": "oCMS", "version": "1.0.0"},
  "transport": {"type": "streamable-http", "endpoint": "https://example.com/api/mcp"},
  "capabilities": {"tools": {}, "prompts": {}, "resources": {}, "rest": {"openapi": "https://example.com/api/v2/openapi.json"}}
}
```

`serverInfo` is exactly what the endpoint reports at `initialize`;
`TestServerCardMatchesInitialize` and authoring capability tests keep identity and capabilities in step. `protocolVersion`
advertises the endpoint's preferred supported revision; `version` is the
card format version. The card does not claim SEP-2127 compatibility.
While the module is inactive, the card omits the schema and protocol fields
and falls back to `"transport": null` with only the REST
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
| Request body | 512 KiB wire cap; 2 MiB SDK hard ceiling |
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
  goroutines, which neither chi's recoverer nor the global timeout
  middleware's recovery reaches. The module therefore recovers panics itself
  at three levels: the endpoint, every MCP method and every tool. A panic
  becomes an internal error, in JSON rather than the timeout middleware's
  plain-text 500, and is logged with its stack; it does not crash the process.
- **`"arguments": null`:** in go-sdk v1.8.0, a `tools/call` whose arguments
  are JSON `null` panics while applying schema defaults. The module turns that
  into absent arguments before validation. A regression test covers it.
- **Prompt injection:** pages are written by site authors, not by the person
  driving the agent. A compromised author account could plant instructions in
  page bodies. The built-in server instructions tell agents that everything the
  tools return is website data, not instructions. Agents should still be run
  with the usual care for untrusted input, and drafts should stay hidden unless
  you need them.
- **Draft-only authoring:** admin opt-in, current `mcp:drafts`, user ownership, AI access, no schedule, retained request IDs and revision checks. No publish/delete tool exists.

## Logging

The module logs through `slog` with `module=mcp`. Tool-call records now also
include the verified `user_id` and `auth_type`; OAuth records use `grant_id`
and `oauth_client_id` in place of API key fields. Arguments and credentials
remain excluded. OAuth-enabled unauthenticated MCP requests use the SDK's
`resource_metadata` challenge; key authentication retains its existing
challenge. The following key-mode details still apply:

| Event | Level | Fields |
|---|---|---|
| Prompt/resource read, one line per `prompts/get` or `resources/read` | Info; Warn for cancellation; Error for internal failures | method, allowlisted name, user/grant/key identity, outcome, duration, error code; no arguments, full URI, content or exception text |
| Tool call, one line per dispatched `tools/call` | Info; Warn for `cancelled`; Error for `internal_error` | `tool`, `outcome`, `duration_ms`, `api_key_id`, `api_key_prefix`, `client_name`, `client_version`, `protocol_version`, `error_code`, plus `error` (the cause) for internal and cancelled calls |
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

Some requests never reach the tool-call log:

- requests the SDK rejects before dispatching them, such as undecodable
  parameters or a mismatched protocol version or header;
- OAuth tool requests rejected before dispatch with an HTTP 403 scope-upgrade
  challenge; their HTTP status remains in the access log;
- JSON-RPC batches, which are answered with a 400.

Client-supplied values (an unknown tool's name, the client's name and version)
are cut to 128 bytes.

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
| `TestEveryRegisteredToolHasTheExpectedMutationClass` | Read/create/update/share annotations do not match the closed catalog |
| `TestCatalogSpecsWellFormed` | A catalog entry has an invalid or duplicate name, lacks a title or description, or declares both or neither of a REST mapping and an MCP-only reason |
| `TestTranslationsCompleteAndUsedKeysExist` | en/ru keys diverge, or code uses an `mcp.*` key that does not exist |
| `TestServerCardMatchesInitialize` | The server card's identity or tool/prompt/resource capabilities differ from `initialize` |

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
- **Draft status with category or tag.** `list_pages` rejects `status: "draft"`
  combined with `category_id` or `tag_id`; these combinations are unsupported.

## Draft authoring (R1.5 development)

Enable draft authoring in Admin → MCP Server and grant `mcp:drafts`. The
new tools are `create_draft_page`, `update_draft_page`,
`create_translation_draft`, `get_my_draft`, `list_my_drafts` and
`get_translation_status`. R1.6 enables explicit revision-pinned
`create_preview_link` with expiring bearer access and lifecycle revocation.
Save/read results remain session-only; see [Editorial review](editorial-review.md).
**Let AI clients create share links** (on by default) controls that tool:
with it off, `create_preview_link` refuses with `share_links_disabled`. The
**Share links issued by AI clients** card lists every usable link an OAuth
grant or API key issued, with its page, account and expiry, and revokes one
with a recorded `Preview share link revoked` event.
Three content mutations require stable UUID request
IDs; updates require the last-read revision. Own reads do not depend on the
separate general drafts policy. All writes use the shared atomic service.
The per-user rolling quota is 60 new successful content operations per hour;
retained retries spend no additional quota. A user may hold at most 50
unpublished AI drafts: creating another is refused with the non-retryable code
`draft_limit_reached`, while updates continue. Each AI draft keeps its newest 20
revisions; the `core:ai_revision_cleanup` scheduler job removes older ones that
nothing still pins or references. See [Draft authoring](draft-authoring.md).

## Authoring context (R1.4 development)

Read-only prompts, resources, tool equivalents, editorial guidance and language
filters are implemented locally. See [Authoring context](authoring-context.md)
for the exact reference/visibility rules, limits, compact prompt arguments,
resource URIs, role/CSRF policy and verification scope. Page list/detail results
include their current revision. Generic page reads retain their existing draft
policy; authoring context also supports the caller's own opted-in plain drafts.

OAuth acceptance is complete for R1.1; see [OAuth](oauth.md). Real CMS draft
creation/update/translation tools have R1.5 staging results. R1.6 editorial
review/previews are deployed on staging, with tested Claude/ChatGPT web flows
and the [transport rollout checks](../.claude/docs/PHASE5.md#r16-preview-transport-fly-rollout--2026-11-02).
Current native Desktop/CLI draft coverage is in the
[R1.7 record](launch/r17-acceptance.md).
This section grants no deployment or remote Git action.

R1.7 adds the [connected-app overview](connected-apps.md): account-owned
OAuth/API-key metadata and revocation at `/account/connected-apps`, with an
administrator overview/revoke in `/admin/mcp`. Credential hashes/values are
absent from these views. Core maintenance drains retired OAuth evidence in
bounded transactions while retaining all active-family replay evidence.
Management/retention is deployed on staging; the current runtime including
the verified inline alt-text repair is `16ac7161`. This does not mark the
pending release decisions or manual publication as complete.


## Page authoring foundations (R1.2 development)

R1.2 adds shared atomic page writes, user-owned AI-edit consent, complete
revisions/provenance and retained idempotency in core. R1.5 exposes gated CMS draft tools through this shared path. REST updates to AI-editable
pages now require `base_revision`. See [Page writes](page-writes.md) for the
compatibility rule, writer inventory, local verification and migration notes.


## Draft validation engine (R1.3 development)

`internal/ai/draft` supplies separate strict create/update schemas, localized
validation issues, safe Markdown/media conversion and explicit link policy.
Metadata-only edits preserve stored HTML. The generic MCP schema builder is
tested against these types; R1.5 page-authoring tools now consume these contracts.
See [Draft contract](draft-contract.md) for the payload rules, issue catalog,
resolver boundary and verification.
