package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The visibility expression that decides whether a release is attested. Every
// place that depends on attestation must use this exact text, so the set cannot
// drift into a state where the release is not attested but something still
// demands an attestation.
const attestationVisibilityExpression = "github.event.repository.private"

func releaseWorkflowSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", DefaultWorkflowDirectory, "release-go.yml"))
	if err != nil {
		t.Fatalf("read the toolkit's own release workflow: %v", err)
	}
	return string(data)
}

// `gh release verify` does not check that a release exists or is immutable; it
// checks for a valid cryptographically signed attestation. Calling it for a
// release this workflow deliberately did not attest can never succeed, and the
// failure lands AFTER publication, so the release exists and the pipeline
// reports failure. This pins the guard that prevents it.
func TestImmutableReleaseVerifyIsSkippedWhenTheReleaseIsNotAttested(t *testing.T) {
	source := releaseWorkflowSource(t)

	if !strings.Contains(source, "gh release verify") {
		t.Skip("the workflow no longer calls gh release verify")
	}
	if !strings.Contains(source, "RELEASE_ATTESTED: ${{ !"+attestationVisibilityExpression+" }}") {
		t.Fatal("the immutability step does not derive RELEASE_ATTESTED from the same visibility expression as the attest step")
	}

	// EVERY call must be guarded, not just the one a reviewer happened to name.
	// There are two: the immutability loop in the release job, and the
	// homebrew-only recovery path in validate. The second was missed by review
	// and found only because this test counted instead of checking the one call
	// it had been told about.
	//
	// A call is a line that invokes the command; mentions inside comments are
	// not. Splitting on lines and ignoring comment lines keeps the two apart
	// without making the test depend on byte offsets.
	var calls, guards int
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "gh release verify") {
			calls++
		}
		if strings.Contains(trimmed, `"$RELEASE_ATTESTED" == "true"`) ||
			strings.Contains(trimmed, `"$RELEASE_ATTESTED" != "true"`) {
			guards++
		}
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 gh release verify calls, found %d; a new one needs its own RELEASE_ATTESTED guard", calls)
	}
	if guards != calls {
		t.Fatalf("found %d gh release verify calls but %d RELEASE_ATTESTED guards", calls, guards)
	}
	// Every step that calls it must also derive the flag, or the guard reads an
	// unset variable and silently takes the unattested branch for a public
	// repository, skipping a check that should have run.
	if got := strings.Count(source, "RELEASE_ATTESTED: ${{ !"+attestationVisibilityExpression+" }}"); got != calls {
		t.Fatalf("%d gh release verify calls but %d steps derive RELEASE_ATTESTED", calls, got)
	}

	// The immutability assertion itself is NOT conditional. An unattested
	// release must still be published and immutable.
	if !strings.Contains(source, `"$state" == $'false\ttrue'`) {
		t.Fatal("the immutability assertion is missing or was rewritten")
	}
}

// The attest step, its notice counterpart and the immutability step must all
// agree about what "attested" means. If one moves and the others do not, a
// release is either unattested and still verified, or attested and silently
// unchecked.
func TestAttestationVisibilityConditionsAgree(t *testing.T) {
	source := releaseWorkflowSource(t)

	for name, expression := range map[string]string{
		"attest step skips a private repository":     "if: ${{ !" + attestationVisibilityExpression + " }}",
		"notice step fires for a private one":        "if: ${{ " + attestationVisibilityExpression + " }}",
		"immutability step derives RELEASE_ATTESTED": "RELEASE_ATTESTED: ${{ !" + attestationVisibilityExpression + " }}",
	} {
		if !strings.Contains(source, expression) {
			t.Errorf("%s: %q is not present", name, expression)
		}
	}

	// The attest and notice conditions are complements, so exactly one of them
	// runs. Counting guards against someone adding a third consumer of the
	// expression without reading this test.
	if count := strings.Count(source, attestationVisibilityExpression); count != 4 {
		t.Errorf("expected 4 uses of %q (attest, notice, and RELEASE_ATTESTED in both steps that verify), found %d",
			attestationVisibilityExpression, count)
	}
}
