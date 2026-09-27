# Worktree scaffolding spec — hextap-toolkit

Project id: **`htk`** · kitty colour **`#689d6a`** · Linear mapping: `CLAUDE.local.md` (untracked,
because this repository is public and the Linear project is not).

Written 2026-08-30 by `htk_orchestrator` before the first parallel worktree spawn. Required
by the `worktree-peers:spawn-worktree` skill, Step 0, which looks for it at this path and only
counts it when Git tracks it. Tracked since 2026-09-27; the companion `.claude/worktree-setup.sh`
it once described was superseded by the plugin's own spawn and teardown helpers and stays local.

## What makes this repo easy

- **Go, standard library only.** No `node_modules`, no virtualenv, no lockfile to reconcile.
- `GOMODCACHE` (`~/.local/share/go/pkg/mod`) and `GOCACHE`
  (`~/Library/Caches/go-build`) are **content-addressed and safe to share** across worktrees.
  Do not copy or isolate them — Go handles concurrent access.
- No local database, no sockets, no long-running service, no beads store.
- Build output goes to `dist/`, which is disposable and per-worktree.

So the only genuinely stateful thing to reason about is `.serena/`.

## `.serena/` — copy the memories, never the cache

| Path | Action | Why |
|---|---|---|
| `.serena/memories/` | **copy** (`cp -R`) | The whole platform knowledge base lives here and is untracked, so a fresh worktree would otherwise have none. |
| `.serena/project.yml` | **copy** | Project config; lets `/sc:load` activate cleanly. |
| `.serena/.gitignore` | **copy** | Auto-generated, contains `/cache` + `/project.local.yml`. |
| `.serena/cache/` | **NEVER copy or symlink** | The LSP cache embeds **absolute paths**. A copied cache makes Serena operate on the *wrong checkout* — the documented symptom is "serena will activate the base project and work with its paths". Serena retracted its own copy-the-cache advice in commit `a10c3e1c` for exactly this. Each worktree builds its own via `serena project index`. |
| `.serena/project.local.yml` | **do not copy** | Machine-local overrides. |

**Copy, do not symlink.** Symlinking a shared `.serena/memories` is maintainer-endorsed *only
for one writer at a time* — Serena has **no file locking anywhere**, and with concurrent
`write_memory` calls "the result is undefined" (issue #1235). Parallel worktree agents are
precisely the case that breaks. Copies are independent and cannot corrupt the orchestrator's
store.

**Consequence, by design:** memories written inside a worktree do **not** flow back.
Worktree agents record findings in **Linear**, which is the durable record. The orchestrator
folds anything durable into the main checkout's memories at merge time.

`.serena/project.yml` migration churn after a Serena upgrade: let the **main** checkout
rewrite it, then re-copy — do not let each worktree generate its own competing rewrite.

## `.omc/` — do not copy

oh-my-claudecode runtime state (session ids, HUD cache, hooks state). Per-session and
per-path; a copy would carry another session's identity. Each worktree generates its own.
Note that local `.omc/` state is destroyed with the worktree unless `OMC_STATE_DIR` is set.

## Pushing from a worktree — origin's push URL is SSH

Verified 2026-08-30. `origin` has **asymmetric URLs**:

```
fetch  https://github.com/SijanC147/hextap-toolkit.git
push   git@github.com:SijanC147/hextap-toolkit.git     ← SSH
```

`ssh-add -l` reports *"The agent has no identities"*. Keys exist under `~/.ssh` but none is
loaded, so a plain `git push` from an agent session fails. `gh auth` meanwhile holds a working
HTTPS `GITHUB_TOKEN`.

Working command, per-invocation:

```sh
git -c credential.helper='!gh auth git-credential' \
    push https://github.com/SijanC147/hextap-toolkit.git HEAD:refs/heads/<branch>
```

> ⚠️ **Never fix this with `git config remote.origin.pushurl`.** Linked worktrees share the main
> checkout's config through `--git-common-dir`, so that single command mutates the orchestrator's
> checkout *and every sibling worktree at once*. It is the exact shared-mutable-state hazard this
> spec exists to prevent. Per-command override only.

`gh pr create` works normally over HTTPS once the push lands.

**Open for Sean:** the asymmetric remote is arguably worth fixing properly — either load a key
into the agent, or set the push URL to HTTPS in the main checkout deliberately. Left alone here
because it is shared config and not this batch's business.

## Release-gate hazard specific to this repo

`hextap dev validate` **hashes every file, mode, size, byte stream and symlink target outside
`.git`** before and after its gates, and fails on any change. It does **not** consult Git
ignore rules (this is SB23-753).

Therefore, inside a worktree:

- A background process writing into `.serena/` or `.omc/` **while validation runs** produces
  a spurious `working tree changed during validation`.
- Run `hextap dev validate` when the agent is not concurrently touching agent state, and read
  a failure there as *possibly* this, not necessarily a real regression.
- `.DS_Store` files count too — Finder can create them mid-run.

CI additionally ends `verify` with `git diff --exit-code`, so never `git add -A`.

## Spawn and teardown

Use the worktree-peers plugin: `/worktree-peers:new-worktree` to spawn,
`/worktree-peers:wind-down-worktree` or `/worktree-peers:roundup-worktrees` to tear down.
Whatever creates the worktree must apply the `.serena/` table above: copy `memories/` and
`project.yml`, never `cache/`.

## Settled: `.serena/` in Git

Decided by Sean on 2026-09-03 (PR #15): the root `.gitignore` ignores `/.serena/*` except
`/.serena/project.yml`, which is tracked. Memories travel by copy at spawn, not by Git, which
suits Serena's lack of file locking. A fresh clone therefore has no memories; Linear is the
durable record.
