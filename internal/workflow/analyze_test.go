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

func preflightFindings(t *testing.T, report *Report, policy Policy) []Finding {
	t.Helper()
	findings, err := report.PreflightFindings(policy)
	if err != nil {
		t.Fatalf("preflight findings: %v", err)
	}
	return findings
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
			findings := report.TagExclusivityFindings(Policy{})
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
	findings := report.TagExclusivityFindings(Policy{})
	if len(findings) != 1 || findings[0].Rule != RuleCompetingTagTrigger {
		t.Fatalf("findings = %v, want one competing-tag-trigger", findings)
	}
}

// TestCallerExemptionRequiresTheToolkitRepository guards the review finding that
// a suffix match accepted any repository publishing a file at the same path.
func TestCallerExemptionRequiresTheToolkitRepository(t *testing.T) {
	tests := map[string]string{
		"foreign repository at the same path":    "attacker/repo/.github/workflows/release-go.yml@0123456789abcdef0123456789abcdef01234567",
		"toolkit path without a commit SHA":      "SijanC147/hextap-toolkit/.github/workflows/release-go.yml@main",
		"relative escape out of the checkout":    "./../evil/.github/workflows/release-go.yml",
		"relative self-call outside the toolkit": "./.github/workflows/release-go.yml",
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
				t.Fatalf("%q was accepted as the external Hextap reusable release workflow", reference)
			}
			if findings := report.TagExclusivityFindings(Policy{}); len(findings) != 1 {
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
	findings := report.TagExclusivityFindings(Policy{})
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
	for _, finding := range report.TagExclusivityFindings(Policy{}) {
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

// TestPinAuditReportsLocalActionsAsUnaudited guards the review finding that a
// local reference merely starting with the workflow directory was exempted. An
// action stored below that directory is not a reusable workflow this pass has
// read, and a step can never reference a reusable workflow at all. The callee
// fixture carries an unpinned action on purpose: the one accepted case must
// show the audit reaching the callee, not merely the caller being excused.
func TestPinAuditReportsLocalActionsAsUnaudited(t *testing.T) {
	reusable := `name: Reusable
on:
  workflow_call:
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`
	tests := []struct {
		name      string
		position  string
		reference string
		want      int
	}{
		{"action beside the workflow directory", "step", "./.github/actions/build", 1},
		{"action stored below the workflow directory", "job", "./.github/workflows/actions/package", 1},
		{"workflow file that is not in the directory", "job", "./.github/workflows/missing.yml", 1},
		{"workflow file referenced from a step", "step", "./.github/workflows/reusable.yml", 1},
		{"workflow file this pass has read", "job", "./.github/workflows/reusable.yml", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var caller string
			if test.position == "job" {
				caller = `name: Local
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    uses: ` + test.reference + `
`
			} else {
				caller = `name: Local
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: ` + test.reference + `
`
			}
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"reusable.yml":    reusable,
				"release.yml":     caller,
			})
			byFile := make(map[string][]Finding)
			for _, finding := range report.PinFindings() {
				byFile[finding.File] = append(byFile[finding.File], finding)
			}
			callee := byFile["reusable.yml"]
			if len(callee) != 1 || callee[0].Rule != RuleUnpinnedAction {
				t.Fatalf("callee findings = %v, want its unpinned action audited in every case", callee)
			}
			refused := byFile["release.yml"]
			if len(refused) != test.want {
				t.Fatalf("caller findings = %v, want %d", refused, test.want)
			}
			if test.want == 1 && refused[0].Rule != RuleUnauditedLocalAction {
				t.Fatalf("finding = %#v, want unaudited-local-action against release.yml", refused[0])
			}
		})
	}
}

// TestLocalCalleeExemptionRequiresAnAuditedCallee guards the hole an
// independent review found in the fix above: a callee this pass had read but
// skipped as unable to release excused its caller's reference while nobody
// audited the callee. A workflow_call callee runs under whatever its caller
// grants, so it is never proven unable, and its mutable inputs are reported.
func TestLocalCalleeExemptionRequiresAnAuditedCallee(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"publish.yml": `name: Publish
on:
  workflow_dispatch:
permissions:
  contents: write
jobs:
  publish:
    uses: ./.github/workflows/helper.yml
`,
		"helper.yml": `name: Helper
on:
  workflow_call:
permissions:
  contents: read
jobs:
  build:
    runs-on: ubuntu-latest
    container: evil:latest
    steps:
      - uses: actions/checkout@v4
      - uses: oven-sh/setup-bun@v2
        with:
          bun-version: latest
      - run: gh release upload "$TAG" dist/app.exe
`,
	})
	if !findWorkflow(t, report, "helper.yml").ReleaseCapable {
		t.Fatal("a workflow_call callee must never be proven unable to release")
	}
	rules := make(map[string]int)
	for _, finding := range report.PinFindings() {
		if finding.File != "helper.yml" {
			t.Fatalf("finding against %s: %v", finding.File, finding)
		}
		rules[finding.Rule]++
	}
	if rules[RuleUnpinnedContainer] != 1 || rules[RuleUnpinnedAction] != 2 || rules[RuleFloatingRuntimeVersion] != 1 {
		t.Fatalf("rules = %v, want the container, both actions and the runtime selector reported", rules)
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

	if findings := report.TagExclusivityFindings(Policy{}); len(findings) != 0 {
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
	findings := report.TagExclusivityFindings(Policy{})
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

	for name, test := range map[string]struct {
		content string
		policy  Policy
	}{
		"adopter full SHA pin":       {hextapCallerWorkflow, Policy{}},
		"toolkit relative self call": {selfCaller, Policy{SelfRelease: true}},
	} {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{DefaultCallerFile: test.content})
			caller := findWorkflow(t, report, DefaultCallerFile)
			if !caller.HextapCaller && !caller.SelfCaller {
				t.Fatalf("caller not recognised, trigger reason %q", caller.TriggerReason)
			}
			if caller.TagTrigger != TagTriggerFiltered {
				t.Fatalf("caller trigger = %v, want a filtered tag trigger", caller.TagTrigger)
			}
			if findings := report.TagExclusivityFindings(test.policy); len(findings) != 0 {
				t.Fatalf("verified caller produced findings: %v", findings)
			}
		})
	}

	// The relative self-call is the toolkit's own form. A directory cannot show
	// which repository it belongs to, so outside a stated self-release an
	// adopter-authored release-go.yml at that path must earn nothing.
	t.Run("relative self call outside the toolkit", func(t *testing.T) {
		report := analyzeWorkflows(t, map[string]string{DefaultCallerFile: selfCaller})
		findings := report.TagExclusivityFindings(Policy{})
		if len(findings) != 1 || findings[0].Rule != RuleCompetingTagTrigger || !strings.Contains(findings[0].Detail, "relative self-call") {
			t.Fatalf("findings = %v, want the self-caller refused as competing", findings)
		}
		rules := make(map[string]string)
		for _, finding := range preflightFindings(t, report, Policy{}) {
			rules[finding.Rule] = finding.Detail
		}
		if !strings.Contains(rules[RuleMissingHextapCaller], "toolkit's own form") {
			t.Fatalf("preflight rules = %v, want no verified caller, explained as the toolkit's own form", rules)
		}
		if _, refused := rules[RuleUnauditedLocalAction]; !refused {
			t.Fatalf("preflight rules = %v, want the absent callee reported as unaudited", rules)
		}
	})
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

	findings := preflightFindings(t, report, Policy{})
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

	findings, err := Preflight(directory, Policy{DefaultBranchDirectory: directory})
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

// TestPinAuditRejectsFloatingRuntimeSelectors covers the selector names and
// value shapes that float. The expression cases guard the review finding that
// a repository variable, which changes with no commit, was accepted as a
// runtime version; only the output of a job or step in the same file is.
func TestPinAuditRejectsFloatingRuntimeSelectors(t *testing.T) {
	floating := []struct{ key, value string }{
		{"bun-version", "latest"},
		{"bun-version", "lts/*"},
		{"bun-version", "1.x"},
		{"bun-version", "^1.3.0"},
		{"bun-version", "stable"},
		{"bun-version", ""},
		{"toolchain", "stable"},
		{"toolchain", "nightly"},
		{"toolchain", "1.80"},
		{"sdk", "stable"},
		{"terraform_version", "1.5"},
		{"Node-Version", "22"},
		{"bun-version", "${{ vars.BUN_VERSION }}"},
		{"bun-version", "${{ inputs.bun }}"},
		{"go-version", "${{ matrix.go }}"},
		{"go-version", "${{ env.GO_VERSION }}"},
		{"bun-version", "v${{ needs.validate.outputs.runtime_version }}"},
		{"bun-version", "${{ needs.validate.outputs.runtime_version || 'latest' }}"},
	}
	exact := []struct{ key, value string }{
		{"toolchain", "1.80.0"},
		{"sdk", "3.4.0"},
		{"terraform_version", "1.5.7"},
		{"bun-version", "${{ needs.validate.outputs.runtime_version }}"},
		{"bun-version", "${{ steps.manifest.outputs.runtime_version }}"},
		// A file in the tagged source is fixed by the tag; its content is
		// outside this directory-only audit and deliberately not read.
		{"go-version-file", "go.mod"},
		// channel names a Slack channel in several actions and a Flutter
		// release channel in one; refusing it would refuse a notification step.
		{"channel", "stable"},
	}

	audit := func(t *testing.T, key, value string) []Finding {
		t.Helper()
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
          ` + key + `: "` + value + `"
`,
		})
		return report.PinFindings()
	}

	for _, test := range floating {
		t.Run("floating "+test.key+" "+test.value, func(t *testing.T) {
			findings := audit(t, test.key, test.value)
			if len(findings) != 1 || findings[0].Rule != RuleFloatingRuntimeVersion {
				t.Fatalf("findings = %v, want one floating-runtime-version finding", findings)
			}
			if !strings.Contains(findings[0].Detail, test.key) {
				t.Fatalf("finding must name the input %q, got %q", test.key, findings[0].Detail)
			}
		})
	}
	for _, test := range exact {
		t.Run("accepted "+test.key+" "+test.value, func(t *testing.T) {
			if findings := audit(t, test.key, test.value); len(findings) != 0 {
				t.Fatalf("%s: %q produced findings: %v", test.key, test.value, findings)
			}
		})
	}
}

func TestPinAuditRejectsMutableContainerImages(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"release.yml": `name: Containers
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: build-env:latest
    services:
      cache:
        image: redis:7
    steps:
      - run: make release
  shorthand:
    runs-on: ubuntu-latest
    container: build-env:latest
    steps:
      - run: make release
  variable:
    runs-on: ubuntu-latest
    container:
      image: ${{ vars.BUILD_IMAGE }}
    services:
      cache:
        image: ${{ vars.CACHE_IMAGE }}
    steps:
      - run: make release
  pinned:
    runs-on: ubuntu-latest
    container:
      image: ghcr.io/example/build@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    steps:
      - run: make release
`,
	})

	findings := report.PinFindings()
	if len(findings) != 5 {
		t.Fatalf("findings = %v, want one each for the job container, the service, the shorthand form and the two expression-valued images", findings)
	}
	for _, finding := range findings {
		if finding.Rule != RuleUnpinnedContainer {
			t.Fatalf("unexpected rule in %#v", finding)
		}
	}
	var expressions int
	for _, finding := range findings {
		if strings.Contains(finding.Detail, "expression") {
			expressions++
		}
	}
	if expressions != 2 {
		t.Fatalf("findings = %v, want the two expression-valued images refused as such", findings)
	}
}

// TestOmittedPermissionsAreNotProofOfSafety guards the review finding that a
// workflow with no permissions block was certified as unable to reach a
// release. The effective default is a repository setting this analysis cannot
// see, so only an explicit restriction proves it.
func TestOmittedPermissionsAreNotProofOfSafety(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"manual.yml": `name: Manual
on:
  workflow_dispatch:
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
	})

	if !findWorkflow(t, report, "manual.yml").ReleaseCapable {
		t.Fatal("a workflow with no permissions block must not be certified as unable to reach a release")
	}
	findings := report.PinFindings()
	if len(findings) != 1 || findings[0].Rule != RuleUnpinnedAction {
		t.Fatalf("findings = %v, want the unpinned action to be audited", findings)
	}
}

// TestJobPermissionsCanEscalateBeyondTheWorkflowDefault covers a read-only
// workflow carrying one job that grants itself write access.
func TestJobPermissionsCanEscalateBeyondTheWorkflowDefault(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"mixed.yml": `name: Mixed
on:
  pull_request:

permissions:
  contents: read

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo lint
  publish:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v5
`,
	})

	if !findWorkflow(t, report, "mixed.yml").ReleaseCapable {
		t.Fatal("a job escalating to contents: write must make the workflow release capable")
	}
}

func TestPinAuditAcceptsUppercaseCommitRevisions(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"release.yml": `name: Uppercase
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3D3C42E5AAC5BA805825DA76410C181273BA90B1
`,
	})

	if findings := report.PinFindings(); len(findings) != 0 {
		t.Fatalf("an uppercase commit revision was reported as mutable: %v", findings)
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
	if findings := preflightFindings(t, report, Policy{SelfRelease: true}); len(findings) != 0 {
		t.Fatalf("the toolkit's own workflows fail the policy: %v", findings)
	}

	caller := findWorkflow(t, report, DefaultCallerFile)
	if !caller.SelfCaller {
		t.Fatal("the toolkit's own release caller was not recognised as the relative self-caller")
	}
	if !caller.TagTrigger.RespondsToTagPush() {
		t.Fatal("the toolkit's own release caller must respond to a tag push")
	}

	// Under the adopter policy the same tree must be refused: the self-call
	// form is exactly what an adopter-authored release-go.yml would use.
	strict := preflightFindings(t, report, Policy{})
	if len(strict) == 0 {
		t.Fatal("the toolkit's own self-call passed the adopter policy")
	}
	for _, finding := range strict {
		if finding.Rule != RuleCompetingTagTrigger && finding.Rule != RuleMissingHextapCaller {
			t.Fatalf("unexpected finding under the adopter policy: %v", finding)
		}
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

// TestCredentialChannelsMakeAReadOnlyWorkflowReleaseCapable guards the review
// finding that an explicit read-only permissions block was taken as proof a
// workflow could not reach a release. The block bounds only GITHUB_TOKEN; each
// case below holds or can obtain another credential, whether through a secret,
// a scope, or an input channel a caller fills, so each must be pin-audited.
func TestCredentialChannelsMakeAReadOnlyWorkflowReleaseCapable(t *testing.T) {
	const pinnedReusable = "SijanC147/example/.github/workflows/publish.yml@0123456789abcdef0123456789abcdef01234567"
	capable := map[string]string{
		"personal access token in a workflow env": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
env:
  GH_TOKEN: ${{ secrets.RELEASE_PAT }}
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"secret read inside a run body": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: |
          gh release upload "$TAG" dist/app.exe
        env:
          GH_TOKEN: ${{ SECRETS.release_pat }}
`,
		"every secret at once": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: echo '${{ toJSON(secrets) }}'
`,
		"secret by index": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: echo '${{ secrets[format('{0}_PAT', github.actor)] }}'
`,
		"secrets inherited by a called workflow": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  publish:
    uses: ` + pinnedReusable + `
    secrets: inherit
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"id-token exchangeable for external credentials": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
  id-token: write
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"actions write can dispatch a releasing workflow": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
  actions: write
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"write-all shorthand": `name: Manual
on:
  workflow_dispatch:
permissions: write-all
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"job escalating id-token beyond the workflow default": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
    steps:
      - uses: actions/checkout@v5
`,
		"token arriving as a workflow_call input": `name: Callee
on:
  workflow_call:
    inputs:
      token:
        required: true
        type: string
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: gh release upload "$TAG" dist/app.exe
        env:
          GH_TOKEN: ${{ inputs.token }}
`,
		"workflow_call callee that reads nothing": `name: Callee
on:
  workflow_call:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"token arriving as a dispatch input": `name: Manual
on:
  workflow_dispatch:
    inputs:
      token:
        required: true
        type: string
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: gh release upload "$TAG" dist/app.exe
        env:
          GH_TOKEN: ${{ github.event.inputs.token }}
`,
		"dispatch input read through brackets": `name: Manual
on:
  workflow_dispatch:
    inputs:
      token:
        required: true
        type: string
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: gh release upload "$TAG" dist/app.exe
        env:
          GH_TOKEN: ${{ github['event']["inputs"].token }}
`,
		"token in a repository_dispatch payload": `name: Dispatch
on:
  repository_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: gh release upload "$TAG" dist/app.exe
        env:
          GH_TOKEN: ${{ github.event.client_payload.token }}
`,
		"the whole event at once": `name: Dispatch
on:
  repository_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: echo '${{ toJSON(github.event) }}'
`,
	}
	for name, content := range capable {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"manual.yml":      content,
			})
			if !findWorkflow(t, report, "manual.yml").ReleaseCapable {
				t.Fatal("a workflow holding a credential beyond GITHUB_TOKEN must be release capable")
			}
			findings := report.PinFindings()
			if len(findings) != 1 || findings[0].Rule != RuleUnpinnedAction {
				t.Fatalf("findings = %v, want the unpinned action to be audited", findings)
			}
		})
	}

	inert := map[string]string{
		"only the bounded token": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
env:
  GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: echo "${{ github.token }}"
`,
		"a property that happens to be named secrets": `name: Manual
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: echo "${{ needs.build.outputs.secrets }}"
`,
		"read-all shorthand": `name: Manual
on:
  workflow_dispatch:
permissions: read-all
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
`,
		"structured event data is not a channel": `name: Review
on:
  pull_request:
permissions:
  contents: read
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: echo "${{ github.event_name }} ${{ github.event.pull_request.number }} ${{ github.ref_name }}"
`,
	}
	for name, content := range inert {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"manual.yml":      content,
			})
			if findWorkflow(t, report, "manual.yml").ReleaseCapable {
				t.Fatal("a workflow bounded to a read-only GITHUB_TOKEN with no other credential must not be release capable")
			}
			if findings := report.PinFindings(); len(findings) != 0 {
				t.Fatalf("pin audit reached a workflow that cannot release: %v", findings)
			}
		})
	}
}

// TestPinAuditVisitsOnlyExecutablePositions guards the review finding that
// every mapping key named uses was audited as an action reference, so a
// workflow_dispatch input named uses was refused as unpinned. Only jobs and
// steps run code; a list of jobs or steps the reader cannot represent is
// reported rather than skipped.
func TestPinAuditVisitsOnlyExecutablePositions(t *testing.T) {
	report := analyzeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"release.yml": `name: Data keys
on:
  push:
    tags:
      - "v*"
  workflow_dispatch:
    inputs:
      uses:
        description: A data key that happens to share the name
        required: false
        type: string
env:
  uses: data
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        uses: [a, b]
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          uses: ${{ inputs.uses }}
          version-uses: mutable
      - run: echo done
        env:
          uses: data
`,
	})
	if findings := report.PinFindings(); len(findings) != 0 {
		t.Fatalf("a fully pinned workflow was refused because a data key is named uses: %v", findings)
	}

	malformed := map[string]string{
		"jobs is a sequence": `name: Malformed
on:
  push:
    tags:
      - "v*"
jobs:
  - build
`,
		"a job is a scalar": `name: Malformed
on:
  push:
    tags:
      - "v*"
jobs:
  build: run everything
`,
		"steps is a mapping": `name: Malformed
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
`,
		"a step is a scalar": `name: Malformed
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
`,
		"with is a block scalar": `name: Malformed
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with: |
          node-version: latest
`,
	}
	for name, content := range malformed {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"release.yml":     content,
			})
			findings := report.PinFindings()
			if len(findings) != 1 || findings[0].Rule != RuleUnreadableWorkflow {
				t.Fatalf("findings = %v, want one unreadable-workflow finding", findings)
			}
		})
	}
}

// TestVerifiedCallerMustOwnATagTrigger guards the review finding that a
// correctly pinned caller which never starts on a tag push still satisfied the
// preflight, certifying a source tree in which nothing recognised responds to
// the release tag.
func TestVerifiedCallerMustOwnATagTrigger(t *testing.T) {
	caller := func(triggers string) string {
		return `name: Hextap release
on:
` + triggers + `
permissions:
  contents: write
jobs:
  release:
    uses: SijanC147/hextap-toolkit/.github/workflows/release-go.yml@0123456789abcdef0123456789abcdef01234567
    with:
      manifest_path: .hextap.json
      tag: ${{ github.ref_name }}
      mode: full
`
	}
	tests := map[string]string{
		"branch push only":     "  push:\n    branches:\n      - main\n",
		"manual dispatch only": "  workflow_dispatch:\n",
		"every tag ignored":    "  push:\n    branches:\n      - main\n    tags-ignore:\n      - \"**\"\n",
	}
	for name, triggers := range tests {
		t.Run(name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{DefaultCallerFile: caller(triggers)})
			if findings := report.TagExclusivityFindings(Policy{}); len(findings) != 0 {
				t.Fatalf("nothing competes for the tag, yet exclusivity reported %v", findings)
			}
			findings := preflightFindings(t, report, Policy{})
			if len(findings) != 1 || findings[0].Rule != RuleMissingHextapCaller {
				t.Fatalf("findings = %v, want one missing-hextap-caller finding", findings)
			}
			if !strings.Contains(findings[0].Detail, "never starts on a tag push") {
				t.Fatalf("detail must say the caller owns no tag, got %q", findings[0].Detail)
			}
		})
	}

	t.Run("unreadable trigger", func(t *testing.T) {
		report := analyzeWorkflows(t, map[string]string{
			DefaultCallerFile: caller("  push:\n    tags:\n      - \"v*\"\n  puush:\n"),
		})
		exclusivity := report.TagExclusivityFindings(Policy{})
		if len(exclusivity) != 1 || exclusivity[0].Rule != RuleUnreadableWorkflow {
			t.Fatalf("exclusivity findings = %v, want the caller refused as unreadable rather than exempted", exclusivity)
		}
		preflight := preflightFindings(t, report, Policy{})
		if len(preflight) != 2 || preflight[1].Rule != RuleMissingHextapCaller {
			t.Fatalf("preflight findings = %v, want the unreadable refusal plus a missing caller", preflight)
		}
		if !strings.Contains(preflight[1].Detail, "could not be read") {
			t.Fatalf("detail must say the trigger was unreadable, got %q", preflight[1].Detail)
		}
	})
}

// TestTagsIgnoreCanExcludeEveryTag covers the one shape under which a push
// trigger carrying tags-ignore provably never starts from a tag. GitHub's
// filter grammar: ** matches zero or more of any character, * does not match
// the / character, and a negated entry re-includes what it matches.
func TestTagsIgnoreCanExcludeEveryTag(t *testing.T) {
	workflow := func(ignore string) string {
		return `name: Branch CI
on:
  push:
    branches:
      - "**"
    tags-ignore: ` + ignore + `
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - run: echo ci
`
	}
	tests := []struct {
		name    string
		ignore  string
		trigger TagTrigger
	}{
		{"double star excludes every tag", `["**"]`, TagTriggerNone},
		{"double star among other entries", `["v*", "**"]`, TagTriggerNone},
		{"single star leaves a tag containing a slash reachable", `["*"]`, TagTriggerAny},
		{"a negated entry re-includes a tag", `["**", "!v*"]`, TagTriggerAny},
		{"a pattern short of every tag", `["internal-*"]`, TagTriggerAny},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"ci.yml":          workflow(test.ignore),
			})
			ci := findWorkflow(t, report, "ci.yml")
			if ci.TagTrigger != test.trigger {
				t.Fatalf("trigger = %v (%s), want %v", ci.TagTrigger, ci.TriggerReason, test.trigger)
			}
			findings := report.TagExclusivityFindings(Policy{})
			if want := map[bool]int{true: 1, false: 0}[test.trigger.RespondsToTagPush()]; len(findings) != want {
				t.Fatalf("findings = %v, want %d", findings, want)
			}
		})
	}
}

// TestWorkflowRunCompetitorOnTheDefaultBranchIsRefused guards the review
// finding that chains were resolved only inside the tagged tree. GitHub runs a
// workflow_run workflow from the default branch's definition, and only if the
// file exists there, so a chained publisher that exists only on the default
// branch was invisible, and the preflight certified a surface it never read.
func TestWorkflowRunCompetitorOnTheDefaultBranchIsRefused(t *testing.T) {
	chained := `name: Chained publisher
on:
  workflow_run:
    workflows:
      - Hextap release
    types:
      - completed
jobs:
  upload:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: gh release upload "$TAG" extra.exe
`
	tagged := writeWorkflows(t, map[string]string{DefaultCallerFile: hextapCallerWorkflow})
	defaultBranch := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"chained.yml":     chained,
	})

	if _, err := Preflight(tagged, Policy{}); err == nil {
		t.Fatal("a policy without a default-branch directory must be refused, not read as the same tree")
	}

	findings, err := Preflight(tagged, Policy{DefaultBranchDirectory: defaultBranch})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	rules := make(map[string]int)
	for _, finding := range findings {
		if finding.File != "chained.yml" {
			t.Fatalf("finding against %s: %v", finding.File, finding)
		}
		if !strings.Contains(finding.Detail, "default branch") && finding.Rule == RuleCompetingTagTrigger {
			t.Fatalf("the competing finding must say where the definition lives: %v", finding)
		}
		rules[finding.Rule]++
	}
	if rules[RuleCompetingTagTrigger] != 1 || rules[RuleUnpinnedAction] != 1 {
		t.Fatalf("rules = %v, want the chained workflow refused and its unpinned action audited", rules)
	}

	// A chained publisher that also declares its own tag trigger is no less
	// chained: on the default branch the chain is the only route by which the
	// file runs, since GitHub reads push from the tagged commit where it does
	// not exist.
	bothTriggers := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"chained.yml":     strings.Replace(chained, "on:\n  workflow_run:", "on:\n  push:\n    tags:\n      - \"v*\"\n  workflow_run:", 1),
	})
	findings, err = Preflight(tagged, Policy{DefaultBranchDirectory: bothTriggers})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	rules = make(map[string]int)
	for _, finding := range findings {
		rules[finding.Rule]++
	}
	if rules[RuleCompetingTagTrigger] != 1 || rules[RuleUnpinnedAction] != 1 {
		t.Fatalf("rules = %v, want a chained publisher with its own push trigger refused all the same", rules)
	}

	// A local reusable workflow the chained publisher calls runs on the tag
	// too, and is audited with it.
	viaCallee := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"publish.yml": `name: Publish
on:
  workflow_run:
    workflows:
      - Hextap release
    types:
      - completed
jobs:
  add:
    uses: ./.github/workflows/helper.yml
`,
		"helper.yml": `name: Helper
on:
  workflow_call:
jobs:
  build:
    runs-on: ubuntu-latest
    container: evil:latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: main
`,
	})
	findings, err = Preflight(tagged, Policy{DefaultBranchDirectory: viaCallee})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	rules = make(map[string]int)
	for _, finding := range findings {
		rules[finding.File+" "+finding.Rule]++
	}
	want := map[string]int{
		"publish.yml " + RuleCompetingTagTrigger: 1,
		"helper.yml " + RuleUnpinnedContainer:    1,
		"helper.yml " + RuleUnpinnedAction:       1,
		"helper.yml " + RuleMutableSourceRef:     1,
	}
	for key, count := range want {
		if rules[key] != count {
			t.Fatalf("rules = %v, want %v: the callee of a chained publisher must be audited", rules, want)
		}
	}

	// Two trees with the same content must not report the same file twice;
	// two directories are used so that the dedupe, not the same-pointer
	// shortcut, is what is exercised.
	first := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"chained.yml":     chained,
	})
	second := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"chained.yml":     chained,
	})
	once, err := Preflight(first, Policy{DefaultBranchDirectory: second})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(once) != 2 {
		t.Fatalf("findings = %v, want one competing finding and one unpinned action, not duplicates", once)
	}

	// The single-tree method refuses a policy naming another tree rather than
	// ignoring it.
	report, err := Analyze(tagged)
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if _, err := report.PreflightFindings(Policy{DefaultBranchDirectory: defaultBranch}); err == nil {
		t.Fatal("a single-tree preflight silently ignored a policy naming another default-branch directory")
	}
	if _, err := report.PreflightFindings(Policy{DefaultBranchDirectory: tagged}); err != nil {
		t.Fatalf("a policy naming the analysed directory itself was refused: %v", err)
	}

	// A push workflow that exists only on the default branch does not run for
	// the tag push: GitHub reads push workflows from the tagged commit.
	pushOnly := writeWorkflows(t, map[string]string{
		DefaultCallerFile: hextapCallerWorkflow,
		"later.yml":       incidentWorkflow,
	})
	quiet, err := Preflight(tagged, Policy{DefaultBranchDirectory: pushOnly})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(quiet) != 0 {
		t.Fatalf("a push workflow only on the default branch was reported: %v", quiet)
	}
}

// TestCheckoutMustSelectAnImmutableCommit guards the review finding that a
// full-SHA-pinned actions/checkout selecting repository: other/project with
// ref: main was reported as pinned, though rerunning the tag builds whatever
// main holds at the time.
func TestCheckoutMustSelectAnImmutableCommit(t *testing.T) {
	audit := func(t *testing.T, with string) []Finding {
		t.Helper()
		report := analyzeWorkflows(t, map[string]string{
			DefaultCallerFile: hextapCallerWorkflow,
			"release.yml": `name: Checkout
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
` + with,
		})
		return report.PinFindings()
	}
	mutable := map[string]string{
		"another repository without a ref": "        with:\n          repository: other/project\n",
		"another repository on a branch":   "        with:\n          repository: other/project\n          ref: main\n",
		"a tag name":                       "        with:\n          ref: v1.2.3\n",
		"the event ref name":               "        with:\n          ref: ${{ github.ref_name }}\n",
		"a dispatch input":                 "        with:\n          ref: ${{ inputs.tag }}\n",
		"an unreadable ref":                "        with:\n          ref: |\n            main\n",
		"a capitalised input name":         "        with:\n          Ref: main\n",
		"an uppercased repository input":   "        with:\n          REPOSITORY: other/project\n",
		"two spellings of one input":       "        with:\n          ref: 3d3c42e5aac5ba805825da76410c181273ba90b1\n          Ref: main\n",
	}
	for name, with := range mutable {
		t.Run("mutable "+name, func(t *testing.T) {
			findings := audit(t, strings.ReplaceAll(with, "\\n", "\n"))
			if len(findings) != 1 || findings[0].Rule != RuleMutableSourceRef {
				t.Fatalf("findings = %v, want one mutable-source-ref finding", findings)
			}
		})
	}
	immutable := map[string]string{
		"no ref and no repository":     "",
		"a full commit SHA":            "        with:\n          ref: 3d3c42e5aac5ba805825da76410c181273ba90b1\n",
		"the event commit":             "        with:\n          ref: ${{ github.sha }}\n",
		"the event commit in any case": "        with:\n          ref: ${{ GITHUB.SHA }}\n",
		"the pinned reusable workflow commit in another repository": "        with:\n          repository: SijanC147/hextap-toolkit\n          ref: ${{ job.workflow_sha }}\n",
		"a commit resolved by an earlier job":                       "        with:\n          ref: ${{ needs.validate.outputs.sha }}\n",
	}
	for name, with := range immutable {
		t.Run("immutable "+name, func(t *testing.T) {
			if findings := audit(t, strings.ReplaceAll(with, "\\n", "\n")); len(findings) != 0 {
				t.Fatalf("an immutable checkout was refused: %v", findings)
			}
		})
	}

	// A ref: input is audited on any action, and a repository: without a
	// ref: on any action whose name says it clones, so a fork of checkout
	// does not lose the guarantee silently.
	forks := map[string]string{
		"a cached checkout fork on a branch":   "      - uses: nschloe/action-cached-lfs-checkout@3d3c42e5aac5ba805825da76410c181273ba90b1\n        with:\n          ref: main\n",
		"a clone action without a ref":         "      - uses: sudosubin/git-clone-action@3d3c42e5aac5ba805825da76410c181273ba90b1\n        with:\n          repository: other/project\n",
		"a dispatch action targeting a branch": "      - uses: benc-uk/workflow-dispatch@3d3c42e5aac5ba805825da76410c181273ba90b1\n        with:\n          workflow: release.yml\n          ref: main\n",
	}
	for name, step := range forks {
		t.Run("mutable "+name, func(t *testing.T) {
			report := analyzeWorkflows(t, map[string]string{
				DefaultCallerFile: hextapCallerWorkflow,
				"release.yml": `name: Fork
on:
  push:
    tags:
      - "v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
` + strings.ReplaceAll(step, "\\n", "\n"),
			})
			findings := report.PinFindings()
			if len(findings) != 1 || findings[0].Rule != RuleMutableSourceRef {
				t.Fatalf("findings = %v, want one mutable-source-ref finding", findings)
			}
		})
	}
}

// TestDirectoriesNamedLikeWorkflowsAreInactive guards the review finding that
// a directory named templates.yml was reported as an unreadable workflow.
// GitHub loads files; a directory has no contents to parse. A symbolic link is
// still refused, because whether GitHub follows it is not established here.
func TestDirectoriesNamedLikeWorkflowsAreInactive(t *testing.T) {
	directory := writeWorkflows(t, map[string]string{DefaultCallerFile: hextapCallerWorkflow})
	if err := os.Mkdir(filepath.Join(directory, "templates.yml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(DefaultCallerFile, filepath.Join(directory, "link.yml")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	report, err := Analyze(directory)
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	templates := findWorkflow(t, report, "templates.yml")
	if templates.Active || templates.InactiveReason == "" {
		t.Fatalf("directory = %#v, want inactive with a reason", templates)
	}
	link := findWorkflow(t, report, "link.yml")
	if !link.Active || link.TagTrigger != TagTriggerUnknown {
		t.Fatalf("symbolic link = %#v, want active and unreadable", link)
	}
	var files []string
	for _, finding := range report.TagExclusivityFindings(Policy{}) {
		files = append(files, finding.File)
	}
	if len(files) != 1 || files[0] != "link.yml" {
		t.Fatalf("flagged = %v, want only the symbolic link", files)
	}
}
