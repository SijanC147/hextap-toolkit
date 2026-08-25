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
[`examples/claude-rc-proxy.json`](examples/claude-rc-proxy.json). The schema is
represented by the Go types under `internal/manifest` and has this stable
shape:

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
| `formula.name` | Lowercase kebab-case Formula name. |
| `formula.class` | One Ruby constant such as `ClaudeRcProxy`; namespaces and punctuation are rejected. |
| `formula.description` | Required, one line, and safe for a Ruby string. |
| `formula.homepage` | HTTPS URL with no credentials, query, or fragment. |
| `formula.license` | Required, one line, and safe for a Ruby string. |
| `formula.repository` | Safe GitHub `owner` and repository `name`; this defines canonical release URLs. |
| `formula.binary` | Safe executable basename installed by the Formula. |
| `formula.assets` | Distinct safe `.tar.gz` basenames for Darwin arm64 and amd64. |
| `release.build_script` | Clean repository-relative path; absolute paths, traversal, backslashes, and unsafe path components are rejected. |
| `release.linux` | Required boolean recording whether the future release workflow should also build Linux archives. |
| `homebrew.macos_only` | Required boolean; when true, rendering adds `depends_on :macos`. |
| `homebrew.test_args` | One or more shell-safe arguments used by the Formula test. |
| `homebrew.service` | Optional. Use `null`, omit it, or set only `{"enabled": false}` to disable service generation. |
| `service.run_args` | Required array for an enabled service; may be empty. |
| `service.keep_alive` | Must select exactly one supported Homebrew policy: `successful_exit` or `crashed`. They are intentionally mutually exclusive because Homebrew prioritizes one when both are present. |
| `service.restart_delay` | Integer from 1 through 3600 seconds. |
| `service.environment` | Required object for an enabled service; keys must be uppercase environment identifiers and values must be single-line Ruby-safe strings. May be empty. Output is sorted by key. |
| `service.log_path` / `error_log_path` | Clean paths relative to Homebrew `var`; never absolute and never traversal paths. |
| `homebrew.caveats` | Safe heredoc text. `{{home}}` renders as `#{Dir.home}` and `{{var}}` as `#{var}`. Other placeholders, Ruby interpolation, carriage returns, and an `EOS` terminator line are rejected. |

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

The write uses a same-directory temporary file and atomic rename. An existing
destination mode is preserved. Validation or write failures do not partially
rewrite the destination.

### Update an existing Formula

```sh
hextapctl formula update \
  --manifest examples/claude-rc-proxy.json \
  --formula Formula/claude-rc-proxy.rb \
  --version 0.2.0 \
  --arm64-sha cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  --amd64-sha dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
```

The updater fails closed unless the existing Formula has exactly the canonical
seven-line architecture block and exactly two URL and SHA stanzas. Both URLs
must target the manifest repository and expected architecture asset, use the
same stable version, and have an immediately following lowercase SHA-256.

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
