# Editorial review status

R1.5 draft tools save into the normal oCMS editor. They return `admin_url` and
an ordinary, current-draft `preview_url` that requires an editor session. The
editor can review content, metadata, existing images/taxonomy, AI-edit consent,
translation membership and version history. Publishing remains a human action.

R1.6 has not started. Theme-isolated, revision-pinned previews, explicit expiring
share tokens, the dedicated AI provenance/review panels and translation-review
UI remain pending. `create_preview_link` returns `preview_unavailable` without
issuing a token, as approved for R1.5. The existing frontend preview is not a
public share URL or a claim about a historical revision.

For now, inspect the current `verified`, `validated` and `reported` objects
through `get_my_draft`. Treat source existence/link checks as server validation,
and model claims/notes as untrusted reports. A public OAuth registration or MCP
client name does not establish vendor identity. Use `get_translation_status`
for current source freshness. Re-read before further agent edits after a human
changes the page; stale `base_revision` saves fail rather than overwriting it.

See [draft authoring](draft-authoring.md) and
[connected-app acceptance](desktop-authoring.md) for current contracts and
checks still pending on staging.
