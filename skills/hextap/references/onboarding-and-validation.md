# Onboarding and validation

Load this reference when creating or checking the local Hextap contract.

## Preflight

1. Resolve the actual Git project root and inspect `origin` without changing it.
2. Confirm `LICENSE`, `README.md`, the release main package, version/commit linker
   symbols, and the exact required-check contexts.
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

Use explicit project metadata and repeat `--required-check` once per protected
status context:

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

# Execute the trusted adapter in a bounded temporary build and verify archives.
brew hextap validate --project PROJECT_ROOT --build

# Check local prerequisites; do not execute the adapter or use GitHub.
brew hextap doctor --project PROJECT_ROOT

# Add bounded, read-only queries pinned to github.com.
brew hextap doctor --project PROJECT_ROOT --online
```

Obtain approval before `--build` when project code execution is not already in
scope. Online doctor requires suitable `gh` authentication but never reads a
secret value or repairs remote state. It checks canonical `main`, immutable
releases, the required secret name, owned active rulesets, exact stable toolkit
tag provenance, and the paired tap Project/Formula content.

## Evidence to retain

Record the CLI version/commit, project commit, workflow toolkit tag/full SHA,
manifest identity, required checks, and each validation result. Never record
credentials. Treat local, hosted source, release, tap, and installed-service
evidence as distinct gates.

For the toolkit's own source repository, use the dedicated developer validation
surface instead of manually reconstructing its CI commands. See
[Toolkit development](toolkit-development.md).
