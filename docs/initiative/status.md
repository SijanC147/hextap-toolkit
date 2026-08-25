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
| Local onboarding | Complete locally | Seven artifacts, create-only rooted transaction, idempotent rerun, zero remote mutation. |
| Read-only online doctor | Complete with fake integration | Main, immutable releases, secret name, exact rulesets, stable tag SHA, tap Project/Formula canonical content. |

## External gates before adopter publication

1. Merge this toolkit PR after CI and human review.
2. Complete the self-release P0 below, then publish a coordinator-controlled
   stable toolkit release and record the exact tag-to-commit mapping. No
   toolkit tag or release exists as part of this PR.
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

## P0 before the first toolkit tag

The release boundary is decided, but self-release wiring is intentionally not
added by this capped PR. Before creating toolkit `v0.1.0`, add and review the
toolkit's own source manifest, build adapter, thin caller workflow, and paired
tap registration/Formula bootstrap. The one-executable archive must contain
`brew-hextap` only. `hextapctl` remains built from pinned source by reusable
workflows and for development; multi-binary archives are out of scope.

## Follow-up work, not blockers for this PR

- Replace the Actionlint 1.7.12 compatibility gate when an upstream release
  natively supports `concurrency.queue` and `job.workflow_*`.
- Capture the first real online-doctor transcript without secret values.
- Add live hosted-runner evidence for queued invocations, all declared native
  runners, immutable published reruns, and tap CI retention behavior.
- Revisit multi-project/tap-scale orchestration only after a real collision or
  throughput need is observed.
