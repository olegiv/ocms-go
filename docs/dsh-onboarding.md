# DSH Onboarding: Git-Approval Gate

This repository ships a guard that makes every DeepSeek Harness (DSH) agent
ask for your explicit approval before it runs any git history or remote
mutation — `commit`, `tag`, `push`, `reset`, `rebase`, `revert`, `merge`,
`cherry-pick`, `am`, force-push, and `remote add/set-url/remove` or
branch/tag deletion. Approving a larger task (for example "cut the release")
is **not** that approval; the agent must get a separate "yes" for the exact
command.

The guard works on two layers:

1. **Instruction level** — the "Git Safety (Hard Rule)" section in
   [`AGENTS.md`](../AGENTS.md) and in the harness-home `AGENTS.md`. Every
   session loads these automatically.
2. **Mechanical level** — a `PreToolUse` hook that intercepts every `bash`
   tool call in new sessions and routes matching git commands through the
   approval prompt. This is what you install here.

## Prerequisites

- [DeepSeek Harness](https://github.com/deepseek-ai/DeepSeekHarness) installed
  and running (`dsh` on PATH, or the Desktop/Web app).
- A clone of this repository.
- `python3` on PATH (the guard script is plain Python).

## One-time setup

```bash
# 1. Install the guard files into the harness home (~/.dsh, or $DSH_HOME)
make install-git-guard

# 2. Install the bundle so the harness mounts the hook — either:
#    a) Web GUI → sidebar "Plugins" page → Install bundle → <repo>/dsh-git-guard
#    b) or ask your agent:
#         "install the dsh-git-guard bundle from <repo>/dsh-git-guard"
```

That's it. **New sessions** now prompt you before any git mutation runs; you
approve or reject per command.

## What each file does

| File | Role |
|------|------|
| `dsh-git-guard/package.json` | Bundle manifest (`dsh.bundle.patch`) |
| `dsh-git-guard/cordis.patch.yml` | Mounts the shipped `@deepseek-ai/dsh-hooks-claude-code` plugin; `configPath` and `pluginRoot` resolve per machine via the Loader's `dshHomePath` expression |
| `dsh-git-guard/hooks.json` | Source of truth for the `PreToolUse` hook config (installed copy: `~/.dsh/hooks.json`) |
| `dsh-git-guard/git-guard.py` | The guard script (installed copy: `~/.dsh/git-guard.py`) |
| `scripts/install-git-guard.sh` | Idempotent installer: merges the hook into the existing configuration, copies the guard script, and refreshes the marked global rule in `~/.dsh/AGENTS.md`, preserving unrelated hooks and rules |

## Behavior notes

- **New sessions only.** Sessions started before the bundle was installed keep
  their original plugin set; the mechanical gate applies to sessions started
  after installation.
- **The hook config is read once at startup.** Editing `~/.dsh/hooks.json` or
  `git-guard.py` takes effect after a harness restart.
- **Fails open if `python3` cannot start.** The hook protocol treats a hook
  that cannot launch as non-blocking. A payload the script cannot parse,
  however, fails closed (exit 2 = deny).
- **This is a tripwire, not a security boundary.** A deliberately adversarial
  model could evade shell-string matching. The durable boundary remains
  conventional: no push credentials for agents, and GitHub branch protection
  requiring pull requests.

## Updating the guard

Edit the files under `dsh-git-guard/` in this repo, re-run
`make install-git-guard`, and restart the harness. If the bundle directory
moved, reinstall the bundle from its new path.
