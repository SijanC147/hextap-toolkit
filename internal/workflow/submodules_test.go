package workflow_test

import (
	"fmt"
	"strings"
	"testing"
)

// The submodule checkout input exists because a caller carrying components as
// git submodules used to build against empty directories: the build runs
// inside the offline network namespace, so the adapter cannot fetch them
// itself and the workflow has to do it first.
//
// The nine actions/checkout calls in release-go.yml are three kinds, and the
// input reaches exactly one of them. Each rule below has a different reason,
// and the failure messages say which, because the cheapest way to break this
// is for a later reader to "tidy up" the asymmetry.
const submodulesThread = "submodules: ${{ inputs.submodules }}"

// checkoutStep is one actions/checkout call, with enough context to say where
// it is and what it is checking out.
type checkoutStep struct {
	job        string
	line       int
	body       string
	toolkit    bool
	taggedRef  bool
	threaded   bool
	stepLabel  string
	jobAndLine string
}

func TestSubmodulesInputReachesOnlyTheTaggedCallerSourceCheckouts(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	steps := parseCheckoutSteps(t, workflow)

	if len(steps) != 9 {
		t.Fatalf("release-go.yml has %d actions/checkout calls, want 9; the classification below was written against those nine and must be re-read, not re-counted", len(steps))
	}

	var threaded, toolkitThreaded, untaggedThreaded, taggedSourceMissing []string
	for _, step := range steps {
		if step.threaded {
			threaded = append(threaded, step.jobAndLine)
		}
		switch {
		case step.toolkit && step.threaded:
			toolkitThreaded = append(toolkitThreaded, step.jobAndLine)
		case !step.toolkit && !step.taggedRef && step.threaded:
			untaggedThreaded = append(untaggedThreaded, step.jobAndLine)
		case !step.toolkit && step.taggedRef && !step.threaded:
			taggedSourceMissing = append(taggedSourceMissing, step.jobAndLine)
		}
	}

	if len(toolkitThreaded) > 0 {
		t.Errorf("the pinned-toolkit checkouts at %s pass %s.\n"+
			"They check out SijanC147/hextap-toolkit at ref: ${{ job.workflow_sha }}, which is the trust boundary the whole workflow rests on. "+
			"submodules is adopter-controlled input and must not reach it. The toolkit carries no submodules, so threading it there buys nothing and widens the boundary.",
			strings.Join(toolkitThreaded, ", "), submodulesThread)
	}

	if len(untaggedThreaded) > 0 {
		t.Errorf("the caller-source checkout at %s passes %s.\n"+
			"That checkout carries no ref:, so it lands on github.ref, and the very next step detaches it to the resolved tag with git -C source checkout --detach. "+
			"git checkout does not move submodule work trees, so a submodule fetched here would sit at the commit recorded on the pushed ref, not on the tag. "+
			"Nothing in that job reads submodule content. A stale tree is worse than the empty one this input exists to fix, because an empty directory fails loudly and a stale one does not.",
			strings.Join(untaggedThreaded, ", "), submodulesThread)
	}

	if len(taggedSourceMissing) > 0 {
		t.Errorf("the caller-source checkout at %s does not pass %s.\n"+
			"It checks out ref: ${{ needs.validate.outputs.sha }}, the resolved tag, and it is where project-owned commands run against the source. "+
			"Dropping the input here is the original defect: release.build_script and release.profile.quality run against empty component directories, inside the offline namespace where nothing can fetch them.",
			strings.Join(taggedSourceMissing, ", "), submodulesThread)
	}

	if len(threaded) != 2 {
		t.Errorf("%s appears on %d checkout calls (%s), want exactly 2: the quality and build caller-source checkouts.",
			submodulesThread, len(threaded), strings.Join(threaded, ", "))
	}
}

func TestSubmodulesInputIsAStringDefaultingToNoSubmodules(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	inputs := textBetween(t, workflow, "    inputs:\n", "\n    secrets:\n")

	assertContains(t, inputs, "      submodules:\n")
	assertContains(t, inputs, "        type: string\n")
	// Quoted on purpose: an unquoted false is a YAML boolean and would not
	// match type: string.
	assertContains(t, inputs, `        default: "false"`)
	assertContains(t, inputs, "        required: false\n")
	assertNotContains(t, inputs, "        default: false\n")
}

// actions/checkout coerces any value it does not recognise to false, silently.
// A typo in a hand-edited caller would therefore reproduce the exact empty-tree
// bug this input exists to fix, and nothing in the run would say so. The guard
// fails the run instead, and it runs before the first checkout so no time is
// spent on a release that cannot work.
func TestInvalidSubmodulesInputFailsBeforeAnyCheckout(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	validateJob := textBetween(t, workflow, "  validate:\n", "\n  quality:\n")

	assertContains(t, validateJob, "      - name: Validate submodule checkout mode\n")
	assertContains(t, validateJob, `          RAW_SUBMODULES: ${{ inputs.submodules }}`)
	assertContains(t, validateJob, `if [[ ! "$RAW_SUBMODULES" =~ ^(false|true|recursive)$ ]]; then`)

	guardAt := strings.Index(validateJob, "- name: Validate submodule checkout mode")
	checkoutAt := strings.Index(validateJob, "uses: actions/checkout@")
	if guardAt == -1 || checkoutAt == -1 || guardAt >= checkoutAt {
		t.Fatalf("the submodules guard must run before the first checkout in the validate job, so an unusable value costs nothing; guard at %d, first checkout at %d", guardAt, checkoutAt)
	}
}

// parseCheckoutSteps splits the workflow into its actions/checkout steps. A
// step runs from its uses: line to the next step marker at the same indent or
// the next job, which is enough to read the with: block that follows it.
func parseCheckoutSteps(t *testing.T, workflow string) []checkoutStep {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	job := ""
	var steps []checkoutStep
	for index, line := range lines {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			job = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if !strings.Contains(line, "uses: actions/checkout@") {
			continue
		}
		body := checkoutStepBody(lines, index)
		label := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if start := index - 1; start >= 0 && strings.Contains(lines[start], "- name:") {
			label = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[start]), "- "))
		}
		steps = append(steps, checkoutStep{
			job:        job,
			line:       index + 1,
			body:       body,
			toolkit:    strings.Contains(body, "repository: SijanC147/hextap-toolkit"),
			taggedRef:  strings.Contains(body, "ref: ${{ needs.validate.outputs.sha }}"),
			threaded:   strings.Contains(body, submodulesThread),
			stepLabel:  label,
			jobAndLine: fmt.Sprintf("%s job, line %d (%s)", job, index+1, label),
		})
	}
	return steps
}

// checkoutStepBody returns the step beginning at the uses: line, stopping at
// the next step or the next job.
func checkoutStepBody(lines []string, start int) string {
	body := []string{lines[start]}
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			body = append(body, line)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || !strings.HasPrefix(line, "        ") {
			break
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n")
}
