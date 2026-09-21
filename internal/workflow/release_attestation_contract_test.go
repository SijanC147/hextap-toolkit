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

// Every file that may invoke `gh release verify`: the reusable workflow and
// every shell script it runs. Scoped by glob rather than by a hand-written list
// so a new script cannot escape the count by not being named here.
func attestationSensitiveFiles(t *testing.T) []string {
	t.Helper()
	paths := []string{filepath.Join("..", "..", DefaultWorkflowDirectory, "release-go.yml")}
	scripts, err := filepath.Glob(filepath.Join("..", "..", "scripts", "*.sh"))
	if err != nil {
		t.Fatalf("glob the toolkit's scripts: %v", err)
	}
	if len(scripts) == 0 {
		t.Fatal("no scripts found; the glob is wrong and this test would pass vacuously")
	}
	paths = append(paths, scripts...)

	sources := make([]string, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		sources = append(sources, string(data))
	}
	return sources
}

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

	// EVERY call must be guarded, everywhere, not just the one a reviewer
	// happened to name. This defect was reported three times, and each report
	// named one site:
	//
	//   1. the immutability loop in the release job      (reported)
	//   2. the homebrew-only path in validate            (found by counting)
	//   3. scripts/publish-release.sh, retry branch      (reported, third round)
	//
	// Two of the three were found only because something counted rather than
	// checking the site it had been told about. So this counts, and it counts
	// the shell scripts too: the workflow file alone would still be missing (3).
	//
	// A call is a line that invokes the command; mentions inside comments are
	// not. Ignoring comment lines keeps prose about the command from satisfying
	// or breaking the count.
	calls, guards := 0, 0
	for _, file := range attestationSensitiveFiles(t) {
		for _, line := range strings.Split(file, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(trimmed, "gh release verify") {
				calls++
			}
			if strings.Contains(trimmed, `"$RELEASE_ATTESTED" == "true"`) ||
				strings.Contains(trimmed, `"$RELEASE_ATTESTED" != "true"`) ||
				strings.Contains(trimmed, `"$RELEASE_ATTESTED" == true`) {
				guards++
			}
		}
	}
	if calls != 3 {
		t.Fatalf("expected exactly 3 gh release verify calls across the workflow and scripts, found %d; a new one needs its own RELEASE_ATTESTED guard", calls)
	}
	if guards != calls {
		t.Fatalf("found %d gh release verify calls but %d RELEASE_ATTESTED guards", calls, guards)
	}
	// Every workflow step that reaches a call must also derive the flag, or the
	// guard reads an unset variable. Two steps call it directly and one calls
	// the publisher, so three steps derive it.
	if got := strings.Count(source, "RELEASE_ATTESTED: ${{ !"+attestationVisibilityExpression+" }}"); got != 3 {
		t.Fatalf("expected 3 steps to derive RELEASE_ATTESTED, found %d", got)
	}
	// The publisher must refuse to run without it rather than assuming a
	// default, because either default is silently wrong for half the adopters.
	publisherData, err := os.ReadFile(filepath.Join("..", "..", "scripts", "publish-release.sh"))
	if err != nil {
		t.Fatalf("read the publisher: %v", err)
	}
	if !strings.Contains(string(publisherData), "RELEASE_ATTESTED must be exported as true or false") {
		t.Fatal("publish-release.sh does not require RELEASE_ATTESTED")
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
	if count := strings.Count(source, attestationVisibilityExpression); count != 5 {
		t.Errorf("expected 5 uses of %q (attest, notice, and RELEASE_ATTESTED in the three steps that reach a verify), found %d",
			attestationVisibilityExpression, count)
	}
}
