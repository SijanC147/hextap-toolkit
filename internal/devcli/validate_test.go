package devcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateIgnoresGitIgnoredStateAndStillDetectsTrackedMutation exercises the
// snapshot against a real Git repository so that Git's own ignore rules, not a
// reimplementation of them, decide what belongs to the working tree.
func TestValidateIgnoresGitIgnoredStateAndStillDetectsTrackedMutation(t *testing.T) {
	t.Run("ignored volatile state mutating mid-run does not fail validation", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		agentState := filepath.Join(project, ".serena", "project.yml")
		runner := gateMutationRunner(t, project, func() {
			writeFixtureFile(t, agentState, "language: go\ngenerated: after\n")
			writeFixtureFile(t, filepath.Join(project, ".serena", "cache", "symbols.bin"), "fresh cache\n")
			writeFixtureFile(t, filepath.Join(project, ".DS_Store"), "finder metadata\n")
		})
		if _, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project}); err != nil {
			t.Fatalf("Validate(ignored state mutated) error = %v", err)
		}
	})

	t.Run("tracked file mutating mid-run still fails validation", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		feature := filepath.Join(project, "feature.go")
		runner := gateMutationRunner(t, project, func() {
			writeFixtureFile(t, feature, "package feature\n\n// mutated by a gate.\n")
		})
		_, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project})
		if err == nil || !strings.Contains(err.Error(), "working tree changed during validation") {
			t.Fatalf("Validate(tracked file mutated) error = %v", err)
		}
	})

	t.Run("untracked non-ignored file appearing mid-run still fails validation", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		runner := gateMutationRunner(t, project, func() {
			writeFixtureFile(t, filepath.Join(project, "generated.go"), "package generated\n")
		})
		_, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project})
		if err == nil || !strings.Contains(err.Error(), "working tree changed during validation") {
			t.Fatalf("Validate(untracked file created) error = %v", err)
		}
	})
}

// TestSnapshotProjectFilesSkipsIgnoredPathsGitDoesNotList proves the snapshot
// contains exactly what Git lists: an ignored file is not hashed even though it
// exists on disk, so its contents cannot influence the result.
func TestSnapshotProjectFilesSkipsIgnoredPathsGitDoesNotList(t *testing.T) {
	project := createGitToolkitFixture(t)
	runner := OSRunner{}
	before, err := snapshotProjectFiles(context.Background(), runner, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles() error = %v", err)
	}
	writeFixtureFile(t, filepath.Join(project, ".serena", "project.yml"), "language: go\nchanged: true\n")
	after, err := snapshotProjectFiles(context.Background(), runner, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(after ignored mutation) error = %v", err)
	}
	if before != after {
		t.Fatal("snapshot changed after mutating a Git-ignored file")
	}
	writeFixtureFile(t, filepath.Join(project, "feature.go"), "package feature\n\n// tracked change.\n")
	mutated, err := snapshotProjectFiles(context.Background(), runner, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(after tracked mutation) error = %v", err)
	}
	if mutated == after {
		t.Fatal("snapshot unchanged after mutating a tracked file")
	}
}

// TestSnapshotProjectFilesHandlesDuplicateAndVanishedPaths covers the two shapes
// Git's own output can take that a naive reader would mishandle: an unmerged
// path listed once per stage, and a listed path deleted before it is read.
func TestSnapshotProjectFilesHandlesDuplicateAndVanishedPaths(t *testing.T) {
	project := t.TempDir()
	writeFixtureFile(t, filepath.Join(project, "conflicted.go"), "package conflicted\n")
	duplicating := &scriptedRunner{handler: func(Command) (Result, error) {
		return Result{Stdout: "conflicted.go\x00conflicted.go\x00conflicted.go\x00"}, nil
	}}
	single := &scriptedRunner{handler: func(Command) (Result, error) {
		return Result{Stdout: "conflicted.go\x00"}, nil
	}}
	duplicated, err := snapshotProjectFiles(context.Background(), duplicating, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(unmerged path) error = %v", err)
	}
	deduplicated, err := snapshotProjectFiles(context.Background(), single, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(single path) error = %v", err)
	}
	if duplicated != deduplicated {
		t.Fatal("unmerged path listed once per stage produced a different snapshot")
	}

	vanishing := &scriptedRunner{handler: func(Command) (Result, error) {
		return Result{Stdout: "conflicted.go\x00deleted.go\x00"}, nil
	}}
	withDeleted, err := snapshotProjectFiles(context.Background(), vanishing, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(vanished path) error = %v", err)
	}
	if withDeleted == deduplicated {
		t.Fatal("a listed but absent path did not change the snapshot")
	}
}

// TestSnapshotProjectFilesReportsGitFailure keeps an unreadable repository a
// hard error rather than an empty snapshot that would silently pass validation.
func TestSnapshotProjectFilesReportsGitFailure(t *testing.T) {
	project := t.TempDir()
	failing := &scriptedRunner{handler: func(Command) (Result, error) {
		return Result{}, fmt.Errorf("not a git repository")
	}}
	_, err := snapshotProjectFiles(context.Background(), failing, project)
	if err == nil || !strings.Contains(err.Error(), "list tracked and non-ignored files") {
		t.Fatalf("snapshotProjectFiles(git failure) error = %v", err)
	}
}

// createGitToolkitFixture builds a real Git repository holding a committed and
// then locally modified tracked file plus ignored agent state. The tracked file
// is left modified so that `git status --porcelain` reports the same shape
// before and after a gate mutates its bytes, isolating the snapshot as the only
// mechanism that can detect the change.
func createGitToolkitFixture(t *testing.T) string {
	t.Helper()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve fixture directory: %v", err)
	}
	writeToolkitFixture(t, project)
	writeFixtureFile(t, filepath.Join(project, "scripts", "check-actionlint.sh"), "#!/usr/bin/env bash\nexit 0\n")
	writeFixtureFile(t, filepath.Join(project, "feature.go"), "package feature\n")
	writeFixtureFile(t, filepath.Join(project, ".gitignore"), "/.serena/\n.DS_Store\n")
	emptyExcludes := filepath.Join(project, "empty-excludes")
	writeFixtureFile(t, emptyExcludes, "")

	runGit(t, project, "init")
	runGit(t, project, "config", "user.name", "Hextap Fixture")
	runGit(t, project, "config", "user.email", "fixture@example.invalid")
	// The developer's global excludes file must not decide what this fixture
	// considers ignored, or the assertions become machine-dependent.
	runGit(t, project, "config", "core.excludesFile", emptyExcludes)
	runGit(t, project, "add", "go.mod", ".hextap.json", ".gitignore", "feature.go", "empty-excludes", filepath.Join("scripts", "check-actionlint.sh"))
	runGit(t, project, "commit", "--no-gpg-sign", "-m", "test: fixture")
	writeFixtureFile(t, filepath.Join(project, "feature.go"), "package feature\n\n// locally modified before validation.\n")
	writeFixtureFile(t, filepath.Join(project, ".serena", "project.yml"), "language: go\ngenerated: before\n")
	return project
}

// gateMutationRunner delegates every Git command to the real Git binary so that
// ignore rules are genuinely exercised, answers each validation gate with
// success, and runs mutate while the gates are in flight.
func gateMutationRunner(t *testing.T, project string, mutate func()) *scriptedRunner {
	t.Helper()
	real := OSRunner{}
	return &scriptedRunner{handler: func(command Command) (Result, error) {
		if command.Name == "git" {
			return real.Run(context.Background(), command)
		}
		if command.Name == "go" && len(command.Args) != 0 && command.Args[0] == "vet" {
			mutate()
		}
		return Result{}, nil
	}}
}

func runGit(t *testing.T, project string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", project}, args...)...)
	command.Env = commandEnvironment(nil)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func writeFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if filepath.Ext(path) == ".sh" {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
