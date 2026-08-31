package workflow

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hextapCallerWorkflow is the shape onboarding generates for an adopter: a
// full-SHA pinned call into the toolkit's reusable release workflow. It is the
// one workflow permitted to start from a tag push.
const hextapCallerWorkflow = `name: Hextap release

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
  attestations: write
  contents: write
  id-token: write

jobs:
  release:
    uses: SijanC147/hextap-toolkit/.github/workflows/release-go.yml@0123456789abcdef0123456789abcdef01234567
    with:
      manifest_path: .hextap.json
      tag: ${{ github.event_name == 'workflow_dispatch' && inputs.tag || github.ref_name }}
      mode: full
    secrets:
      op_service_account_token: ${{ secrets.OP_SERVICE_ACCOUNT_TOKEN }}
`

// incidentWorkflow is the file that actually evaded the original substring
// check, reproduced byte for byte in the part that mattered: a single-quoted
// tag filter with a trailing comment, contents: write, and a release upload.
const incidentWorkflow = `name: Sign and Upload Windows Release Binary

on:
  push:
    tags:
      - 'v*'  # Matches all v* tags (stable and pre-release)

permissions:
  contents: write

jobs:
  sign:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: oven-sh/setup-bun@v2
        with:
          bun-version: latest
      - name: Upload the signed binary
        run: |
          gh release upload "${{ github.ref_name }}" dist/app.exe
`

func writeWorkflows(t *testing.T, files map[string]string) string {
	t.Helper()
	directory := t.TempDir()
	for name, content := range files {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write workflow fixture %q: %v", name, err)
		}
	}
	return directory
}

func analyzeWorkflows(t *testing.T, files map[string]string) *Report {
	t.Helper()
	report, err := Analyze(writeWorkflows(t, files))
	if err != nil {
		t.Fatalf("analyse workflow fixtures: %v", err)
	}
	return report
}

func findWorkflow(t *testing.T, report *Report, file string) Workflow {
	t.Helper()
	for _, workflow := range report.Workflows {
		if workflow.File == file {
			return workflow
		}
	}
	t.Fatalf("workflow %q missing from report", file)
	return Workflow{}
}

// TestTagExclusivityRejectsEveryEvasionShape pairs the legitimate caller with
// one competing workflow at a time. Each case must produce exactly one finding,
// against the competing file and never against the caller.
func TestTagExclusivityRejectsEveryEvasionShape(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		rule    string
	}{
		{
			name:    "the exact evading workflow from the incident",
			file:    "signpath-release.yml",
			content: incidentWorkflow,
			rule:    RuleCompetingTagTrigger,
		},
		{
			name: "single quoted tag filter",
			file: "single.yml",
			content: `name: Single
on:
  push:
    tags:
      - 'v*'
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo single
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "double quoted tag filter",
			file: "double.yml",
			content: `name: Double
on:
  push:
    tags:
      - "v*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo double
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "unquoted tag filter with a trailing comment",
			file: "comment.yml",
			content: `name: Comment
on:
  push:
    tags:
      - v*   # every version tag
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo comment
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "inline array tag filter",
			file: "inline.yml",
			content: `name: Inline
on:
  push:
    tags: ['v*']
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo inline
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "inline array with spaces and double quotes",
			file: "inline-spaced.yml",
			content: `name: Inline spaced
on:
  push:
    tags: [ "v*", "release-*" ]
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo spaced
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "bare push with no filters at all",
			file: "bare-push.yml",
			content: `name: Bare push
on:
  push:
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo bare
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "scalar on push shorthand",
			file: "scalar-push.yml",
			content: `name: Scalar push
on: push
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo scalar
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "sequence on push shorthand",
			file: "sequence-push.yml",
			content: `name: Sequence push
on: [push]
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo sequence
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "push filtered only by paths still reaches every tag",
			file: "paths-only.yml",
			content: `name: Paths only
on:
  push:
    paths:
      - "src/**"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo paths
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "tags-ignore leaves every other tag reachable",
			file: "tags-ignore.yml",
			content: `name: Tags ignore
on:
  push:
    tags-ignore:
      - "internal-*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo ignore
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name:    "a yaml extension is loaded exactly like a yml extension",
			file:    "signpath-release.yaml",
			content: incidentWorkflow,
			rule:    RuleCompetingTagTrigger,
		},
		{
			name:    "an uppercased extension is not assumed to be inert",
			file:    "Signpath-Release.YML",
			content: incidentWorkflow,
			rule:    RuleCompetingTagTrigger,
		},
		{
			name: "create fires on every new tag and cannot be filtered",
			file: "create.yml",
			content: `name: Create
on: create
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo create
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "workflow_run chained from the tag responsive caller",
			file: "chained.yml",
			content: `name: Chained
on:
  workflow_run:
    workflows:
      - Hextap release
    types:
      - completed
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo chained
`,
			rule: RuleCompetingTagTrigger,
		},
		{
			name: "push combining tags and tags-ignore is refused rather than guessed",
			file: "contradictory.yml",
			content: `name: Contradictory
on:
  push:
    tags:
      - "v*"
    tags-ignore:
      - "v0.*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo contradictory
`,
			rule: RuleUnreadableWorkflow,
		},
		{
			name: "a misspelled ref filter is refused rather than read as safe",
			file: "misspelled.yml",
			content: `name: Misspelled
on:
  push:
    branch:
      - main
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo misspelled
`,
			rule: RuleUnreadableWorkflow,
		},
		{
			name: "a workflow with no on key is refused rather than read as safe",
			file: "no-triggers.yml",
			content: `name: No triggers
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo none
`,
			rule: RuleUnreadableWorkflow,
		},
		{
			name: "an unrecognised event is refused rather than read as safe",
			file: "invented.yml",
			content: `name: Invented
on:
  puush:
    tags:
      - "v*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo invented
`,
			rule: RuleUnreadableWorkflow,
		},
		{
			name: "a duplicated on key is refused rather than resolved by precedence",
			file: "duplicate.yml",
			content: `name: Duplicate
on:
  pull_request:
on:
  push:
    tags:
      - "v*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo duplicate
`,
			rule: RuleUnreadableWorkflow,
		},
		{
			name: "a YAML anchor is refused rather than resolved",
			file: "anchored.yml",
			content: `name: Anchored
on: &triggers
  push:
    tags:
      - "v*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo anchored
`,
			rule: RuleUnreadableWorkflow,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				test.file:         test.content,
			})
			findings := report.TagExclusivityFindings("")
			if len(findings) != 1 {
				t.Fatalf("findings = %v, want exactly one against %s", findings, test.file)
			}
			if findings[0].File != test.file {
				t.Fatalf("finding file = %q, want %q", findings[0].File, test.file)
			}
			if findings[0].Rule != test.rule {
				t.Fatalf("finding rule = %q, want %q (detail: %s)", findings[0].Rule, test.rule, findings[0].Detail)
			}
			if findings[0].Detail == "" || findings[0].Remedy == "" {
				t.Fatalf("finding must name the construct and the fix, got %#v", findings[0])
			}
		})
	}
}

// TestCallerExemptionCoversTheWholeFile guards the review finding that a single
// matching job was enough to exempt a file. The exemption is granted to the
// whole workflow, so every job in it has to be the call.
func TestCallerExemptionCoversTheWholeFile(t *testing.T) {
	smuggled := `name: Hextap release
on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    uses: SijanC147/hextap-toolkit/.github/workflows/release-go.yml@0123456789abcdef0123456789abcdef01234567
    with:
      manifest_path: .hextap.json
      tag: ${{ github.ref_name }}
      mode: full
  extra:
    runs-on: ubuntu-latest
    steps:
      - run: gh release upload "${{ github.ref_name }}" extra.exe
`
	report := analyzeWorkflows(t, map[string]string{DefaultCallerFile: smuggled})
	if findWorkflow(t, report, DefaultCallerFile).HextapCaller {
		t.Fatal("a caller carrying a second asset-uploading job must not be recognised as the caller")
	}
	findings := report.TagExclusivityFindings("")
	if len(findings) != 1 || findings[0].Rule != RuleCompetingTagTrigger {
		t.Fatalf("findings = %v, want one competing-tag-trigger", findings)
	}
}

// TestCallerExemptionRequiresTheToolkitRepository guards the review finding that
// a suffix match accepted any repository publishing a file at the same path.
func TestCallerExemptionRequiresTheToolkitRepository(t *testing.T) {
	tests := map[string]string{
		"foreign repository at the same path": "attacker/repo/.github/workflows/release-go.yml@0123456789abcdef0123456789abcdef01234567",
		"toolkit path without a commit SHA":   "SijanC147/hextap-toolkit/.github/workflows/release-go.yml@main",
		"relative escape out of the checkout": "./../evil/.github/workflows/release-go.yml",
	}
	for name, reference := range tests {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: `name: Hextap release
on:
  push:
    tags:
      - "v*"
jobs:
  release:
    uses: ` + reference + `
`,
			})
			if findWorkflow(t, report, DefaultCallerFile).HextapCaller {
				t.Fatalf("%q was accepted as the Hextap reusable release workflow", reference)
			}
			if findings := report.TagExclusivityFindings(""); len(findings) != 1 {
				t.Fatalf("findings = %v, want one", findings)
			}
		})
	}
}

// TestUnreadableWorkflowNameIsRefused guards the review finding that a name the
// reader could not represent was recorded as empty, which let a sibling
// workflow_run trigger watching that name go unresolved.
func TestUnreadableWorkflowNameIsRefused(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: `name: >-
  Hextap release
on:
  push:
    tags:
      - "v*"
jobs:
  release:
    uses: ./.github/workflows/release-go.yml
`,
	})
	caller := findWorkflow(t, report, DefaultCallerFile)
	if caller.TagTrigger != TagTriggerUnknown {
		t.Fatalf("trigger = %v, want unknown when the name cannot be represented", caller.TagTrigger)
	}
	findings := report.TagExclusivityFindings("")
	if len(findings) != 1 || findings[0].Rule != RuleUnreadableWorkflow {
		t.Fatalf("findings = %v, want one unreadable-workflow finding", findings)
	}
}

// TestWorkflowRunResolvesTheDefaultWorkflowName covers the GitHub fallback: a
// workflow that declares no name is referred to by its file path.
func TestWorkflowRunResolvesTheDefaultWorkflowName(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"tagged.yml": `on:
  push:
    tags:
      - "v*"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo tagged
`,
		"chained.yml": `name: Chained
on:
  workflow_run:
    workflows:
      - .github/workflows/tagged.yml
    types:
      - completed
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo chained
`,
	})
	flagged := make(map[string]bool)
	for _, finding := range report.TagExclusivityFindings("") {
		flagged[finding.File] = true
	}
	if !flagged["tagged.yml"] || !flagged["chained.yml"] {
		t.Fatalf("flagged = %v, want both the unnamed tag responder and the workflow chained off its default name", flagged)
	}
}

// TestFlowMappingTriggersAreRead guards the review finding that an unquoted flow
// mapping key was consumed through its colon, so every flow mapping was reported
// unreadable rather than classified.
func TestFlowMappingTriggersAreRead(t *testing.T) {
	document := parseFixture(t, "on: {push: {tags: ['v*']}}\n")
	analysis := analyzeTriggers(document)
	if analysis.trigger != TagTriggerFiltered {
		t.Fatalf("trigger = %v (%s), want a filtered tag trigger", analysis.trigger, analysis.reason)
	}

	colonised := parseFixture(t, "jobs: {build: {image: node:18}}\n")
	if got := colonised.child("jobs").child("build").child("image").value; got != "node:18" {
		t.Fatalf("image = %q, a colon inside a flow value must not end the scalar", got)
	}
}

func TestPinAuditRejectsPartialRuntimeVersions(t *testing.T) {
	floating := []string{"22", "1.24", "20"}
	exact := []string{"1.3.14", "v1.3.14", "1.3.14-canary.1", "22.11.0"}

	audit := func(t *testing.T, version string) []Finding {
		t.Helper()
		report := analyzeWorkflows(t, map[string]string{
			DefaultCallerFile: hextapCallerWorkflow,
			"release.yml": `name: Runtime
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          node-version: "` + version + `"
`,
		})
		return report.PinFindings()
	}

	for _, version := range floating {
		t.Run("floating "+version, func(t *testing.T) {
			if findings := audit(t, version); len(findings) != 1 || findings[0].Rule != RuleFloatingRuntimeVersion {
				t.Fatalf("findings = %v, want one floating-runtime-version finding", findings)
			}
		})
	}
	for _, version := range exact {
		t.Run("exact "+version, func(t *testing.T) {
			if findings := audit(t, version); len(findings) != 0 {
				t.Fatalf("exact version %q produced findings: %v", version, findings)
			}
		})
	}
}

func TestPinAuditReportsLocalActionsAsUnaudited(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"release.yml": `name: Local action
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: ./.github/actions/build
`,
	})
	findings := report.PinFindings()
	if len(findings) != 1 || findings[0].Rule != RuleUnauditedLocalAction {
		t.Fatalf("findings = %v, want one unaudited-local-action finding", findings)
	}
}

// TestTagExclusivityAcceptsSafeWorkflows is the other half of the contract. A
// checker that refuses everything is as useless as one that refuses nothing.
func TestTagExclusivityAcceptsSafeWorkflows(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"ci.yml": `name: CI
on:
  push:
    branches:
      - main
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - run: go test ./...
`,
		"branches-inline.yml": `name: Branches inline
on:
  push:
    branches: [main, develop]
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo branches
`,
		"branches-ignore.yml": `name: Branches ignore
on:
  push:
    branches-ignore:
      - "wip/**"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo ignore
`,
		"manual.yml": `name: Manual
on:
  workflow_dispatch:
    inputs:
      reason:
        description: Why this run was started
        required: false
        type: string
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo manual
`,
		"review.yml": `name: Review
on:
  pull_request:
    types: [opened, synchronize]
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo review
`,
		"reusable.yml": `name: Reusable
on:
  workflow_call:
    inputs:
      tag:
        required: true
        type: string
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo reusable
`,
		"scheduled.yml": `name: Scheduled
on:
  schedule:
    - cron: "0 3 * * *"
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo scheduled
`,
		"chain-from-branch-workflow.yml": `name: Chain from CI
on:
  workflow_run:
    workflows:
      - CI
    types:
      - completed
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo chained
`,
		"signpath-release.yml.disabled": incidentWorkflow,
		"notes.md":                      "not a workflow at all\n",
	})

	if findings := report.TagExclusivityFindings(""); len(findings) != 0 {
		t.Fatalf("safe workflows produced findings: %v", findings)
	}

	disabled := findWorkflow(t, report, "signpath-release.yml.disabled")
	if disabled.Active {
		t.Fatal("a .disabled workflow must not be treated as active")
	}
	if disabled.InactiveReason == "" {
		t.Fatal("an inactive workflow must explain why it never runs")
	}
}

// TestCallerExemptionIsEarnedByContent is the governing rule of the platform
// applied to this check: sitting at the caller path grants nothing. Only a file
// that actually calls the Hextap release workflow is exempt.
func TestCallerExemptionIsEarnedByContent(t *testing.T) {
	squatter := `name: Hextap release
on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - run: gh release upload "${{ github.ref_name }}" extra.exe
`

	report := analyzeWorkflows(t, map[string]string{DefaultCallerFile: squatter})
	findings := report.TagExclusivityFindings("")
	if len(findings) != 1 || findings[0].Rule != RuleCompetingTagTrigger {
		t.Fatalf("findings = %v, want one competing-tag-trigger against the unverified caller", findings)
	}
	if !strings.Contains(findings[0].Detail, "earns no exemption") {
		t.Fatalf("detail must explain the refused exemption, got %q", findings[0].Detail)
	}

	caller := findWorkflow(t, report, DefaultCallerFile)
	if caller.HextapCaller {
		t.Fatal("a workflow that never calls the reusable release workflow must not be recognised as the caller")
	}
}

func TestVerifiedCallerIsRecognisedInBothReferenceForms(t *testing.T) {
	selfCaller := `name: Hextap toolkit release
on:
  push:
    tags:
      - "v*"
jobs:
  release:
    uses: ./.github/workflows/release-go.yml
    with:
      manifest_path: .hextap.json
      tag: ${{ github.ref_name }}
      mode: full
`

	for name, content := range map[string]string{
		"adopter full SHA pin":       hextapCallerWorkflow,
		"toolkit relative self call": selfCaller,
	} {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{DefaultCallerFile: content})
			caller := findWorkflow(t, report, DefaultCallerFile)
			if !caller.HextapCaller {
				t.Fatalf("caller not recognised, trigger reason %q", caller.TriggerReason)
			}
			if caller.TagTrigger != TagTriggerFiltered {
				t.Fatalf("caller trigger = %v, want a filtered tag trigger", caller.TagTrigger)
			}
			if findings := report.TagExclusivityFindings(""); len(findings) != 0 {
				t.Fatalf("verified caller produced findings: %v", findings)
			}
		})
	}
}

func TestPreflightRefusesSourceWithoutAVerifiedCaller(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		"ci.yml": `name: CI
on:
  pull_request:
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo ci
`,
	})

	findings := report.PreflightFindings("")
	if len(findings) != 1 || findings[0].Rule != RuleMissingHextapCaller {
		t.Fatalf("findings = %v, want one missing-hextap-caller finding", findings)
	}
}

func TestPreflightAcceptsAConformingSourceTree(t *testing.T) {
	directory := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"ci.yml": `name: CI
on:
  push:
    branches:
      - main
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo ci
`,
	})

	findings, err := Preflight(directory, "")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("conforming source produced findings: %v", findings)
	}
}

func TestPinAuditRejectsMutableReleaseInputs(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile:      hextapCallerWorkflow,
		"signpath-release.yml": incidentWorkflow,
	})

	findings := report.PinFindings()
	byRule := make(map[string][]Finding)
	for _, finding := range findings {
		if finding.File != "signpath-release.yml" {
			t.Fatalf("pin finding against an unexpected file: %#v", finding)
		}
		byRule[finding.Rule] = append(byRule[finding.Rule], finding)
	}

	if len(byRule[RuleUnpinnedAction]) != 2 {
		t.Fatalf("unpinned action findings = %v, want one each for checkout and setup-bun", byRule[RuleUnpinnedAction])
	}
	if len(byRule[RuleFloatingRuntimeVersion]) != 1 {
		t.Fatalf("floating runtime findings = %v, want one for bun-version", byRule[RuleFloatingRuntimeVersion])
	}
	if !strings.Contains(byRule[RuleFloatingRuntimeVersion][0].Detail, "bun-version") {
		t.Fatalf("floating runtime finding must name the input, got %q", byRule[RuleFloatingRuntimeVersion][0].Detail)
	}
}

func TestPinAuditAcceptsPinnedAndRuntimeResolvedInputs(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"release.yml": `name: Pinned release
on:
  push:
    branches:
      - main

permissions:
  contents: write

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2.2.0
        with:
          bun-version: "1.3.14"
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2.2.0
        with:
          bun-version: ${{ needs.validate.outputs.runtime_version }}
`,
	})

	if findings := report.PinFindings(); len(findings) != 0 {
		t.Fatalf("pinned workflow produced findings: %v", findings)
	}
}

func TestPinAuditRejectsFloatingRuntimeSelectors(t *testing.T) {
	tests := []string{"latest", "lts/*", "1.x", "^1.3.0", "stable", ""}
	for _, version := range tests {
		t.Run("bun-version "+version, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"release.yml": `name: Floating
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2.2.0
        with:
          bun-version: "` + version + `"
`,
			})
			findings := report.PinFindings()
			if len(findings) != 1 || findings[0].Rule != RuleFloatingRuntimeVersion {
				t.Fatalf("findings = %v, want one floating-runtime-version finding", findings)
			}
		})
	}
}

func TestPinAuditSkipsWorkflowsThatCannotReachARelease(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"lint.yml": `name: Lint
on:
  pull_request:

permissions:
  contents: read

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          bun-version: latest
`,
	})

	lint := findWorkflow(t, report, "lint.yml")
	if lint.ReleaseCapable {
		t.Fatal("a read-only pull_request workflow must not be treated as release capable")
	}
	if findings := report.PinFindings(); len(findings) != 0 {
		t.Fatalf("pin audit reached a workflow that cannot release: %v", findings)
	}
}

// TestToolkitOwnWorkflowsSatisfyTheirOwnPolicy dogfoods the analyser against
// the workflows this repository releases itself with. If the toolkit cannot
// pass its own check, adopters have no reason to trust it.
func TestToolkitOwnWorkflowsSatisfyTheirOwnPolicy(t *testing.T) {
	report, err := Analyze(filepath.Join("..", "..", DefaultWorkflowDirectory))
	if err != nil {
		t.Fatalf("analyse the toolkit's own workflows: %v", err)
	}

	for _, workflow := range report.Workflows {
		if workflow.Active && workflow.TagTrigger == TagTriggerUnknown {
			t.Errorf("%s was not readable: %s", workflow.File, workflow.TriggerReason)
		}
	}
	if findings := report.PreflightFindings(""); len(findings) != 0 {
		t.Fatalf("the toolkit's own workflows fail the policy: %v", findings)
	}

	caller := findWorkflow(t, report, DefaultCallerFile)
	if !caller.HextapCaller {
		t.Fatal("the toolkit's own release caller was not recognised as a Hextap caller")
	}
	if !caller.TagTrigger.RespondsToTagPush() {
		t.Fatal("the toolkit's own release caller must respond to a tag push")
	}
}

func TestAnalyzeWrapsDirectoryAndFileFailures(t *testing.T) {
	sentinel := errors.New("disk unavailable")

	if _, err := analyze("workflows",
		func(string) ([]fs.DirEntry, error) { return nil, sentinel },
		func(string) ([]byte, error) { return nil, nil },
	); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "read workflow directory") {
		t.Fatalf("directory failure = %v, want a wrapped read workflow directory error", err)
	}

	directory := writeWorkflows(t, map[string]string{DefaultCallerFile: hextapCallerWorkflow})
	if _, err := analyze(directory, os.ReadDir,
		func(string) ([]byte, error) { return nil, sentinel },
	); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "read workflow") {
		t.Fatalf("file failure = %v, want a wrapped read workflow error", err)
	}
}
