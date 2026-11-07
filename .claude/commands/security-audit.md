---
description: "Run a report-only security audit through the guarded launcher"
---

Direct the user to run the security audit launcher from a terminal in this
checkout.

```bash
./scripts/codex-commands security-audit
```

The launcher preserves archived reports, limits the audit session's tools
and saves the new report in `.audit/`. Use the report to discuss findings
and proposed fixes. Ask for explicit approval before starting a separate
dependency or source change.
