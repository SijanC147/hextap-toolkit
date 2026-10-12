# Hextap setup

Onboarding created only local files and did not inspect or mutate any remote repository, secret, ruleset, release, or tap.

Before releasing, make `main` the default branch and enable immutable releases for `SijanC147/hextap-toolkit`.

Set the one required Actions secret. This command prompts securely; do not put a value in argv or a file:

```sh
gh secret set OP_SERVICE_ACCOUNT_TOKEN --repo github.com/SijanC147/hextap-toolkit
```

Review the two owned ruleset payloads:

```sh
cat .hextap/rulesets/main.json
cat .hextap/rulesets/release-tags.json
```

Apply each reviewed payload manually:

```sh
gh api --hostname github.com --method POST repos/SijanC147/hextap-toolkit/rulesets --input .hextap/rulesets/main.json
gh api --hostname github.com --method POST repos/SijanC147/hextap-toolkit/rulesets --input .hextap/rulesets/release-tags.json
```

The tap registration destination is exactly `Projects/hextap.json`, but the initial tap pull request must not contain that JSON alone. It must pair the byte-exact `.hextap/tap-registration.json` with `Formula/hextap.rb`, and that Formula must declare `class Hextap < Formula`. The tap remains the Formula registry; the paired pull request and merge are owner-controlled manual actions.

Coordinator bootstrap/recovery is an external adopter task:

1. Merge the reviewed onboarding files to `main`, apply the two reviewed rulesets, set the required secret, and enable immutable releases.
2. Push the first stable tag and let the full caller create and verify the immutable source release. When the project is not registered yet, the initial Homebrew publication can stop at the tap registry gate; do not replace or recreate that release.
3. From that immutable release and its verified `SHA256SUMS`, have the coordinator use the trusted pinned toolkit to render the exact Formula. Do not invent checksums or commit a placeholder Formula.
4. Open one tap pull request that adds both `Projects/hextap.json` and the release-backed `Formula/hextap.rb`; merge only after tap CI passes.
5. Dispatch the existing stable tag in `homebrew-only` mode to finish or recover publication. Do not create a replacement tag.

This repository owns the reusable release workflow, so its caller references it relatively and carries no external pin. There is no toolkit tag or commit to keep in sync here, and none may be added: a pin would make the repository an adopter of a different copy of itself.

What runs is therefore whichever commit the run was started from, not the tag being released. On a tag push the two are the same, and `full` mode asserts it. On a `homebrew-only` dispatch from `main`, step 5 above, the toolkit code that executes is `main`, while the source being released is still checked out at the tag and still required to be contained in `main`. Read a recovery run's provenance as the tag for the released source and the dispatched ref for the code that published it.
