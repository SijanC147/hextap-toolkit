package onboard

import (
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

// The generated caller is compared byte for byte against the template, in both
// directions: onboarding writes it and validate rejects anything that differs.
// So a manifest field that changes the template changes what every adopter's
// committed workflow has to look like. A manifest that checks out no
// submodules must therefore produce the same bytes as before the field
// existed, or every adopter on the old template fails validation the day the
// toolkit ships this.
func TestCallerIsUnchangedForEveryManifestThatChecksOutNoSubmodules(t *testing.T) {
	baseline := string(workflowBytes("v1.2.3", testToolkitSHA, ""))
	for _, mode := range []string{"", manifest.SubmodulesNone} {
		if got := string(workflowBytes("v1.2.3", testToolkitSHA, mode)); got != baseline {
			t.Fatalf("workflowBytes(submodules = %q) changed the caller:\n%s", mode, got)
		}
		if strings.Contains(baseline, "submodules:") {
			t.Fatalf("the default caller carries a submodules line:\n%s", baseline)
		}
	}
	if got := string(selfCallerBytes("")); strings.Contains(got, "submodules:") {
		t.Fatalf("the default self-caller carries a submodules line:\n%s", got)
	}
}

// The field is only worth having if it reaches the workflow. A manifest that
// asks for submodules and a caller that does not pass them is the dangling
// half of a contract: the manifest validates, the release runs, and the
// component directories are still empty.
func TestCallerPassesTheManifestSubmodulesModeToTheReusableWorkflow(t *testing.T) {
	for _, mode := range []string{manifest.SubmodulesTop, manifest.SubmodulesRecursive} {
		caller := string(workflowBytes("v1.2.3", testToolkitSHA, mode))
		want := "      mode: ${{ github.event_name == 'workflow_dispatch' && 'homebrew-only' || 'full' }}\n      submodules: \"" + mode + "\"\n    secrets:\n"
		if !strings.Contains(caller, want) {
			t.Fatalf("caller for submodules = %q does not pass it inside the with: block:\n%s", mode, caller)
		}
		self := string(selfCallerBytes(mode))
		if !strings.Contains(self, "      submodules: \""+mode+"\"\n    secrets:\n") {
			t.Fatalf("self-caller for submodules = %q does not pass it:\n%s", mode, self)
		}
	}
}

// The value is quoted in the caller because the workflow declares the input as
// type: string. An unquoted true is a YAML boolean and would not match.
func TestCallerQuotesTheSubmodulesValue(t *testing.T) {
	caller := string(workflowBytes("v1.2.3", testToolkitSHA, manifest.SubmodulesTop))
	if strings.Contains(caller, "submodules: true") {
		t.Fatalf("the caller passes an unquoted YAML boolean, which does not match the workflow's type: string input:\n%s", caller)
	}
	if !strings.Contains(caller, `submodules: "true"`) {
		t.Fatalf("the caller does not quote the submodules value:\n%s", caller)
	}
}
