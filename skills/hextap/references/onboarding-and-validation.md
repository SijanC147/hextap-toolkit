# Onboarding and validation

Load this reference when creating or checking the local Hextap contract.

## Preflight

1. Resolve the actual Git project root and inspect `origin` without changing it.
2. Confirm `LICENSE`, `README.md`, the exact required-check contexts, and the
   adapter identity contract. For schema 1 also confirm the Go main package and
   version/commit linker symbols. For schema 2 inspect the pinned Bun runtime,
   frozen install argv, quality/build-preparation argv, and every target asset.
3. Inspect an existing `.hextap.json` before supplying generation flags. An
   existing valid manifest is authoritative; conflicting flags must fail.
4. Confirm the installed command provenance:

   ```sh
   brew hextap --version
   brew hextap help
   ```

An installed stable CLI derives its stable toolkit tag and full commit pin. A
development build requires explicit `--toolkit-version vX.Y.Z` and
`--toolkit-sha FULL_40_CHARACTER_SHA`; do not guess either value.

## New-project plan

For a new Go project, use explicit project metadata and repeat
`--required-check` once per protected status context:

```sh
brew hextap onboard \
  --project PROJECT_ROOT \
  --description "ONE_LINE_DESCRIPTION" \
  --license LICENSE_IDENTIFIER \
  --go-package GO_MAIN_PACKAGE \
  --required-check REQUIRED_CHECK \
  --linux=true \
  --dry-run
```

Add `--formula`, `--binary`, `--repository`, `--version-symbol`, or
`--commit-symbol` only when the derived defaults are not the project contract.
Use `--linux=false` only when Linux assets are deliberately excluded.

For a Bun/TypeScript project, first add and review an authoritative schema-2
`.hextap.json` plus executable `scripts/hextap-build`. Use
`examples/better-ccflare.json` in the pinned toolkit as the field reference.
Then run `brew hextap onboard` without Go generation flags. Hextap preserves the
manifest and custom adapter byte-for-byte and creates only its managed caller,
tap payload, rulesets, and setup document. Schema 2 requires:

- pinned stable `runtime_version` and direct argv commands, never shell strings;
- `bun install --frozen-lockfile` as the install command;
- Darwin arm64/amd64 targets, paired optional Linux, and optional Windows amd64;
- explicit raw/archive names and archive contents;
- a tap-owned `homebrew.formula_profile` plus `service_enabled`, not copied Ruby;
- exact normalized version and commit embedded in every target executable.

Review the complete lexical plan. `CREATE` means absent managed content will be
created, `UNCHANGED` means exact managed bytes and mode already exist, and
`VALIDATED` means an executable project-owned adapter is retained. Any conflict
is a stop condition, not permission to replace a file. Apply by rerunning the
identical reviewed command without `--dry-run`.

Onboarding creates or preserves the strict manifest, build adapter, pinned thin
caller, exact tap-registration payload, main/tag ruleset bodies, and
`.hextap/SETUP.md`. It makes no remote changes.

## Validation ladder

Run each rung separately so its trust boundary remains visible:

```sh
# Read local files; do not execute the project adapter or use GitHub.
brew hextap validate --project PROJECT_ROOT

# Execute trusted profile preparation and the adapter in a bounded temporary
# build, then verify the exact asset set and one matching native executable.
brew hextap validate --project PROJECT_ROOT --build

# Check local prerequisites; do not execute the adapter or use GitHub.
brew hextap doctor --project PROJECT_ROOT

# Add bounded, read-only queries pinned to github.com.
brew hextap doctor --project PROJECT_ROOT --online
```

For a schema-2 `brew hextap validate --build`, the coordinator creates one
private temporary Bun runtime cache, preserves it across profile preparation
and adapter build, removes it afterward, and restores the caller environment.
Do not invent or persist a cache variable for this user-facing validation path.
The lower-level `hextapctl release profile --phase build` interface requires an
explicit cache because its caller owns the following build boundary.

Obtain approval before `--build` when project code execution is not already in
scope. Schema-2 build validation runs the install and preparation argv before
the adapter. Doctor requires `go` for schema 1 and the pinned runtime (`bun`)
for schema 2. Online doctor requires suitable `gh` authentication but never reads a
secret value or repairs remote state. It checks canonical `main`, immutable
releases, the required secret name, owned active rulesets, exact stable toolkit
tag provenance, and the paired tap Project/Formula content.
Schema 1 requires exact rendered Formula bytes. Schema 2 validates the class
and architecture metadata locally while the tap-owned profile gate remains
authoritative for service, caveats, tests, comments, and formatting.

## Evidence to retain

Record the CLI version/commit, project commit, workflow toolkit tag/full SHA,
manifest identity, required checks, and each validation result. Never record
credentials. Treat local, hosted source, release, tap, and installed-service
evidence as distinct gates.

For Bun cross-compilation, require the actual runtime to equal the manifest pin.
Build preparation compiles a private probe for every declared target into the
explicit `BUN_INSTALL_CACHE_DIR`. The reusable Ubuntu build then executes the
adapter as the runner user inside a root-created network namespace. Toolkit CI
must prove an empty cache fails there and the warmed five-target matrix passes;
absence of that hosted proof is a release blocker.

For the toolkit's own source repository, use the dedicated developer validation
surface instead of manually reconstructing its CI commands. See
[Toolkit development](toolkit-development.md).
