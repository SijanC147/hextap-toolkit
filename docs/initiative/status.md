# Initiative status and roadmap

Snapshot: 2026-08-25 (Europe/Malta). This is implementation status, not live
publication proof.

## Implemented in this branch

| Area | Status | Evidence boundary |
|---|---|---|
| Strict schema-1 manifest | Complete locally | Exact keys, duplicate/Unicode rejection, path/name limits, schema conformance tests. |
| Deterministic build and verification | Complete locally | Darwin/Linux target control, canonical archives, checksums, live-bounded native execution. |
| Reusable workflow versioning | Complete locally | Caller manifest artifact ID/SHA handoff; five `job.workflow_sha` self-checkouts; queued per-repository concurrency. |
| Release publication/recovery | Complete locally | Exact assets, draft resume, immutable published rerun acceptance, stable-only Homebrew. |
| Tap publisher | Complete locally | Source/tap equality, manifest-declared assets, Formula-only stage, direct-push retry, exact tap CI SHA. |
| `brew-hextap` command | Complete locally | `version`, `onboard`, `validate`, `doctor`; standard-library-only build. |
| Toolkit self-release source | Complete locally | Strict self-manifest, one-binary adapter, same-commit relative caller, four native targets. |
| Local onboarding | Complete locally | Seven artifacts, create-only rooted transaction, idempotent rerun, zero remote mutation. |
| Read-only online doctor | Complete with fake integration | Main, immutable releases, secret name, exact rulesets, stable tag SHA, tap Project/Formula canonical content. |

## External gates before adopter publication

1. Merge this toolkit PR after CI and human review.
2. After the source-side self-release P0 below is merged, satisfy its external
   repository gates, publish a coordinator-controlled stable toolkit release,
   and record the exact tag-to-commit mapping. No toolkit tag or release exists
   as part of this PR.
3. Merge the separately reviewed adopter and tap PRs. Coordinator evidence says
   adopter PR #4 head `dc576a7...5009` and tap PR #5 head `5e21bf3...9a3d`
   currently have equal manifests and green required checks; merge state remains
   external.
4. Run `brew hextap doctor --online` with repository-admin read access and
   confirm immutable releases, `main`, the single secret name, exact source
   rulesets, toolkit pin, and the now-present paired tap registration/Formula.
5. Execute one controlled full release and one `homebrew-only` recovery, then
   verify the immutable release, Formula bytes, direct tap commit, and exact tap
   CI run. Use the next aligned stable adopter tag: existing
   `claude-rc-proxy v0.1.0` predates the current XDG-aware caveats and is outside
   the strict manifest-equality recovery window.

## Source-side P0 before the first toolkit tag

This branch adds the toolkit's own strict source manifest, validating build
adapter, and same-commit thin caller. The one-executable archive contains
`brew-hextap`, `LICENSE`, and `README.md`; `hextapctl` remains built from source
by the reusable workflow and for development. The caller runs `full` only from
a tag and reserves manual dispatch for `homebrew-only` recovery of an existing
stable immutable tag.

Before creating toolkit `v0.1.0`, the coordinator must merge this source
change, enable immutable releases, configure the one required secret, and
confirm the source rulesets. After the immutable source release exists, the
separate tap bootstrap must pair the exact `Projects/hextap.json` registration
with a release-backed `Formula/hextap.rb`; a later `homebrew-only` dispatch
then completes or recovers Formula publication. None of those live or sibling
repository mutations are performed by this branch.

## Follow-up work, not blockers for this PR

- Replace the Actionlint 1.7.12 compatibility gate when an upstream release
  natively supports `concurrency.queue` and `job.workflow_*`.
- Capture the first real online-doctor transcript without secret values.
- Add live hosted-runner evidence for queued invocations, all declared native
  runners, immutable published reruns, and tap CI retention behavior.
- Revisit multi-project/tap-scale orchestration only after a real collision or
  throughput need is observed.
