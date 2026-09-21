package workflow_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// The step that seals the submodule set. Named once, because the mutation
// tests below rewrite the workflow text and the contract must be read from the
// same string both times.
const sealedSubmodulesStep = "- name: Require the tagged submodules to be sealed by the manifest"

// sealedSubmodulesContract is the whole rule, as a predicate over workflow
// text rather than a list of assertions, so the mutations at the bottom of
// this file can run it against a deliberately broken workflow and require it
// to complain. A contract that can only be checked against the real file
// proves the file passes today and says nothing about what would be caught.
//
// The rule: the tagged .gitmodules is compared against the manifest's sealed
// list exactly once, in a job that every credential-bearing checkout waits
// for, after the source has been detached to the tag and the manifest sealed.
func sealedSubmodulesContract(workflow string) error {
	if count := strings.Count(workflow, sealedSubmodulesStep); count != 1 {
		return fmt.Errorf("the sealing step appears %d times, want exactly 1; two copies can disagree and nought is the defect", count)
	}

	jobs := parseJobs(workflow)
	validate, found := jobs["validate"]
	if !found {
		return fmt.Errorf("the workflow has no validate job")
	}
	if !strings.Contains(validate, sealedSubmodulesStep) {
		return fmt.Errorf("the sealing step is not in the validate job.\n" +
			"It must be, because that is the job every credential-bearing checkout declares needs on. " +
			"Anywhere else it either runs after the credential has already been presented, or does not run at all")
	}

	// It reads the tagged source and the sealed manifest, not the caller's
	// pushed ref and not an adopter-controlled value passed through an output.
	for _, required := range []string{
		`"$RUNNER_TEMP/hextapctl" release submodules`,
		`--manifest "$RUNNER_TEMP/project-manifest.json"`,
		`--gitmodules "$GITHUB_WORKSPACE/source/.gitmodules"`,
		`--server-url "$GITHUB_SERVER_URL"`,
	} {
		if !strings.Contains(validate, required) {
			return fmt.Errorf("the validate job does not carry %q; the sealing step must read the tagged .gitmodules and the sealed manifest", required)
		}
	}

	// Ordering inside validate. Before the detach the work tree is on the
	// pushed ref, so the file read would not be the tagged one; before the
	// seal the manifest copy the list comes from does not exist yet.
	stepAt := strings.Index(validate, sealedSubmodulesStep)
	detachAt := strings.Index(validate, "- name: Resolve and detach tagged source")
	sealAt := strings.Index(validate, "        id: manifest\n")
	if detachAt == -1 || sealAt == -1 {
		return fmt.Errorf("the validate job no longer carries the detach and manifest-seal steps this check depends on")
	}
	if stepAt < detachAt {
		return fmt.Errorf("the sealing step runs before the source is detached to the tag, so it would read .gitmodules at the pushed ref rather than at the tag; step at %d, detach at %d", stepAt, detachAt)
	}
	if stepAt < sealAt {
		return fmt.Errorf("the sealing step runs before the manifest is sealed, so the list it compares against does not exist yet; step at %d, seal at %d", stepAt, sealAt)
	}

	// Every job that carries the credential must wait for the job that holds
	// the check. This is what makes "before both credential-bearing
	// checkouts" true across jobs rather than only within one.
	credentialJobs := jobsCarryingTheSubmoduleCredential(workflow)
	if len(credentialJobs) == 0 {
		return fmt.Errorf("no job carries %s; either the credential moved or this contract is reading the wrong marker", submodulesToken)
	}
	for _, name := range credentialJobs {
		if name == "validate" {
			return fmt.Errorf("the validate job itself carries the submodule credential, so the check cannot precede it by living in the same job; the credential must stay out of validate")
		}
		body := jobs[name]
		header := body
		if at := strings.Index(body, "\n    steps:\n"); at != -1 {
			header = body[:at]
		}
		if !regexp.MustCompile(`needs:\s*(\[[^\]]*\b)?validate\b`).MatchString(header) {
			return fmt.Errorf("job %q carries the submodule credential but does not declare needs on validate, so the sealing check does not gate its checkout:\n%s", name, header)
		}
	}
	return nil
}

// parseJobs splits the workflow into its top-level jobs by their two-space
// headers.
func parseJobs(workflow string) map[string]string {
	header := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):$`)
	matches := header.FindAllStringSubmatchIndex(workflow, -1)
	jobs := make(map[string]string, len(matches))
	for i, match := range matches {
		end := len(workflow)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		jobs[workflow[match[2]:match[3]]] = workflow[match[0]:end]
	}
	return jobs
}

// jobsCarryingTheSubmoduleCredential names every job with a checkout that
// carries the credential, read from the checkout steps rather than from a
// hardcoded list, so a third such checkout added later is covered without this
// test being edited.
func jobsCarryingTheSubmoduleCredential(workflow string) []string {
	var names []string
	seen := map[string]bool{}
	for _, job := range orderedJobNames(workflow) {
		body := parseJobs(workflow)[job]
		if strings.Contains(body, submodulesToken) && !seen[job] {
			seen[job] = true
			names = append(names, job)
		}
	}
	return names
}

func orderedJobNames(workflow string) []string {
	header := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):$`)
	var names []string
	for _, match := range header.FindAllStringSubmatch(workflow, -1) {
		names = append(names, match[1])
	}
	return names
}

// The contract holds on the workflow as committed.
func TestTheTaggedSubmodulesAreSealedBeforeAnyCredentialBearingCheckout(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	if err := sealedSubmodulesContract(workflow); err != nil {
		t.Fatalf("release-go.yml does not seal the tagged submodules: %v", err)
	}
}

// Mutation one: the step is deleted. This is the defect the whole change
// closes, so a contract that stays green without the step proves nothing.
func TestDeletingTheSealingStepBreaksTheContract(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	mutated := removeSealingStep(t, workflow)
	if strings.Contains(mutated, sealedSubmodulesStep) {
		t.Fatalf("the mutation did not remove the step, so this test proves nothing")
	}
	if err := sealedSubmodulesContract(mutated); err == nil {
		t.Fatal("the contract accepted a workflow with no sealing step; a tagged .gitmodules could then steer the credential at any repository it can read")
	}
}

// Mutation two: the step is moved out of validate and into the quality job,
// after the checkout that carries the credential. The step still exists, and
// a check that only searched the file for its name would stay green while the
// credential had already been presented by the time it ran.
func TestMovingTheSealingStepAfterACredentialBearingCheckoutBreaksTheContract(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	step := extractSealingStep(t, workflow)
	mutated := removeSealingStep(t, workflow)

	anchor := "  quality:\n"
	qualityAt := strings.Index(mutated, anchor)
	if qualityAt == -1 {
		t.Fatalf("the quality job header was not found, so the mutation cannot be placed")
	}
	// Insert after the first credential-bearing checkout in quality.
	tokenAt := strings.Index(mutated[qualityAt:], submodulesToken)
	if tokenAt == -1 {
		t.Fatalf("the quality job no longer carries %s", submodulesToken)
	}
	lineEnd := strings.Index(mutated[qualityAt+tokenAt:], "\n")
	if lineEnd == -1 {
		t.Fatal("the credential line has no end")
	}
	insertAt := qualityAt + tokenAt + lineEnd + 1
	mutated = mutated[:insertAt] + step + mutated[insertAt:]

	if !strings.Contains(mutated, sealedSubmodulesStep) {
		t.Fatal("the mutation lost the step, so it is testing deletion rather than reordering")
	}
	if err := sealedSubmodulesContract(mutated); err == nil {
		t.Fatal("the contract accepted a workflow whose sealing step runs after the credential-bearing checkout; by then the credential has already been presented to every repository the tagged .gitmodules named, which is the whole attack")
	}
}

// Mutation three: a credential-bearing job stops waiting for validate. The
// step is untouched and in the right job, and the ordering is still wrong,
// because nothing makes the two jobs run in that order.
func TestDroppingTheNeedsOnValidateBreaksTheContract(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")
	mutated := strings.Replace(workflow, "  quality:\n    name: Source quality\n    needs: validate\n", "  quality:\n    name: Source quality\n", 1)
	if mutated == workflow {
		t.Fatal("the quality job's needs line no longer matches, so this mutation changed nothing")
	}
	if err := sealedSubmodulesContract(mutated); err == nil {
		t.Fatal("the contract accepted a credential-bearing job that does not wait for validate, so the check in validate does not gate it")
	}
}

// extractSealingStep returns the step's full text, from its name line to the
// blank line before the next step.
func extractSealingStep(t *testing.T, workflow string) string {
	t.Helper()
	start := strings.Index(workflow, "      "+sealedSubmodulesStep)
	if start == -1 {
		t.Fatalf("the sealing step was not found in the workflow")
	}
	rest := workflow[start:]
	end := strings.Index(rest, "\n\n")
	if end == -1 {
		t.Fatalf("the sealing step has no end")
	}
	return rest[:end+1]
}

// removeSealingStep deletes the step and the comment block above it.
func removeSealingStep(t *testing.T, workflow string) string {
	t.Helper()
	step := extractSealingStep(t, workflow)
	mutated := strings.Replace(workflow, step, "", 1)
	if mutated == workflow {
		t.Fatalf("the sealing step was not removed")
	}
	return mutated
}
