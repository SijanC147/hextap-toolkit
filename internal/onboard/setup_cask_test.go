package onboard

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetupDocumentNamesACaskForCaskProfileProjects(t *testing.T) {
	cask := string(setupDocumentWith("SijanC147/a-bar", "a-bar", "v0.8.0", testToolkitSHA, "", true))
	for _, want := range []string{"Casks/a-bar.rb", "sha256 arm:", "Do not invent checksums or commit a placeholder Cask."} {
		if !strings.Contains(cask, want) {
			t.Errorf("cask SETUP.md is missing %q", want)
		}
	}
	for _, refused := range []string{"Formula/a-bar.rb", "< Formula", "render the exact Formula"} {
		if strings.Contains(cask, refused) {
			t.Errorf("cask SETUP.md still says %q", refused)
		}
	}
}

func TestSetupDocumentFormulaTextIsUnchanged(t *testing.T) {
	formula := setupDocument("SijanC147/example-tool", "example-tool", "v1.2.3", testToolkitSHA, "")
	explicit := setupDocumentWith("SijanC147/example-tool", "example-tool", "v1.2.3", testToolkitSHA, "", false)
	if !bytes.Equal(formula, explicit) || !bytes.Contains(formula, []byte("class ExampleTool < Formula")) {
		t.Fatal("formula SETUP.md changed")
	}
}
