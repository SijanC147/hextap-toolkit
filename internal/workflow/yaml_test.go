package workflow

import (
	"strings"
	"testing"
)

func parseFixture(t *testing.T, source string) *node {
	t.Helper()
	document, err := parseWorkflowDocument(source)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return document
}

// TestBareOnKeyIsTheStringOn guards the trap that motivated writing a reader
// rather than reusing one: a YAML 1.1 parser resolves the bare key on to the
// boolean true, and a workflow analysed under that reading appears to declare
// no triggers at all. Keys here are strings by construction.
func TestBareOnKeyIsTheStringOn(t *testing.T) {
	for name, source := range map[string]string{
		"bare":         "on:\n  push:\n    tags:\n      - \"v*\"\n",
		"doubleQuoted": "\"on\":\n  push:\n    tags:\n      - \"v*\"\n",
		"singleQuoted": "'on':\n  push:\n    tags:\n      - \"v*\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			document := parseFixture(t, source)
			if !document.has("on") {
				t.Fatalf("document keys = %v, want an on key", document.keys)
			}
			if analysis := analyzeTriggers(document); analysis.trigger != TagTriggerFiltered {
				t.Fatalf("trigger = %v (%s), want a filtered tag trigger", analysis.trigger, analysis.reason)
			}
		})
	}
}

// TestCapitalisedOnKeyIsNotTheTriggerKey confirms the analyser does not invent
// an equivalence GitHub does not make. GitHub matches the key exactly, so a
// capitalised key is not a trigger, and a file with no usable on key is refused
// rather than read as harmless.
func TestCapitalisedOnKeyIsNotTheTriggerKey(t *testing.T) {
	document := parseFixture(t, "On:\n  push:\n    tags:\n      - \"v*\"\n")
	analysis := analyzeTriggers(document)
	if analysis.trigger != TagTriggerUnknown {
		t.Fatalf("trigger = %v, want unknown", analysis.trigger)
	}
	if !strings.Contains(analysis.reason, "no on: key") {
		t.Fatalf("reason = %q, want it to name the missing on: key", analysis.reason)
	}
}

// TestBlockScalarContentIsNeverReadAsStructure is the mirror image of the
// original defect. The substring check was too loose; a checker that scanned
// text without tracking block scalars would be too noisy, flagging a workflow
// that merely prints a trigger inside a shell heredoc. Both failures come from
// not actually parsing.
func TestBlockScalarContentIsNeverReadAsStructure(t *testing.T) {
	source := `name: Prints a workflow
on:
  pull_request:
jobs:
  document:
    runs-on: ubuntu-latest
    steps:
      - name: Show the release trigger
        run: |
          cat <<'YAML'
          on:
            push:
              tags:
                - "v*"
          YAML
      - name: Second step still parses
        run: echo done
`
	document := parseFixture(t, source)

	analysis := analyzeTriggers(document)
	if analysis.trigger != TagTriggerNone {
		t.Fatalf("trigger = %v (%s), want none: a trigger printed inside a run body is not a trigger",
			analysis.trigger, analysis.reason)
	}

	steps := document.child("jobs").child("document").child("steps")
	if steps == nil || steps.kind != nodeSequence || len(steps.items) != 2 {
		t.Fatalf("steps = %#v, want two parsed steps after the block scalar", steps)
	}
	if got := steps.items[1].child("name").value; got != "Second step still parses" {
		t.Fatalf("second step name = %q", got)
	}
}

func TestBlockScalarHeaderModifiers(t *testing.T) {
	source := `jobs:
  build:
    steps:
      - run: |2-
            two space indented body
        shell: bash
      - run: >-
          folded body
        shell: bash
      - run: |+
          kept trailing newlines
        shell: bash
`
	document := parseFixture(t, source)
	steps := document.child("jobs").child("build").child("steps")
	if steps == nil || len(steps.items) != 3 {
		t.Fatalf("steps = %#v, want three parsed steps", steps)
	}
	for index, step := range steps.items {
		body := step.child("run")
		if body == nil || body.style != scalarBlock {
			t.Fatalf("step %d run is not a block scalar: %#v", index, body)
		}
		if shell := step.child("shell"); shell == nil || shell.value != "bash" {
			t.Fatalf("step %d lost the key after its block scalar: %#v", index, shell)
		}
	}
}

func TestQuotingAndCommentHandling(t *testing.T) {
	source := `name: Don't panic
on:
  push:
    tags:
      - 'v*'  # every version tag
      - "hash # not a comment"
      - 'quote '' inside'
description: value with a trailing space   # trimmed
`
	document := parseFixture(t, source)

	if got, want := document.child("name").value, "Don't panic"; got != want {
		t.Fatalf("name = %q, want %q: an apostrophe in a plain scalar is not a quoted string", got, want)
	}
	tags := document.child("on").child("push").child("tags")
	want := []string{"v*", "hash # not a comment", "quote ' inside"}
	if len(tags.items) != len(want) {
		t.Fatalf("tags = %#v, want %d entries", tags, len(want))
	}
	for index, expected := range want {
		if got := tags.items[index].value; got != expected {
			t.Fatalf("tag %d = %q, want %q", index, got, expected)
		}
	}
	if got, want := document.child("description").value, "value with a trailing space"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestFlowCollections(t *testing.T) {
	document := parseFixture(t, "on:\n  push:\n    tags: [ 'v*', \"release-*\" ]\n    branches: []\n")
	tags := document.child("on").child("push").child("tags")
	if tags.kind != nodeSequence || len(tags.items) != 2 {
		t.Fatalf("tags = %#v, want two flow entries", tags)
	}
	if tags.items[0].value != "v*" || tags.items[1].value != "release-*" {
		t.Fatalf("tags = %q, %q", tags.items[0].value, tags.items[1].value)
	}
	if branches := document.child("on").child("push").child("branches"); branches.kind != nodeSequence || len(branches.items) != 0 {
		t.Fatalf("branches = %#v, want an empty sequence", branches)
	}
}

func TestSequenceItemsCarryingMappings(t *testing.T) {
	source := `jobs:
  build:
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
          fetch-depth: 0
      - name: Build
        run: go build ./...
`
	document := parseFixture(t, source)
	steps := document.child("jobs").child("build").child("steps")
	if len(steps.items) != 2 {
		t.Fatalf("steps = %#v, want two", steps)
	}
	if got := steps.items[0].child("uses").value; got != "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1" {
		t.Fatalf("uses = %q, the trailing version comment was not removed", got)
	}
	if got := steps.items[0].child("with").child("persist-credentials").value; got != "false" {
		t.Fatalf("persist-credentials = %q", got)
	}
	if got := steps.items[1].child("run").value; got != "go build ./..." {
		t.Fatalf("run = %q", got)
	}
}

// TestParserRefusesWhatItCannotRepresent is the fail-closed contract. Each of
// these is a construct a real YAML reader resolves and this one deliberately
// does not, and every refusal must name the construct so an adopter can fix it
// rather than work around the check.
func TestParserRefusesWhatItCannotRepresent(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		contains string
	}{
		{
			name:     "anchor",
			source:   "defaults: &shared\n  shell: bash\njobs:\n  build:\n    shell: bash\n",
			contains: "anchor",
		},
		{
			name:     "alias",
			source:   "defaults:\n  shell: bash\njobs:\n  build: *shared\n",
			contains: "anchor",
		},
		{
			name:     "merge key",
			source:   "jobs:\n  build:\n    <<: base\n    shell: bash\n",
			contains: "merge key",
		},
		{
			name:     "explicit tag",
			source:   "on: !!str push\n",
			contains: "anchor, alias or tag",
		},
		{
			name:     "second document",
			source:   "on: push\n---\non: pull_request\n",
			contains: "YAML document marker",
		},
		{
			name:     "yaml directive",
			source:   "%YAML 1.2\n---\non: push\n",
			contains: "directive",
		},
		{
			name:     "tab in indentation",
			source:   "on:\n\tpush:\n",
			contains: "tab",
		},
		{
			name:     "duplicate key",
			source:   "on: push\njobs:\n  build:\n    shell: bash\n    shell: sh\n",
			contains: "duplicates mapping key",
		},
		{
			name:     "multi-line plain scalar",
			source:   "name: a long name\n  continued on the next line\non: push\n",
			contains: "continues a scalar across lines",
		},
		{
			name:     "unterminated single quote",
			source:   "on:\n  push:\n    tags:\n      - 'v*\n",
			contains: "unterminated single-quoted value",
		},
		{
			name:     "unterminated double quote",
			source:   "name: \"unclosed\non: push\n",
			contains: "unterminated double-quoted value",
		},
		{
			name:     "flow sequence spanning lines",
			source:   "on:\n  push:\n    tags: [\n      'v*',\n    ]\n",
			contains: "unterminated flow collection",
		},
		{
			name:     "trailing comma in a flow sequence",
			source:   "on:\n  push:\n    tags: ['v*',]\n",
			contains: "trailing comma",
		},
		{
			name:     "sequence entry inside a mapping",
			source:   "on: push\njobs:\n  - build\n  build:\n    shell: bash\n",
			contains: "mixes",
		},
		{
			name:     "unreadable block scalar header",
			source:   "jobs:\n  build:\n    run: |x\n      body\n",
			contains: "block scalar header",
		},
		{
			name:     "colon inside an unquoted value",
			source:   "jobs:\n  build:\n    run: echo key: value\n",
			contains: "unreadable plain value",
		},
		{
			name:     "empty document",
			source:   "# only a comment\n",
			contains: "empty",
		},
		{
			name:     "carriage return alone",
			source:   "on: push\rjobs:\n",
			contains: "carriage return",
		},
		{
			name:     "unsupported escape",
			source:   "name: \"bad \\q escape\"\non: push\n",
			contains: "unsupported escape",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := parseWorkflowDocument(test.source)
			if err == nil {
				t.Fatalf("parsed a construct outside the accepted subset: %#v", document)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %q, want it to name %q", err, test.contains)
			}
		})
	}
}

func TestParserAcceptsOrdinaryWorkflowShapes(t *testing.T) {
	sources := map[string]string{
		"leading document marker": "---\non: push\n",
		"windows line endings":    "on:\r\n  push:\r\n    branches:\r\n      - main\r\n",
		"byte order mark":         "\ufeffon: push\n",
		"blank and comment lines": "# top\n\non:\n\n  # why\n  push:\n\n    branches: [main]\n",
		"empty trailing value":    "on:\n  workflow_dispatch:\n",
		"escapes":                 "name: \"tab\\there\"\non: push\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWorkflowDocument(source); err != nil {
				t.Fatalf("refused an ordinary workflow shape: %v", err)
			}
		})
	}
}

func TestDocumentSizeAndDepthLimits(t *testing.T) {
	if _, err := parseWorkflowDocument(strings.Repeat("a", maxDocumentBytes+1)); err == nil {
		t.Fatal("accepted a document over the size limit")
	}

	var deep strings.Builder
	deep.WriteString("on: push\n")
	for depth := 0; depth <= maxNestingDepth+2; depth++ {
		deep.WriteString(strings.Repeat(" ", depth*2))
		deep.WriteString("key:\n")
	}
	if _, err := parseWorkflowDocument(deep.String()); err == nil {
		t.Fatal("accepted a document nested past the depth limit")
	}
}
