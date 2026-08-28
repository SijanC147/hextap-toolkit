# Toolkit development

Load this reference when changing, reviewing, publishing, or locally installing
the Hextap toolkit itself.

## Trust boundaries

- `hextap dev status` and `dev plan` are read-only Git/GitHub inventory.
- `dev validate` runs trusted toolkit source and local developer tools.
- `dev deploy` may push a feature branch, create or reuse a PR, merge through
  repository protection, create one confirmed tag, and trigger its release.
- `dev release` performs the release portion only from clean canonical `main`.
- `dev install` updates only the Hextap Formula and explicitly selected managed
  Hextap skill copies. It never invokes `brew services` or changes another
  Formula, proxy, certificate, port, or runtime configuration.

Require an explicit user request covering every selected mutation boundary.

## Implementation loop

1. Use an isolated Git worktree from current `origin/main`. Verify that `origin`
   fetches and pushes only `SijanC147/hextap-toolkit`; reject any additional
   writable remote.
2. Inspect the live repository and release baseline:

   ```sh
   hextap dev status --project TOOLKIT_ROOT
   ```

3. Implement the requested source change with failing tests first. Keep the Go
   runtime dependency-free unless separately approved. Never encode credentials,
   machine-only paths, or transient release values in the portable skill.
4. Iterate with the quick gate, then require the full gate before committing:

   ```sh
   hextap dev validate --project TOOLKIT_ROOT --quick
   hextap dev validate --project TOOLKIT_ROOT
   ```

5. Review the complete diff, run an independent code-quality review, commit with
   a conventional message, and leave the worktree clean.

## Release planning

Select SemVer from user-visible compatibility impact:

- `patch`: compatible defect, documentation, or hardening correction.
- `minor`: backward-compatible command, feature, or contract extension.
- `major`: incompatible command, manifest, marker, or workflow contract.

Compute rather than guess the exact next version:

```sh
hextap dev plan --project TOOLKIT_ROOT --bump patch|minor|major
```

Review the current immutable release, computed tag, and target commit. The
mutating command requires both `--execute` and the exact displayed
`--confirm-tag`; a mismatched or stale confirmation must fail before mutation.

## Protected end-to-end deployment

From a clean feature branch with reviewed commits, run:

```sh
hextap dev deploy \
  --project TOOLKIT_ROOT \
  --bump BUMP \
  --confirm-tag vNEXT \
  --execute
```

Add `--install` only when local Hextap mutation is authorized. Add one concrete
`--skill-agent AGENT` per user-scoped managed skill copy that should be installed
or upgraded after the new CLI is verified.

The command must:

1. Rerun full local validation.
2. Push only the current feature branch to canonical origin.
3. Create or reuse its exact PR and wait for updated-head hosted checks.
4. Stop on review comments, unresolved threads, absent checks, or a non-clean
   merge state. It never resolves feedback or uses admin/auto bypass.
5. Merge through the protected method, capture the merge commit, and require the
   exact merged-main CI run.
6. Recompute the release baseline. Stop if the confirmed tag became stale.
7. Create or reuse only the exact annotated tag, wait for the exact self-release
   workflow, and verify the stable release is immutable with the complete assets.
8. Require the release workflow's exact Homebrew/tap gate before completion.

Fix review or CI findings in the same branch, rerun full validation, push the
new head, and invoke the identical deploy command again. Resumption must reuse
the PR and never move or delete an existing tag or immutable release.

## Release-only and install-only paths

When the protected source change is already merged and clean canonical `main`
is checked out:

```sh
hextap dev release \
  --project TOOLKIT_ROOT \
  --bump BUMP \
  --confirm-tag vNEXT \
  --execute
```

When an immutable release already exists and only local Hextap installation is
authorized:

```sh
hextap dev install \
  --project TOOLKIT_ROOT \
  --tag vVERSION \
  --commit FULL_RELEASE_COMMIT \
  --execute
```

The install path selects the Homebrew installation that owns the active
`brew-hextap`, updates tap metadata, upgrades only `sean/hextap/hextap`, checks
the exact version/commit, runs the Formula test, and then reconciles only named
managed skill targets.

## Skill inventory and updates

Use read-only inventory without an agent filter to inspect every concrete path:

```sh
hextap skills status --scope user
hextap skills status --scope user --json
```

Treat `agent` as the physical installation target and `discovered_by` as the
agents that load that path. Cursor may therefore be covered by a current shared
`.agents` copy even when its separate native `.cursor` target is not installed.

An installed lower marker version reports `UPDATE_AVAILABLE`. Upgrade only an
intact marker-owned target after reviewing the dry-run:

```sh
hextap skills upgrade --agent AGENT --scope user --dry-run
hextap skills upgrade --agent AGENT --scope user
```

Never downgrade `NEWER_THAN_CLI`, replace `SAME_VERSION_DIFFERENT`, repair
`DRIFTED`, or claim `UNMANAGED`/`INVALID` content. A successful upgrade retains
the exact prior bundle in the reported recovery path outside agent discovery.

## Completion evidence

Report the feature commit, PR URL, updated-head CI, merge commit, merged-main CI,
tag, immutable release URL, release workflow, exact tap CI, installed CLI
version/commit, Formula test, and final skill inventory. Treat each as a separate
gate. Recheck protected local runtimes only read-only unless their own mutation
was explicitly authorized.
