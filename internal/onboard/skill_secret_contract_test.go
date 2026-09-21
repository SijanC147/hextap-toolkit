package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

// The bundled skill is the route an agent takes through this toolkit, so an
// invariant asserted there is an instruction that gets followed. When
// release.checkout.submodules arrived, the generated caller gained a second
// secret while the skill still told an agent to preserve
// OP_SERVICE_ACCOUNT_TOKEN as the sole source-repository secret and to verify
// before releasing that only the required secret name exists. An agent obeying
// the shipped text would have deleted SUBMODULES_TOKEN or refused to release,
// which made the whole private-submodule path unusable through the documented
// route. Raised by Codex on PR #24 as P1.
//
// This is the sixth surface derived from one manifest field, and the first
// that contradicted the change by asserting something the change made false
// rather than by restating a claim wrongly. Searching for copies of a sentence
// does not find those; only asking what else asserts anything about the field
// does. This test is that question, asked mechanically.
func TestTheSkillDoesNotAssertASoleSecretTheCallerContradicts(t *testing.T) {
	caller := string(workflowBytes("v1.2.3", testToolkitSHA, manifest.SubmodulesRecursive))
	if !strings.Contains(caller, "SUBMODULES_TOKEN") {
		t.Fatal("the generated caller no longer references SUBMODULES_TOKEN; this test exists to keep the skill in step with it and must be re-read, not deleted")
	}

	// Any wording that tells a reader one secret is the whole set. Each of
	// these was in the shipped skill when the second secret arrived.
	soleSecretClaims := []string{
		"sole source-repository secret",
		"only the required secret name exists",
		"the required secret name,",
	}

	references := filepath.Join("..", "..", "skills", "hextap", "references")
	entries, err := os.ReadDir(references)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", references, err)
	}
	namesConditionalSecret := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(references, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		text := string(data)
		for _, claim := range soleSecretClaims {
			if strings.Contains(text, claim) {
				t.Errorf("%s asserts %q.\n"+
					"The generated caller maps SUBMODULES_TOKEN for a project whose release.checkout.submodules is not false, so an agent following that instruction would remove the credential or refuse the release. The manifest is the authority for which secrets belong, not a fixed list.",
					path, claim)
			}
		}
		if strings.Contains(text, "SUBMODULES_TOKEN") {
			namesConditionalSecret = true
		}
	}
	if !namesConditionalSecret {
		t.Errorf("no skill reference names SUBMODULES_TOKEN.\n"+
			"An agent working a project with private submodules needs to know the second secret exists and when, or it will treat it as unexpected. References read: %s", references)
	}
}
