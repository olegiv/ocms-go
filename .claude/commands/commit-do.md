---
description: "Hand off the prepared commit to the terminal approval helper"
---

Confirm the user requested this commit and preparation succeeded. Show the
tested tree, exact draft and intended file list from preparation. If
preparation is missing or the intended changes have changed, direct the user
to `/commit-prepare` first.

After the user has reviewed the draft, stage only the prepared paths by name.
Ask the user to run this command themselves from a terminal.

```bash
./scripts/codex-commands commit-do
```

The helper checks HEAD, the staged tree and the effective Git hooks. It reruns
the test gate against the staged tree and requires the user to type `YES` for
the displayed tree and draft. If the helper refuses the commit, report the
reason and stop.
