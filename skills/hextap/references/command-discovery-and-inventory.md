# Command discovery and inventory

Load this reference when discovering Hextap commands, inspecting the local
installation, or verifying native shell completion.

## Invocation contract

Prefer the direct command:

```sh
hextap --version
hextap --help
```

Homebrew's external-command form remains equivalent:

```sh
brew hextap --version
brew hextap --help
```

The Formula installs one `brew-hextap` executable and a relative
`hextap -> brew-hextap` symlink. Treat different version or commit output from
the two invocations as an installation defect. Never replace the symlink with a
second binary copy.

## Authoritative help

Use `--help` or `-h` at any command depth before constructing an unfamiliar
operation:

```sh
hextap onboard --help
hextap skills upgrade -h
hextap dev deploy --help
hextap info --help
```

Every help page states purpose, arguments, long and short flags, allowed values,
safety boundaries, and examples. Every user-facing long flag has one
single-character alias within its command scope. Do not infer an alias from its
first letter; read the command help or completion metadata.

Use global `--version` or `-V` for immutable build identity. The `version`
subcommand is equivalent.

## Zsh completion

The Homebrew Formula installs the release-owned completion below the Homebrew
prefix that owns the active Hextap binary. Resolve that exact prefix from the
inventory rather than assuming the shell's default `brew` is the owner:

```sh
hextap status --json | jq -r \
  '.homebrew.prefix + "/share/zsh/site-functions/_hextap"'
```

The file registers both `hextap` and `brew-hextap` and is generated from the
same command metadata as help. Inspect its exact installed source without
writing shell configuration:

```sh
hextap completion zsh
```

Require the generated output to equal the installed `_hextap` bytes. The
completion covers nested commands, both flag spellings, path arguments, enum
values, repeatable options, descriptions, and examples. Homebrew's standard Zsh
completion setup normally discovers it after starting a fresh shell. Do not add
manual sourcing to shell startup files unless normal `compinit` discovery has
been proven absent.

## System overview

Run the concise offline inventory first:

```sh
hextap status
hextap status --json
hextap status --project PROJECT_ROOT
```

The report includes the running CLI, owning Homebrew installation, canonical
tap Git identity, registered Projects, Formulae, Casks, managed agent skills,
and an optional local `.hextap.json`. JSON output is the complete versioned
schema; human output is a summary of the same report.

`status` is read-only and disables Homebrew auto-update, API access, and
analytics. It never starts or inspects live service processes and never prints
environment values, dependency stderr, or credentials. A missing component is
reported as an explicit warning rather than silently omitted.

## Detailed and filtered inventory

Use `info` when exact package or registration details are required:

```sh
hextap info
hextap info --kind project
hextap info --kind formula --name hextap
hextap info -k cask -j
hextap info --kind skill
```

Valid categories are `all`, `project`, `formula`, `cask`, and `skill`.
`--name` applies an exact name filter after the category selection. Formula and
Cask entries distinguish available and installed versions, outdated state, and
pinning where Homebrew exposes it locally. Formula service metadata contains
only safe definition fields and environment-variable names, never values.

Treat warnings as unresolved evidence. Do not turn an inventory warning into an
automatic `brew update`, installation, registration rewrite, network query, or
service operation.
