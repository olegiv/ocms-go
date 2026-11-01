# Connected-app draft authoring

R1.5 is implemented locally. The new CMS-write flow has not yet been deployed
or accepted in Claude Desktop or ChatGPT. R1.1's OAuth compatibility results
remain valid for that earlier build, not evidence for these new tools.

## Workflow

1. An administrator enables MCP Server and **Enable draft authoring**. Connect
   the app to the site's `/api/mcp` endpoint using fresh login, MFA when required
   and consent for `mcp:access` plus `mcp:drafts` only. Existing API-key policy
   also applies; broad page-write permission is unnecessary.
2. Ask for `get_site_info`, `get_page_draft_schema` and `get_authoring_context`
   with selected source/example references. The three prompts also have tool
   equivalents for clients that do not support MCP prompts/resources.
3. Review the proposed text, references, existing taxonomy and media. Approve
   saving a draft. The agent chooses one UUID `request_id`, retaining it for
   retries, and calls `create_draft_page`.
4. Inspect `issues`, `revision`, `admin_url` and `preview_url`. Open the URLs in
   a signed-in editor session. Anonymous visitors cannot open draft content.
   Explicit share links are unavailable until R1.6.
5. To continue in another app, connect with the same oCMS user and scopes,
   use `list_my_drafts`/`get_my_draft`, then update with a new request UUID and
   the last-read `base_revision`. Foreign-user drafts remain inaccessible,
   including with an admin account. Re-read on a revision conflict.
6. For translation, read the source revision, author the target text, then call
   `create_translation_draft`. A changed source text or existing target-language
   member prevents creation. Review source freshness with
   `get_translation_status`.

The agent cannot publish, schedule, delete, switch ownership or opt another
page into AI editing. Publishing remains a deliberate human editor action.
See [draft authoring](draft-authoring.md) for retries, quotas and provenance.

## Acceptance record after authorized deployment

For each available client record date/version, tested commit/image, account
role and non-secret settings. Verify normal save/read/update/translation, the
same lost-response retry, two apps on one user's draft, a stale editor revision,
foreign and published-page denial, disabled-toggle denial, anonymous preview
denial and revoke → refusal → reconnect. Check the complete structured error
and text fallback, annotations, prompt/tool parity and log redaction. Deactivate
all temporary test keys afterwards. Use the dedicated agent account and
[shared development authenticator](dev-authenticator.md); preserve the owner's
connections. Record unavailable capabilities as pending. Do not mark R1.5
external acceptance complete from SDK tests or from R1.1 probe results.
