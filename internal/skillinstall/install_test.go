package skillinstall

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	toolskills "github.com/SijanC147/hextap-toolkit/skills"
)

func TestTargetsMatchReviewedPortableRegistry(t *testing.T) {
	targets := Targets()
	want := []Target{
		{ID: "agents", UserSkillsDir: ".agents/skills", ProjectSkillsDir: ".agents/skills"},
		{ID: "all", Virtual: true},
		{ID: "claude-code", UserSkillsDir: ".claude/skills", ProjectSkillsDir: ".claude/skills"},
		{ID: "codex", UserSkillsDir: ".agents/skills", ProjectSkillsDir: ".agents/skills"},
		{ID: "cursor", UserSkillsDir: ".cursor/skills", ProjectSkillsDir: ".cursor/skills"},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("Targets() = %#v, want %#v", targets, want)
	}
	copyOfTargets := Targets()
	copyOfTargets[0].ID = "mutated"
	if Targets()[0].ID == "mutated" {
		t.Fatal("Targets exposed mutable registry storage")
	}
	for _, target := range targets {
		if target.Virtual {
			if target.UserSkillsDir != "" || target.ProjectSkillsDir != "" {
				t.Errorf("virtual target %q has physical paths", target.ID)
			}
			continue
		}
		for scope, directory := range map[string]string{"user": target.UserSkillsDir, "project": target.ProjectSkillsDir} {
			clean := filepath.Clean(directory)
			if directory == "" || filepath.IsAbs(directory) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				t.Errorf("target %q %s path %q is unsafe", target.ID, scope, directory)
			}
		}
	}
}

func TestInstallUserScopeCreatesBundleAndOwnershipMarker(t *testing.T) {
	home := t.TempDir()
	result, err := Install(Options{
		Agents:  []string{"claude-code", "claude-code"},
		Scope:   UserScope,
		HomeDir: home,
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	wantEntries := expectedEntries(t, home, UserScope, []string{"claude-code"}, CreateAction)
	if !reflect.DeepEqual(result.Entries, wantEntries) {
		t.Fatalf("entries = %#v, want %#v", result.Entries, wantEntries)
	}
	assertCurrentInstall(t, home, UserScope, "claude-code")
	for _, unselected := range []string{".agents", ".cursor"} {
		if _, statErr := os.Lstat(filepath.Join(home, unselected)); !os.IsNotExist(statErr) {
			t.Fatalf("unselected target %q was touched: %v", unselected, statErr)
		}
	}
}

func TestInstallProjectScopedCodexUsesAgentsConventionAndNeverTouchesHome(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	result, err := Install(Options{
		Agents:     []string{"codex"},
		Scope:      ProjectScope,
		HomeDir:    home,
		ProjectDir: project,
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	wantEntries := expectedEntries(t, project, ProjectScope, []string{"codex"}, CreateAction)
	if !reflect.DeepEqual(result.Entries, wantEntries) {
		t.Fatalf("entries = %#v, want %#v", result.Entries, wantEntries)
	}
	assertCurrentInstall(t, project, ProjectScope, "codex")
	if _, err := os.Stat(filepath.Join(project, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("Codex install created obsolete .codex path: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("project install touched temporary home: entries=%v error=%v", entries, err)
	}
}

func TestInstallDryRunReportsCompletePlanWithoutWrites(t *testing.T) {
	home := t.TempDir()
	result, err := Install(Options{Agents: []string{"cursor"}, Scope: UserScope, HomeDir: home, DryRun: true})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	wantEntries := expectedEntries(t, home, UserScope, []string{"cursor"}, CreateAction)
	if !reflect.DeepEqual(result.Entries, wantEntries) {
		t.Fatalf("entries = %#v, want %#v", result.Entries, wantEntries)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("dry run touched temporary home: entries=%v error=%v", entries, err)
	}
}

func TestSupportFilesPublishBeforeSkillAndMarkerOnLateCollision(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	wantPublished := filepath.Join(skillDir, "references", "onboarding-and-validation.md")
	wantCollision := filepath.Join(skillDir, "references", "release-and-recovery.md")
	wantSupport := fileDataByName(t, "references/onboarding-and-validation.md")
	var attempted []string
	_, err := install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}, applyControl{
		beforePublish: func(_ int, entry Entry) {
			attempted = append(attempted, entry.Path)
			if entry.Path == wantCollision {
				if writeErr := os.WriteFile(entry.Path, []byte("late conflict\n"), 0o600); writeErr != nil {
					t.Fatalf("create late conflict: %v", writeErr)
				}
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "after 1 of") {
		t.Fatalf("Install(late conflict) error = %v", err)
	}
	var partial *PartialInstallError
	if !errors.As(err, &partial) {
		t.Fatalf("Install(late conflict) error = %T %v, want PartialInstallError", err, err)
	}
	if !reflect.DeepEqual(attempted, []string{wantPublished, wantCollision}) {
		t.Fatalf("publication order = %v, want support files before SKILL.md", attempted)
	}
	if !reflect.DeepEqual(partial.Published, []string{wantPublished}) {
		t.Fatalf("partial paths = %v, want %v", partial.Published, []string{wantPublished})
	}
	if !reflect.DeepEqual(partial.Claimed, []string{skillDir}) {
		t.Fatalf("claimed paths = %v, want %v", partial.Claimed, []string{skillDir})
	}
	gotSupport, readErr := os.ReadFile(wantPublished)
	if readErr != nil || !bytes.Equal(gotSupport, wantSupport) {
		t.Fatalf("published support file = %q, error=%v, want canonical embedded bytes", gotSupport, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(skillDir, "SKILL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("failed installation published SKILL.md: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(skillDir, markerFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("failed installation published ownership marker: %v", statErr)
	}
}

func TestDirectoryClaimRefusesConcurrentTargetAndPreservesContent(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	localPath := filepath.Join(skillDir, "LOCAL-NOTES.md")
	claimCalls := 0

	_, err := install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}, applyControl{
		beforeClaim: func(index int, agent, path string) {
			claimCalls++
			if index != 0 || agent != "claude-code" || path != skillDir {
				t.Fatalf("claim hook = (%d, %q, %q)", index, agent, path)
			}
			if mkdirErr := os.Mkdir(path, 0o755); mkdirErr != nil {
				t.Fatalf("concurrent mkdir: %v", mkdirErr)
			}
			if writeErr := os.WriteFile(localPath, []byte("concurrent owner\n"), 0o600); writeErr != nil {
				t.Fatalf("concurrent write: %v", writeErr)
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "claim") {
		t.Fatalf("Install(concurrent directory) error = %v", err)
	}
	if claimCalls != 1 {
		t.Fatalf("claim hook calls = %d, want 1", claimCalls)
	}
	data, readErr := os.ReadFile(localPath)
	if readErr != nil || string(data) != "concurrent owner\n" {
		t.Fatalf("concurrent content changed: data=%q error=%v", data, readErr)
	}
	for _, absent := range []string{"SKILL.md", markerFileName} {
		if _, statErr := os.Lstat(filepath.Join(skillDir, absent)); !os.IsNotExist(statErr) {
			t.Fatalf("failed claim published %s: %v", absent, statErr)
		}
	}
}

func TestSecondDirectoryClaimFailureReportsFirstClaimAndPreservesConcurrentContent(t *testing.T) {
	home := t.TempDir()
	agentsDir := filepath.Join(home, ".agents", "skills", "hextap")
	claudeDir := filepath.Join(home, ".claude", "skills", "hextap")
	concurrentPath := filepath.Join(claudeDir, "LOCAL-NOTES.md")
	const concurrentContent = "second target belongs to another actor\n"

	_, err := install(Options{
		Agents:                    []string{"all"},
		Scope:                     UserScope,
		HomeDir:                   home,
		AllowOverlappingDiscovery: true,
	}, applyControl{
		beforeClaim: func(index int, _, path string) {
			if index != 1 {
				return
			}
			if path != claudeDir {
				t.Fatalf("second claim path = %q, want %q", path, claudeDir)
			}
			if mkdirErr := os.Mkdir(path, 0o755); mkdirErr != nil {
				t.Fatalf("concurrent mkdir: %v", mkdirErr)
			}
			if writeErr := os.WriteFile(concurrentPath, []byte(concurrentContent), 0o600); writeErr != nil {
				t.Fatalf("concurrent write: %v", writeErr)
			}
		},
	})
	var partial *PartialInstallError
	if !errors.As(err, &partial) {
		t.Fatalf("Install(second claim race) error = %T %v, want PartialInstallError", err, err)
	}
	if !reflect.DeepEqual(partial.Claimed, []string{agentsDir}) || len(partial.Published) != 0 {
		t.Fatalf("partial state = claimed %v, published %v", partial.Claimed, partial.Published)
	}
	if strings.Contains(err.Error(), concurrentContent) {
		t.Fatalf("partial error leaked concurrent content: %q", err)
	}
	data, readErr := os.ReadFile(concurrentPath)
	if readErr != nil || string(data) != concurrentContent {
		t.Fatalf("concurrent content changed: data=%q error=%v", data, readErr)
	}
	for _, directory := range []string{agentsDir, claudeDir} {
		for _, absent := range []string{"SKILL.md", markerFileName} {
			if _, statErr := os.Lstat(filepath.Join(directory, absent)); !os.IsNotExist(statErr) {
				t.Fatalf("failed second claim published %s below %s: %v", absent, directory, statErr)
			}
		}
	}
}

func TestPostClaimPreLinkFailureReportsClaimAndPreservesConcurrentContent(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	concurrentPath := filepath.Join(skillDir, "references")
	const concurrentContent = "support path claimed concurrently\n"

	_, err := install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}, applyControl{
		beforeStage: func(index int, _ Entry) {
			if index != 0 {
				return
			}
			if writeErr := os.WriteFile(concurrentPath, []byte(concurrentContent), 0o600); writeErr != nil {
				t.Fatalf("concurrent support write: %v", writeErr)
			}
		},
	})
	var partial *PartialInstallError
	if !errors.As(err, &partial) {
		t.Fatalf("Install(post-claim failure) error = %T %v, want PartialInstallError", err, err)
	}
	if !reflect.DeepEqual(partial.Claimed, []string{skillDir}) || len(partial.Published) != 0 {
		t.Fatalf("partial state = claimed %v, published %v", partial.Claimed, partial.Published)
	}
	if strings.Contains(err.Error(), concurrentContent) {
		t.Fatalf("partial error leaked concurrent content: %q", err)
	}
	data, readErr := os.ReadFile(concurrentPath)
	if readErr != nil || string(data) != concurrentContent {
		t.Fatalf("concurrent content changed: data=%q error=%v", data, readErr)
	}
	for _, absent := range []string{"SKILL.md", markerFileName} {
		if _, statErr := os.Lstat(filepath.Join(skillDir, absent)); !os.IsNotExist(statErr) {
			t.Fatalf("post-claim failure published %s: %v", absent, statErr)
		}
	}
}

func TestPartialPublicationPreservesConcurrentEditsAndReportsExactPath(t *testing.T) {
	home := t.TempDir()
	var firstPath string
	var conflictPath string
	_, err := install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}, applyControl{
		beforePublish: func(index int, entry Entry) {
			switch index {
			case 0:
				firstPath = entry.Path
			case 1:
				conflictPath = entry.Path
				if writeErr := os.WriteFile(firstPath, []byte("concurrent local edit\n"), 0o644); writeErr != nil {
					t.Fatalf("edit first published file: %v", writeErr)
				}
				if writeErr := os.WriteFile(entry.Path, []byte("late conflict\n"), 0o600); writeErr != nil {
					t.Fatalf("create late conflict: %v", writeErr)
				}
			}
		},
	})
	var partial *PartialInstallError
	if !errors.As(err, &partial) {
		t.Fatalf("Install(concurrent edit) error = %T %v, want PartialInstallError", err, err)
	}
	if !reflect.DeepEqual(partial.Published, []string{firstPath}) {
		t.Fatalf("partial paths = %v, want %v", partial.Published, []string{firstPath})
	}
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	if !reflect.DeepEqual(partial.Claimed, []string{skillDir}) {
		t.Fatalf("claimed paths = %v, want %v", partial.Claimed, []string{skillDir})
	}
	data, readErr := os.ReadFile(firstPath)
	if readErr != nil || string(data) != "concurrent local edit\n" {
		t.Fatalf("concurrent edit was not preserved: data=%q error=%v", data, readErr)
	}
	conflictData, conflictErr := os.ReadFile(conflictPath)
	if conflictErr != nil || string(conflictData) != "late conflict\n" {
		t.Fatalf("concurrent conflict was not preserved: data=%q error=%v", conflictData, conflictErr)
	}
	if _, statErr := os.Lstat(filepath.Join(home, target.UserSkillsDir, "hextap", markerFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("failed installation published ownership marker: %v", statErr)
	}
}

func TestAllRequiresOverlapAcknowledgementAndOmitsCursorNativeCopy(t *testing.T) {
	home := t.TempDir()
	_, err := Install(Options{Agents: []string{"all"}, Scope: UserScope, HomeDir: home})
	if err == nil || !strings.Contains(err.Error(), "--allow-overlapping-discovery") {
		t.Fatalf("Install(all) error = %v", err)
	}
	if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
		t.Fatalf("rejected overlap touched home: entries=%v error=%v", entries, readErr)
	}

	result, err := Install(Options{
		Agents:                    []string{"all"},
		Scope:                     UserScope,
		HomeDir:                   home,
		AllowOverlappingDiscovery: true,
	})
	if err != nil {
		t.Fatalf("Install(all acknowledged) error = %v", err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("Install(all acknowledged) returned no entries")
	}
	assertCurrentInstall(t, home, UserScope, "agents")
	assertCurrentInstall(t, home, UserScope, "claude-code")
	if _, statErr := os.Lstat(filepath.Join(home, ".cursor")); !os.IsNotExist(statErr) {
		t.Fatalf("all install created redundant Cursor-native copy: %v", statErr)
	}
}

func TestUnmanagedConflictRefusesEveryWriteAndDoesNotLeak(t *testing.T) {
	home := t.TempDir()
	codex := targetByIDForTest(t, "codex")
	conflictPath := filepath.Join(home, codex.UserSkillsDir, "hextap", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "github_pat_conflict_should_never_be_echoed_1234567890"
	if err := os.WriteFile(conflictPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Install(Options{
		Agents:                    []string{"claude-code", "codex"},
		Scope:                     UserScope,
		HomeDir:                   home,
		AllowOverlappingDiscovery: true,
	})
	if err == nil || !strings.Contains(err.Error(), "unmanaged") || strings.Contains(err.Error(), secret) {
		t.Fatalf("Install() conflict error = %q", err)
	}
	if _, statErr := os.Lstat(filepath.Join(home, ".claude")); !os.IsNotExist(statErr) {
		t.Fatalf("conflict preflight touched another target: %v", statErr)
	}
	got, readErr := os.ReadFile(conflictPath)
	if readErr != nil || string(got) != secret {
		t.Fatalf("conflict changed unmanaged file: data=%q error=%v", got, readErr)
	}
}

func TestUnmarkedSkillDirectoryIsAlwaysUnmanaged(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(skillDir, "LOCAL-NOTES.md")
	if err := os.WriteFile(notes, []byte("user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("Install(unmarked directory) error = %v", err)
	}
	data, readErr := os.ReadFile(notes)
	if readErr != nil || string(data) != "user-owned\n" {
		t.Fatalf("unmarked directory changed: data=%q error=%v", data, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(skillDir, markerFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("unmarked directory gained ownership marker: %v", statErr)
	}
}

func TestDifferentManagedBundleIsReportedAndNeverMutated(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	oldFiles := []bundleFile{{name: "SKILL.md", data: []byte("old managed skill\n")}}
	writeManagedFixture(t, skillDir, oldFiles)
	extra := filepath.Join(skillDir, "LOCAL-NOTES.md")
	if err := os.WriteFile(extra, []byte("preserve me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := Status(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err != nil || len(status.Entries) != 1 || status.Entries[0].State != DifferentState {
		t.Fatalf("Status(different bundle) = %#v, %v", status, err)
	}
	if _, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}); err == nil || !strings.Contains(err.Error(), "different managed") {
		t.Fatalf("Install(different bundle) error = %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if readErr != nil || string(data) != "old managed skill\n" {
		t.Fatalf("different bundle changed: data=%q error=%v", data, readErr)
	}
	if got, err := os.ReadFile(extra); err != nil || string(got) != "preserve me\n" {
		t.Fatalf("different bundle changed unbundled file: data=%q error=%v", got, err)
	}
}

func TestCurrentInstallIsUnchangedWithoutMetadataMutation(t *testing.T) {
	home := t.TempDir()
	if _, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	target := targetByIDForTest(t, "claude-code")
	destination := filepath.Join(home, target.UserSkillsDir, "hextap", "SKILL.md")
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(destination, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	result, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range result.Entries {
		if entry.Action != UnchangedAction {
			t.Fatalf("entry = %#v, want UNCHANGED", entry)
		}
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("unchanged file modtime = %s, want %s", info.ModTime(), oldTime)
	}
}

func TestInstallRefusesSymlinkedTargetParents(t *testing.T) {
	home := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	_, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Install() symlink error = %v", err)
	}
	entries, readErr := os.ReadDir(external)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("installer wrote through symlink: entries=%v error=%v", entries, readErr)
	}
}

func TestStatusDistinguishesAbsentCurrentDifferentDriftedAndUnmanaged(t *testing.T) {
	stateFor := func(t *testing.T, prepare func(home, skillDir string)) State {
		t.Helper()
		home := t.TempDir()
		target := targetByIDForTest(t, "claude-code")
		skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
		if prepare != nil {
			prepare(home, skillDir)
		}
		result, err := Status(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if len(result.Entries) != 1 {
			t.Fatalf("status entries = %#v", result.Entries)
		}
		return result.Entries[0].State
	}

	if got := stateFor(t, nil); got != NotInstalledState {
		t.Errorf("absent state = %s", got)
	}
	if got := stateFor(t, func(home, _ string) {
		if _, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}); err != nil {
			t.Fatal(err)
		}
	}); got != CurrentState {
		t.Errorf("current state = %s", got)
	}
	if got := stateFor(t, func(_ string, skillDir string) {
		writeManagedFixture(t, skillDir, []bundleFile{{name: "SKILL.md", data: []byte("old managed skill\n")}})
	}); got != DifferentState {
		t.Errorf("different state = %s", got)
	}
	if got := stateFor(t, func(home, skillDir string) {
		if _, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("locally edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}); got != DriftedState {
		t.Errorf("drifted state = %s", got)
	}
	if got := stateFor(t, func(_ string, skillDir string) {
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("unmanaged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}); got != UnmanagedState {
		t.Errorf("unmanaged state = %s", got)
	}
}

func TestStatusIsReadOnlyAndReportsInvalidMarker(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(skillDir, markerFileName)
	if err := os.WriteFile(markerPath, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Status(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].State != InvalidState {
		t.Fatalf("status = %#v, want INVALID", result.Entries)
	}
	after, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Fatal("status mutated invalid marker")
	}
}

func TestInstallRejectsInvalidOptionsBeforeOpeningRoots(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		want    string
	}{
		{name: "agents required", options: Options{Scope: UserScope, HomeDir: "/not/opened"}, want: "at least one --agent"},
		{name: "unknown agent", options: Options{Agents: []string{"unknown"}, Scope: UserScope, HomeDir: "/not/opened"}, want: "unknown agent"},
		{name: "all must stand alone", options: Options{Agents: []string{"all", "codex"}, Scope: UserScope, HomeDir: "/not/opened", AllowOverlappingDiscovery: true}, want: "used alone"},
		{name: "invalid scope", options: Options{Agents: []string{"codex"}, Scope: "system", HomeDir: "/not/opened"}, want: "scope"},
		{name: "missing scope", options: Options{Agents: []string{"codex"}, HomeDir: "/not/opened"}, want: "scope"},
		{name: "missing home", options: Options{Agents: []string{"codex"}, Scope: UserScope}, want: "home"},
		{name: "missing project", options: Options{Agents: []string{"codex"}, Scope: ProjectScope}, want: "project"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Install(test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Install() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func bundledFiles(t *testing.T) []bundleFile {
	t.Helper()
	bundle := toolskills.Hextap()
	var files []bundleFile
	err := fs.WalkDir(bundle.Files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(bundle.Files, name)
		if err != nil {
			return err
		}
		files = append(files, bundleFile{name: name, data: data})
		return nil
	})
	if err != nil {
		t.Fatalf("walk Hextap bundle: %v", err)
	}
	if len(files) == 0 || files[0].name != "SKILL.md" {
		t.Fatalf("Hextap bundle files = %v, want SKILL.md", files)
	}
	sort.Slice(files, func(i, j int) bool { return publicationPathLess(files[i].name, files[j].name) })
	return files
}

func fileDataByName(t *testing.T, name string) []byte {
	t.Helper()
	for _, file := range bundledFiles(t) {
		if file.name == name {
			return file.data
		}
	}
	t.Fatalf("embedded bundle is missing %q", name)
	return nil
}

func expectedEntries(t *testing.T, root string, scope Scope, agents []string, action Action) []Entry {
	t.Helper()
	files := bundledFiles(t)
	marker, err := encodeMarker("hextap", toolskills.Hextap().Version, files)
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, bundleFile{name: markerFileName, data: marker})
	var result []Entry
	for _, agent := range agents {
		target := targetByIDForTest(t, agent)
		directory := target.UserSkillsDir
		if scope == ProjectScope {
			directory = target.ProjectSkillsDir
		}
		for _, file := range files {
			result = append(result, Entry{
				Action: action,
				Agent:  agent,
				Path:   filepath.Join(root, directory, "hextap", filepath.FromSlash(file.name)),
				Mode:   0o644,
				Size:   len(file.data),
			})
		}
	}
	return result
}

func assertCurrentInstall(t *testing.T, root string, scope Scope, agent string) {
	t.Helper()
	target := targetByIDForTest(t, agent)
	directory := target.UserSkillsDir
	if scope == ProjectScope {
		directory = target.ProjectSkillsDir
	}
	skillDir := filepath.Join(root, directory, "hextap")
	for _, file := range bundledFiles(t) {
		path := filepath.Join(skillDir, filepath.FromSlash(file.name))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, file.data) {
			t.Fatalf("%s differs from embedded bundle", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != fs.FileMode(0o644) {
			t.Fatalf("%s mode = %v", path, info.Mode())
		}
	}
	markerData, err := os.ReadFile(filepath.Join(skillDir, markerFileName))
	if err != nil {
		t.Fatal(err)
	}
	marker, err := decodeMarker(markerData)
	if err != nil {
		t.Fatalf("decode installed marker: %v", err)
	}
	want, err := markerForBundle("hextap", toolskills.Hextap().Version, bundledFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(marker, want) {
		t.Fatalf("marker = %#v, want %#v", marker, want)
	}
}

func writeManagedFixture(t *testing.T, skillDir string, files []bundleFile) {
	t.Helper()
	for _, file := range files {
		path := filepath.Join(skillDir, filepath.FromSlash(file.name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	marker, err := encodeMarker("hextap", "0.9.0", files)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, markerFileName), marker, 0o644); err != nil {
		t.Fatal(err)
	}
}

func targetByIDForTest(t *testing.T, id string) Target {
	t.Helper()
	for _, target := range Targets() {
		if target.ID == id {
			return target
		}
	}
	t.Fatalf("target %q is not registered", id)
	return Target{}
}
