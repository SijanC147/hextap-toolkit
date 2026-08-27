# Release and recovery

Load this reference for source/tap coordination, tags, releases, or recovery.

## Preconditions before a tag

Require all of the following:

- Onboarding artifacts are reviewed and merged to canonical `main` through the
  protected PR path; required hosted checks and merged-main CI are green.
- The caller pins an exact stable toolkit tag comment and the corresponding full
  40-character commit, never `@main` or a floating major version.
- The project manifest, build adapter, thin caller, tap payload, and owned
  ruleset payloads pass local validation; the authorized build smoke passes.
- The source repository uses `main` as default, immutable releases are enabled,
  exact owned rulesets are active, and only the required secret name exists for
  Hextap publication.
- The tag target is the intended commit reachable from canonical `main`, the
  worktree is clean, Git remotes are safe, and the strict tag is unused.
- For an already registered project, the complete tag-source manifest is
  semantically equal to `Projects/FORMULA.json`, and the matching Formula is
  canonical. Run online doctor immediately before release.

Do not create, move, or recreate a tag until every applicable precondition is
proved. Use strict `vX.Y.Z` for stable releases and strict SemVer prerelease tags
such as `vX.Y.Z-rc.1` for prereleases. Build metadata is unsupported.

## Full release behavior

The generated tag-triggered caller selects `full`. It validates the resolved tag
source, runs source quality, builds every declared native target, verifies the
exact deterministic assets, creates or resumes a draft, attests assets, and
publishes one immutable GitHub release.

Schema-2 Bun releases use the manifest-pinned Bun version, execute only the
project-owned direct argv phases, and require tracked source to remain unchanged
after quality and after building. The complete asset graph may include raw
Linux executables, raw plus single-binary Darwin archives, and optional raw
Windows amd64 `.exe`; Windows receives PE32+ structural verification and native
`--version` execution. Every raw/archive representation of a target must contain
the same executable bytes.
Before the adapter boundary, Hextap compiles a private probe for every declared
target into a fresh dedicated Bun runtime cache. The reusable Ubuntu build runs
the complete adapter matrix inside a network namespace as the original runner
user. Do not release when the CI proof for empty-cache failure and warmed-cache
offline success is absent or skipped.

- A prerelease publishes an immutable GitHub prerelease and intentionally skips
  Homebrew. A missing Formula update is success, not a recovery condition.
- A stable release may publish Homebrew only after all source/release gates pass.
  The publisher may change only Formula URL/SHA metadata and must correlate tap
  CI to the exact direct-push commit.
- A rerun may accept an existing published release only when its prerelease state,
  exact asset set, bytes, and attestations match. Otherwise stop; never delete or
  replace the release.

## First registration bootstrap

An unregistered project's first stable release can correctly reach an immutable
source release and then stop at the tap registry gate. Preserve that release.

1. Verify the immutable release and its `SHA256SUMS`.
2. For schema 1, use the trusted pinned toolkit and real release URLs/checksums
   to render the exact Formula. For a schema-2 tap-owned Formula profile, pair
   the manifest with the already reviewed tap template; never render or coerce
   its service/caveats/tests from source. Never invent a checksum.
3. Open one protected tap PR containing both the byte-exact
   `Projects/FORMULA.json` registration and `Formula/FORMULA.rb`.
4. Require whole-tap PR CI, merge through protection, and require tap-main CI.
5. Dispatch the generated source workflow for the same existing stable tag; its
   manual path selects `homebrew-only`.

## Stable Homebrew recovery

Use `homebrew-only` only when an immutable stable source release already exists
and its exact tag manifest still equals the current tap registration. Recovery
must not rebuild archives, mutate GitHub release assets, create a replacement
tag, or loosen Formula checks. It re-verifies the release and either publishes
the metadata-only Formula update or reports it already current.

Recovery is intentionally windowed: any registry evolution closes recovery for
an older tag whose complete manifest differs. Resolve the registry/source policy
for a future release; do not weaken equality or rewrite history.

## Completion proof

Require the protected source merge, tag-to-main identity, immutable release and
attestations, stable/prerelease classification, exact Formula bytes when stable,
tap commit and exact correlated tap CI, plus installed consumer/service proof
when that local mutation has separately been approved.

For Hextap's own repository, prefer the confirmed `brew hextap dev deploy` or
`dev release` state machine described in
[Toolkit development](toolkit-development.md). It preserves these same release
and recovery gates and stops before unresolved PR feedback.
