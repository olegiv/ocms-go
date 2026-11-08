---
description: "Run a report-only security audit through the guarded launcher"
---

Direct the user to run the security audit launcher from a terminal in this
checkout.

```bash
./scripts/codex-commands security-audit
```

The launcher preserves existing reports and active notes in place, limits the
audit session's tools and saves one combined report under `.audit/run-*/report.md`.
For a separate section review, `@security-auditor` returns Markdown to the
caller, who saves it if needed. Ask for explicit approval before starting a
separate dependency or source change.
