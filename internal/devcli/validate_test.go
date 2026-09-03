package devcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestValidateRefusesGitOpaqueDirectories covers the two shapes Git reports as a
// single entry rather than as their contents. Both are false-pass hazards: the
// entry's mode and size do not change when a file inside it changes, and
// `git status --porcelain` reports the same entry either way, so a gate could
// rewrite a file inside one and the working tree would still look unchanged.
func TestValidateRefusesGitOpaqueDirectories(t *testing.T) {
	t.Run("initialized submodule with a modified file inside is refused", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		addSubmoduleFixture(t, project)
		writeFixtureFile(t, filepath.Join(project, "vendor", "file.txt"), "changed inside the submodule\n")
		_, err := (Service{Runner: gateMutationRunner(t, project, func() {})}).Validate(context.Background(), ValidateOptions{Project: project})
		if err == nil || !strings.Contains(err.Error(), "one opaque entry") || !strings.Contains(err.Error(), "vendor") {
			t.Fatalf("Validate(dirty submodule) error = %v", err)
		}
	})

	t.Run("embedded repository is refused", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		embedded := filepath.Join(project, "embedded")
		writeFixtureFile(t, filepath.Join(embedded, "file.txt"), "one\n")
		runGit(t, embedded, "init")
		_, err := (Service{Runner: gateMutationRunner(t, project, func() {})}).Validate(context.Background(), ValidateOptions{Project: project})
		if err == nil || !strings.Contains(err.Error(), "one opaque entry") || !strings.Contains(err.Error(), "embedded") {
			t.Fatalf("Validate(embedded repository) error = %v", err)
		}
	})
}

// TestSnapshotProjectFilesRefusesOpaqueEntriesFromGit pins the refusal to the two
// signals Git gives, independently of how a repository came to contain them: a
// trailing-slash path, and a listed path that is a directory on disk.
func TestSnapshotProjectFilesRefusesOpaqueEntriesFromGit(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, listing := range map[string]string{
		"trailing slash": "embedded/\x00",
		"gitlink":        "vendor\x00",
	} {
		runner := &scriptedRunner{handler: func(Command) (Result, error) {
			return Result{Stdout: listing}, nil
		}}
		_, err := snapshotProjectFiles(context.Background(), runner, project)
		if err == nil || !strings.Contains(err.Error(), "one opaque entry") {
			t.Errorf("snapshotProjectFiles(%s) error = %v", name, err)
		}
	}
}

// addSubmoduleFixture adds a real initialized submodule at vendor/. Git refuses
// the file transport by default, so it is enabled for these commands only.
func addSubmoduleFixture(t *testing.T, project string) {
	t.Helper()
	inner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve submodule source directory: %v", err)
	}
	writeFixtureFile(t, filepath.Join(inner, "file.txt"), "one\n")
	runGit(t, inner, "init")
	runGit(t, inner, "config", "user.name", "Hextap Fixture")
	runGit(t, inner, "config", "user.email", "fixture@example.invalid")
	runGit(t, inner, "add", "file.txt")
	runGit(t, inner, "commit", "--no-gpg-sign", "-m", "test: submodule fixture")
	runGit(t, project, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", inner, "vendor")
	runGit(t, project, "commit", "--no-gpg-sign", "-m", "test: add submodule")
}

// TestValidateDoesNotReadThroughSymlinkedDirectoryAncestors covers a tracked
// directory replaced by a symbolic link in a dirty checkout. Git then lists both
// the link, as an untracked path, and the index entries beneath it, but it never
// reads through the link: `git status` reports those entries as deleted. The
// snapshot must not read through the link either, or content outside the
// working tree could fail a validation that `git status --porcelain` and CI's
// `git diff --exit-code` would both pass.
func TestValidateDoesNotReadThroughSymlinkedDirectoryAncestors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks requires privileges on Windows")
	}

	t.Run("content behind the link mutating mid-run does not fail validation", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		external := replaceTrackedDirectoryWithSymlink(t, project)
		runner := gateMutationRunner(t, project, func() {
			writeFixtureFile(t, filepath.Join(external, "tracked.go"), "package tracked\n\n// rewritten outside the working tree.\n")
		})
		if _, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project}); err != nil {
			t.Fatalf("Validate(content behind symlink mutated) error = %v", err)
		}
	})

	t.Run("the link being retargeted mid-run still fails validation", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		replaceTrackedDirectoryWithSymlink(t, project)
		elsewhere := t.TempDir()
		writeFixtureFile(t, filepath.Join(elsewhere, "tracked.go"), "package tracked\n")
		runner := gateMutationRunner(t, project, func() {
			replaceDirectoryWithSymlink(t, filepath.Join(project, "pkg"), elsewhere)
		})
		_, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project})
		if err == nil || !strings.Contains(err.Error(), "working tree changed during validation") {
			t.Fatalf("Validate(symlink retargeted) error = %v", err)
		}
	})

	t.Run("a tracked directory replaced by a link mid-run still fails validation", func(t *testing.T) {
		project := createGitToolkitFixture(t)
		directory := commitTrackedDirectory(t, project)
		elsewhere := t.TempDir()
		writeFixtureFile(t, filepath.Join(elsewhere, "tracked.go"), "package tracked\n")
		runner := gateMutationRunner(t, project, func() {
			replaceDirectoryWithSymlink(t, directory, elsewhere)
		})
		_, err := (Service{Runner: runner}).Validate(context.Background(), ValidateOptions{Project: project})
		if err == nil || !strings.Contains(err.Error(), "working tree changed during validation") {
			t.Fatalf("Validate(tracked directory replaced by symlink) error = %v", err)
		}
	})
}

// TestSnapshotProjectFilesTreatsPathsBeneathSymlinkAsMissing pins the rule to
// the listing shape Git produces, independently of how a repository came to
// contain it: a listed path beneath a symbolic link is recorded as missing, so
// nothing is read through the link, while the link itself is still hashed by
// its target. The link sits one directory down to cover ancestors deeper than
// the repository root.
func TestSnapshotProjectFilesTreatsPathsBeneathSymlinkAsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks requires privileges on Windows")
	}
	project := t.TempDir()
	external := t.TempDir()
	writeFixtureFile(t, filepath.Join(external, "file.go"), "package external\n")
	link := filepath.Join(project, "nested", "link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{handler: func(Command) (Result, error) {
		return Result{Stdout: "nested/link\x00nested/link/file.go\x00"}, nil
	}}
	before, err := snapshotProjectFiles(context.Background(), runner, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles() error = %v", err)
	}
	writeFixtureFile(t, filepath.Join(external, "file.go"), "package external\n\n// changed behind the link.\n")
	after, err := snapshotProjectFiles(context.Background(), runner, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(after mutation behind link) error = %v", err)
	}
	if before != after {
		t.Fatal("snapshot changed after mutating content behind a symbolic link")
	}
	elsewhere := t.TempDir()
	writeFixtureFile(t, filepath.Join(elsewhere, "file.go"), "package external\n")
	replaceDirectoryWithSymlink(t, link, elsewhere)
	retargeted, err := snapshotProjectFiles(context.Background(), runner, project)
	if err != nil {
		t.Fatalf("snapshotProjectFiles(after retargeting link) error = %v", err)
	}
	if retargeted == after {
		t.Fatal("snapshot unchanged after retargeting the symbolic link")
	}
}

// commitTrackedDirectory adds a committed directory holding one tracked file to
// the fixture and returns the directory's path.
func commitTrackedDirectory(t *testing.T, project string) string {
	t.Helper()
	directory := filepath.Join(project, "pkg")
	writeFixtureFile(t, filepath.Join(directory, "tracked.go"), "package tracked\n")
	runGit(t, project, "add", filepath.Join("pkg", "tracked.go"))
	runGit(t, project, "commit", "--no-gpg-sign", "-m", "test: tracked directory")
	return directory
}

// replaceTrackedDirectoryWithSymlink commits a tracked directory, replaces it
// with a symbolic link to a directory outside the repository that holds a file
// of the same name, and returns that outside directory. Git's view of the
// result is ` D pkg/tracked.go` plus `?? pkg`, before and after anything behind
// the link changes.
func replaceTrackedDirectoryWithSymlink(t *testing.T, project string) string {
	t.Helper()
	directory := commitTrackedDirectory(t, project)
	external := t.TempDir()
	writeFixtureFile(t, filepath.Join(external, "tracked.go"), "package tracked\n\n// lives outside the working tree.\n")
	replaceDirectoryWithSymlink(t, directory, external)
	return external
}

// replaceDirectoryWithSymlink removes path, a directory or an existing link,
// and puts a symbolic link to target in its place.
func replaceDirectoryWithSymlink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}
