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
schema is not expanded to multiple binaries in this PR.

## D-009: Exact Actionlint schema-lag handling

Status: accepted until upstream release support lands.

Current GitHub documentation supports `concurrency.queue: max` and the
`job.workflow_*` identity fields. Latest released Actionlint 1.7.12 predates
both and has open upstream work for them. The repository does not use a broad
ignore: its checker accepts either a clean result or exactly one queue and five
workflow-SHA diagnostics, failing on any other output.

Authoritative references: [GitHub concurrency queues](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency),
[GitHub reusable-job workflow identity](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#example-usage-of-job-context-workflow-identity),
and [Actionlint queue schema issue #680](https://github.com/rhysd/actionlint/issues/680).
