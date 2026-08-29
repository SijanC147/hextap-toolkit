# hextap-toolkit

`hextap-toolkit` is the deterministic, dependency-free release, onboarding,
and self-development toolkit for the private Hextap Homebrew tap. `hextapctl` remains the workflow
engine that validates manifests, builds and verifies archives, and renders or
updates Formulae. The separately installable `brew-hextap` executable is the
human-facing `brew hextap` command and is also installed as the shorter direct
`hextap` command. Both names provide conflict-safe local onboarding,
validation, read-only doctor checks, managed agent skills, and protected
toolkit development/release orchestration.

The durable initiative architecture, ownership boundaries, decisions, and
live-gate roadmap are under [`docs/initiative`](docs/initiative/architecture.md).

## Requirements and build

- Go 1.26 or newer
- Standard library only; there are no production dependencies

Build the development binaries:

```sh
go build -o dist/hextapctl ./cmd/hextapctl
go build -o dist/brew-hextap ./cmd/brew-hextap
```

Release builds inject a stable version and source commit:

```sh
go build \
  -ldflags '-s -w -X main.version=0.1.0 -X main.commit=abc1234' \
  -o dist/hextapctl \
  ./cmd/hextapctl

dist/hextapctl version
# hextapctl 0.1.0 (commit abc1234)

go build \
  -ldflags '-s -w -X main.version=0.1.0 -X main.commit=0123456789abcdef0123456789abcdef01234567' \
  -o dist/brew-hextap \
  ./cmd/brew-hextap

dist/brew-hextap --version
# brew-hextap 0.1.0 (commit 0123456789abcdef0123456789abcdef01234567)
```

## Local onboarding

Homebrew exposes `brew-hextap` as `brew hextap` and installs `hextap` as a
symlink to that same executable. It also installs the metadata-generated native
Zsh definition as `_hextap` in Homebrew's `zsh_completion` directory. A stable installed build uses
its normalized linker-injected runtime version and full commit to derive the
workflow pin: runtime `0.1.0` becomes the human provenance tag `v0.1.0` while
the executable continues to report normalized `0.1.0`. Development builds
must receive both pin values explicitly:

```sh
brew hextap onboard \
  --project . \
  --description "A narrow Go command" \
  --license MIT \
  --go-package . \
  --toolkit-version v0.1.0 \
  --toolkit-sha 0123456789abcdef0123456789abcdef01234567 \
  --required-check test \
  --required-check lint \
  --linux=true
```

Use `--dry-run` to print the complete `CREATE` / `UNCHANGED` / `VALIDATED`
plan without writing. Onboarding preflights every target before its first
write and never replaces an existing managed file or valid custom adapter.
For a new Go project it creates the strict schema-1 manifest, fixed Go build adapter, pinned reusable
workflow caller, exact tap-registration payload, two reviewable ruleset API
bodies, and `.hextap/SETUP.md` with the remaining owner-controlled steps.
For a Bun/TypeScript project, add and review an authoritative schema-2 manifest
and executable project-owned adapter first; onboarding preserves both and
creates the same managed caller, registration, ruleset, and setup artifacts
without requiring fake Go package or linker-symbol metadata.
An authoritative existing manifest is preserved byte-for-byte, including a
valid document without a trailing newline, and a custom adapter may contain
arbitrary executable bytes. Final-newline checks apply only to
toolkit-generated managed text. All decoded manifest string metadata is
credential-scanned before planning or copying the exact tap payload; custom
adapter bytes are deliberately exempt because they are neither copied nor
published as metadata.

Apply mode pins the project root before preparation, snapshots every existing
managed target and parent, acquires an exclusive create-only onboarding lock,
and revalidates before publication and success. All file content is prepared
and synced under a private staging directory before create-only hard-link
publication. A noncooperative publication race preserves the competing path
and reports any already-published lexical prefix instead of attempting an
unsafe rollback.

Local validation is read-only unless the explicit build smoke is selected:

```sh
brew hextap validate --project .
brew hextap validate --project . --build
brew hextap doctor --project .
brew hextap doctor --project . --online
```

Every command and nested subcommand supports complete `--help` and `-h`
output. The global build identity is available as `--version` or `-V`, and
every command-scoped long flag has one single-character alias. Generate the
same exhaustive native Zsh definition without installing it with:

```sh
hextap completion zsh
```

Help, flag parsing, and completion share one authoritative command metadata
tree; a traversal test fails if a command, option, shorthand, value, or
description drifts between them.

Default doctor never executes the project adapter and never calls GitHub. It
requires `go` for schema 1 and the pinned profile runtime (`bun` today) for
schema 2.
Online doctor adds bounded, read-only `gh` queries for authentication, `main`,
immutable releases, the required secret name, owned active rulesets, stable
toolkit provenance, and the paired tap registration plus Formula. Rulesets
must be active repository-owned objects whose fetched bodies exactly match the
local normalized policies. Toolkit provenance starts from the exact tag ref
and peels annotated tag objects with bounded cycle/type checks; it never uses
an ambiguous branch-or-tag commit-ish lookup. Doctor never reads a secret
value or repairs remote state. Remote Formula validation fails closed: after
an optional blank/comment/block-comment prelude, the first semantic Ruby line
must be the exact registered top-level Formula class declaration.
Every `gh` call is explicitly pinned to `github.com`; `GH_HOST` cannot redirect
doctor or the generated setup commands. For schema 1, readiness also requires
the entire Formula to equal the deterministic manifest rendering for its
validated current stable URLs and SHA-256 metadata, not merely a matching class
line. For a schema-2 tap-owned profile, doctor validates the class and exact
architecture metadata structure; the tap's own whole-Formula/profile gate
remains authoritative for nonmetadata bytes.

## Agent skill installation

`brew-hextap` embeds one portable Agent Skills package under the stable name
`hextap`. Inspect the reviewed target registry before writing:

```sh
brew hextap skills targets
```

Installations require an explicit agent and scope. User scope writes below the
selected user skill root; project scope resolves the supplied path to its Git
top level before planning any file:

```sh
brew hextap skills install --agent codex --scope user --dry-run
brew hextap skills install --agent codex --scope user

brew hextap skills install --agent claude-code --scope project --project . --dry-run
brew hextap skills install --agent claude-code --scope project --project .
```

Supported targets are `agents`, `codex`, `claude-code`, `cursor`, and the
virtual `all` target. Codex and the portable `agents` target share
`.agents/skills`; Claude Code uses `.claude/skills`; Cursor's native root is
`.cursor/skills`. Cursor also discovers the shared and Claude roots, so
multi-root selections fail closed unless the caller explicitly acknowledges
overlapping discovery:

```sh
brew hextap skills install \
  --agent all \
  --scope user \
  --allow-overlapping-discovery
```

The acknowledged `all` plan writes one shared `.agents` copy and one Claude
copy; it deliberately omits a redundant Cursor-native copy. Installation is
copy-only and refuses symlinked parents, unmanaged destinations, drifted
managed files, or a different managed bundle. Every destination carries a
canonical `.hextap-install.json` ownership marker with a strict bundle version
and per-file hashes. Existing exact installations are idempotent and metadata
timestamps are left unchanged.

Inspect state without writing:

```sh
brew hextap skills status --agent codex --scope user
brew hextap skills status --agent cursor --scope project --project .
```

Inventory defaults to every concrete location in the selected scope and reports
the installed/available versions, physical target, discovering agents, and safe
recommendation. JSON is intended for agents and automation; `discovered_by`
distinguishes Cursor discovery of shared `.agents`/`.claude` copies from its
optional native `.cursor` location:

```sh
brew hextap skills status --scope user
brew hextap skills status --scope user --json
```

An intact marker-owned lower version may be upgraded after reviewing the dry
run:

```sh
brew hextap skills upgrade --agent codex --scope user --dry-run
brew hextap skills upgrade --agent codex --scope user
```

Upgrade refuses unmanaged paths, drift, invalid markers, untracked extras,
same-version/different-byte bundles, and downgrades. It stages the complete new
bundle outside agent discovery, switches the directory as one transaction, and
retains the exact previous bundle at the reported recovery path. Uninstall and
automatic recovery-path deletion remain intentionally unsupported.

If installation reports a partial state, its exact `claimed directories` and
`published files` are the durable create-only prefix that Hextap itself
created. Preserve and reconcile those explicitly reported paths. Never infer
that another unmarked or unreported path is safe to delete: it may contain
concurrent or otherwise unmanaged user content, and pathname rollback is not a
recovery mechanism.

## Toolkit development and self-release

`brew hextap dev` is specific to `SijanC147/hextap-toolkit`. It validates the
canonical module and origin identity, rejects additional writable remotes, and
never reads secret values.

Read-only inventory and SemVer planning:

```sh
brew hextap dev status --project .
brew hextap dev plan --project . --bump patch
brew hextap dev plan --project . --bump minor --json
```

Local validation mirrors the repository CI contract. Full mode includes the
race detector; `--quick` is the iteration-only rung:

```sh
brew hextap dev validate --project . --quick
brew hextap dev validate --project .
```

From a clean reviewed feature branch, the protected end-to-end path is:

```sh
brew hextap dev deploy \
  --project . \
  --bump minor \
  --confirm-tag vNEXT \
  --execute
```

It reruns full validation, pushes only the feature branch to canonical origin,
creates or reuses its PR, waits for updated-head checks, and stops on unresolved
reviews or a non-clean merge state. It never uses administrator or auto-merge
bypass. After a protected merge it requires the exact merged-main CI run,
recomputes the release baseline, creates or reuses only the confirmed annotated
tag, waits for the exact self-release workflow, and verifies the immutable
stable release and complete assets.

Use `dev release` for the release-only half from clean canonical `main`. Add
`--install` only when local Hextap mutation is authorized. Standalone install
requires the exact released tag and commit:

```sh
brew hextap dev install \
  --project . \
  --tag vVERSION \
  --commit FULL_RELEASE_COMMIT \
  --execute
```

The install phase discovers the Homebrew tree that owns active `brew-hextap`,
updates tap metadata, upgrades only `sean/hextap/hextap`, requires the exact
version/commit, runs the Formula test, and optionally reconciles concrete
`--skill-agent` targets. No developer command invokes `brew services` or
changes another Formula, proxy, certificate, port, or runtime configuration.

## Managed reusable workflow

Generated callers pin the full commit SHA of an exact stable toolkit release;
they never call mutable `@main`. Within the called workflow, all six toolkit
checkouts use GitHub's server-resolved `job.workflow_sha` so YAML, Go code, and
publisher scripts come from one commit.

The validate job resolves and detaches the requested source tag before reading
the tracked manifest. That exact validated file is uploaded once, identified by
artifact ID, and bound to an explicit content SHA-256. Build, native verify,
release, and Homebrew jobs download and recheck the same bytes. Source quality
is a hard publication dependency. Schema 1 derives the original Darwin/Linux
matrix from `release.linux`; schema 2 exports its validated matrix, including
optional Windows amd64, from the sealed manifest. Per-repository release runs
use a non-canceling queued concurrency group.

On Windows native runners, Hextap converts the native `RUNNER_TEMP` value once
to an absolute Git Bash path before testing the downloaded manifest or invoking
the verifier. The manifest file/type/digest assertions emit distinct diagnostics
without printing paths or digest values, and hosted `windows-2025` toolkit CI
proves the normalized path remains usable by the built `hextapctl.exe`.

Full mode accepts stable and prerelease tags and produces an exact immutable
GitHub release. Only stable releases may enter Homebrew. `homebrew-only` is a
stable recovery mode for an existing immutable verified release only while the
requested tag's manifest remains fully semantically equal to the current tap
registration. It is deliberately not an all-historical-tags recovery promise:
there are no field exceptions or versioned registry snapshots in schema 1.
The Homebrew job is the only place the explicitly mapped
`OP_SERVICE_ACCOUNT_TOKEN` is visible; it loads the tap credential from
1Password, requires that equality, changes only Formula URL/SHA metadata, and
verifies tap CI at the exact direct-push commit. An out-of-window attempt fails
explicitly with `tap/source manifest mismatch` before Formula mutation.

The existing `claude-rc-proxy v0.1.0` tag is outside that recovery window after
the XDG-aware caveat evolution. Recovery will be proven with the next stable
tag whose source manifest is aligned with the current tap registration.

For the first toolkit `v0.1.0`, the one-executable archive contains
`brew-hextap` only. The Formula installs that binary to expose
`brew hextap`; `hextapctl` stays source-built inside the pinned reusable
workflow. The checked-in `.hextap.json` uses the explicit disabled-service
shape, and `scripts/hextap-build` validates the normalized version, commit,
OS, and architecture before building only `./cmd/brew-hextap`.

Current toolkit manifests may additionally declare `homebrew.binary_aliases`
and `homebrew.zsh_completion`. The Hextap self-release now appends the exact
generated `completions/_hextap` member after `brew-hextap`, `LICENSE`, and
`README.md`; the generic Formula renderer installs the alias and completion
from those explicit manifest fields.

The toolkit self-caller uses the relative
`./.github/workflows/release-go.yml` path so the reusable workflow resolves at
the same tag commit. Tag pushes select `full`; manual dispatch accepts an
existing stable tag and selects `homebrew-only`. It maps only
`OP_SERVICE_ACCOUNT_TOKEN` and grants the three release permissions required
by the reusable workflow. External project callers continue to use full-SHA
toolkit pins. No tag, release, tap registration, Formula, secret, ruleset, or
immutable-release setting is created by this source change.

## Project manifest schema

Every project is described by one strict JSON document. Schema `1` is the
compatible Go/Darwin/Linux contract with optional binary-alias and Zsh-completion packaging; schema `2` adds a pinned Bun profile,
project-owned direct argv commands, explicit multi-artifact targets, optional
Windows amd64, and a tap-owned Formula profile. Duplicate object keys at any nesting level, mis-cased/case-fold aliases,
invalid or lossy Unicode, unknown fields, missing required fields, multiple
JSON documents, unsafe paths, and values that could escape generated Ruby are
rejected. Filesystem basenames/components are limited to 255 ASCII bytes,
relative paths to 1024 bytes, and formula names reserve enough room for every
generated Darwin/Linux archive and tap-registration suffix.

The canonical examples are the legacy Go
[`examples/claude-rc-proxy.json`](examples/claude-rc-proxy.json) and Bun profile
[`examples/better-ccflare.json`](examples/better-ccflare.json). The checked-in
[Draft 2020-12 JSON Schema](schema/project-manifest.schema.json) provides a
machine-readable contract for editors and other tooling without adding a
runtime dependency. The checked-in standard-library conformance suite runs one
shared valid/invalid corpus through both the schema evaluator and
`manifest.Parse`, and rejects schema keywords the evaluator does not implement.
The Go validator remains authoritative. JSON Schema cannot express every
cross-field/input check exactly: `formula.class` derived from `formula.name`,
distinct arm64/amd64 asset values, the caveats rule that only `{{home}}` and
`{{var}}` placeholders are accepted, and the raw-input requirement that a file
contain no duplicate object keys and exactly one JSON document with no
trailing value. Schema 2 additionally requires paired Linux targets, Darwin
archive equality with `formula.assets`, unique asset names, frozen Bun install,
and a `.exe` Windows binary. Call `hextapctl manifest validate` before rendering or
publishing even when an editor reports
that the JSON Schema is valid. Schema 1 retains this stable shape:

```json
{
  "schema": 1,
  "formula": {
    "name": "claude-rc-proxy",
    "class": "ClaudeRcProxy",
    "description": "Selective Anthropic proxy preserving Claude Code Remote Control",
    "homepage": "https://github.com/SijanC147/claude-rc-proxy",
    "license": "MIT",
    "repository": {
      "owner": "SijanC147",
      "name": "claude-rc-proxy"
    },
    "binary": "claude-rc-proxy",
    "assets": {
      "darwin_arm64": "claude-rc-proxy-darwin-arm64.tar.gz",
      "darwin_amd64": "claude-rc-proxy-darwin-amd64.tar.gz"
    }
  },
  "release": {
    "build_script": "scripts/hextap-build",
    "linux": true
  },
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "service": {
      "enabled": true,
      "run_args": [],
      "keep_alive": {
        "crashed": true
      },
      "restart_delay": 5,
      "environment": {
        "CLAUDE_RC_PROXY_LISTEN": "127.0.0.1:9801"
      },
      "log_path": "log/claude-rc-proxy/launchd.log",
      "error_log_path": "log/claude-rc-proxy/launchd.log"
    },
    "caveats": "Configuration lives below {{home}}. Logs live below {{var}}."
  }
}
```

Schema 2 keeps `formula` unchanged and replaces `release.linux` with a profile
and explicit target map. Commands are named argv arrays executed directly,
never shell strings. Each target declares a raw `binary`, an `archive`, or both;
`archive_contents` must explicitly select `binary` (single executable) or
`bundle` (executable plus `LICENSE` and `README.md`). Darwin arm64/amd64 are
required, Linux arm64/amd64 are optional as a pair, and Windows amd64 is an
optional raw `.exe`. `homebrew.formula_profile` names the tap-owned Formula
template and `service_enabled` records service status without copying its Ruby
service, caveats, tests, comments, or formatting into source metadata.

### Field contract

| Field | Contract |
|---|---|
| `schema` | `1` selects the legacy Go target contract; `2` selects the Bun/profile target contract. |
| `formula.name` | Lowercase kebab-case name whose segments each begin with a letter, so class derivation is unambiguous. |
| `formula.class` | Must exactly equal the PascalCase class derived from `formula.name`; `claude-rc-proxy` therefore requires `ClaudeRcProxy`. |
| `formula.description` | Required, one line, and safe for a Ruby string. All Ruby interpolation introducers (`#{`, `#@`, and `#$`) are rejected. |
| `formula.homepage` | HTTPS URL with no credentials, query, or fragment. |
| `formula.license` | Required, one line, and safe for a Ruby string. All Ruby interpolation introducers are rejected. |
| `formula.repository` | Safe GitHub `owner` and repository `name`; this defines canonical release URLs. |
| `formula.binary` | Safe executable basename installed by the Formula. |
| `formula.assets` | Distinct safe `.tar.gz` basenames for Darwin arm64 and amd64. |
| `release.build_script` | Clean repository-relative path; absolute paths, traversal, backslashes, and unsafe path components are rejected. |
| `release.linux` | Required boolean recording whether the future release workflow should also build Linux archives. |
| `release.profile` | Schema 2 only. Requires `runtime: bun`, a pinned stable `runtime_version`, exact frozen-lock install argv, named quality argv, and named build-preparation argv. |
| `release.targets` | Schema 2 only. Requires Darwin arm64/amd64; permits paired Linux arm64/amd64 and optional Windows amd64. Asset basenames must be globally unique after case-folding and may not use reserved `SHA256SUMS`. |
| `target.binary` / `archive` | Explicit raw executable and/or canonical `.tar.gz` output derived from one adapter invocation. `archive_contents` is required with an archive. |
| `homebrew.macos_only` | Must be `true` in schema 1 because its Formula contract defines only Darwin assets. Rendering always adds `depends_on :macos`. |
| `homebrew.test_args` | One or more shell-safe arguments used by the Formula test. |
| `homebrew.binary_aliases` | Schema 1 only. Optional nonempty list of up to 16 case-insensitively unique safe basenames, none equal to `formula.binary`; the Formula installs each as a symlink to the packaged binary. |
| `homebrew.zsh_completion` | Schema 1 only. Optional exact `completions/_COMMAND` path whose stem must equal `formula.binary` or one declared binary alias. The source must be a regular single-link UTF-8 file of at most 1 MiB ending in a newline. |
| `homebrew.service` | Optional. Use `null`, omit it, or set only `{"enabled": false}` to disable service generation. |
| `service.run_args` | Required array for an enabled service; may be empty. |
| `service.keep_alive` | Must select exactly one supported Homebrew policy: `successful_exit` or `crashed`. They are intentionally mutually exclusive because Homebrew prioritizes one when both are present. |
| `service.restart_delay` | Integer from 1 through 3600 seconds. |
| `service.environment` | Required object for an enabled service; keys must be uppercase environment identifiers and values must be single-line Ruby-safe strings with no `#{`, `#@`, or `#$` interpolation. May be empty. Output is sorted by key. |
| `service.log_path` / `error_log_path` | Clean paths relative to Homebrew `var`; never absolute and never traversal paths. |
| `homebrew.caveats` | Safe heredoc text. `{{home}}` and `{{var}}` are the only accepted placeholders and render to fixed toolkit-owned expressions. Other placeholders, literal `#{`/`#@`/`#$` interpolation, carriage returns, and an `EOS` terminator line are rejected. |
| `homebrew.formula_profile` / `service_enabled` | Schema 2 only. The profile must equal `formula.name`; the tap owns all nonmetadata Formula bytes. Source publication may update only the two Darwin URLs and SHA-256 values and cannot render this Formula. |

Each tap-owned Formula profile is bound to a reviewed regular, non-symlink
`packaging/<formula_profile>.rb.tmpl` in the tap. That template must contain
exactly one canonical Darwin architecture block and exactly one each of
`@ARM64_URL@`, `@ARM64_SHA256@`, `@AMD64_URL@`, and `@AMD64_SHA256@`; every
other token-like `@…@` placeholder is rejected. Before publication, the current
Formula must be byte-identical to that template rendered with its canonical
current URLs and lowercase checksums. Hextap then renders the new Formula solely
from the same template, so service, caveats, tests, comments, Ruby expressions,
and formatting remain reviewed tap-owned bytes without interpreting Ruby.

Stable versions accepted by Formula commands are strict `X.Y.Z` SemVer:

- no leading `v`
- no prerelease or build suffix
- no leading zero in a multi-digit component

SHA-256 arguments must be exactly 64 lowercase hexadecimal characters.

## Commands

### Version

```sh
hextapctl version
```

Development builds report `dev` and `unknown`; release builds inject both
values through the linker as shown above.

### Validate a manifest

```sh
hextapctl manifest validate \
  --file examples/claude-rc-proxy.json
```

Success is deterministic:

```text
manifest valid: examples/claude-rc-proxy.json (schema 1, formula claude-rc-proxy)
```

### Export manifest values for GitHub Actions

```sh
hextapctl manifest export \
  --file examples/claude-rc-proxy.json \
  --repository SijanC147/claude-rc-proxy \
  --github-output "$GITHUB_OUTPUT"
```

The repository argument must exactly match the manifest's canonical
`owner/name`. The command appends these validated, single-line outputs in a
stable order:

```text
formula
binary
owner
repository_name
repository
arm64_asset
amd64_asset
build_script
linux
runtime
native_matrix
runtime_version # schema 2 only
```

`--github-output` must name the existing regular, non-symlink file created by
the GitHub Actions runner. Existing newline-terminated outputs are preserved.
The command rejects duplicate, multiline, control-character, oversized, or
otherwise unsafe output records before appending anything.

The append is a same-directory atomic replacement: the complete existing file
and new records are written, mode-preserved, synced, and closed before rename.
Any failure before rename leaves the original byte-for-byte unchanged and
removes the temporary file. As with Formula writes, a parent-directory sync
failure occurs after rename; the command reports that crash durability was not
confirmed, but the complete replacement may already be visible.

### Normalize release metadata

```sh
hextapctl release metadata \
  --tag v1.2.3 \
  --mode full \
  --github-output "$GITHUB_OUTPUT"
```

Tags must be strict, `v`-prefixed SemVer without build metadata. Stable tags
such as `v1.2.3` and prereleases such as `v1.2.3-rc.1` are accepted in `full`
mode; `homebrew-only` accepts stable tags only. When `--github-output` is
provided, the command appends `tag`, normalized `version`, `stable`,
`prerelease`, and `mode`. Omitting it skips the output-file append; either form
prints one deterministic summary line to standard output.

### Run project-owned release profile commands

```sh
hextapctl release profile \
  --manifest /path/to/project/.hextap.json \
  --source /path/to/project \
  --phase quality
```

Build preparation uses an explicit cache:

```sh
mkdir /path/to/private-bun-runtime-cache
BUN_INSTALL_CACHE_DIR=/path/to/private-bun-runtime-cache \
  hextapctl release profile \
    --manifest /path/to/project/.hextap.json \
    --source /path/to/project \
    --phase build
```

Schema 2 exposes `quality` and `build` phases. Both run the pinned-runtime
install argv first after requiring the actual Bun binary to equal
`runtime_version`; `quality` then runs the ordered quality commands. `build`
requires an existing real directory in `BUN_INSTALL_CACHE_DIR`, compiles one
private probe for every declared target to populate that dedicated runtime
cache, then runs the ordered preparation commands. Arguments are passed
directly to the executable with no shell evaluation and a
Bun/cache/process allowlist that excludes ambient credentials. The reusable workflow runs a
tracked-source diff after quality and again after all adapter builds, so lock,
generated dashboard/worker, or other tracked-source drift fails the tag.

### Build deterministic release assets

```sh
mkdir /path/to/project/dist
hextapctl release build \
  --manifest /path/to/project/.hextap.json \
  --version 1.2.3 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source /path/to/project \
  --output /path/to/project/dist
```

The source and output must be real directories rather than symlinks; the
output directory must already exist and be empty. The manifest must resolve
inside the source tree. The source must contain regular `LICENSE` and
`README.md` files and the manifest's executable `release.build_script`. When
`homebrew.zsh_completion` is declared, that exact project-local completion
file is also required and is appended to every schema-1 bundle with mode
`0644` after the binary, license, and readme.

For schema 1, the builder invokes the adapter once for each Darwin target and,
when `release.linux` is true, once for each Linux target, preserving the
four-target bundle archives. For schema 2 it invokes the adapter once per
declared target and derives every raw/archive asset for that target from the
same executable bytes. It then writes a sorted `SHA256SUMS`. Tar and gzip
metadata, member order, modes, owners, and timestamps are canonical, so
identical adapter output produces byte-identical release files. A failed target
leaves the output directory empty.

### Verify release assets

```sh
hextapctl release verify \
  --manifest /path/to/project/.hextap.json \
  --version 1.2.3 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --dir /path/to/project/dist
```

Verification derives the exact asset set from the manifest, checks the
strict sorted `SHA256SUMS` file, and validates every gzip/tar header, member,
mode, owner, timestamp, size, and executable format. Darwin archives must
contain a single-architecture Mach-O executable; Linux assets must contain a
64-bit x86_64 or aarch64 ELF executable; Windows amd64 must be a PE32+
executable image whose entry lies in an executable section. Raw and archive
assets for one target must contain byte-identical executables. The distribution
directory is read-only to the verifier. On a matching host, an optional target check also
extracts only that target's binary into a private temporary directory and
requires its exact version output:

```sh
hextapctl release verify \
  --manifest /path/to/project/.hextap.json \
  --version 1.2.3 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --dir /path/to/project/dist \
  --execute-target darwin-arm64
```

Windows native execution normalizes only a single terminal `\r\n` to `\n`;
all other stdout bytes, empty stderr, version, and commit remain exact.

The verification runner executes from a private temporary working directory
with a minimal environment. Stdout and stderr are bounded live in memory to
16 KiB each; overflow cancels execution before output can fill disk. On Unix,
the binary runs in a dedicated process group and timeout or overflow kills and
reaps that group, including ordinary descendants. Other platforms retain the
same live memory bounds and terminate the direct process.

#### Build-adapter contract

The adapter runs with the source directory as its working directory, no stdin,
and discarded stdout/stderr. It receives a sanitized environment: a small
toolchain/OS allowlist from the parent plus exactly these Hextap variables:

| Variable | Meaning |
|---|---|
| `HEXTAP_TARGET_OS` | `darwin`, `linux`, or optional `windows`. |
| `HEXTAP_TARGET_ARCH` | `arm64` or `amd64`. |
| `HEXTAP_OUTPUT` | Absolute path at which the adapter must write the target binary. |
| `HEXTAP_VERSION` | Validated normalized SemVer without a leading `v`. |
| `HEXTAP_COMMIT` | Validated lowercase source commit. Schema 1 accepts 7–64 hexadecimal characters; schema 2 requires the full 40- or 64-character identity. |

The inherited allowlist is limited to compiler/toolchain and basic process
variables: `BUN_INSTALL_CACHE_DIR`, `CC`, `CGO_ENABLED`, `CXX`, `DEVELOPER_DIR`, `GOCACHE`, `GOENV`,
`GOMODCACHE`, `GOPATH`, `GOPROXY`, `GOROOT`, `GOSUMDB`, `GOTOOLCHAIN`, `HOME`,
`LANG`, `LC_ALL`, `LOGNAME`, `PATH`, `SDKROOT`, `SHELL`, `SYSTEMROOT`, `TEMP`,
`TERM`, `TMP`, `TMPDIR`, `TZ`, and `USER`. Other parent variables, including
credentials, are not inherited.

For Windows the output basename ends in `.exe`. For each invocation, the adapter must create exactly the file named by
`HEXTAP_OUTPUT` and no other entry in its isolated staging directory. That file
must be a regular, non-symlink, single-link binary no larger than 256 MiB. The
toolkit normalizes its archived mode to `0755`; the adapter must embed
`HEXTAP_VERSION` and `HEXTAP_COMMIT` and make `--version` print exactly
`BINARY VERSION (commit COMMIT)`. Each adapter invocation has a 15-minute timeout.
Schema-2 preparation prefetches every pinned Bun cross-target runtime into the
dedicated cache before adapter execution. The reusable Ubuntu build then runs
`hextapctl release build` as the original runner user inside a root-created
network namespace, so the adapter has no network interface while retaining
access to only the warmed cache. Toolkit PR CI proves that an empty cache fails
inside this boundary and the warmed five-target matrix succeeds. Project checks
remain responsible for deterministic application resources and source cleanliness.
Adapters must not daemonize or intentionally leave child processes behind;
they must finish all work before the adapter process exits. On timeout, the
toolkit kills only the adapter leader and waits for it; child-process cleanup
is the adapter or runner's responsibility and is outside this core.

### Render a new Formula

```sh
hextapctl formula render \
  --manifest examples/claude-rc-proxy.json \
  --version 0.1.0 \
  --arm64-sha aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  --amd64-sha bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
  --output Formula/claude-rc-proxy.rb
```

Rendering produces deterministic Ruby with:

- no explicit `version` stanza; Homebrew derives it from canonical release URLs
- `if Hardware::CPU.arm?` followed by the arm64 URL/SHA, `else`, and amd64 URL/SHA
- `bin.install`, declared `bin.install_symlink` aliases, an optional
  `zsh_completion.install`, optional service, optional caveats, and a version test
- sorted environment variables and a final newline

The write uses a same-directory temporary file, atomic rename, and parent
directory sync. An existing destination mode is preserved. Every failure
before rename leaves the original destination unchanged and removes the
temporary file. A parent-directory sync failure happens after rename: the
command returns an error explaining that crash durability was not confirmed,
but the complete replacement may already be visible and is not rolled back.

### Update an existing Formula

```sh
hextapctl formula update \
  --manifest examples/claude-rc-proxy.json \
  --formula Formula/claude-rc-proxy.rb \
  --version 0.2.0 \
  --arm64-sha cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  --amd64-sha dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
```

The updater supports only the generated golden-path Formula and fails closed
unless the existing file has exactly the canonical seven-line architecture
block and exactly two `url` and two `sha256` declarations across all Formula
code. Additional declarations at any indentation, including `resource` blocks,
are unsupported and rejected. The only supported heredoc is the exact optional
generated `def caveats` / `<<~EOS` / `EOS` / `end` block with its expected
indentation and manifest presence. Every other heredoc operator—including one
written in a comment or Ruby string—is rejected, so it cannot hide a version,
URL, or SHA declaration from validation. Text inside the recognized generated
caveats body is not treated as Ruby code. Both release URLs must target the
manifest repository and expected architecture asset, use the same stable version, and have an
immediately following lowercase SHA-256. Any top-level `version` invocation is
rejected, including quoted, parenthesized, and trailing-comment forms.

It then changes only the two quoted URL values and two quoted SHA values. Every
other byte and the file mode are preserved. Downgrades and explicit `version`
stanzas are rejected. An equal-version update is byte-idempotent when all
metadata matches, while an equal-version checksum correction is permitted.

For a schema-2 tap-owned Formula profile, pass the exact reviewed tap template:

```sh
hextapctl formula update \
  --manifest Projects/better-ccflare.json \
  --formula Formula/better-ccflare.rb \
  --template packaging/better-ccflare.rb.tmpl \
  --version 3.8.2 \
  --arm64-sha cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  --amd64-sha dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
```

The profile template is the complete nonmetadata authority. Hextap refuses a
symlink or non-regular template, missing/duplicate/misplaced metadata tokens,
any additional token-like placeholder, noncanonical current URL or checksum,
Formula/template byte drift, and version downgrades. Schema-1 Formula rendering
and update behavior is unchanged.

All errors use a concise `error: ...` message on stderr and a nonzero exit
status. Successful validate, render, and update operations print one
deterministic status line.

## Verification

```sh
test -z "$(gofmt -l .)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -trimpath ./...
bash -n scripts/*.sh
shellcheck scripts/*.sh
scripts/check-actionlint.sh
```

GitHub currently documents `concurrency.queue: max` and the reusable-job
`job.workflow_sha` identity fields. Actionlint 1.7.12 predates both. The checker
script does not suppress diagnostics broadly: for the pinned 1.7.12 version it
requires a nonzero result containing exactly the reviewed one queue plus six
workflow-SHA schema-lag diagnostics, and fails on a clean/no-op, missing,
additional, changed, or differently versioned result. A future Actionlint
upgrade must update that expectation explicitly.
