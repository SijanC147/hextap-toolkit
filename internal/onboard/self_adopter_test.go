package onboard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfAdopterOptions onboards the repository that owns the reusable workflow:
// the toolkit's own identity, with no external pin.
func selfAdopterOptions(project string) Options {
	options := validOptions(project)
	options.Repository = toolkitSelfRepository
	options.Formula = "hextap"
	options.Binary = "brew-hextap"
	options.ToolkitVersion = ""
	options.ToolkitSHA = ""
	return options
}

func setOrigin(t *testing.T, project, repository string) {
	t.Helper()
	command := exec.Command("git", "-C", project, "remote", "set-url", "origin", "git@github.com:"+repository+".git")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git remote set-url: %v: %s", err, output)
	}
}

// assertNoManagedWrites checks a refused onboarding left no caller and no
// .hextap/ directory behind.
func assertNoManagedWrites(t *testing.T, project string) {
	t.Helper()
	for _, path := range []string{workflowPath, ".hextap"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a refused onboarding: %v", path, err)
		}
	}
}

// TestOnboardSelfAdopterWritesTheRelativeCaller guards SB23-755: onboarding
// the toolkit must write the relative same-repository caller, never a pinned
// one, and the result must pass Validate end to end.
func TestOnboardSelfAdopterWritesTheRelativeCaller(t *testing.T) {
	project := writeToolkitSelfProject(t)
	workflow, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(workflowPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(workflow, selfCallerBytes("false")) {
		t.Fatalf("self-adopter caller is not the relative self-caller:\n%s", workflow)
	}
	setup, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(setupPath)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(setup), "pinned to stable toolkit tag") {
		t.Fatalf("self-adopter SETUP.md claims an external pin:\n%s", setup)
	}
	result, err := Validate(ValidateOptions{Project: project})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.ToolkitVersion != "" || result.ToolkitSHA != "" {
		t.Fatalf("self-adopter validated with a pin: %q %q", result.ToolkitVersion, result.ToolkitSHA)
	}

	// A second run over the committed relative caller changes nothing.
	again, err := Onboard(selfAdopterOptions(project))
	if err != nil {
		t.Fatalf("second Onboard() error = %v", err)
	}
	for _, entry := range again.Entries {
		if entry.Action == ActionCreate {
			t.Fatalf("second Onboard() planned a create at %s", entry.Path)
		}
	}
}

// TestOnboardSelfAdopterRequiresTheReusableWorkflow refuses the relative
// caller where it would resolve to nothing.
func TestOnboardSelfAdopterRequiresTheReusableWorkflow(t *testing.T) {
	project := writeGoProject(t)
	setOrigin(t, project, toolkitSelfRepository)
	_, err := Onboard(selfAdopterOptions(project))
	if err == nil || !strings.Contains(err.Error(), "relative caller requires the same-repository reusable workflow") {
		t.Fatalf("Onboard() error = %v, want missing reusable workflow", err)
	}
	assertNoManagedWrites(t, project)
}

// TestOnboardSelfAdopterRequiresAn0644ReusableWorkflow mirrors the mode check
// validateSelfCaller applies, so onboarding cannot write a caller Validate
// would then reject.
func TestOnboardSelfAdopterRequiresAn0644ReusableWorkflow(t *testing.T) {
	project := writeGoProject(t)
	setOrigin(t, project, toolkitSelfRepository)
	reusable := filepath.Join(project, filepath.FromSlash(reusableWorkflowPath))
	if err := os.MkdirAll(filepath.Dir(reusable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reusable, []byte("name: Hextap release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(reusable, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Onboard(selfAdopterOptions(project))
	if err == nil || !strings.Contains(err.Error(), "mode must be 0644") {
		t.Fatalf("Onboard() error = %v, want reusable workflow mode refusal", err)
	}
	assertNoManagedWrites(t, project)
}

// TestOnboardToolkitRefusesAPinnedCaller keeps the toolkit from being
// onboarded as an external adopter of a different copy of itself.
func TestOnboardToolkitRefusesAPinnedCaller(t *testing.T) {
	project := writeGoProject(t)
	setOrigin(t, project, toolkitSelfRepository)
	options := selfAdopterOptions(project)
	options.ToolkitVersion = "v1.2.3"
	options.ToolkitSHA = testToolkitSHA
	_, err := Onboard(options)
	if err == nil || !strings.Contains(err.Error(), "keeps its relative caller") {
		t.Fatalf("Onboard() error = %v, want pinned toolkit refusal", err)
	}
	assertNoManagedWrites(t, project)
}

// TestOnboardExternalAdopterCannotReachTheSelfAdopterPath proves the empty pin
// is not an escape hatch: an external adopter with an empty or half-empty pin
// still fails, even when it ships a same-repository reusable workflow, and the
// toolkit itself with a half-empty pin is a malformed pin, not a self-adopter.
func TestOnboardExternalAdopterCannotReachTheSelfAdopterPath(t *testing.T) {
	cases := []struct {
		name, repository, version, sha, want string
	}{
		{"external empty pin", "SijanC147/example-tool", "", "", "toolkit version must be exact stable"},
		{"external version only", "SijanC147/example-tool", "v1.2.3", "", "toolkit SHA must be exactly 40"},
		{"external sha only", "SijanC147/example-tool", "", testToolkitSHA, "toolkit version must be exact stable"},
		{"lookalike name", "SijanC147/hextap-toolkit-fork", "", "", "toolkit version must be exact stable"},
		{"case-variant name", "SijanC147/Hextap-Toolkit", "", "", "toolkit version must be exact stable"},
		{"toolkit version only", toolkitSelfRepository, "v1.2.3", "", "toolkit SHA must be exactly 40"},
		{"toolkit sha only", toolkitSelfRepository, "", testToolkitSHA, "toolkit version must be exact stable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := writeGoProject(t)
			setOrigin(t, project, tc.repository)
			reusable := filepath.Join(project, filepath.FromSlash(reusableWorkflowPath))
			if err := os.MkdirAll(filepath.Dir(reusable), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(reusable, []byte("name: Hextap release\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			options := validOptions(project)
			options.Repository = tc.repository
			options.ToolkitVersion = tc.version
			options.ToolkitSHA = tc.sha
			_, err := Onboard(options)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Onboard() error = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(project, filepath.FromSlash(workflowPath))); !os.IsNotExist(statErr) {
				t.Fatalf("caller workflow was written: %v", statErr)
			}
		})
	}
}

// TestCommittedToolkitArtifactsMatchTheirGenerators pins the repository's own
// .hextap/** to the generators, with the live hextap/main required context
// (ruleset 21411731: one context, Toolkit).
func TestCommittedToolkitArtifactsMatchTheirGenerators(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	mainRuleset, err := mainRulesetBytes([]string{"Toolkit"})
	if err != nil {
		t.Fatal(err)
	}
	tagRuleset, err := tagRulesetBytes()
	if err != nil {
		t.Fatal(err)
	}
	project, err := parseManifestBytes(read(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{
		mainRulesetPath: mainRuleset,
		tagRulesetPath:  tagRuleset,
		tapPath:         read(manifestPath),
		setupPath:       setupDocumentWith(toolkitSelfRepository, project.Formula.Name, "", "", project.Release.SubmodulesMode(), false),
		workflowPath:    selfCallerBytes(project.Release.SubmodulesMode()),
	}
	for path, expected := range want {
		if !bytes.Equal(read(path), expected) {
			t.Errorf("%s does not match its generator", path)
		}
	}
}
