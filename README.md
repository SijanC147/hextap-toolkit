# hextap-toolkit

`hextap-toolkit` is the deterministic, dependency-free core for reusable
release automation targeting the private Hextap Homebrew tap. Its
`hextapctl` command validates versioned project manifests and renders or
metadata-updates Homebrew Formulae without allowing project input to inject
Ruby or shell code.

This repository currently contains **toolkit core only**. It does not yet
contain the complete release stack: reusable GitHub Actions workflows, secret
loading, repository onboarding/mutation, direct tap publication, CI polling,
the `brew hextap` wrapper, and portable LLM Agent Skills will be added and
validated separately.

## Requirements and build

- Go 1.26 or newer
- Standard library only; there are no production dependencies

Build a development binary:

```sh
go build -o dist/hextapctl ./cmd/hextapctl
```

Release builds inject a stable version and source commit:

```sh
go build \
  -ldflags '-s -w -X main.version=0.1.0 -X main.commit=abc1234' \
  -o dist/hextapctl \
  ./cmd/hextapctl

dist/hextapctl version
# hextapctl 0.1.0 (commit abc1234)
```

## Project manifest schema

Every project is described by one strict JSON document. The current schema is
`1`; unknown fields, missing required fields, multiple JSON documents, unsafe
paths, and values that could escape generated Ruby are rejected.

The canonical example is
[`examples/claude-rc-proxy.json`](examples/claude-rc-proxy.json). The checked-in
[Draft 2020-12 JSON Schema](schema/project-manifest.schema.json) provides a
machine-readable contract for editors and other tooling without adding a
runtime dependency. The checked-in standard-library conformance suite runs one
shared valid/invalid corpus through both the schema evaluator and
`manifest.Parse`, and rejects schema keywords the evaluator does not implement.
The Go validator remains authoritative. JSON Schema cannot express four
schema-1/input checks exactly: `formula.class` derived from `formula.name`,
distinct arm64/amd64 asset values, the caveats rule that only `{{home}}` and
`{{var}}` placeholders are accepted, and the raw-input requirement that a file
contain exactly one JSON document with no trailing value. Call `hextapctl
manifest validate` before rendering or publishing even when an editor reports
that the JSON Schema is valid. The schema has this stable shape:

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

### Field contract

| Field | Contract |
|---|---|
| `schema` | Must be exactly `1`. |
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
| `homebrew.macos_only` | Must be `true` in schema 1 because its Formula contract defines only Darwin assets. Rendering always adds `depends_on :macos`. |
| `homebrew.test_args` | One or more shell-safe arguments used by the Formula test. |
| `homebrew.service` | Optional. Use `null`, omit it, or set only `{"enabled": false}` to disable service generation. |
| `service.run_args` | Required array for an enabled service; may be empty. |
| `service.keep_alive` | Must select exactly one supported Homebrew policy: `successful_exit` or `crashed`. They are intentionally mutually exclusive because Homebrew prioritizes one when both are present. |
| `service.restart_delay` | Integer from 1 through 3600 seconds. |
| `service.environment` | Required object for an enabled service; keys must be uppercase environment identifiers and values must be single-line Ruby-safe strings with no `#{`, `#@`, or `#$` interpolation. May be empty. Output is sorted by key. |
| `service.log_path` / `error_log_path` | Clean paths relative to Homebrew `var`; never absolute and never traversal paths. |
| `homebrew.caveats` | Safe heredoc text. `{{home}}` and `{{var}}` are the only accepted placeholders and render to fixed toolkit-owned expressions. Other placeholders, literal `#{`/`#@`/`#$` interpolation, carriage returns, and an `EOS` terminator line are rejected. |

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

### Build deterministic release archives

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
`README.md` files and the manifest's executable `release.build_script`.

The builder invokes the adapter once for each Darwin target and, when
`release.linux` is true, once for each Linux target. It packages the resulting
binary with `LICENSE` and `README.md` into the declared `.tar.gz` asset names,
then writes a sorted `SHA256SUMS`. Tar and gzip metadata, member order, modes,
owners, and timestamps are canonical, so identical adapter output produces
byte-identical release files. A failed target leaves the output directory
empty.

#### Build-adapter contract

The adapter runs with the source directory as its working directory, no stdin,
and discarded stdout/stderr. It receives a sanitized environment: a small
toolchain/OS allowlist from the parent plus exactly these Hextap variables:

| Variable | Meaning |
|---|---|
| `HEXTAP_TARGET_OS` | `darwin` or `linux`. |
| `HEXTAP_TARGET_ARCH` | `arm64` or `amd64`. |
| `HEXTAP_OUTPUT` | Absolute path at which the adapter must write the target binary. |
| `HEXTAP_VERSION` | Validated normalized SemVer without a leading `v`. |
| `HEXTAP_COMMIT` | Validated lowercase source commit, 7–64 hexadecimal characters. |

The inherited allowlist is limited to compiler/toolchain and basic process
variables: `CC`, `CGO_ENABLED`, `CXX`, `DEVELOPER_DIR`, `GOCACHE`, `GOENV`,
`GOMODCACHE`, `GOPATH`, `GOPROXY`, `GOROOT`, `GOSUMDB`, `GOTOOLCHAIN`, `HOME`,
`LANG`, `LC_ALL`, `LOGNAME`, `PATH`, `SDKROOT`, `SHELL`, `SYSTEMROOT`, `TEMP`,
`TERM`, `TMP`, `TMPDIR`, `TZ`, and `USER`. Other parent variables, including
credentials, are not inherited.

For each invocation, the adapter must create exactly the file named by
`HEXTAP_OUTPUT` and no other entry in its isolated staging directory. That file
must be a regular, non-symlink, single-link binary no larger than 256 MiB. The
toolkit normalizes its archived mode to `0755`; the adapter must embed
`HEXTAP_VERSION` and `HEXTAP_COMMIT` itself when the project exposes build
metadata. Each adapter invocation has a 15-minute timeout.
Adapters must not daemonize or intentionally leave child processes behind;
they must finish all work before the adapter process exits.

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
- `bin.install`, an optional service, optional caveats, and a version test
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

All errors use a concise `error: ...` message on stderr and a nonzero exit
status. Successful validate, render, and update operations print one
deterministic status line.

## Verification

```sh
test -z "$(gofmt -l .)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -trimpath ./cmd/hextapctl
```
