package onboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rewriteRootFile(t *testing.T, root *os.Root, path string, data []byte, mode os.FileMode) {
	t.Helper()
	file, err := root.OpenFile(filepath.FromSlash(path), os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertTransactionInternalsAbsent(t *testing.T, root string) {
	t.Helper()
	for _, relative := range []string{onboardLockPath, onboardStagePath} {
		if _, err := os.Lstat(filepath.Join(root, relative)); !os.IsNotExist(err) {
			t.Fatalf("transaction residue %s: %v", relative, err)
		}
	}
}

func TestTransactionRejectsUnchangedTargetMutationAfterPreflight(t *testing.T) {
	project := writeGoProject(t)
	if _, err := Onboard(validOptions(project)); err != nil {
		t.Fatal(err)
	}
	hooks := defaultTransactionHooks()
	const replacement = "same-size-workflow-competitor\n"
	hooks.afterPreflight = func(root *os.Root) error {
		rewriteRootFile(t, root, workflowPath, []byte(replacement), 0o644)
		return nil
	}
	if _, err := onboardWithTransactionHooks(validOptions(project), hooks); err == nil {
		t.Fatal("onboarding accepted a changed unchanged-target snapshot")
	}
	data, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(workflowPath)))
	if err != nil || string(data) != replacement {
		t.Fatalf("competitor mutation was removed: %v, %q", err, data)
	}
	assertTransactionInternalsAbsent(t, project)
}

func TestTransactionRevalidatesUnchangedTargetsBeforeSuccess(t *testing.T) {
	project := writeGoProject(t)
	if _, err := Onboard(validOptions(project)); err != nil {
		t.Fatal(err)
	}
	hooks := defaultTransactionHooks()
	const replacement = "late-competitor\n"
	hooks.beforeSuccess = func(root *os.Root) error {
		rewriteRootFile(t, root, workflowPath, []byte(replacement), 0o644)
		return nil
	}
	if _, err := onboardWithTransactionHooks(validOptions(project), hooks); err == nil {
		t.Fatal("onboarding accepted a target mutation before success")
	}
	data, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(workflowPath)))
	if err != nil || string(data) != replacement {
		t.Fatalf("late competitor mutation was removed: %v, %q", err, data)
	}
	assertTransactionInternalsAbsent(t, project)
}

func TestTransactionRejectsExistingParentMutationAfterLock(t *testing.T) {
	project := writeGoProject(t)
	parent := filepath.Join(project, ".github")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := defaultTransactionHooks()
	hooks.afterLock = func(*os.Root) error { return os.Chmod(parent, 0o700) }
	if _, err := onboardWithTransactionHooks(validOptions(project), hooks); err == nil {
		t.Fatal("onboarding accepted a changed parent snapshot")
	}
	if info, err := os.Lstat(parent); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("competitor parent mutation was removed: %v, %v", info, err)
	}
	assertTransactionInternalsAbsent(t, project)
	if _, err := os.Lstat(filepath.Join(project, manifestPath)); !os.IsNotExist(err) {
		t.Fatalf("parent snapshot failure published a manifest: %v", err)
	}
}

func TestTransactionRejectsProjectRootReplacement(t *testing.T) {
	project := writeGoProject(t)
	moved := project + "-moved"
	hooks := defaultTransactionHooks()
	hooks.afterPreflight = func(*os.Root) error {
		if err := os.Rename(project, moved); err != nil {
			return err
		}
		return os.Mkdir(project, 0o755)
	}
	if _, err := onboardWithTransactionHooks(validOptions(project), hooks); err == nil {
		t.Fatal("onboarding accepted a replaced project root")
	}
	assertTransactionInternalsAbsent(t, moved)
	if _, err := os.Lstat(filepath.Join(moved, manifestPath)); !os.IsNotExist(err) {
		t.Fatalf("root replacement failure published into anchored root: %v", err)
	}
}

func TestTransactionStagesBeforePublishingAndLeavesNoFinalFilesOnStageFailure(t *testing.T) {
	project := writeGoProject(t)
	hooks := defaultTransactionHooks()
	hooks.apply.fileSync = func(*os.File) error { return errInjectedApply }
	if _, err := onboardWithTransactionHooks(validOptions(project), hooks); err == nil {
		t.Fatal("onboarding unexpectedly survived staged sync failure")
	}
	for _, relative := range []string{manifestPath, workflowPath, tapPath, mainRulesetPath, tagRulesetPath, setupPath, defaultAdapterPath} {
		if _, err := os.Lstat(filepath.Join(project, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("staging failure published %s: %v", relative, err)
		}
	}
	assertTransactionInternalsAbsent(t, project)
}

func TestTransactionReportsRetainedPrefixAndPreservesCompetingPublication(t *testing.T) {
	project := writeGoProject(t)
	hooks := defaultTransactionHooks()
	const competitor = "competitor\n"
	var competitorPath string
	hooks.beforePublish = func(root *os.Root, path string, index int) error {
		if index != 1 {
			return nil
		}
		competitorPath = path
		file, err := root.OpenFile(filepath.FromSlash(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.Write([]byte(competitor)); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}
	_, err := onboardWithTransactionHooks(validOptions(project), hooks)
	if err == nil || !strings.Contains(err.Error(), "retained prefix") {
		t.Fatalf("publication race error = %v", err)
	}
	if competitorPath == "" {
		t.Fatal("competitor hook did not run")
	}
	data, readErr := os.ReadFile(filepath.Join(project, filepath.FromSlash(competitorPath)))
	if readErr != nil || string(data) != competitor {
		t.Fatalf("competitor publication was deleted: %v, %q", readErr, data)
	}
	if _, err := os.Lstat(filepath.Join(project, filepath.FromSlash(workflowPath))); err != nil {
		t.Fatalf("first lexically published file was not retained: %v", err)
	}
	assertTransactionInternalsAbsent(t, project)
}

func TestTransactionJoinsCleanupFailure(t *testing.T) {
	project := writeGoProject(t)
	hooks := defaultTransactionHooks()
	hooks.cleanupFailure = func() error { return errors.New("injected cleanup failure") }
	if _, err := onboardWithTransactionHooks(validOptions(project), hooks); err == nil || !strings.Contains(err.Error(), "injected cleanup failure") {
		t.Fatalf("cleanup error was not reported: %v", err)
	}
	assertTransactionInternalsAbsent(t, project)
}

func TestTransactionLockIsExclusiveAndCreateOnly(t *testing.T) {
	project := writeGoProject(t)
	lock := filepath.Join(project, onboardLockPath)
	if err := os.WriteFile(lock, []byte("competitor\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Onboard(validOptions(project)); err == nil {
		t.Fatal("onboarding accepted an existing lock")
	}
	data, err := os.ReadFile(lock)
	if err != nil || string(data) != "competitor\n" {
		t.Fatalf("existing lock was changed: %v, %q", err, data)
	}
}
