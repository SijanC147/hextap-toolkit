package commandmeta

import (
	"strings"
	"testing"
)

func TestRollbackHelpAndCompletionExposeTheCompleteSafetyContract(t *testing.T) {
	for _, kind := range []string{"formula", "cask"} {
		path := []string{"rollback", kind}
		help := Help("hextap", path)
		for _, required := range []string{
			"usage: hextap rollback " + kind + " <NAME> [OPTIONS]",
			"-t, --to-commit FULL_SHA",
			"-v, --to-version VERSION",
			"-m, --mode MODE",
			"-x, --execute[=BOOL]",
			"-c, --confirm TEXT",
			"-j, --json[=BOOL]",
			"-h, --help",
			"Planning is the default",
			"Examples:",
		} {
			if !strings.Contains(help, required) {
				t.Errorf("%s help lacks %q", kind, required)
			}
		}
	}
	completion := Zsh()
	for _, required := range []string{
		"_hextap_rollback_formula()",
		"_hextap_rollback_cask()",
		"_hextap_values_rollback_formula_mode()",
		"local[temporarily check out one historical definition",
		"remote[prepare a canonical metadata rollback",
		"(-t --to-commit)-t",
		"(-x --execute)--execute",
		"1:name:_message",
		"# Example: Execute the reviewed local plan with its exact printed confirmation",
	} {
		if !strings.Contains(completion, required) {
			t.Errorf("rollback completion lacks %q", required)
		}
	}
}
