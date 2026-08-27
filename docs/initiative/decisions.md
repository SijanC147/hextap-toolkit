# Initiative decisions

## D-001: One strict manifest identity

Status: accepted.

The resolved source tag manifest is validated once, carried by artifact ID and
content SHA-256, and required to be semantically equal to the private tap
registration. No toolkit-owned per-project example participates in a caller
release. Caveats are part of equality and receive no special normalization.
Schema 1 does not add field exceptions or versioned registry snapshots.

## D-002: Immutable workflow code

Status: accepted.

External callers pin the full commit SHA of a stable toolkit release. Every
called job checks out co-located toolkit code at `job.workflow_sha`. Mutable
`@main` is forbidden. A stable version comment supplies human provenance
without weakening execution identity.

## D-003: Immutable artifacts and queued publication

Status: accepted.

Manifest and release artifacts are uploaded once, passed by artifact ID, and
digest-checked. Source releases serialize per caller repository with
`queue: max` and are never canceled in progress. Published reruns succeed only
for exact immutable assets with valid attestations.

## D-004: One source secret

Status: accepted.

`OP_SERVICE_ACCOUNT_TOKEN` is the only configured source secret. It loads the
tap credential from 1Password only inside the Homebrew job. Secret inheritance,
embedded tap credentials, and generated secret values are rejected.

## D-005: Stable-only Homebrew, explicit recovery

Status: accepted.

Prereleases may publish to GitHub but never update Homebrew. Stable
`homebrew-only` recovery consumes an already immutable verified release; it
does not rebuild or mutate GitHub assets. Recovery is supported only while the
requested tag's complete manifest equals the current tap registration. A
registry change may close the window for historical tags, which then fail
explicitly before Formula mutation.

## D-006: The tap is the Formula registry

Status: accepted.

`Projects/<formula>.json` and its matching Formula are tap-owned. Initial
registration is a paired owner-reviewed PR. Routine releases update only the
four Formula metadata values and push tap `main` directly with exact-SHA CI
correlation.

## D-007: Local onboarding, remote owner control

Status: accepted.

`brew hextap onboard` creates only absent local files after a full preflight.
It emits reviewable ruleset, secret, and tap instructions but never applies
them. `doctor --online` is bounded and read-only; it does not repair drift.

## D-008: One executable in toolkit v0.1.0

Status: accepted.

The initial toolkit archive and future Formula install `brew-hextap`, exposing
`brew hextap`. `hextapctl` remains an internal/source-built engine. The archive
schema remains the existing one-binary contract.

## D-009: Exact Actionlint schema-lag handling

Status: accepted until upstream release support lands.

Current GitHub documentation supports `concurrency.queue: max` and the
`job.workflow_*` identity fields. Latest released Actionlint 1.7.12 predates
both and has open upstream work for them. The repository does not use a broad
ignore: its pinned checker must return nonzero with exactly one queue and six
workflow-SHA diagnostics. Clean/no-op, missing, extra, changed, or differently
versioned results fail. A future Actionlint upgrade must revise the expectation
explicitly.

Authoritative references: [GitHub concurrency queues](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency),
[GitHub reusable-job workflow identity](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#example-usage-of-job-context-workflow-identity),
and [Actionlint queue schema issue #680](https://github.com/rhysd/actionlint/issues/680).

## D-010: Deterministic self-development with explicit authority

Status: accepted.

The installable `brew-hextap` binary owns one toolkit-specific `dev` state
machine for repository inventory, local validation, SemVer planning, protected
PR deployment, release-only recovery, and local Hextap installation. Agents use
this interface rather than reconstructing Git/GitHub/Homebrew shell policy.

Status/plan are read-only. Validation executes trusted toolkit source. Remote
mutation requires `--execute` and the exact computed tag, never bypasses review
or branch protection, and delegates final asset/tap publication to the existing
immutable release workflow. Local installation is separately explicit and may
change only Hextap and selected marker-owned Hextap skill copies.

## D-011: Versioned runtime profiles and tap-owned Formula profiles

Status: accepted.

Schema 1 remains the exact Go four-target contract. Schema 2 is additive and
uses a pinned Bun runtime, direct argv command phases, and explicit target
artifacts. Darwin targets remain required for Homebrew, Linux remains paired,
and Windows amd64 is optional. One adapter invocation creates one executable;
the toolkit derives declared raw/archive representations and verifies their
identity. Windows executables must be PE32+ amd64 and pass native version/commit
execution.

Schema-2 Formula profiles are update-only: source/tap manifests carry the
profile name and service-enabled status, but the tap owns service, caveats,
tests, comments, and formatting in a reviewed
`packaging/<formula_profile>.rb.tmpl`. Publication requires exact byte equality
with that template rendered using current canonical metadata and renders the
new Formula solely from its four Darwin URL/SHA tokens. Project preparation must
prefetch pinned Bun cross-target runtimes into an explicit dedicated cache.
The reusable Ubuntu build runs the adapter as the original runner user inside
a root-created network namespace. Hosted CI must prove an empty cache fails and
the warmed five-target matrix succeeds before publication.
