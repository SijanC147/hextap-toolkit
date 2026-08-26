---
name: hextap
description: "This skill should be used when onboarding a GitHub project to Hextap, validating Hextap files or release builds, checking remote release readiness, coordinating tap registration, cutting stable or prerelease releases, or recovering stable Homebrew publication."
compatibility: "Requires Homebrew and the brew hextap external command."
metadata:
  hextap-skill-version: "1.0.0"
---

# Hextap

Operate Hextap through the deterministic `brew hextap` interface and the
repository-owned artifacts it creates. Keep agent-specific behavior out of the
release contract.

## Trigger examples

- "Onboard this Go CLI into Hextap."
- "Validate this Hextap project locally without network access."
- "Smoke-test its release archives."
- "Check whether this repository is ready for a Hextap release."
- "Cut an RC or stable release, or recover the Homebrew step."
- "Why did Hextap stop at the tap-registration gate?"

## Non-negotiable boundaries

1. Run `brew hextap --version` and `brew hextap help` before relying on a
   command surface. Never substitute an unpinned checkout for an installed
   stable CLI without saying so.
2. Treat `validate` and default `doctor` as read-only. Treat `validate --build`
   as execution of trusted project code. Treat `doctor --online` as bounded,
   read-only GitHub access.
3. Verify Git remotes before any push or tag action. Require the writable remote
   to be the intended owned repository. Keep third-party upstreams fetch-only
   with a disabled push URL; never push to an upstream repository.
4. Never read, print, copy, or persist secret values. Preserve the single source
   secret-name contract and never use inherited workflow secrets.
5. Never install, upgrade, restart, stop, signal, or reconfigure a local service
   or proxy without fresh, explicit approval. Hextap release readiness does not
   imply permission to alter a running installation.
6. Stop before owner-controlled remote mutations unless the request expressly
   authorizes them. This includes rulesets, secrets, tags, releases, tap writes,
   merges, and direct pushes.

## Core workflow

1. Inspect the project root, clean Git state, canonical origin, current branch,
   existing `.hextap.json`, and required CI check names.
2. For a new project, run `brew hextap onboard --dry-run` with complete metadata
   and exact required checks. Review every `CREATE`, `UNCHANGED`, or `VALIDATED`
   entry. Apply the same command without `--dry-run` only when local writes are
   in scope. Never overwrite a conflict. Follow
   [Onboarding and validation](references/onboarding-and-validation.md).
3. Run the validation ladder in order: offline structural validation, explicit
   build/archive smoke when authorized, local prerequisite doctor, then online
   doctor when authenticated network checks are needed.
4. Review `.hextap/SETUP.md` and the exact generated artifacts. Coordinate the
   source and tap repositories as separate protected changes. Pair initial tap
   registration with its release-backed Formula; never publish registration
   JSON alone or invent checksums.
5. Satisfy every tag precondition before creating an immutable tag. Distinguish
   stable from prerelease behavior and use `homebrew-only` solely to recover an
   existing stable immutable release. Follow
   [Release and recovery](references/release-and-recovery.md).
6. Require hosted source CI, protected-PR evidence, exact release assets, tap CI
   at the published commit, and merged-main CI where applicable before reporting
   completion. Local success alone is not release proof.

## Failure routing

Fail closed and preserve evidence. Do not delete or replace an immutable release,
move a tag, force-push, hand-edit generated policy, or paper over source/tap
drift. Route the exact diagnostic through
[Safety and failure routing](references/safety-and-failure-routing.md).

## References

- [Onboarding and validation](references/onboarding-and-validation.md) - Local,
  offline, build, and online-doctor command patterns.
- [Release and recovery](references/release-and-recovery.md) - Protected release,
  tap bootstrap, stable/prerelease, and recovery sequence.
- [Safety and failure routing](references/safety-and-failure-routing.md) - Secret,
  upstream, runtime, and failure-specific boundaries.
