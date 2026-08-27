# Cross-repository contracts

## Ownership matrix

| Owner | Owned state | Toolkit expectation |
|---|---|---|
| `SijanC147/hextap-toolkit` | Schema, `hextapctl`, `brew-hextap`, reusable workflow, publisher scripts | Versioned as one reviewed commit; reusable jobs self-check out that commit. |
| Adopter source repository | `.hextap.json`, build adapter, caller workflow, `main`, stable tags, rulesets, `OP_SERVICE_ACCOUNT_TOKEN` secret name | Manifest repository must equal the caller; tags must resolve to canonical `main`; adapter is trusted project code. |
| `SijanC147/homebrew-hextap` | `Projects/<formula>.json`, `Formula/<formula>.rb`, `.github/workflows/tests.yml`, direct `main` history | Project JSON is the canonical Formula registry; Formula metadata changes are validated and CI-bound to the exact push SHA. |
| Coordinator / repository owner | Authorization for merges, tags/releases, local installation, immutable-release setting, secret entry, ruleset application, tap registration, and recovery | Reviews exact deltas and may delegate deterministic authorized mutations to `brew hextap dev`. |

## Manifest identity

- Schemas `1` and `2` are exact and case-sensitive. Schema 1 preserves the Go
  Darwin/Linux archive contract. Schema 2 adds a pinned Bun command profile,
  explicit raw/archive targets, optional Windows amd64, and a tap-owned Formula
  profile. Duplicate keys, case-fold aliases,
  malformed/lossy Unicode, unknown/missing keys, unsafe paths, unsafe Ruby
  values, and filesystem-overlong names fail closed.
- The source manifest copied from the resolved tag is the release input.
- The tap registration must parse to the same complete JSON value. Equality is
  not limited to repository, assets, or service fields; caveats are included
  and receive no normalization or exception.
- The registration destination is exactly `Projects/<formula>.json`, so the
  filename must equal `formula.name`.
- Tap registration cannot land alone. The same tap PR must contain
  `Formula/<formula>.rb`, and its first semantic declaration must be
  `class <formula.class> < Formula`. Tap CI fails a missing or mismatched
  Formula.

The first adopter bootstrap is therefore explicit: publish and verify the
immutable source release, prepare the paired tap Project/Formula change using
the real release URLs and checksums, merge that separately reviewed tap PR,
then run stable `homebrew-only` recovery. The toolkit never invents placeholder
checksums.

## Workflow versioning

- Production callers pin a full 40-character toolkit commit SHA associated
  with an exact stable `vX.Y.Z` release. They do not use `@main` or a floating
  major tag.
- `job.workflow_sha`, not caller-oriented `github.workflow_sha`, pins all code
  co-located with the called reusable workflow.
- GitHub currently documents both `job.workflow_sha` and
  `concurrency.queue: max`. Actionlint 1.7.12 does not yet model either field;
  `scripts/check-actionlint.sh` requires that pinned version to fail with
  exactly the reviewed schema-lag set. A clean/no-op result also fails.
- Manifest schema changes require a toolkit release. Existing stable toolkit
  releases and their exact-SHA callers remain immutable.
- Schema-2 command argv is executed directly without a shell. The reusable
  workflow selects Bun only from the sealed manifest and pins both setup Action
  and runtime version.
- Schema-2 build preparation requires a dedicated `BUN_INSTALL_CACHE_DIR`,
  prefetches each declared Bun target, and passes that cache through the
  sanitized adapter environment. The hosted Ubuntu adapter build runs as the
  runner user inside a root-created network namespace.

## Release and Homebrew modes

- Tags and normalized versions use strict SemVer without build metadata.
- Full mode accepts stable and prerelease tags. Source quality and every
  declared native target must pass before publication. Schema-2 tagged source
  must remain unchanged after quality and after asset production.
- GitHub release assets are an exact deterministic set. Existing published
  releases are accepted on rerun only when immutable, byte-identical,
  prerelease-matched, and attestation-verifiable.
- Homebrew runs only for stable releases. `homebrew-only` accepts only an
  existing stable immutable release whose tag manifest still equals the
  current tap registration in full.
- Recovery is therefore windowed by registry evolution. Schema 1 has no field
  exceptions and no versioned registry snapshots. An older tag with different
  caveats or any other field fails explicitly with `tap/source manifest
  mismatch` before Formula mutation.
- Formula updates preserve every nonmetadata byte and change only the two URLs
  and two SHA-256 values after validating the supported metadata structure.
- A tap-owned Formula profile cannot be rendered from source. Its profile name
  and service-enabled status remain in the equal source/tap manifest while the
  tap owns service, caveats, tests, comments, and formatting. Publication binds
  the Formula to the reviewed regular, non-symlink
  `packaging/<formula_profile>.rb.tmpl`, requires exact current-render bytes,
  and renders only the four canonical Darwin URL/SHA tokens from that template.
- Tap pushes are direct, non-force updates to `main`; non-fast-forward races
  retry from a fresh clone, while authorization or ruleset failures stop with
  the real diagnostic.

## One-secret boundary

The source repository configures exactly one Actions secret:
`OP_SERVICE_ACCOUNT_TOKEN`. It is passed explicitly to the reusable workflow,
is visible only to the 1Password loading step in the Homebrew job, and loads
the tap credential from `op://CICD/GH_PAT/credential`. The tap credential is
not stored in the source repository, generated files, command arguments, logs,
or toolkit memory. `secrets: inherit` is forbidden.

## Live-operations boundary

Default Hextap commands generate local payloads or perform read-only queries.
The toolkit-specific `dev` state machine may push its feature branch, merge
through the protected path, create one confirmed unused tag, and trigger the
existing release workflow only when the caller passes `--execute` and the exact
computed `--confirm-tag`. It never applies admin/auto bypass, resolves review
threads, sets secrets/rulesets, force-pushes, or mutates immutable history.

`dev install` is a distinct local boundary: it upgrades only the Hextap Formula
through its owning Homebrew tree and only named intact managed skill copies. It
never invokes services or changes another package or runtime.

Coordinator-provided cross-stream evidence on 2026-08-25 records adopter PR #4
at `dc576a749a5c6e0dfbd63647b34dbb93f1d75009` and tap PR #5 at
`5e21bf3606605b5f6e0a34978ffa841431a19a3d` as clean/green, with an empty fresh
`jq -S` diff between the source and tap manifests. This repository does not
claim those PRs are merged. That alignment applies to the amended heads, not to
the already published `claude-rc-proxy v0.1.0` tag: the historical tag predates
the XDG-aware caveat change and is outside the current `homebrew-only` recovery
window. Recovery evidence must use the next aligned stable tag.
