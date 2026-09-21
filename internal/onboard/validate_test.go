package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// toolkitSelfRepository is the canonical toolkit identity used by the fixtures.
const toolkitSelfRepository = supportedOwner + "/" + toolkitRepositoryName

// writeReusableWorkflowProject creates a project root that owns the reusable
// release workflow a relative caller resolves to.
func writeReusableWorkflowProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(reusableWorkflowPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("name: Hextap release\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return root
}

func externalCaller(reference string) []byte {
	owned := string(workflowBytes("v1.2.3", testToolkitSHA, ""))
	pinned := "    uses: SijanC147/hextap-toolkit/.github/workflows/release-go.yml@" + testToolkitSHA + " # v1.2.3\n"
	replacement := "    uses: SijanC147/hextap-toolkit/.github/workflows/release-go.yml" + reference + "\n"
	if !strings.Contains(owned, pinned) {
		panic("owned caller no longer contains the expected pinned reference")
	}
	return []byte(strings.Replace(owned, pinned, replacement, 1))
}

func TestRelativeSelfCallerIsAcceptedOnlyForTheToolkitRepositoryIdentity(t *testing.T) {
	cases := []struct {
		name        string
		repository  string
		data        []byte
		omitCalled  bool
		wantVersion string
		wantCommit  string
		wantReject  string
	}{
		{
			name:       "toolkit self-caller in its own repository is accepted without an external pin",
			repository: toolkitSelfRepository,
			data:       selfCallerBytes(""),
		},
		{
			name:       "relative caller in an adopter repository is rejected",
			repository: "SijanC147/example-tool",
			data:       selfCallerBytes(""),
			wantReject: "relative same-repository caller is permitted only in",
		},
		{
			name:       "relative caller in a look-alike repository name is rejected",
			repository: supportedOwner + "/hextap-toolkit-fork",
			data:       selfCallerBytes(""),
			wantReject: "relative same-repository caller is permitted only in",
		},
		{
			// The self-caller condition matches on repository name only, because
			// Validate already rejects any owner other than supportedOwner before
			// this function runs. This unit case documents that division of
			// responsibility; it is not end-to-end proof that a fork can release.
			name:       "the caller condition matches on repository name and leaves owner enforcement upstream",
			repository: "another-owner/" + toolkitRepositoryName,
			data:       selfCallerBytes(""),
		},
		{
			name:       "relative caller with a malformed repository identity is rejected",
			repository: "not-a-slug",
			data:       selfCallerBytes(""),
			wantReject: "repository must be OWNER/REPO with a safe GitHub identity",
		},
		{
			name:       "relative caller without the same-repository reusable workflow is rejected",
			repository: toolkitSelfRepository,
			data:       selfCallerBytes(""),
			omitCalled: true,
			wantReject: "relative caller requires the same-repository reusable workflow",
		},
		{
			name:       "self-caller with an additional inherited secret mapping is rejected",
			repository: toolkitSelfRepository,
			data:       []byte(strings.Replace(string(selfCallerBytes("")), "    secrets:\n", "    secrets: inherit\n    secrets:\n", 1)),
			wantReject: "does not match the exact owned same-repository self-caller",
		},
		{
			name:       "self-caller granting an extra permission is rejected",
			repository: toolkitSelfRepository,
			data:       []byte(strings.Replace(string(selfCallerBytes("")), "permissions:\n", "permissions:\n  packages: write\n", 1)),
			wantReject: "does not match the exact owned same-repository self-caller",
		},
		{
			name:        "external adopter with a full SHA pin is accepted",
			repository:  "SijanC147/example-tool",
			data:        workflowBytes("v1.2.3", testToolkitSHA, ""),
			wantVersion: "v1.2.3",
			wantCommit:  testToolkitSHA,
		},
		{
			name:        "the toolkit repository may still use an external full SHA pin",
			repository:  toolkitSelfRepository,
			data:        workflowBytes("v1.2.3", testToolkitSHA, ""),
			wantVersion: "v1.2.3",
			wantCommit:  testToolkitSHA,
		},
		{
			name:       "external adopter without any pin is still rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller(""),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "external adopter pinned to main is still rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller("@main # v1.2.3"),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "external adopter pinned to a floating major tag is still rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller("@v1 # v1.2.3"),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "external adopter pinned to a short SHA is still rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller("@" + testToolkitSHA[:12] + " # v1.2.3"),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "external adopter pinned to an uppercase SHA is still rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller("@" + strings.ToUpper(testToolkitSHA) + " # v1.2.3"),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "external adopter without the stable version provenance comment is rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller("@" + testToolkitSHA),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "external adopter pinning a prerelease provenance comment is rejected",
			repository: "SijanC147/example-tool",
			data:       externalCaller("@" + testToolkitSHA + " # v1.2.3-rc.1"),
			wantReject: "lacks an exact stable toolkit version and full SHA pin",
		},
		{
			name:       "an adopter that adds the relative caller alongside a valid pin is rejected",
			repository: "SijanC147/example-tool",
			data:       []byte(string(workflowBytes("v1.2.3", testToolkitSHA, "")) + "\n  self:\n    uses: ./.github/workflows/release-go.yml\n"),
			wantReject: "relative same-repository caller is permitted only in",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := writeReusableWorkflowProject(t)
			if testCase.omitCalled {
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(reusableWorkflowPath))); err != nil {
					t.Fatalf("Remove(%s): %v", reusableWorkflowPath, err)
				}
			}
			version, commit, err := validateWorkflow(root, testCase.repository, "", testCase.data)
			if testCase.wantReject != "" {
				if err == nil {
					t.Fatalf("validateWorkflow() accepted %s; want rejection containing %q", testCase.name, testCase.wantReject)
				}
				if !strings.Contains(err.Error(), testCase.wantReject) {
					t.Fatalf("validateWorkflow() error = %q, want it to contain %q", err, testCase.wantReject)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateWorkflow() error = %v", err)
			}
			if version != testCase.wantVersion || commit != testCase.wantCommit {
				t.Fatalf("validateWorkflow() = (%q, %q), want (%q, %q)", version, commit, testCase.wantVersion, testCase.wantCommit)
			}
		})
	}
}

func TestSelfCallerBytesRemainTheCommittedToolkitCaller(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "..", workflowPath))
	if err != nil {
		t.Fatalf("read committed toolkit caller: %v", err)
	}
	if string(committed) != string(selfCallerBytes("")) {
		t.Fatalf("the committed toolkit caller no longer equals the owned self-caller:\n%s", committed)
	}
	if strings.Contains(string(committed), "SijanC147/hextap-toolkit/.github/workflows/release-go.yml@") {
		t.Fatalf("the toolkit self-caller must stay relative, never an external reference:\n%s", committed)
	}
}

func TestSelfCallerModeMustBeExactlyRegularZeroSixFourFour(t *testing.T) {
	root := writeReusableWorkflowProject(t)
	path := filepath.Join(root, filepath.FromSlash(reusableWorkflowPath))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod(%s): %v", path, err)
	}
	_, _, err := validateWorkflow(root, toolkitSelfRepository, "", selfCallerBytes(""))
	if err == nil || !strings.Contains(err.Error(), "mode must be 0644") {
		t.Fatalf("validateWorkflow() error = %v, want a 0644 mode rejection", err)
	}
}

func TestToolkitSelfAdopterValidatesTheCompleteLocalContract(t *testing.T) {
	project := writeToolkitSelfProject(t)
	validated, err := Validate(ValidateOptions{Project: project})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if validated.Manifest.RepositorySlug() != toolkitSelfRepository {
		t.Fatalf("repository = %q, want %q", validated.Manifest.RepositorySlug(), toolkitSelfRepository)
	}
	if validated.ToolkitVersion != "" || validated.ToolkitSHA != "" {
		t.Fatalf("self-adopter toolkit pin = (%q, %q), want both empty", validated.ToolkitVersion, validated.ToolkitSHA)
	}
}

func TestRelativeCallerInAnAdopterProjectFailsTheCompleteLocalContract(t *testing.T) {
	project := writeGoProject(t)
	if _, err := Onboard(validOptions(project)); err != nil {
		t.Fatalf("Onboard() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(workflowPath)), selfCallerBytes(""), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", workflowPath, err)
	}
	reusable := filepath.Join(project, filepath.FromSlash(reusableWorkflowPath))
	if err := os.WriteFile(reusable, []byte("name: Hextap release\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", reusableWorkflowPath, err)
	}
	_, err := Validate(ValidateOptions{Project: project})
	if err == nil || !strings.Contains(err.Error(), "relative same-repository caller is permitted only in") {
		t.Fatalf("Validate() error = %v, want the adopter relative caller to be rejected", err)
	}
}

// writeToolkitSelfProject onboards a project under the canonical toolkit
// identity and then converts its caller to the owned relative self-caller,
// which is the shape the toolkit itself releases with.
func writeToolkitSelfProject(t *testing.T) string {
	t.Helper()
	project := writeGoProject(t)
	command := exec.Command("git", "-C", project, "remote", "set-url", "origin", "git@github.com:"+toolkitSelfRepository+".git")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git remote set-url: %v: %s", err, output)
	}
	options := validOptions(project)
	options.Repository = toolkitSelfRepository
	options.Formula = "hextap"
	options.Binary = "brew-hextap"
	if _, err := Onboard(options); err != nil {
		t.Fatalf("Onboard() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(workflowPath)), selfCallerBytes(""), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", workflowPath, err)
	}
	reusable := filepath.Join(project, filepath.FromSlash(reusableWorkflowPath))
	if err := os.WriteFile(reusable, []byte("name: Hextap release\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", reusableWorkflowPath, err)
	}
	setup := setupDocument(toolkitSelfRepository, options.Formula, "", "")
	if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(setupPath)), setup, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", setupPath, err)
	}
	return project
}

// TestSetupDocumentHasASelfAdopterVariant guards SB23-755's first defect. The
// document ends by naming the stable toolkit tag and commit the caller is
// pinned to. Both are empty for the repository that owns the reusable workflow,
// and validate.go compares the committed file byte-for-byte against this
// generator, so without a variant .hextap/SETUP.md could never match for the
// toolkit itself.
func TestSetupDocumentHasASelfAdopterVariant(t *testing.T) {
	selfAdopter := string(setupDocument(toolkitSelfRepository, "hextap", "", ""))
	external := string(setupDocument("SijanC147/example-tool", "example-tool", "v1.2.3", testToolkitSHA))

	if strings.Contains(selfAdopter, "pinned to stable toolkit tag") {
		t.Fatalf("self-adopter document claims an external pin:\n%s", selfAdopter)
	}
	if !strings.Contains(selfAdopter, "owns the reusable release workflow") {
		t.Fatalf("self-adopter document does not explain the relative caller:\n%s", selfAdopter)
	}

	// The external form must be untouched: this variant exists for one identity
	// and widening it would hand an adopter a way out of the full-SHA pin.
	if !strings.Contains(external, "pinned to stable toolkit tag `v1.2.3` at full commit `"+testToolkitSHA+"`") {
		t.Fatalf("external document lost its pin paragraph:\n%s", external)
	}
	if strings.Contains(external, "owns the reusable release workflow") {
		t.Fatalf("external document carries the self-adopter paragraph:\n%s", external)
	}

	// One half empty is a malformed pin, not a self-adopter, and must keep the
	// pinned paragraph so the mismatch is visible rather than excused.
	for name, half := range map[string][2]string{
		"an empty version alone": {"", testToolkitSHA},
		"an empty SHA alone":     {"v1.2.3", ""},
	} {
		t.Run(name, func(t *testing.T) {
			document := string(setupDocument(toolkitSelfRepository, "hextap", half[0], half[1]))
			if strings.Contains(document, "owns the reusable release workflow") {
				t.Fatalf("%s was treated as a self-adopter:\n%s", name, document)
			}
		})
	}
}
