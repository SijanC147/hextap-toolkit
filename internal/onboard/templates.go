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
	RequiredApprovingReviewCount   int      `json:"required_approving_review_count"`
	DismissStaleReviewsOnPush      bool     `json:"dismiss_stale_reviews_on_push"`
	RequireCodeOwnerReview         bool     `json:"require_code_owner_review"`
	RequireLastPushApproval        bool     `json:"require_last_push_approval"`
	RequiredReviewThreadResolution bool     `json:"required_review_thread_resolution"`
	AllowedMergeMethods            []string `json:"allowed_merge_methods"`
}

type requiredStatusCheck struct {
	Context string `json:"context"`
}

type requiredStatusParameters struct {
	RequiredStatusChecks             []requiredStatusCheck `json:"required_status_checks"`
	StrictRequiredStatusChecksPolicy bool                  `json:"strict_required_status_checks_policy"`
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

func workflowBytes(toolkitVersion, toolkitSHA string) []byte {
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
      mode: ${{ github.event_name == 'workflow_dispatch' && 'homebrew-only' || 'full' }}
    secrets:
      op_service_account_token: ${{ secrets.OP_SERVICE_ACCOUNT_TOKEN }}
`, toolkitSHA, toolkitVersion))
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
				RequiredApprovingReviewCount:   0,
				DismissStaleReviewsOnPush:      false,
				RequireCodeOwnerReview:         false,
				RequireLastPushApproval:        false,
				RequiredReviewThreadResolution: true,
				AllowedMergeMethods:            []string{"merge", "rebase", "squash"},
			}},
			{Type: "required_status_checks", Parameters: requiredStatusParameters{
				RequiredStatusChecks:             statusChecks,
				StrictRequiredStatusChecksPolicy: true,
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

func setupDocument(repository, formula, toolkitVersion, toolkitSHA string) []byte {
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

Set the one required Actions secret. This command prompts securely; do not put a value in argv or a file:

`)
	result.WriteString("```sh\n")
	fmt.Fprintf(&result, "gh secret set OP_SERVICE_ACCOUNT_TOKEN --repo %s\n", repository)
	result.WriteString("```\n\n")
	result.WriteString("Review the two owned ruleset payloads:\n\n```sh\n")
	result.WriteString("cat .hextap/rulesets/main.json\n")
	result.WriteString("cat .hextap/rulesets/release-tags.json\n")
	result.WriteString("```\n\nApply each reviewed payload manually:\n\n```sh\n")
	fmt.Fprintf(&result, "gh api --method POST repos/%s/rulesets --input .hextap/rulesets/main.json\n", repository)
	fmt.Fprintf(&result, "gh api --method POST repos/%s/rulesets --input .hextap/rulesets/release-tags.json\n", repository)
	result.WriteString("```\n\n")
	fmt.Fprintf(&result, "The tap registration destination is exactly `Projects/%s.json`, but the initial tap pull request must not contain that JSON alone. It must pair the byte-exact `.hextap/tap-registration.json` with `Formula/%s.rb`, and that Formula must declare `class %s < Formula`. The tap remains the Formula registry; the paired pull request and merge are owner-controlled manual actions.\n\n", formula, formula, classForFormula(formula))
	result.WriteString("Coordinator bootstrap/recovery is an external adopter task:\n\n")
	result.WriteString("1. Merge the reviewed onboarding files to `main`, apply the two reviewed rulesets, set the required secret, and enable immutable releases.\n")
	result.WriteString("2. Push the first stable tag and let the full caller create and verify the immutable source release. When the project is not registered yet, the initial Homebrew publication can stop at the tap registry gate; do not replace or recreate that release.\n")
	result.WriteString("3. From that immutable release and its verified `SHA256SUMS`, have the coordinator use the trusted pinned toolkit to render the exact Formula. Do not invent checksums or commit a placeholder Formula.\n")
	fmt.Fprintf(&result, "4. Open one tap pull request that adds both `Projects/%s.json` and the release-backed `Formula/%s.rb`; merge only after tap CI passes.\n", formula, formula)
	result.WriteString("5. Dispatch the existing stable tag in `homebrew-only` mode to finish or recover publication. Do not create a replacement tag.\n\n")
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
