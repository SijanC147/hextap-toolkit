# Safety and failure routing

Load this reference whenever a command fails or a requested action crosses a
repository, secret, or local-runtime boundary.

## Fixed safety boundaries

### Secrets

- Preserve `OP_SERVICE_ACCOUNT_TOKEN` as the sole source-repository secret name.
- Let the Homebrew job retrieve the tap credential from the configured
  1Password reference. Never retrieve, echo, log, copy, persist, or pass that
  credential in command arguments.
- Never use `secrets: inherit`, add a direct tap token to the source repository,
  or place secret values in generated files, issue comments, or agent memory.

### Remotes and upstreams

- Inspect fetch and push URLs before any push or tag operation.
- Require the writable remote to be the intended owned repository.
- Keep any third-party upstream fetch-only and give it a disabled push URL such
  as `no_push`. Treat a writable upstream as a blocker. Never push to upstream,
  even to repair CI or release automation.
- Never force-push protected branches or tags.

### Local runtime

- Treat package installation/upgrades, proxy or service lifecycle, launch
  configuration, certificates, ports, and runtime configuration as separate
  operations requiring fresh explicit approval.
- A release request alone authorizes neither a Homebrew upgrade nor a service
  restart. Report the remote result and pause at the local boundary.

## Diagnostic routing

| Diagnostic or observation | Route |
|---|---|
| Existing managed file differs | Stop. Diff the exact path and decide whether the repository or requested metadata is authoritative. Do not overwrite it. |
| `validate` reports manifest/tap or workflow drift | Repair the source change through a reviewed PR, then rerun offline validation. Do not hand-edit around the validator. |
| `validate --build` fails | Fix the trusted project adapter or binary contract; rerun the structural rung before the build rung. |
| Default `doctor` fails | Repair only the named local prerequisite when authorized; do not escalate automatically to network or service mutation. |
| Online doctor reports ruleset, secret-name, default-branch, or immutable-release drift | Keep the generated payload as evidence and route the owner-controlled remote change for approval. Doctor never repairs it. |
| Tap Project missing or Formula missing/mismatched | For first bootstrap, prepare one paired protected tap PR. For an existing registration, reconcile complete source/tap identity before release. |
| Full stable release publishes source assets but Homebrew fails | Preserve the immutable release, repair the tap-side gate, then use `homebrew-only` for the same stable tag. |
| `tap/source manifest mismatch` during recovery | Stop. The historical recovery window is closed; do not add field exceptions, mutate the tag, or rewrite registry history. |
| Existing release differs in assets, bytes, attestation, or prerelease state | Stop and escalate. Never delete, replace, or make the immutable release mutable. |
| Prerelease has no Formula update | Expected behavior. Verify the GitHub prerelease and record that Homebrew was intentionally skipped. |
| Tap push race | Allow only the toolkit's bounded fresh-clone retry. Authorization/ruleset failure must surface unchanged. Never force-push. |
| Hosted checks are absent or GitHub Actions is unavailable | Make no release or repository mutation dependent on the missing evidence. Wait and recheck. |
| Local install/service proof remains | Ask for a separate maintenance window and exact target approval before changing anything. |

## Reporting

State which gate passed, which exact gate failed, whether any immutable source
release already exists, and the safest next action. Distinguish "source release
complete", "Homebrew publication complete", and "installed service validated";
none implies the others.
