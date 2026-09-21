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
//
// keys holds the step's directive lines, trimmed, with comment lines dropped,
// and every test asks whether a whole key is present rather than whether the
// step's text contains a substring. A substring match reads a commented-out
// `# submodules: ...` as threaded, which YAML ignores, so the original defect
// could be restored with this file still green. Found by the reviewer of
// PR #23, who commented out the build job's line and watched the test print
// ok. A prefix match has the same hole from the other side: it reads
// `submodules: ${{ inputs.submodules }}-typo` as threaded.
type checkoutStep struct {
	job        string
	line       int
	keys       []string
	toolkit    bool
	taggedRef  bool
	threaded   bool
	stepLabel  string
	jobAndLine string
}

// declares reports whether the step carries this exact directive line.
func (s checkoutStep) declares(key string) bool {
	for _, line := range s.keys {
		if line == key {
			return true
		}
	}
	return false
}

func TestSubmodulesInputReachesOnlyTheTaggedCallerSourceCheckouts(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	steps := parseCheckoutSteps(t, workflow)

	if len(steps) != 9 {
		t.Fatalf("release-go.yml has %d actions/checkout calls, want 9; the classification below was written against those nine and must be re-read, not re-counted", len(steps))
	}

	var threaded, toolkitThreaded, untaggedThreaded, taggedSourceMissing, unclassified []string
	for _, step := range steps {
		if step.threaded {
			threaded = append(threaded, step.jobAndLine)
		}
		switch {
		case step.toolkit:
			if step.threaded {
				toolkitThreaded = append(toolkitThreaded, step.jobAndLine)
			}
		case step.taggedRef:
			if !step.threaded {
				taggedSourceMissing = append(taggedSourceMissing, step.jobAndLine)
			}
		case step.job == "validate":
			if step.threaded {
				untaggedThreaded = append(untaggedThreaded, step.jobAndLine)
			}
		default:
			unclassified = append(unclassified, step.jobAndLine)
		}
	}

	// Without this, a caller-source checkout written with the resolved tag in
	// some other form, through an env var or a differently named job output,
	// matches no case and is accepted in silence. That is exactly the shape
	// that needs the input.
	if len(unclassified) > 0 {
		t.Errorf("the checkout at %s fits none of the three kinds this test knows.\n"+
			"It does not check out the pinned toolkit, it does not carry ref: ${{ needs.validate.outputs.sha }}, and it is not the validate job's detached caller checkout. "+
			"Classify it here before merging: if it runs project-owned commands against the caller source, it needs %s, and if it does not, say why in this test rather than leaving it to fall through.",
			strings.Join(unclassified, ", "), submodulesThread)
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

// The reviewer of PR #23 commented the build job's submodules line out, which
// is the original defect restored, and this test printed ok. The classifier
// matched a substring of the step's raw text, and YAML ignores a comment. This
// pins the repair so the hole cannot come back through a refactor, without
// needing the mutation to be re-run by hand.
func TestACommentedOutDirectiveIsNotADirective(t *testing.T) {
	lines := strings.Split(`      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          ref: ${{ needs.validate.outputs.sha }}
          path: source
          persist-credentials: false
          # submodules: ${{ inputs.submodules }}
`, "\n")
	step := checkoutStep{keys: checkoutStepKeys(lines, 0)}

	if step.declares(submodulesThread) {
		t.Fatalf("a commented-out %s was read as threaded; keys = %q", submodulesThread, step.keys)
	}
	if !step.declares("ref: ${{ needs.validate.outputs.sha }}") {
		t.Fatalf("the real ref directive was dropped; keys = %q", step.keys)
	}
}

// A prefix match has the same hole from the other side. actions/checkout would
// coerce this value, and an adopter would be back to empty component
// directories with the test green.
func TestACorruptedValueIsNotTheDirective(t *testing.T) {
	lines := strings.Split(`      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          ref: ${{ needs.validate.outputs.sha }}
          submodules: ${{ inputs.submodules }}-typo
`, "\n")
	step := checkoutStep{keys: checkoutStepKeys(lines, 0)}

	if step.declares(submodulesThread) {
		t.Fatalf("a corrupted value was read as threaded; keys = %q", step.keys)
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
		keys := checkoutStepKeys(lines, index)
		label := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if start := index - 1; start >= 0 && strings.Contains(lines[start], "- name:") {
			label = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[start]), "- "))
		}
		step := checkoutStep{
			job:        job,
			line:       index + 1,
			keys:       keys,
			stepLabel:  label,
			jobAndLine: fmt.Sprintf("%s job, line %d (%s)", job, index+1, label),
		}
		step.toolkit = step.declares("repository: SijanC147/hextap-toolkit")
		step.taggedRef = step.declares("ref: ${{ needs.validate.outputs.sha }}")
		step.threaded = step.declares(submodulesThread)
		steps = append(steps, step)
	}
	return steps
}

// checkoutStepKeys returns the step's directive lines, trimmed, beginning at
// the uses: line and stopping at the next step or the next job. Blank lines
// and comment lines are dropped: YAML ignores a comment, so this must too, or
// commenting a directive out reads as leaving it in.
func checkoutStepKeys(lines []string, start int) []string {
	keys := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[start]), "- "))}
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || !strings.HasPrefix(line, "        ") {
			break
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		keys = append(keys, trimmed)
	}
	return keys
}

// The input is read from the caller workflow file at the dispatched ref. The
// manifest is read at the resolved tag and is the authority for everything
// else here. Without a comparison a tag that changes
// release.checkout.submodules while its thin caller was not regenerated builds
// with the stale caller value, or with the false default, against empty
// component directories, and reports success. That is the defect this whole
// change exists to close, arriving through a different door. Raised by Codex
// on PR #23 as P1.
func TestTheCallerSubmoduleModeIsBoundToTheSealedManifest(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	validateJob := textBetween(t, workflow, "  validate:\n", "\n  quality:\n")

	assertContains(t, validateJob, "      - name: Require the caller submodule mode to match the sealed manifest\n")
	assertContains(t, validateJob, "          RAW_SUBMODULES: ${{ inputs.submodules }}")
	assertContains(t, validateJob, "          MANIFEST_SUBMODULES: ${{ steps.manifest.outputs.submodules }}")
	assertContains(t, validateJob, `if [[ "$RAW_SUBMODULES" != "$MANIFEST_SUBMODULES" ]]; then`)

	// The comparison is worth nothing if it runs after the checkouts it
	// guards. Both threaded checkouts live in jobs that need validate, so
	// being anywhere in validate is enough, but it must come after the step
	// that produces the manifest value it reads.
	sealAt := strings.Index(validateJob, "        id: manifest\n")
	compareAt := strings.Index(validateJob, "- name: Require the caller submodule mode to match the sealed manifest")
	if sealAt == -1 || compareAt == -1 || sealAt >= compareAt {
		t.Fatalf("the comparison must follow the step that seals the manifest; seal at %d, compare at %d", sealAt, compareAt)
	}

	for _, job := range []string{"  quality:\n", "  build:\n"} {
		body := textBetween(t, workflow, job, "\n    steps:\n")
		if !strings.Contains(body, "needs:") || !strings.Contains(body, "validate") {
			t.Fatalf("job %q must declare needs on validate, or the comparison cannot gate its checkout:\n%s", strings.TrimSpace(job), body)
		}
	}
}

// The credential is placed only on the checkouts that fetch submodules. Read
// the next sentence before citing that as the blast radius, because it is not.
//
// token: is not a submodule-only credential. actions/checkout writes it into
// an http.<origin>/.extraheader in configureAuth() before any fetch, so on the
// steps that carry it it replaces GITHUB_TOKEN for the PRIMARY CLONE of the
// caller's own repository as well. A token scoped to the submodule
// repositories alone therefore fails the primary clone before reaching a
// submodule. The security reviewer of PR #24 found that the README documented
// the narrower scope, which would have broken the release for the normal case
// and pushed the adopter to widen the token until it went green, which is the
// over-scoping SB23-736 exists to undo.
//
// The job's GITHUB_TOKEN cannot read a sibling private repository, and seven
// of the eight repositories in this family are private, so for those adopters
// the input alone produces a clone error rather than an empty tree.
const submodulesToken = "token: ${{ secrets.submodules_token || github.token }}"

func TestTheSubmoduleCredentialReachesOnlyTheCheckoutsThatFetchSubmodules(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	steps := parseCheckoutSteps(t, workflow)

	var tokened, threadedWithoutToken, tokenedWithoutThread []string
	for _, step := range steps {
		carries := step.declares(submodulesToken)
		if carries {
			tokened = append(tokened, step.jobAndLine)
		}
		if step.threaded && !carries {
			threadedWithoutToken = append(threadedWithoutToken, step.jobAndLine)
		}
		if carries && !step.threaded {
			tokenedWithoutThread = append(tokenedWithoutThread, step.jobAndLine)
		}
	}

	if len(threadedWithoutToken) > 0 {
		t.Errorf("the checkout at %s fetches submodules but carries no %s.\n"+
			"Every adopter repository in this family except the toolkit is private, so without the credential this checkout fails on the first private submodule with a clone error.",
			strings.Join(threadedWithoutToken, ", "), submodulesToken)
	}

	if len(tokenedWithoutThread) > 0 {
		t.Errorf("the checkout at %s carries %s but fetches no submodules.\n"+
			"A credential belongs only where it is used, and this one is not narrow: actions/checkout authenticates the primary clone with it too, so every step that carries it presents the adopter's token for the whole repository. "+
			"Every other checkout here either pins the toolkit, which is the trust boundary, or is the validate caller checkout whose submodules would be stale anyway.",
			strings.Join(tokenedWithoutThread, ", "), submodulesToken)
	}

	if len(tokened) != 2 {
		t.Errorf("%s appears on %d checkout calls (%s), want exactly 2, the same two that carry %s.",
			submodulesToken, len(tokened), strings.Join(tokened, ", "), submodulesThread)
	}
}

// The secret is optional and falls back to github.token. A caller that maps
// nothing must keep working exactly as it did, which is what makes this
// landable before the credential itself exists.
func TestTheSubmoduleCredentialIsOptionalAndFallsBackToTheJobToken(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	secrets := textBetween(t, workflow, "    secrets:\n", "\nconcurrency:\n")

	assertContains(t, secrets, "      submodules_token:\n")
	assertContains(t, secrets, "        required: false\n")
	assertNotContains(t, secrets, "      submodules_token:\n        required: true")
	assertContains(t, workflow, "secrets.submodules_token || github.token")
}

// persist-credentials: false is what stops the credential surviving the step
// that used it, and it has to hold on all nine after the change, not only on
// the two that now carry a token.
func TestEveryCheckoutStillRefusesToPersistCredentials(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	steps := parseCheckoutSteps(t, workflow)

	var persisting []string
	for _, step := range steps {
		if !step.declares("persist-credentials: false") {
			persisting = append(persisting, step.jobAndLine)
		}
	}
	if len(persisting) > 0 {
		t.Fatalf("the checkout at %s does not set persist-credentials: false.\n"+
			"A credential written into the runner's git config outlives the step that wrote it, and the build runs later in the same job.",
			strings.Join(persisting, ", "))
	}
	if len(steps) != 9 {
		t.Fatalf("expected 9 checkout calls, got %d", len(steps))
	}
}

// The credential's safety argument is a property of this exact action version,
// not of anything in this repository.
//
// The security reviewer of PR #24 read actions/checkout at the pinned sha and
// established, from its source, that the file holding the token is deleted
// under await before the checkout step returns, that no submodule config can
// name it because the only code that writes one is gated on persistCredentials
// at its call site, and that the token never enters the job environment. That
// is what makes it safe for the quality job to run adopter-declared commands
// after a checkout that held a credential.
//
// TestEveryCheckoutStillRefusesToPersistCredentials asserts persist-credentials:
// false, which is the input to that cleanup rather than its outcome. A bump to
// a version that persisted submodule auth differently would break the argument
// and nothing else here would go red. So the pin itself is the control, and
// changing it has to be deliberate.
//
// The offline boundary is not what protects the credential. It stops network
// egress from the build adapter. The credential is protected by the cleanup
// inside the checkout step and by never entering the environment, which are
// independent of it: the Go path has no unshare and is equally safe on this
// point. Anyone citing "the offline boundary contains the credential" is
// citing the wrong mechanism.
func TestTheCredentialBearingCheckoutsUseTheAuditedActionVersion(t *testing.T) {
	const auditedCheckout = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")

	for _, step := range parseCheckoutSteps(t, workflow) {
		if !step.declares(submodulesToken) {
			continue
		}
		if !step.declares("uses: " + auditedCheckout) {
			t.Fatalf("the credential-bearing checkout at %s does not use %s.\n"+
				"The credential cleanup argument was verified by reading that exact version's source, not this repository's code. "+
				"Re-read git-source-provider.ts and git-auth-helper.ts at the new version and confirm the token file is still removed under await and that submodule auth is still gated on persistCredentials, then update this test and say so in the pull request.",
				step.jobAndLine, auditedCheckout)
		}
	}
}

// The workflow's own secret description is the fourth place this credential's
// scope is written down, after the README, the generated setup document and
// the contract test comment. Codex found the first three wrong in turn and
// then found this one still wrong after the others were fixed, which is what a
// derived set looks like when it is audited one file at a time.
//
// An adopter reading the reusable workflow rather than the README gets this
// text, so it has to carry the same claim: the token authenticates the primary
// clone, therefore the caller repository belongs in its scope.
func TestTheSecretDescriptionNamesTheCallerRepositoryInItsScope(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	description := textBetween(t, workflow, "      submodules_token:\n", "\n        required: false")

	for _, required := range []string{
		"Contents read on the CALLER repository and on each",
		"submodule repository",
		"replaces\n          GITHUB_TOKEN for the primary clone",
		"fails that clone before reaching one",
		"falls back to github.token",
	} {
		if !strings.Contains(description, required) {
			t.Fatalf("the submodules_token description is missing %q.\n"+
				"This text is what an adopter reading the workflow provisions a credential from, so it carries the same scope claim as the README and the generated setup document.\n%s",
				required, description)
		}
	}
}
