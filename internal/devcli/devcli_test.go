package devcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/release"
)

func TestDeveloperCLIHelpAndMutationArgumentErrors(t *testing.T) {
	service := Service{Runner: &scriptedRunner{handler: func(command Command) (Result, error) {
		return Result{}, fmt.Errorf("unexpected command %s", commandKey(command))
	}}}
	for _, args := range [][]string{{"--help"}, {"status", "--help"}, {"validate", "--help"}, {"plan", "--help"}, {"release", "--help"}, {"deploy", "--help"}, {"install", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := service.runCLI(context.Background(), args, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.HasPrefix(stdout.String(), "usage: brew-hextap dev") {
			t.Fatalf("runCLI(%v) = %d, %q, %q", args, code, stdout.String(), stderr.String())
		}
	}
	for _, args := range [][]string{{}, {"unknown"}, {"plan", "--bump", "other"}, {"release", "--bump", "minor"}, {"install", "--tag", "v0.3.0"}} {
		var stdout, stderr bytes.Buffer
		if code := service.runCLI(context.Background(), args, &stdout, &stderr); code == 0 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "error: ") {
			t.Errorf("runCLI(%v) = %d, %q, %q", args, code, stdout.String(), stderr.String())
		}
	}
}

type scriptedRunner struct {
	handler  func(Command) (Result, error)
	commands []Command
}

func (runner *scriptedRunner) Run(_ context.Context, command Command) (Result, error) {
	runner.commands = append(runner.commands, command)
	return runner.handler(command)
}

func commandKey(command Command) string {
	if len(command.Args) == 0 {
		return command.Name
	}
	return command.Name + " " + strings.Join(command.Args, " ")
}

func TestStatusReportsCanonicalRepositoryAndStableRelease(t *testing.T) {
	project := createToolkitFixture(t)
	runner := canonicalStatusRunner(t, project)
	service := Service{Runner: runner, Version: "0.2.0", Commit: strings.Repeat("a", 40)}

	status, err := service.Status(context.Background(), project)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Project != project || status.Repository != ToolkitRepository || status.Branch != "codex/dev-release" || status.Head != strings.Repeat("b", 40) || !status.Clean || status.GitHubUser != ToolkitOwner {
		t.Fatalf("Status() = %#v", status)
	}
	if status.LatestStableTag != "v0.2.0" || status.LatestStableVersion != "0.2.0" {
		t.Fatalf("Status() release = %#v", status)
	}
	if status.NextPatch != "0.2.1" || status.NextMinor != "0.3.0" || status.NextMajor != "1.0.0" {
		t.Fatalf("Status() next versions = %#v", status)
	}
}

func TestStatusRejectsWritableNonOriginRemote(t *testing.T) {
	project := createToolkitFixture(t)
	runner := canonicalStatusRunner(t, project)
	original := runner.handler
	runner.handler = func(command Command) (Result, error) {
		switch commandKey(command) {
		case "git -C " + project + " remote":
			return Result{Stdout: "origin\nupstream\n"}, nil
		case "git -C " + project + " remote get-url --push --all upstream":
			return Result{Stdout: "https://github.com/third-party/hextap-toolkit.git\n"}, nil
		default:
			return original(command)
		}
	}
	service := Service{Runner: runner, Version: "dev", Commit: "unknown"}
	_, err := service.Status(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "writable remote") || strings.Contains(err.Error(), "credential") {
		t.Fatalf("Status(writable upstream) error = %v", err)
	}
}

func TestPlanComputesExactBumpAndConfirmation(t *testing.T) {
	project := createToolkitFixture(t)
	service := Service{Runner: canonicalStatusRunner(t, project), Version: "0.2.0", Commit: strings.Repeat("a", 40)}
	for bump, want := range map[release.Bump]string{
		release.PatchBump: "v0.2.1",
		release.MinorBump: "v0.3.0",
		release.MajorBump: "v1.0.0",
	} {
		plan, err := service.Plan(context.Background(), project, bump)
		if err != nil || plan.Tag != want || plan.CurrentTag != "v0.2.0" || plan.Commit != strings.Repeat("b", 40) {
			t.Errorf("Plan(%s) = %#v, %v; want tag %s", bump, plan, err, want)
		}
	}
	if err := RequireConfirmation(ReleasePlan{Tag: "v0.3.0"}, "v0.3.0", true); err != nil {
		t.Fatalf("RequireConfirmation(valid) = %v", err)
	}
	for _, test := range []struct {
		confirm string
		execute bool
	}{
		{confirm: "", execute: true},
		{confirm: "v0.2.1", execute: true},
		{confirm: "v0.3.0", execute: false},
	} {
		if err := RequireConfirmation(ReleasePlan{Tag: "v0.3.0"}, test.confirm, test.execute); err == nil {
			t.Errorf("RequireConfirmation(%q, %v) unexpectedly succeeded", test.confirm, test.execute)
		}
	}
}

func TestValidateRunsExactQuickAndFullGatesAndDetectsMutation(t *testing.T) {
	project := createToolkitFixture(t)
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-actionlint.sh", "hextap-build"} {
		if err := os.WriteFile(filepath.Join(project, "scripts", name), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := &scriptedRunner{}
	statusCalls := 0
	runner.handler = func(command Command) (Result, error) {
		key := commandKey(command)
		if key == "git -C "+project+" rev-parse --show-toplevel" {
			return Result{Stdout: project + "\n"}, nil
		}
		if key == "git -C "+project+" status --porcelain=v1 --untracked-files=all" {
			statusCalls++
			return Result{Stdout: " M feature.go\n"}, nil
		}
		if key == "git -C "+project+" ls-files -z --cached --others --exclude-standard" {
			return fixtureListing("go.mod", ".hextap.json", "scripts/check-actionlint.sh", "scripts/hextap-build"), nil
		}
		if key == "gofmt -l ." {
			return Result{}, nil
		}
		return Result{Stdout: "ok\n"}, nil
	}
	service := Service{Runner: runner}
	result, err := service.Validate(context.Background(), ValidateOptions{Project: project, Full: true})
	if err != nil {
		t.Fatalf("Validate(full) error = %v", err)
	}
	if !result.Race || statusCalls != 2 {
		t.Fatalf("Validate(full) = %#v, status calls %d", result, statusCalls)
	}
	keys := make([]string, len(runner.commands))
	for index, command := range runner.commands {
		keys[index] = commandKey(command)
	}
	for _, required := range []string{
		"gofmt -l .",
		"go test -count=1 ./...",
		"go test -race -count=1 ./...",
		"go vet ./...",
		"go build -trimpath ./...",
		"bash -n " + filepath.Join(project, "scripts", "check-actionlint.sh"),
		"shellcheck " + filepath.Join(project, "scripts", "check-actionlint.sh"),
		filepath.Join(project, "scripts", "check-actionlint.sh"),
		"git -C " + project + " diff --check",
	} {
		if !containsString(keys, required) {
			t.Errorf("validation commands %v are missing %q", keys, required)
		}
	}

	mutationRunner := &scriptedRunner{}
	mutationCalls := 0
	mutationRunner.handler = func(command Command) (Result, error) {
		if commandKey(command) == "git -C "+project+" rev-parse --show-toplevel" {
			return Result{Stdout: project + "\n"}, nil
		}
		if commandKey(command) == "git -C "+project+" ls-files -z --cached --others --exclude-standard" {
			return fixtureListing("go.mod", ".hextap.json", "scripts/check-actionlint.sh", "scripts/hextap-build"), nil
		}
		if commandKey(command) == "git -C "+project+" status --porcelain=v1 --untracked-files=all" {
			mutationCalls++
			if mutationCalls == 1 {
				return Result{Stdout: " M feature.go\n"}, nil
			}
			return Result{Stdout: " M feature.go\n?? generated.txt\n"}, nil
		}
		return Result{}, nil
	}
	_, err = (Service{Runner: mutationRunner}).Validate(context.Background(), ValidateOptions{Project: project})
	if err == nil || !strings.Contains(err.Error(), "working tree changed during validation") {
		t.Fatalf("Validate(mutating gate) error = %v", err)
	}
}

func TestValidateDetectsByteMutationEvenWhenGitStatusShapeIsUnchanged(t *testing.T) {
	project := createToolkitFixture(t)
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "scripts", "check-actionlint.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	feature := filepath.Join(project, "feature.go")
	if err := os.WriteFile(feature, []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{}
	runner.handler = func(command Command) (Result, error) {
		key := commandKey(command)
		switch key {
		case "git -C " + project + " rev-parse --show-toplevel":
			return Result{Stdout: project + "\n"}, nil
		case "git -C " + project + " ls-files -z --cached --others --exclude-standard":
			return fixtureListing("go.mod", ".hextap.json", "feature.go", "scripts/check-actionlint.sh"), nil
		case "git -C " + project + " status --porcelain=v1 --untracked-files=all":
			return Result{Stdout: "?? feature.go\n"}, nil
		case "go vet ./...":
			if err := os.WriteFile(feature, []byte("package changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return Result{}, nil
		default:
			return Result{}, nil
		}
	}
	_, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project})
	if err == nil || !strings.Contains(err.Error(), "working tree changed during validation") {
		t.Fatalf("Validate(byte mutation) error = %v", err)
	}
}

// fixtureListing renders the NUL-separated shape of `git ls-files -z` for a
// scripted repository listing.
func fixtureListing(relatives ...string) Result {
	return Result{Stdout: strings.Join(relatives, "\x00") + "\x00"}
}

func createToolkitFixture(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	writeToolkitFixture(t, project)
	return project
}

// writeToolkitFixture populates an existing directory with the minimum a
// project needs to be accepted as the toolkit itself.
func writeToolkitFixture(t *testing.T, project string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module "+ToolkitModule+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "schema": 1,
  "formula": {
    "name": "hextap",
    "class": "Hextap",
    "description": "Deterministic release and onboarding toolkit for Hextap",
    "homepage": "https://github.com/SijanC147/hextap-toolkit",
    "license": "MIT",
    "repository": {"owner": "SijanC147", "name": "hextap-toolkit"},
    "binary": "brew-hextap",
    "assets": {
      "darwin_arm64": "hextap-darwin-arm64.tar.gz",
      "darwin_amd64": "hextap-darwin-amd64.tar.gz"
    }
  },
  "release": {"build_script": "scripts/hextap-build", "linux": true},
  "homebrew": {"macos_only": true, "test_args": ["--version"], "service": {"enabled": false}, "caveats": ""}
}
`
	if err := os.WriteFile(filepath.Join(project, ".hextap.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func canonicalStatusRunner(t *testing.T, project string) *scriptedRunner {
	t.Helper()
	head := strings.Repeat("b", 40)
	releases, err := json.Marshal([]map[string]any{
		{"tagName": "v0.1.2", "isDraft": false, "isPrerelease": false, "isImmutable": true},
		{"tagName": "v0.2.0", "isDraft": false, "isPrerelease": false, "isImmutable": true},
		{"tagName": "v0.3.0-rc.1", "isDraft": false, "isPrerelease": true, "isImmutable": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{}
	runner.handler = func(command Command) (Result, error) {
		switch commandKey(command) {
		case "git -C " + project + " rev-parse --show-toplevel":
			return Result{Stdout: project + "\n"}, nil
		case "git -C " + project + " remote":
			return Result{Stdout: "origin\n"}, nil
		case "git -C " + project + " remote get-url origin":
			return Result{Stdout: ToolkitOriginHTTPS + "\n"}, nil
		case "git -C " + project + " remote get-url --push origin":
			return Result{Stdout: ToolkitOriginHTTPS + "\n"}, nil
		case "git -C " + project + " symbolic-ref --quiet --short HEAD":
			return Result{Stdout: "codex/dev-release\n"}, nil
		case "git -C " + project + " rev-parse HEAD":
			return Result{Stdout: head + "\n"}, nil
		case "git -C " + project + " status --porcelain=v1 --untracked-files=all":
			return Result{}, nil
		case "gh api user --hostname github.com --jq .login":
			return Result{Stdout: ToolkitOwner + "\n"}, nil
		case "gh release list --repo " + ToolkitRepository + " --limit 100 --json tagName,isDraft,isPrerelease,isImmutable":
			return Result{Stdout: string(releases) + "\n"}, nil
		default:
			return Result{}, fmt.Errorf("unexpected command: %s", commandKey(command))
		}
	}
	return runner
}

func containsString(values []string, wanted string) bool {
	return slices.Contains(values, wanted)
}

func TestStatusJSONShapeIsStable(t *testing.T) {
	project := createToolkitFixture(t)
	service := Service{Runner: canonicalStatusRunner(t, project), Version: "0.2.0", Commit: strings.Repeat("a", 40)}
	status, err := service.Status(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "project", "repository", "branch", "head", "clean", "github_user", "latest_stable_tag", "latest_stable_version", "next_patch", "next_minor", "next_major", "cli_version", "cli_commit"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("status JSON is missing %q: %s", key, data)
		}
	}
}
