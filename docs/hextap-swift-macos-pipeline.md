# Hextap pipeline for a Swift macOS app

Instructions for the hextap-toolkit agent. a-bar is the reference app. This document does not change a-bar, the toolkit, or the tap.

Two kinds of statement appear below.

- **Fact.** Something read from a file or printed by a command named in the same paragraph. Do not treat a fact as permission to edit.
- **Proposal.** A spec shape that does not exist in Hextap 0.7.3. Implement it in the toolkit, with tests, before any a-bar manifest is written.

Do not push, tag, or open a pull request against `Jean-Tinland/a-bar`. Sean's fork is `https://github.com/SijanC147/a-bar`. If a later task authorizes a push of a-bar, that fork's `main` is the only push target. This handoff does not authorize that push, an onboard apply, a release, or a tap write.

## Goal

Add a first-class Hextap build pipeline for a Swift macOS app built with `xcodebuild`. a-bar is the reference: an Xcode app target that produces `a-bar.app`, a unit-test bundle with no `TEST_HOST`, and a local-only copy of the built app into the checkout. CI must keep the app in DerivedData.

Non-goals for the toolkit agent while doing this work:

- Do not publish a GitHub release, create a tag, or upload checksums.
- Do not register a-bar in `SijanC147/homebrew-hextap`.
- Do not notarize. a-bar does not notarize today (fact, below).
- Do not add an `upstream` remote. None exists in this checkout.
- Do not assume a CLI `--version` contract. No Swift source in this tree contains `--version`.

## Facts: this a-bar checkout

Command `git rev-parse --abbrev-ref HEAD` and `git rev-parse HEAD` in `/Users/seanbugeja/Code/a-bar`: branch `main`, commit `8e97575aced513686e4f20bdce2e7da3446899f9`. `git status --porcelain` was empty after the reads for this document. `git remote -v` shows only `origin`, fetch and push both `https://github.com/sijanc147/a-bar`. There is no remote named `upstream` and no remote URL containing `Jean-Tinland`.

The fork the user named is `https://github.com/SijanC147/a-bar`. The stored origin URL uses a lowercase owner (`sijanc147`). That case difference is a Hextap gate; see the gap section.

## Facts: how a-bar is built, tested, versioned, signed, and released

### Project, scheme, deployment, Swift

`a-bar.xcodeproj/project.pbxproj`:

- Target `T1000001` / `a-bar`, product type `com.apple.product-type.application`.
- Target `T1000002` / `a-barTests`, product type `com.apple.product-type.bundle.unit-test`. Its build phases are `S1000002` (Sources) and `F1000002` (Frameworks). The string `TEST_HOST` does not occur in this file.
- Target `T1000003` / `CopyAppToCheckout`, a `PBXAggregateTarget`. Its shell phase runs `"$SRCROOT/scripts/copy-app-to-checkout.sh" --apply`. Configurations `Y1000005` (Debug) and `Y1000006` (Release) set `CODE_SIGNING_ALLOWED = NO`, `ENABLE_USER_SCRIPT_SANDBOXING = NO`, and `SKIP_INSTALL = YES`.
- App target Debug and Release (around lines 1156–1207): `MACOSX_DEPLOYMENT_TARGET = 13.0`, `SWIFT_VERSION = 5.0`, `MARKETING_VERSION = 1.6.0`, `CODE_SIGN_STYLE = Automatic`, `DEVELOPMENT_TEAM = ""`, `ENABLE_HARDENED_RUNTIME = YES`, `CODE_SIGN_ENTITLEMENTS = "a-bar/a-bar.entitlements"`, `PRODUCT_BUNDLE_IDENTIFIER = "com.jeantinland.a-bar"`, `INFOPLIST_FILE = "a-bar/Info.plist"`, `GENERATE_INFOPLIST_FILE = YES`.
- Test target Debug and Release (around lines 1215–1243): `MARKETING_VERSION = 1.0`, `PRODUCT_BUNDLE_IDENTIFIER = "com.jeantinland.a-barTests"`, same deployment target and Swift version, `CODE_SIGN_STYLE = Automatic`, empty development team. No entitlements line in those test settings.
- Project-level `CURRENT_PROJECT_VERSION = 1` appears once in the extracted build settings.

`a-bar.xcodeproj/xcshareddata/xcschemes/a-bar.xcscheme`:

- `LastUpgradeVersion = "2600"`.
- Build action includes both `a-bar` (`T1000001`, product `a-bar.app`) and `CopyAppToCheckout` (`T1000003`), all build flags YES (testing, running, profiling, archiving, analyzing).
- Post-action shell script, title `Copy a-bar.app to checkout`: `/bin/zsh "$SRCROOT/scripts/copy-app-to-checkout.sh" --apply`, environment buildable `T1000001`.
- `TestAction` uses configuration Debug and testable `a-barTests` (`T1000002`).
- `LaunchAction` uses Debug. `ProfileAction` and `ArchiveAction` use Release.

`a-bar/Info.plist`:

- `CFBundlePackageType` is `APPL`. `LSUIElement` is true. `LSMinimumSystemVersion` is `$(MACOSX_DEPLOYMENT_TARGET)`.
- `CFBundleShortVersionString` is the literal `1.0.0`. `CFBundleVersion` is the literal `1`. These are not `$(MARKETING_VERSION)` or `$(CURRENT_PROJECT_VERSION)`.
- `NSPrincipalClass` is `NSApplication`. Usage strings exist for Apple Events, calendars, Bluetooth, and location. `NSAppleScriptEnabled` is true.

`AGENTS.md` states the test command and the membership rule:

`xcodebuild test -project a-bar.xcodeproj -scheme a-bar -destination 'platform=macOS'`

It also says `a-barTests` has no `TEST_HOST` and uses no `@testable import`: the test bundle recompiles a whitelist of production sources. A new test file, and every production file it depends on, must be in the `S1000002` Sources phase. Run `./scripts/check-test-membership.sh` after adding a test file.

`scripts/check-test-membership.sh` implements that check. It scans `a-barTests/*.swift` against filenames in the `S1000002` Sources phase of `a-bar.xcodeproj/project.pbxproj`. A test file on disk that is not a member fails. A `*Tests.swift` or `*Fixtures.swift` member that is not on disk fails. The script's own comment says a missing membership still lets the suite report success.

On this Mac, `xcodebuild -version` printed `Xcode 27.0` / `Build version 27A266a`, and `swift --version` printed Apple Swift 6.4 (`swiftlang-6.4.0.34.1`). That is the machine toolchain. The project setting remains `SWIFT_VERSION = 5.0`. Do not treat 6.4 as the project's declared version.

### Local app copy versus CI

`scripts/copy-app-to-checkout.sh` is zsh. Dry-run is the default. `--apply` removes `$SRCROOT/a-bar.app` and copies `$BUILT_PRODUCTS_DIR/a-bar.app` there with `/usr/bin/ditto`. It exits 0 without copying when `CI` or `GITHUB_ACTIONS` is non-empty, and when `ACTION` is `clean`. It refuses a destination that is not exactly `$SRCROOT/a-bar.app`.

The scheme post-action and the aggregate target both pass `--apply`. A local `xcodebuild` of the `a-bar` scheme therefore copies the app twice when those actions run. CI must not receive a checkout-root copy; the script no-ops when those environment variables are set.

`.gitignore` ignores `/DerivedData/`, `/Build/`, `*.xcarchive`, and `/a-bar.app` (root only). The comment on `/a-bar.app` says the signed app copied to the checkout root is ignored and the rest of DerivedData stays outside the repo.

A successful local product is the built `a-bar.app` bundle (scheme buildable name `a-bar.app`). The checkout-root copy is an extra local convenience, not the CI artifact. A successful CI product stays in the runner's DerivedData. This handoff did not run `xcodebuild build` or `xcodebuild test`.

### Tests and badges on GitHub

`.github/workflows/tests.yml`, workflow name `Tests`:

- Triggers: push to `main`, tags `v*`, and `pull_request`. Permissions `contents: read` on the workflow. Concurrency group `tests-${{ github.ref }}` cancels in progress.
- Job id `test`, display name `Unit tests`, `runs-on: macos-15`.
- Steps: `actions/checkout@v7.0.1` with `fetch-depth: 0`; `./scripts/check-test-membership.sh`; then `xcodebuild test -project a-bar.xcodeproj -scheme a-bar -destination 'platform=macOS' -enableCodeCoverage YES` with `CODE_SIGNING_ALLOWED=NO` and a result bundle under `$RUNNER_TEMP`. The step comment says there is no signing identity on a runner.
- On `push` only, `./scripts/generate-badges.sh` writes SVGs and `actions/upload-artifact@v4` uploads them as artifact `badges`.
- Job id `publish-badges`, display name `Publish badges`, `if: github.event_name == 'push'`, `needs: test`, `runs-on: ubuntu-latest`, `contents: write`. It checks out an orphan `badges` branch and force-pushes `coverage.svg`, `logic.svg`, and `version.svg`.

`scripts/generate-badges.sh` takes an xcresult path and an output directory. The version badge comes from `git describe --tags --abbrev=0 --match 'v[0-9]*'`. Coverage is read with `xcrun xccov` for target `a-bar.app`.

No hosted check-run name was queried for this document. The names above are the workflow file's `name:` fields. Do not invent the branch-protection context string (`Unit tests` versus `Tests / Unit tests`) until a real check run is read.

### Version, archive, signing, release

Three version sources exist and they are not the same string:

- App target `MARKETING_VERSION = 1.6.0` and `CURRENT_PROJECT_VERSION = 1` in `project.pbxproj`. `CHANGELOG.md` has a heading `## v1.6.0 - 2026-09-21`.
- `a-bar/Info.plist` hardcodes `CFBundleShortVersionString` `1.0.0` and `CFBundleVersion` `1`.
- `scripts/release.sh` takes `<version> <build_number>` on argv and passes them as `MARKETING_VERSION` and `CURRENT_PROJECT_VERSION` to `xcodebuild archive`. It does not rewrite `Info.plist`.
- Badge version is the latest `v[0-9]*` git tag (`scripts/generate-badges.sh`), not the plist.

`scripts/release.sh` (bash, `set -e`):

- Scheme `a-bar`, project `a-bar.xcodeproj`, configuration Release.
- Archive path `build/a-bar.xcarchive`. It then copies `Products/Applications/a-bar.app` into `build/export/`.
- Archive flags: `CODE_SIGN_IDENTITY="-"`, `CODE_SIGNING_REQUIRED=NO`, `CODE_SIGNING_ALLOWED=NO`.
- After the copy it runs `codesign --force --deep --sign - --options runtime --entitlements a-bar/a-bar.entitlements` and `codesign --verify --deep --strict`.
- It zips with `ditto -c -k --sequesterRsrc --keepParent` to `build/a-bar.zip` and prints `shasum -a 256`.
- The script's printed next steps are manual: create a GitHub release tagged `v$VERSION`, upload the zip, update `appcast.xml`, upload `appcast.xml`. There is no `appcast.xml` in the release script's own work, and no other file matched `appcast` except this script. `CHANGELOG.md` line 181 says Sparkle integration and the updater service were removed. Do not resurrect Sparkle.

`a-bar/a-bar.entitlements` turns the app sandbox off and sets Apple Events, unsigned executable memory, library validation disabled, audio input, network client/server, user-selected files, a System Events Apple Events exception, JIT, and dyld environment variables.

`README.md` (about line 20) says a-bar is not signed or notarized, that users must bypass Gatekeeper, and that notarization is not planned because of the annual cost. Lines 77–83 tell users to clear quarantine with `xattr` because the app is not notarized. `README.md` line 99 says `GPL-3.0 license` and points at `LICENSE`, which is the GNU GPL version 3 text.

No Swift file under the checkout contains the string `--version` (search of `*.swift`, excluding `a-bar.app`). `Info.plist` marks the process as a UI element (`LSUIElement` true), not a command-line tool.

## Facts: Hextap 0.7.3 as installed

Commands run for this document:

- `hextap --version` → `hextap 0.7.3 (commit ff562176a286662bdef9674b896e6d2c4664edbd)`.
- `hextap --help` lists `version`, `status`, `info`, `rollback`, `onboard`, `validate`, `doctor`, `skills`, `dev`, `completion`. Help says status, inventory, default validation, and default doctor are read-only. `validate --build` is not the default; the skill and `validate.go` treat `Build: true` as executing the adapter.
- `hextap info --kind project --json` lists four registered projects, all under the tap `sean/hextap` at `/opt/homebrew/Library/Taps/sean/homebrew-hextap`, revision `a88944409e394d39bf8a8d06b326f9ab51ebdd79`, remote `https://github.com/SijanC147/homebrew-hextap.git`:
  - `better-ccflare`, repo `SijanC147/better-ccflare`, schema 2, manifest `Projects/better-ccflare.json`
  - `claude-peers`, repo `SijanC147/claude-peers-suite`, schema 2, manifest `Projects/claude-peers.json`
  - `claude-rc-proxy`, repo `SijanC147/claude-rc-proxy`, schema 1, manifest `Projects/claude-rc-proxy.json`
  - `hextap`, repo `SijanC147/hextap-toolkit`, schema 1, manifest `Projects/hextap.json`
- The same inventory's `casks` array is empty (`hextap status --json` from the same CLI). There is no Cask registration to copy.
- `hextap status --project /Users/seanbugeja/Code/a-bar --json` warning: `the supplied project's .hextap.json is unavailable or invalid`.
- `ls` of `.hextap.json`, `scripts/hextap-build`, `go.mod`, and `package.json` in the a-bar root: all absent.

Skill `~/.cursor/skills/hextap/SKILL.md` frontmatter: `hextap-skill-version: "1.3.0"`. Its references under `references/` describe onboarding, release, safety, toolkit development, and command inventory. Those references match the 0.7.3 behavior cited below; where they disagree with the Go source at commit `ff562176a286662bdef9674b896e6d2c4664edbd`, the source wins.

Toolkit files below were read from `SijanC147/hextap-toolkit` at that commit (GitHub raw contents). They were not checked out into a working tree.

### Manifest schema

`schema/project-manifest.schema.json` (`$id` `https://github.com/SijanC147/hextap-toolkit/schema/project-manifest.schema.json`):

- Title: Hextap project manifest. Description: schema 1 preserves Go archives; schema 2 adds a pinned Bun profile, explicit targets, optional Windows amd64, and a tap-owned Formula profile. Additional properties are forbidden.
- Required top-level keys: `schema`, `formula`, `release`, `homebrew`.
- `schema` is exactly `1` or `2`.
- `formula` requires `name`, `class`, `description`, `homepage`, `license`, `repository` (`owner`, `name`), `binary`, `assets`.
- `formula.name` matches `^[a-z][a-z0-9]*(?:-[a-z][a-z0-9]*)*$` (so `a-bar` matches this pattern). `class` matches `^[A-Z][A-Za-z0-9]*$`.
- `assets` requires `darwin_arm64` and `darwin_amd64`, each matching a name that ends in `.tar.gz`.
- Schema 1 `release` (`releaseLegacy`) requires `build_script` (a relative path) and `linux` (boolean). It does not allow `profile` or `targets`.
- Schema 2 `release` (`releaseProfileContract`) requires `build_script`, `profile`, and `targets`. `profile.runtime` is the const `"bun"`. `profile.runtime_version` is strict `major.minor.patch`. `profile.install` is one command `{name, argv}`. `profile.quality` is a non-empty array of those commands. `profile.prepare` is an array of commands. Argv items are direct strings, not a shell line.
- Schema 2 targets require `darwin_arm64` and `darwin_amd64`. Optional keys: `linux_arm64`, `linux_amd64`, `windows_amd64`. Each target may set `binary`, `archive` (must end in `.tar.gz`), and `archive_contents` (`binary` or `bundle`).
- `homebrew.macos_only` is the const `true`. `test_args` is a non-empty array of argument tokens.
- Schema 1 homebrew (`homebrewLegacy`) also requires `caveats`. Optional: `service`, `binary_aliases`, `zsh_completion` (path under `completions/_…`).
- Schema 2 homebrew (`homebrewProfile`) requires `formula_profile` and `service_enabled` instead of inline service and caveats.

`examples/claude-rc-proxy.json` is schema 1: `build_script` `scripts/hextap-build`, `linux` true, Darwin `.tar.gz` assets, `test_args` `["--version"]`, and an enabled service block.

`examples/better-ccflare.json` is schema 2: Bun `1.3.14`, `install` argv `bun install --frozen-lockfile`, quality commands for typecheck, test, and dashboard, Darwin archives with `archive_contents` `"binary"`, raw Linux and Windows binary names, `formula_profile` `better-ccflare`, `service_enabled` true. `docs/initiative/contracts.md` says a schema-2 Formula is not rendered from the source manifest; the tap owns `packaging/<formula_profile>.rb.tmpl`.

`internal/onboard/types.go` lists the files onboarding owns:

- `.hextap.json`
- `.github/workflows/hextap-release.yml`
- `.hextap/tap-registration.json`
- `.hextap/rulesets/main.json`
- `.hextap/rulesets/release-tags.json`
- `.hextap/SETUP.md`
- default adapter `scripts/hextap-build`

`supportedOwner` in that file is the exact string `SijanC147`.

### What Go and Bun declare as the build

Generated Go adapter, `internal/onboard/templates.go` `adapterBytes`: a `/bin/sh` script with `set -eu` that requires `HEXTAP_TARGET_OS`, `HEXTAP_TARGET_ARCH`, `HEXTAP_OUTPUT`, `HEXTAP_VERSION`, and `HEXTAP_COMMIT`, then runs:

`CGO_ENABLED=0 GOOS=… GOARCH=… go build -mod=readonly -trimpath -buildvcs=false -ldflags "-s -w -X=<versionSymbol>=$HEXTAP_VERSION -X=<commitSymbol>=$HEXTAP_COMMIT" -o "$HEXTAP_OUTPUT" <go package>`

Default symbols, from `hextap onboard --help`, are `main.version` and `main.commit`. `--go-package` is inferred only when one narrow Go main package is unambiguous; otherwise the flag is required (`internal/onboard/project.go`, `inferGoPackage`).

Schema 2 does not generate that Go script as the description of the build. The manifest's `profile` argv is the install, quality, and prepare contract. The adapter path is still `scripts/hextap-build`, and it must be an executable regular file (`validateAdapter` in `internal/onboard/validate.go`).

`internal/release/build.go` `buildTargets`:

- Schema 1 always builds `darwin/arm64` and `darwin/amd64` bundle archives named by `formula.assets`, plus Linux `arm64` and `amd64` bundle archives named `<formula>-linux-<arch>.tar.gz` when `linux` is true. The executable name is `formula.binary`.
- Schema 2 walks the declared targets. A `binary` artifact is the raw executable. An `archive` is a tar.gz of the binary alone when `archive_contents` is `binary`, or a bundle tar when it is `bundle`.

`artifactBundleArchive` members (`build.go`): the executable mode `0755`, plus `LICENSE` and `README.md` mode `0644`, plus the zsh completion file when set. `archive_contents: "bundle"` does **not** mean an Apple `.app` bundle. It means that tar member set.

`Build` runs the adapter once per target with those environment variables, requires the adapter to write a single regular file at `HEXTAP_OUTPUT`, then writes `SHA256SUMS` (sha256 of each asset, sorted, trailing newline). `internal/formula/render.go` `Render` refuses a manifest whose `formula_profile` is set. For schema 1 it emits `class <Class> < Formula`, Darwin `url`/`sha256` pairs, `bin.install` of the binary, and:

`assert_match version.to_s, shell_output("#{bin}/<binary> <test_args…>")`

`releaseURL` is `https://github.com/<owner>/<repo>/releases/download/v<version>/<asset>`. Checksums are arguments to `Render` (`arm64SHA`, `amd64SHA`); the renderer does not download them. `docs/initiative/contracts.md` says the toolkit never invents placeholder checksums. The first tap PR pairs `Projects/<formula>.json` with `Formula/<formula>.rb` using real release URLs and checksums.

`internal/release/verify.go` `executeVerifiedBinaryWithTimeout` runs the extracted binary as `<path> --version` and requires stdout to be exactly `<binary> <version> (commit <commit>)\n` after a small normalization. That is independent of `homebrew.test_args`.

`validate --build` (`validateBuildSmoke`) creates a private temp directory, for schema 2 runs the Bun profile prepare phase, calls `release.Build` with a smoke version and commit, then `release.Verify` for the host OS/arch when that target is declared. Default `validate` does not set `Build`.

### What `onboard --dry-run` expects

`hextap onboard --help` (this machine): options only. Required for a new Go plan, from the help example and the skill reference `references/onboarding-and-validation.md`: `--description`, `--license`, and one `--required-check` per protected status context, plus `--dry-run` to print actions without writing. `--linux` defaults to true. `--go-package` defaults to inference. `--repository` overrides origin. A stable installed CLI supplies its own toolkit tag and SHA; a development build must be given `--toolkit-version` and `--toolkit-sha`.

Actions are `CREATE`, `UNCHANGED`, and `VALIDATED` (`internal/onboard/types.go`). The skill says a conflict is a stop, not permission to overwrite. Applying is the same command without `--dry-run`. Onboarding writes only those local files. Help safety text: it does not create repositories, rulesets, pull requests, tags, releases, tap entries, installations, or service changes.

The generated caller (`.github/workflows/hextap-release.yml` from `workflowBytes`) is named `Hextap release`. It runs on tags `v*` and `workflow_dispatch`. Job `release` calls `SijanC147/hextap-toolkit/.github/workflows/release-go.yml@<full SHA>` with a comment of the toolkit version. `workflow_dispatch` selects mode `homebrew-only`; a tag push selects `full`. The secret mapping is `OP_SERVICE_ACCOUNT_TOKEN` only, plus `SUBMODULES_TOKEN` when checkout submodules are not false (`templates.go` comments and `docs/initiative/contracts.md`).

Required CI check names are not invented by the toolkit. They are the repeatable `--required-check` strings stored in `.hextap/rulesets/main.json` as `required_status_checks` contexts (`mainRulesetBytes`). a-bar's existing workflow job display names are `Unit tests` and `Publish badges`. `Publish badges` does not run on `pull_request`. Do not pass either string as a required check until a GitHub ruleset or a completed check run shows the exact context.

`docs/initiative/contracts.md` also requires, before a release is called done: hosted source CI, an immutable release, tap CI on `SijanC147/homebrew-hextap` (it names that repo's `.github/workflows/tests.yml`), and merged-main CI. Local success is not release proof. The skill reference `references/release-and-recovery.md` says the same, and says a private repository has no attestation.

## Exact gap for a-bar

Quoted from commands run in `/Users/seanbugeja/Code/a-bar` on 2026-09-27. No onboard command was run. No build was run.

`hextap validate --project /Users/seanbugeja/Code/a-bar` exited 2:

```
error: validate: repository owner "sijanc147" is unsupported; the current publisher contract supports only SijanC147
```

That comparison is `supportedOwner` (`SijanC147`) against the owner parsed from `git config --get remote.origin.url` (`internal/onboard/validate.go` and `parseGitHubOrigin` in `project.go`). Origin is `https://github.com/sijanc147/a-bar`. Validate stops before it reads a manifest, so this error does not by itself prove the missing file. The missing file is a separate fact:

`hextap status --project /Users/seanbugeja/Code/a-bar --json` warning component `local_project`:

```
the supplied project's .hextap.json is unavailable or invalid
```

And the project root has no `.hextap.json`, no `scripts/hextap-build`, no `go.mod`, and no `package.json`.

`hextap info --kind project --name a-bar` is not required to see the registry gap: the unfiltered project inventory contains only `better-ccflare`, `claude-peers`, `claude-rc-proxy`, and `hextap`. a-bar is not registered.

Why the current schemas cannot describe this app, from the files above rather than from a failed onboard:

- Schema 1's generated adapter is `go build` of one main package to one file. There is no Go module.
- Schema 2's `profile.runtime` is only `"bun"`. There is no Bun package and no frozen lockfile.
- Both schemas require the adapter to emit one regular executable file. `release.Verify` then executes that file with `--version` and expects `<binary> <version> (commit <40-char>)\n`. a-bar's product is an `a-bar.app` bundle. No Swift source defines `--version`.
- Release assets must be named `*.tar.gz`. `scripts/release.sh` produces `build/a-bar.zip` containing the `.app`.
- Schema 1 `Render` installs a binary with `bin.install` and tests it with `shell_output`. That is a Formula for a CLI. The installed tap has an empty Cask list, and the manifest schema has no Cask object.
- `homebrew.macos_only` is already `true` for every project, which matches "macOS app", but the rest of the contract is still a cross-compiled single file plus Linux when `linux` is true (the onboard default).

Do not paper over this by writing a fake `scripts/hextap-build` that shells out to `xcodebuild` inside schema 1 or 2. `validate` and `release.Build` would still demand a single executable and a `--version` line. Extending the schema is the task.

## Proposal: schema addition for a Swift macOS Xcode app

This section is a proposal. It is not in `project-manifest.schema.json` at `ff562176`.

Add a new schema constant (do not overload `1` or `2`). A reasonable shape is `schema: 3` with `release.profile.runtime` const `"xcode"`, parallel to Bun's `"bun"`. Keep `additionalProperties: false`. Commands stay `{name, argv}` arrays, never shell strings, matching schema 2.

Suggested fields, mapped to a-bar facts. The toolkit agent chooses the JSON names, but the values for a-bar are not free:

| Concern | a-bar value the spec must be able to state | Do not invent |
|---|---|---|
| Project | `a-bar.xcodeproj` | A Swift package or workspace. This repo has only the xcodeproj. |
| Scheme | `a-bar` | A second scheme. |
| Destination | `platform=macOS` | An iOS or Catalyst destination. |
| Test configuration | Debug, from the scheme `TestAction` | A Release test run. |
| Archive configuration | Release, from the scheme `ArchiveAction` and `scripts/release.sh` | A Debug archive as the release artifact. |
| Local build | `xcodebuild build -project a-bar.xcodeproj -scheme a-bar -destination 'platform=macOS'` is the command `AGENTS.md` implies by using the same project, scheme, and destination for tests. This handoff did not re-run it. | Setting `SYMROOT` or `CONFIGURATION_BUILD_DIR` to the repo root. DerivedData stays outside the repo. |
| Test command | `./scripts/check-test-membership.sh` then the `xcodebuild test` line in `.github/workflows/tests.yml`, including `CODE_SIGNING_ALLOWED=NO` on CI | `@testable import` or a `TEST_HOST`. |
| Signing, CI | `CODE_SIGNING_ALLOWED=NO` | A development team or a keychain on the runner. |
| Signing, release | Archive unsigned (`CODE_SIGN_IDENTITY=-`, signing required and allowed NO), then ad-hoc `codesign --sign - --options runtime` with `a-bar/a-bar.entitlements`, then `codesign --verify --deep --strict` | Notarization, Developer ID, or a non-empty `DEVELOPMENT_TEAM`. README says those are out of scope. |
| Hardened runtime | `ENABLE_HARDENED_RUNTIME = YES` on the app target, and `--options runtime` in `release.sh` | Dropping hardened runtime to make a simpler codesign. |
| Version source | Declare all three and which one the artifact embeds: build setting `MARKETING_VERSION` / `CURRENT_PROJECT_VERSION`, plist literals `1.0.0` / `1`, and release-script argv. Do not pick silently. | Treating `1.6.0` as the bundle short version while `Info.plist` still contains `1.0.0`. |
| Commit identity | Hextap's smoke test wants `<name> <version> (commit <sha>)` on stdout. This app has no such CLI. | Adding a hidden `--version` to a-bar just to satisfy `verify.go`. Change the verifier for app bundles instead, or do not execute `--version` for this runtime. |
| Built product | `a-bar.app` inside `BUILT_PRODUCTS_DIR` / the archive's `Products/Applications/` | Requiring the product to be a Mach-O file at `HEXTAP_OUTPUT`. |
| Checkout copy | `scripts/copy-app-to-checkout.sh` may run on a local scheme build. It must no-op when `CI` or `GITHUB_ACTIONS` is set. | Deleting that script, or teaching CI to copy `a-bar.app` into the repo. |
| Release archive | Today: `build/a-bar.zip` of the `.app` via `ditto -c -k --sequesterRsrc --keepParent`, then sha256. | Silently switching the published name to `.tar.gz` because the current asset pattern requires it. If the new schema allows `.zip`, say so in the schema. If it keeps `.tar.gz`, that is a product change and needs an explicit decision. |
| Formula versus Cask | Current renderer only emits a Formula that `bin.install`s a CLI and matches `version` in `--version` output. A menu-bar `.app` is not that. | Publishing a Cask, a service block, or `test_args: ["--version"]` as if a-bar already had them. The tap's cask list is empty and the schema has no cask type. A Cask can be a later schema, not a silent render. |
| Linux | Onboard's default `--linux=true` is wrong for this app. | Linux archives. |
| License | README identifier `GPL-3.0`. The file is GPL-3. | `MIT`, which the other registered examples use. |
| Repository | `SijanC147/a-bar` once origin's owner string matches `supportedOwner`. | Pointing `formula.repository` at `Jean-Tinland/a-bar`. |
| Required checks | Only contexts read from a real check run or ruleset. Candidates visible in the workflow file are the job names `Unit tests` and, on push only, `Publish badges`. | Guessing `test` because the onboard help example uses `--required-check test`. |
| Secrets | Existing caller maps `OP_SERVICE_ACCOUNT_TOKEN`. Do not read or write the value. | `secrets: inherit`, a tap token in the a-bar repo, or notarization credentials. |
| Owner case | Validate rejected `sijanc147`. | Rewriting the remote as a drive-by. Record it as a precondition: either the toolkit accepts GitHub's case-insensitive owner, or the a-bar origin URL is updated in a reviewed change to `https://github.com/SijanC147/a-bar`. Fetch and push stay on that fork. |

Generated onboarding files should stay the same set (`types.go`), with an Xcode adapter instead of `adapterBytes`'s `go build`. The adapter, if still a script, should pass through `HEXTAP_VERSION` and `HEXTAP_COMMIT` only in ways the Xcode project actually consumes. Today the release script accepts version and build number as argv and does not embed `HEXTAP_COMMIT`.

`archive_contents: "bundle"` must not be reused for `.app`. In 0.7.3 that token means "tar includes LICENSE and README". A new token such as `app` is clearer than overloading `bundle`.

## Acceptance checks

Prove the pipeline against a-bar without a release and without touching upstream. Stop if a step would push, tag, open a PR, write the tap, or onboard without a reviewed dry-run.

1. Toolkit tests, in the hextap-toolkit repo, cover the new schema: reject a Swift manifest that uses `runtime: bun` or a Go `linux: true` block; accept a fixture whose project, scheme, destination, and artifact name are the a-bar values; reject notarization fields if you add them as optional and the fixture leaves them unset. Follow `hextap dev validate` only inside that toolkit checkout, and only when the toolkit skill's development path is the task. Do not point `dev deploy` at a-bar.
2. On a toolkit build that contains the new schema, run `hextap onboard --dry-run` for `/Users/seanbugeja/Code/a-bar` with the description, `GPL-3.0`, repository `SijanC147/a-bar`, and required checks you have actually observed. Read every `CREATE` / `UNCHANGED` / `VALIDATED` line. Do not re-run without `--dry-run` unless a human explicitly asks for the apply. If origin is still lowercase `sijanc147`, either the dry-run fails with the owner error quoted above or your patch accepts that URL. Do not "fix" it by adding a push URL to Jean-Tinland.
3. Structural `hextap validate --project /Users/seanbugeja/Code/a-bar` (no `--build`) after an authorized apply. It must get past the owner check and the missing-manifest warning. It must not execute `xcodebuild`.
4. Build smoke, only with explicit permission to execute project code: `hextap validate --project /Users/seanbugeja/Code/a-bar --build`. The smoke must produce an `a-bar.app` in its private output directory. It must set `CI` or otherwise avoid `scripts/copy-app-to-checkout.sh --apply` writing `/Users/seanbugeja/Code/a-bar/a-bar.app`, because that path is gitignored and a local scheme build runs the copy twice. Do not delete an existing checkout `a-bar.app` as part of the proof. Confirm `codesign --verify` only if the smoke uses the release script's ad-hoc signature; a CI-style unsigned build is valid for the test job and is not a failed release.
5. Checksum proof stays local: `shasum -a 256` of the zip or archive the new schema declares, compared to the `SHA256SUMS` the builder writes. Do not call `formula.Render` with made-up hashes, and do not open a tap PR.
6. Confirm `git remote -v` still has a single `origin` whose push URL is `https://github.com/SijanC147/a-bar` (any owner-case decision from the table above should already have landed). `git remote` must not list `upstream`. `git status` must not show `.hextap` files you did not mean to leave, and must not show `a-bar.app` as a tracked file.
7. Do not dispatch `workflow_dispatch`, push a `v*` tag, or merge. Those are release gates in `templates.go` and `docs/initiative/contracts.md`, and they are outside this handoff.

## Sources

- `/Users/seanbugeja/Code/a-bar` at `8e97575aced513686e4f20bdce2e7da3446899f9`: `a-bar.xcodeproj/project.pbxproj`, `a-bar.xcodeproj/xcshareddata/xcschemes/a-bar.xcscheme`, `a-bar/Info.plist`, `a-bar/a-bar.entitlements`, `AGENTS.md`, `README.md`, `LICENSE`, `CHANGELOG.md`, `.gitignore`, `.github/workflows/tests.yml`, `scripts/release.sh`, `scripts/generate-badges.sh`, `scripts/check-test-membership.sh`, `scripts/copy-app-to-checkout.sh`. Commands: `git rev-parse`, `git status --porcelain`, `git remote -v`, `xcodebuild -version`, `swift --version`, a search of `*.swift` for `--version`.
- Installed skill `~/.cursor/skills/hextap/SKILL.md` (version 1.3.0) and `references/onboarding-and-validation.md`, `references/release-and-recovery.md`, `references/safety-and-failure-routing.md`, `references/toolkit-development.md`, `references/command-discovery-and-inventory.md`.
- Installed CLI: `hextap --version`, `hextap --help`, `hextap onboard --help`, `hextap info --kind project --json`, `hextap status --json`, `hextap status --project /Users/seanbugeja/Code/a-bar --json`, `hextap validate --project /Users/seanbugeja/Code/a-bar`.
- `SijanC147/hextap-toolkit` commit `ff562176a286662bdef9674b896e6d2c4664edbd`: `schema/project-manifest.schema.json`, `examples/better-ccflare.json`, `examples/claude-rc-proxy.json`, `docs/initiative/contracts.md`, `internal/onboard/types.go`, `internal/onboard/project.go`, `internal/onboard/validate.go`, `internal/onboard/templates.go`, `internal/release/build.go`, `internal/release/verify.go`, `internal/formula/render.go`.
- Tap inventory only, not edited: `/opt/homebrew/Library/Taps/sean/homebrew-hextap` as reported by `hextap info`, including `Projects/hextap.json` homepage `https://github.com/SijanC147/hextap-toolkit`.
