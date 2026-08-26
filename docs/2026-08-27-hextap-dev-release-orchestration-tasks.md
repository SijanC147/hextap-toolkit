# Hextap Developer and Release Orchestration Task Breakdown

**Goal:** Ship a deterministic `brew hextap dev` surface that lets an authorized
agent develop, validate, publish, install, and verify Hextap itself while keeping
repository protection, immutable-release, skill ownership, secret, and local
runtime boundaries intact.

**Approach:** Add a standard-library-only developer orchestrator around the
toolkit's existing Git, GitHub Actions, immutable release, tap, Homebrew, and
skill-marker contracts. Keep read-only planning separate from mutations, require
exact confirmation for releases, stop on review or CI blockers, and update the
bundled portable skill so an agent can drive the complete workflow without
reimplementing shell policy.

**Skills:** `subagents-discipline`, `using-git-worktrees`, `task-breakdown`,
`skill-creator`

**Tech Details:** Go 1.26 standard library, `git`, GitHub CLI, GitHub Actions,
Homebrew, strict SemVer, Agent Skills common subset, TDD, protected PRs, immutable
GitHub releases, exact tap-run correlation.

---

### Task 1: Lock the developer command contract

**Files:**
- Create: `docs/2026-08-27-hextap-dev-release-orchestration-tasks.md`
- Modify: `README.md`
- Modify: `docs/initiative/architecture.md`
- Modify: `docs/initiative/contracts.md`
- Modify: `docs/initiative/decisions.md`
- Modify: `docs/initiative/status.md`

**Steps:**

1. Define `dev status`, `dev validate`, `dev plan`, `dev release`, `dev deploy`,
   and `dev install` as the only v1 developer subcommands.
2. Classify status/plan as read-only, validate as trusted local code execution,
   release/deploy as explicit remote mutation, and install as explicit local
   Homebrew/skill mutation.
3. Require the exact repository identity `SijanC147/hextap-toolkit`, canonical
   `origin`, clean release inputs, protected PR merge, merged-main CI, unused
   strict tag, immutable release, and exact tap CI before completion.
4. Require `--execute` and `--confirm-tag vX.Y.Z` for remote deployment and an
   explicit `--install` or standalone `dev install` for local mutation.
5. State that no dev command reads secret values, bypasses protection, resolves
   review threads, force-pushes, mutates immutable history, or touches proxy or
   service state.
6. Verify the documented command and safety contract is internally consistent.

### Task 2: Add stable SemVer planning

**Files:**
- Modify: `internal/release/metadata.go`
- Modify: `internal/release/metadata_test.go`

**Steps:**

1. Write failing table tests for stable version comparison and patch/minor/major
   bumps, including zero versions, component reset, overflow, leading zeros,
   prereleases, and malformed input.
2. Run the focused tests and require the new cases to fail.
3. Implement exported stable-version parsing, comparison, and bump helpers by
   reusing the existing strict release parser.
4. Run focused normal and race tests and require them to pass.

### Task 3: Expand skill status into inventory

**Files:**
- Modify: `internal/skillinstall/types.go`
- Modify: `internal/skillinstall/status.go`
- Modify: `internal/skillinstall/targets.go`
- Modify: `internal/skillinstall/install_test.go`
- Modify: `internal/brewcli/brewcli.go`
- Modify: `internal/brewcli/skills_test.go`

**Steps:**

1. Write failing tests for installed and available skill versions, actionable
   states, all-target read-only discovery, shared-path deduplication, and JSON.
2. Distinguish `UPDATE_AVAILABLE`, `NEWER_THAN_CLI`, and
   `SAME_VERSION_DIFFERENT` from generic `DIFFERENT` by strict SemVer and hashes.
3. Make `skills status` default to every concrete target for the requested scope;
   keep repeated `--agent` as an optional filter and remove overlap acknowledgement
   from read-only inventory.
4. Print stable human columns and add versioned JSON output for agents.
5. Run focused normal and race tests and verify status makes no writes.

### Task 4: Add safe marker-owned skill upgrades

**Files:**
- Create: `internal/skillinstall/upgrade.go`
- Create: `internal/skillinstall/upgrade_test.go`
- Modify: `internal/skillinstall/types.go`
- Modify: `internal/skillinstall/status.go`
- Modify: `internal/brewcli/brewcli.go`
- Modify: `internal/brewcli/skills_test.go`

**Steps:**

1. Write failing tests for dry-run, current no-op, strict forward upgrade,
   higher-version refusal, same-version/hash mismatch, drift, unmanaged content,
   extra untracked paths, symlink/mode races, and multi-target preflight.
2. Stage the complete new bundle outside the discovery directory, validate all
   bytes and modes, revalidate the existing marker-owned directory, and perform
   a same-filesystem directory transaction without in-place mixed-version files.
3. Preserve an exact recovery directory outside the agent discovery root and
   report it explicitly; never delete or infer ownership by pathname.
4. Add `skills upgrade --agent ... --scope ... --dry-run` and deterministic
   output, with no automatic install, downgrade, or unmanaged repair.
5. Run focused normal/race tests and adversarial mutation hooks.

### Task 5: Implement the developer repository core

**Files:**
- Create: `internal/devcli/types.go`
- Create: `internal/devcli/runner.go`
- Create: `internal/devcli/repository.go`
- Create: `internal/devcli/status.go`
- Create: `internal/devcli/validate.go`
- Create: `internal/devcli/devcli_test.go`
- Modify: `internal/brewcli/brewcli.go`
- Modify: `internal/brewcli/brewcli_test.go`

**Steps:**

1. Add a fakeable bounded command runner with explicit stdin, environment,
   output limits, timeouts, and no shell interpolation.
2. Write failing tests for exact origin fetch/push identity, writable-extra-remote
   refusal, project-root resolution, branch/dirty/ahead state, tool discovery,
   GitHub login identity, and credential-output redaction.
3. Implement `dev status` with deterministic human and JSON output.
4. Implement `dev validate` using the exact CI gates: formatting, normal/race
   tests, vet, trimpath build, Bash syntax, ShellCheck, Actionlint compatibility,
   and diff checks.
5. Verify component tests and a real read-only status/validation run in the
   isolated toolkit worktree.

### Task 6: Implement release planning and release-only orchestration

**Files:**
- Create: `internal/devcli/plan.go`
- Create: `internal/devcli/release.go`
- Create: `internal/devcli/github.go`
- Create: `internal/devcli/plan_test.go`
- Create: `internal/devcli/release_test.go`
- Modify: `internal/brewcli/brewcli.go`
- Modify: `internal/brewcli/brewcli_test.go`

**Steps:**

1. Write failing tests for latest immutable stable release selection and exact
   patch/minor/major plans.
2. Require `--confirm-tag` to equal the computed tag and `--execute` before any
   tag or GitHub mutation.
3. Require clean canonical main identity, successful exact merged-main CI, and
   an unused local/remote tag before creating one annotated tag at the confirmed
   commit and pushing only that tag to `origin`.
4. Wait for the exact tag/head release workflow, require success, and verify the
   final release is non-draft, immutable, stable, and complete.
5. Verify all failure paths stop without moving/deleting tags or releases.

### Task 7: Implement protected PR deployment orchestration

**Files:**
- Create: `internal/devcli/deploy.go`
- Create: `internal/devcli/deploy_test.go`
- Modify: `internal/brewcli/brewcli.go`
- Modify: `internal/brewcli/brewcli_test.go`

**Steps:**

1. Write failing state-machine tests for new and existing PRs, updated-head CI,
   unresolved review blockers, protected merge, merge SHA capture, merged-main
   CI, release handoff, rerun idempotence, and no administrator bypass.
2. Require a clean non-main branch with commits ahead of current `origin/main`.
3. Run full local validation, push only the current branch to origin, create or
   reuse its PR, and wait for required hosted checks.
4. Stop on review comments, unresolved threads, non-clean merge state, or absent
   checks; never auto-resolve feedback.
5. Merge only through the allowed protected method, wait for exact merged-main
   CI, then invoke the confirmed release state machine.
6. Verify deterministic resumability after any remote gate stops the command.

### Task 8: Add optional local Hextap installation

**Files:**
- Create: `internal/devcli/install.go`
- Create: `internal/devcli/install_test.go`
- Modify: `internal/brewcli/brewcli.go`
- Modify: `internal/brewcli/brewcli_test.go`

**Steps:**

1. Write failing tests for dual-Homebrew discovery, exact Formula ownership,
   version/commit verification, Formula test, and no unrelated upgrades.
2. Select the Homebrew installation that owns the current `brew-hextap`, update
   tap metadata, and upgrade only `sean/hextap/hextap` with auto-updates disabled
   for the upgrade command.
3. Require the installed binary to report the exact released version/commit and
   run the Formula test.
4. Upgrade only explicitly selected intact managed skill targets when requested.
5. Never invoke `brew services` or inspect, signal, restart, upgrade, or modify
   another Formula.

### Task 9: Update and validate the portable Hextap skill

**Files:**
- Modify: `skills/bundle.go`
- Modify: `skills/hextap/SKILL.md`
- Modify: `skills/hextap/references/onboarding-and-validation.md`
- Modify: `skills/hextap/references/release-and-recovery.md`
- Modify: `skills/hextap/references/safety-and-failure-routing.md`
- Create: `skills/hextap/references/toolkit-development.md`
- Modify: `internal/skillcontent/skill_content_test.go`
- Modify: `skills/bundle_test.go`

**Steps:**

1. Bump the bundled skill version independently from the CLI version.
2. Add concise triggers and a progressively disclosed toolkit-development
   reference covering status, implementation, validation, PR/review repair,
   SemVer choice, deploy, release recovery, local installation, and final proof.
3. Make the skill invoke deterministic CLI commands instead of reproducing
   Git/GitHub/Homebrew shell sequences.
4. Preserve remote, secret, immutable-history, and local service boundaries.
5. Run the cross-agent skill validator and portable content tests.

### Task 10: Verify and ship the self-hosting feature

**Files:**
- Modify: `.github/workflows/ci.yml` only if the exact dev validation contract
  requires a parity correction.
- Modify: `.github/workflows/hextap-release.yml` only if needed for exact
  developer orchestration correlation.
- Test: complete repository and hosted release path.

**Steps:**

1. Run focused tests, full normal and race tests, formatting, vet, trimpath
   builds, Bash syntax, ShellCheck, Actionlint, skill validation, and diff checks.
2. Run an independent specification and code-quality review and close every
   P0/P1/P2 finding.
3. Build a candidate `brew-hextap` from the feature branch and exercise
   `dev status`, `dev validate`, and `dev plan` against the real repository.
4. Use the candidate `dev deploy` command with an exact minor-version
   confirmation to create/reuse the protected PR, require updated-head CI,
   merge through protection, require merged-main CI, tag, and verify the
   immutable release plus exact tap CI.
5. Use the explicit install phase to upgrade only Hextap and selected managed
   Hextap skill copies; run installed command, Formula, inventory, and skill
   validation checks.
6. Recheck the two proxy PIDs/start times/Cellar links and prove they were not
   changed.
