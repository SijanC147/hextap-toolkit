package onboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

type refNameCondition struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

type rulesetConditions struct {
	RefName refNameCondition `json:"ref_name"`
}

type rulesetRule struct {
	Type       string `json:"type"`
	Parameters any    `json:"parameters,omitempty"`
}

type rulesetBody struct {
	Name        string            `json:"name"`
	Target      string            `json:"target"`
	Enforcement string            `json:"enforcement"`
	Conditions  rulesetConditions `json:"conditions"`
	Rules       []rulesetRule     `json:"rules"`
}

type pullRequestParameters struct {
	RequiredApprovingReviewCount              int      `json:"required_approving_review_count"`
	DismissStaleReviewsOnPush                 bool     `json:"dismiss_stale_reviews_on_push"`
	RequireCodeOwnerReview                    bool     `json:"require_code_owner_review"`
	RequireExtraApprovalForUnattributedChange bool     `json:"require_extra_approval_for_unattributed_changes"`
	RequireLastPushApproval                   bool     `json:"require_last_push_approval"`
	RequiredReviewThreadResolution            bool     `json:"required_review_thread_resolution"`
	RequiredReviewers                         []any    `json:"required_reviewers"`
	AllowedMergeMethods                       []string `json:"allowed_merge_methods"`
}

type requiredStatusCheck struct {
	Context string `json:"context"`
}

type requiredStatusParameters struct {
	RequiredStatusChecks             []requiredStatusCheck `json:"required_status_checks"`
	StrictRequiredStatusChecksPolicy bool                  `json:"strict_required_status_checks_policy"`
	DoNotEnforceOnCreate             bool                  `json:"do_not_enforce_on_create"`
}

type updateParameters struct {
	UpdateAllowsFetchAndMerge bool `json:"update_allows_fetch_and_merge"`
}

func manifestBytes(project manifest.Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func adapterBytes(goPackage, versionSymbol, commitSymbol string) []byte {
	return []byte(fmt.Sprintf(`#!/bin/sh
set -eu

: "${HEXTAP_TARGET_OS:?HEXTAP_TARGET_OS is required}"
: "${HEXTAP_TARGET_ARCH:?HEXTAP_TARGET_ARCH is required}"
: "${HEXTAP_OUTPUT:?HEXTAP_OUTPUT is required}"
: "${HEXTAP_VERSION:?HEXTAP_VERSION is required}"
: "${HEXTAP_COMMIT:?HEXTAP_COMMIT is required}"

CGO_ENABLED=0 GOOS="$HEXTAP_TARGET_OS" GOARCH="$HEXTAP_TARGET_ARCH" \
  go build -mod=readonly -trimpath -buildvcs=false \
  -ldflags "-s -w -X=%s=$HEXTAP_VERSION -X=%s=$HEXTAP_COMMIT" \
  -o "$HEXTAP_OUTPUT" %s
`, versionSymbol, commitSymbol, goPackage))
}

// submodulesInput renders the caller's submodules line, or nothing at all when
// the manifest leaves release.checkout out or sets the default. A caller that
// checks out no submodules stays byte-identical to the one onboarding wrote
// before this input existed, so no adopter's committed workflow is invalidated
// by the field arriving.
func submodulesInput(submodules string) string {
	if submodules == "" || submodules == manifest.SubmodulesNone {
		return ""
	}
	return fmt.Sprintf("\n      submodules: %q", submodules)
}

// submodulesSecret renders the caller's submodules_token mapping, or nothing,
// on the same condition as submodulesInput. Keying both off the effective
// checkout mode is deliberate: every generated caller is compared byte for
// byte by validate and by doctor, so mapping the secret unconditionally would
// change the expected bytes for every existing adopter, including ones with no
// submodules, and the first thing each would see is their own caller reported
// as drifted from what the toolkit generates. An adopter who does not use
// submodules never learns this exists.
//
// The secret is optional in the reusable workflow and falls back to
// github.token when unset, so a caller that maps a repository secret which
// does not exist yet still runs; it fails on the private submodule rather than
// on the mapping.
func submodulesSecret(submodules string) string {
	if submodules == "" || submodules == manifest.SubmodulesNone {
		return ""
	}
	return "\n      submodules_token: ${{ secrets.SUBMODULES_TOKEN }}"
}

func workflowBytes(toolkitVersion, toolkitSHA, submodules string) []byte {
	return []byte(fmt.Sprintf(`name: Hextap release

on:
  push:
    tags:
      - "v*"
  workflow_dispatch:
    inputs:
      tag:
        description: Existing stable release tag
        required: true
        type: string

permissions:
  contents: write
  attestations: write
  id-token: write

jobs:
  release:
    uses: SijanC147/hextap-toolkit/.github/workflows/release-go.yml@%s # %s
    with:
      manifest_path: .hextap.json
      tag: ${{ github.event_name == 'workflow_dispatch' && inputs.tag || github.ref_name }}
      mode: ${{ github.event_name == 'workflow_dispatch' && 'homebrew-only' || 'full' }}%s
    secrets:
      op_service_account_token: ${{ secrets.OP_SERVICE_ACCOUNT_TOKEN }}%s
`, toolkitSHA, toolkitVersion, submodulesInput(submodules), submodulesSecret(submodules)))
}

func mainRulesetBytes(checks []string) ([]byte, error) {
	statusChecks := make([]requiredStatusCheck, len(checks))
	for index, check := range checks {
		statusChecks[index] = requiredStatusCheck{Context: check}
	}
	body := rulesetBody{
		Name:        "hextap/main",
		Target:      "branch",
		Enforcement: "active",
		Conditions: rulesetConditions{RefName: refNameCondition{
			Include: []string{"~DEFAULT_BRANCH"},
			Exclude: []string{},
		}},
		Rules: []rulesetRule{
			{Type: "deletion"},
			{Type: "non_fast_forward"},
			{Type: "pull_request", Parameters: pullRequestParameters{
				RequiredApprovingReviewCount:              0,
				DismissStaleReviewsOnPush:                 false,
				RequireCodeOwnerReview:                    false,
				RequireExtraApprovalForUnattributedChange: false,
				RequireLastPushApproval:                   false,
				RequiredReviewThreadResolution:            true,
				RequiredReviewers:                         []any{},
				AllowedMergeMethods:                       []string{"merge", "rebase", "squash"},
			}},
			{Type: "required_status_checks", Parameters: requiredStatusParameters{
				RequiredStatusChecks:             statusChecks,
				StrictRequiredStatusChecksPolicy: true,
				DoNotEnforceOnCreate:             false,
			}},
		},
	}
	return encodeJSON(body)
}

func tagRulesetBytes() ([]byte, error) {
	body := rulesetBody{
		Name:        "hextap/release-tags",
		Target:      "tag",
		Enforcement: "active",
		Conditions: rulesetConditions{RefName: refNameCondition{
			Include: []string{"refs/tags/v*"},
			Exclude: []string{},
		}},
		Rules: []rulesetRule{{Type: "deletion"}, {Type: "update", Parameters: updateParameters{UpdateAllowsFetchAndMerge: false}}},
	}
	return encodeJSON(body)
}

func encodeJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode JSON artifact: %w", err)
	}
	return append(data, '\n'), nil
}

func setupDocument(repository, formula, toolkitVersion, toolkitSHA, submodules string) []byte {
	var result bytes.Buffer
	result.WriteString(`# Hextap setup

Onboarding created only local files and did not inspect or mutate any remote repository, secret, ruleset, release, or tap.

Before releasing, make `)
	result.WriteString("`main`")
	result.WriteString(` the default branch and enable immutable releases for `)
	result.WriteString("`")
	result.WriteString(repository)
	result.WriteString("`.")
	result.WriteString(`

`)
	// A caller that checks out submodules maps a second secret, so the
	// instructions have to name both or the adopter sets one and the checkout
	// falls back to github.token, which cannot read a sibling private
	// repository. Raised by Codex on PR #24 as P1: the document said "the one
	// required Actions secret" for every project, including ones whose
	// generated caller already referenced SUBMODULES_TOKEN.
	if submodules == "" || submodules == manifest.SubmodulesNone {
		result.WriteString("Set the one required Actions secret. This command prompts securely; do not put a value in argv or a file:\n\n")
		result.WriteString("```sh\n")
		fmt.Fprintf(&result, "gh secret set OP_SERVICE_ACCOUNT_TOKEN --repo github.com/%s\n", repository)
		result.WriteString("```\n\n")
	} else {
		result.WriteString("Set the required Actions secret. This command prompts securely; do not put a value in argv or a file:\n\n")
		result.WriteString("```sh\n")
		fmt.Fprintf(&result, "gh secret set OP_SERVICE_ACCOUNT_TOKEN --repo github.com/%s\n", repository)
		result.WriteString("```\n\n")
		fmt.Fprintf(&result, "This project declares `release.checkout.submodules: %q`, so its caller also maps a second secret, `SUBMODULES_TOKEN`. **Set it only if any of those submodules is private.** Leave it unset for public submodules: the caller then falls back to the job's `GITHUB_TOKEN`, which clones a public submodule perfectly well, and you have not created a long-lived credential nothing needs.\n\n", submodules)
		result.WriteString("If any submodule is private, set it too, because `GITHUB_TOKEN` cannot read a sibling private repository:\n\n")
		result.WriteString("```sh\n")
		fmt.Fprintf(&result, "gh secret set SUBMODULES_TOKEN --repo github.com/%s\n", repository)
		result.WriteString("```\n\n")
		result.WriteString("Work out what the credential needs from one rule rather than from a list of cases. **The credential must be able to read, privately, every repository this workflow clones: this one and each submodule. Whatever it cannot read privately, it cannot clone.** `actions/checkout` presents it for the primary clone as well as for the submodule fetches, which is why this repository is in the rule and not only its submodules.\n\n")
		result.WriteString("Two consequences follow, and between them they answer any layout:\n\n")
		result.WriteString("1. A **public** repository imposes no constraint, because cloning it needs no credential at all. Only the private ones determine the scope.\n")
		result.WriteString("2. A **fine-grained** personal access token selects repositories under a single resource owner, so it suffices exactly when every repository that must be read privately sits under one owner.\n\n")
		result.WriteString("To apply it: list this repository and every submodule, strike the public ones, and what remains is the scope. If the remainder shares one owner, use a fine-grained token limited to exactly those repositories with Contents read and nothing else. It is a different credential from the tap publisher token and the two must not be conflated.\n\n")
		result.WriteString("If the remainder spans owners, a fine-grained token cannot express it, and **that configuration is not supported yet**. Move those repositories under one owner: nothing in the manifest constrains a submodule URL, so that is a choice about layout rather than something the toolkit enforces.\n\n")
		result.WriteString("Do not reach for a broader credential instead. This workflow does not yet validate the submodule URLs a tagged commit declares, so a credential that can read more than the repositories above is a credential a later commit can point somewhere else, and the quality job runs project-declared commands with network after the checkout. Support for that configuration needs a sealed allowlist of submodule URLs first, tracked as SB23-2504. Until it lands, keep the credential narrow or keep the repositories under one owner.\n\n")
	}
	result.WriteString("Review the two owned ruleset payloads:\n\n```sh\n")
	result.WriteString("cat .hextap/rulesets/main.json\n")
	result.WriteString("cat .hextap/rulesets/release-tags.json\n")
	result.WriteString("```\n\nApply each reviewed payload manually:\n\n```sh\n")
	fmt.Fprintf(&result, "gh api --hostname github.com --method POST repos/%s/rulesets --input .hextap/rulesets/main.json\n", repository)
	fmt.Fprintf(&result, "gh api --hostname github.com --method POST repos/%s/rulesets --input .hextap/rulesets/release-tags.json\n", repository)
	result.WriteString("```\n\n")
	fmt.Fprintf(&result, "The tap registration destination is exactly `Projects/%s.json`, but the initial tap pull request must not contain that JSON alone. It must pair the byte-exact `.hextap/tap-registration.json` with `Formula/%s.rb`, and that Formula must declare `class %s < Formula`. The tap remains the Formula registry; the paired pull request and merge are owner-controlled manual actions.\n\n", formula, formula, classForFormula(formula))
	result.WriteString("Coordinator bootstrap/recovery is an external adopter task:\n\n")
	result.WriteString("1. Merge the reviewed onboarding files to `main`, apply the two reviewed rulesets, set the required secret, and enable immutable releases.\n")
	result.WriteString("2. Push the first stable tag and let the full caller create and verify the immutable source release. When the project is not registered yet, the initial Homebrew publication can stop at the tap registry gate; do not replace or recreate that release.\n")
	result.WriteString("3. From that immutable release and its verified `SHA256SUMS`, have the coordinator use the trusted pinned toolkit to render the exact Formula. Do not invent checksums or commit a placeholder Formula.\n")
	fmt.Fprintf(&result, "4. Open one tap pull request that adds both `Projects/%s.json` and the release-backed `Formula/%s.rb`; merge only after tap CI passes.\n", formula, formula)
	result.WriteString("5. Dispatch the existing stable tag in `homebrew-only` mode to finish or recover publication. Do not create a replacement tag.\n\n")
	// The toolkit's own caller is relative and carries no external pin, so the
	// pinned-caller paragraph cannot be written for it: both values are empty by
	// definition, and validate.go compares this document byte-for-byte against
	// this generator. Without the branch, .hextap/SETUP.md could never match for
	// the one repository that owns the reusable workflow.
	if toolkitVersion == "" && toolkitSHA == "" {
		result.WriteString("This repository owns the reusable release workflow, so its caller references it relatively and carries no external pin. There is no toolkit tag or commit to keep in sync here, and none may be added: a pin would make the repository an adopter of a different copy of itself.\n\nWhat runs is therefore whichever commit the run was started from, not the tag being released. On a tag push the two are the same, and `full` mode asserts it. On a `homebrew-only` dispatch from `main`, step 5 above, the toolkit code that executes is `main`, while the source being released is still checked out at the tag and still required to be contained in `main`. Read a recovery run's provenance as the tag for the released source and the dispatched ref for the code that published it.\n")
		return result.Bytes()
	}
	fmt.Fprintf(&result, "The caller is pinned to stable toolkit tag `%s` at full commit `%s`; keep both the tag comment and immutable SHA provenance when upgrading. Never replace the pin with `@main` or a floating major tag.\n", toolkitVersion, toolkitSHA)
	return result.Bytes()
}

func normalizeChecks(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func classForFormula(name string) string {
	parts := strings.Split(name, "-")
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}
