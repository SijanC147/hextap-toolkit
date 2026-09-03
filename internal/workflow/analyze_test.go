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
			if findings := report.TagExclusivityFindings(""); len(findings) != 0 {
				t.Fatalf("nothing competes for the tag, yet exclusivity reported %v", findings)
			}
			findings := report.PreflightFindings("")
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
		exclusivity := report.TagExclusivityFindings("")
		if len(exclusivity) != 1 || exclusivity[0].Rule != RuleUnreadableWorkflow {
			t.Fatalf("exclusivity findings = %v, want the caller refused as unreadable rather than exempted", exclusivity)
		}
		preflight := report.PreflightFindings("")
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
			findings := report.TagExclusivityFindings("")
			if want := map[bool]int{true: 1, false: 0}[test.trigger.RespondsToTagPush()]; len(findings) != want {
				t.Fatalf("findings = %v, want %d", findings, want)
			}
		})
	}
}
