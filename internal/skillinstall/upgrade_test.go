package skillinstall

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	toolskills "github.com/SijanC147/hextap-toolkit/skills"
)

func TestUpgradeDryRunPlansOlderManagedBundleWithoutWrites(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	oldFiles := []bundleFile{{name: "SKILL.md", data: []byte("old managed skill\n")}}
	writeManagedFixtureVersion(t, skillDir, "0.9.0", oldFiles)
	before, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := Upgrade(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home, DryRun: true})
	if err != nil {
		t.Fatalf("Upgrade(dry-run) error = %v", err)
	}
	want := []UpgradeEntry{{Action: UpgradeAction, Agent: "claude-code", Path: skillDir, FromVersion: "0.9.0", ToVersion: toolskills.Hextap().Version}}
	if !reflect.DeepEqual(result.Entries, want) {
		t.Fatalf("Upgrade(dry-run) = %#v, want %#v", result.Entries, want)
	}
	after, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("dry-run changed installed skill: data=%q error=%v", after, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", ".hextap-transactions")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created transaction root: %v", err)
	}
}

func TestUpgradeReplacesIntactOlderBundleAndPreservesExactRecoveryDirectory(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	oldData := []byte("old managed skill\n")
	writeManagedFixtureVersion(t, skillDir, "0.9.0", []bundleFile{{name: "SKILL.md", data: oldData}})

	result, err := Upgrade(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("Upgrade() = %#v", result)
	}
	entry := result.Entries[0]
	if entry.Action != UpgradeAction || entry.FromVersion != "0.9.0" || entry.ToVersion != toolskills.Hextap().Version || entry.BackupPath == "" {
		t.Fatalf("upgrade entry = %#v", entry)
	}
	wantBackupPrefix := filepath.Join(home, ".claude", ".hextap-transactions", "backup-hextap-0.9.0-")
	if !strings.HasPrefix(entry.BackupPath, wantBackupPrefix) || strings.Contains(entry.BackupPath, filepath.Join("skills", "hextap")) {
		t.Fatalf("backup path = %q, want prefix %q outside discovery", entry.BackupPath, wantBackupPrefix)
	}
	backupData, err := os.ReadFile(filepath.Join(entry.BackupPath, "SKILL.md"))
	if err != nil || !bytes.Equal(backupData, oldData) {
		t.Fatalf("backup data = %q, %v", backupData, err)
	}
	assertCurrentInstall(t, home, UserScope, "claude-code")
	status, err := Status(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
	if err != nil || len(status.Entries) != 1 || status.Entries[0].State != CurrentState {
		t.Fatalf("post-upgrade status = %#v, %v", status, err)
	}
}

func TestUpgradeCurrentBundleIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := Install(Options{Agents: []string{"codex"}, Scope: UserScope, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	result, err := Upgrade(Options{Agents: []string{"codex"}, Scope: UserScope, HomeDir: home})
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Action != UnchangedAction || result.Entries[0].BackupPath != "" {
		t.Fatalf("Upgrade(current) = %#v, %v", result, err)
	}
}

func TestUpgradeFailsClosedForUnsafeStatesAndUntrackedContent(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, home, skillDir string)
		want    string
	}{
		{name: "not installed", want: "not installed"},
		{name: "unmanaged", prepare: func(t *testing.T, _ string, skillDir string) {
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("unmanaged\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "unmanaged"},
		{name: "newer", prepare: func(t *testing.T, _ string, skillDir string) {
			writeManagedFixtureVersion(t, skillDir, "9.0.0", []bundleFile{{name: "SKILL.md", data: []byte("newer\n")}})
		}, want: "newer"},
		{name: "same version different", prepare: func(t *testing.T, _ string, skillDir string) {
			writeManagedFixtureVersion(t, skillDir, toolskills.Hextap().Version, []bundleFile{{name: "SKILL.md", data: []byte("different\n")}})
		}, want: "different"},
		{name: "drifted", prepare: func(t *testing.T, _ string, skillDir string) {
			writeManagedFixtureVersion(t, skillDir, "0.9.0", []bundleFile{{name: "SKILL.md", data: []byte("old\n")}})
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("local edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "drifted"},
		{name: "untracked extra", prepare: func(t *testing.T, _ string, skillDir string) {
			writeManagedFixtureVersion(t, skillDir, "0.9.0", []bundleFile{{name: "SKILL.md", data: []byte("old\n")}})
			if err := os.WriteFile(filepath.Join(skillDir, "LOCAL.md"), []byte("preserve\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "untracked"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			target := targetByIDForTest(t, "claude-code")
			skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
			if test.prepare != nil {
				test.prepare(t, home, skillDir)
			}
			_, err := Upgrade(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("Upgrade() error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".claude", ".hextap-transactions")); !os.IsNotExist(statErr) {
				t.Fatalf("failed preflight created transaction root: %v", statErr)
			}
		})
	}
}

func TestUpgradePreflightsEveryTargetBeforeFirstWrite(t *testing.T) {
	home := t.TempDir()
	codex := targetByIDForTest(t, "codex")
	claude := targetByIDForTest(t, "claude-code")
	codexDir := filepath.Join(home, codex.UserSkillsDir, "hextap")
	writeManagedFixtureVersion(t, codexDir, "0.9.0", []bundleFile{{name: "SKILL.md", data: []byte("old\n")}})
	claudeDir := filepath.Join(home, claude.UserSkillsDir, "hextap")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "SKILL.md"), []byte("unmanaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Upgrade(Options{Agents: []string{"codex", "claude-code"}, Scope: UserScope, HomeDir: home, AllowOverlappingDiscovery: true})
	if err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("Upgrade(multi-target) error = %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(codexDir, "SKILL.md"))
	if readErr != nil || string(data) != "old\n" {
		t.Fatalf("multi-target preflight changed first target: data=%q error=%v", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".agents", ".hextap-transactions")); !os.IsNotExist(statErr) {
		t.Fatalf("multi-target preflight created transaction root: %v", statErr)
	}
}

func TestUpgradeRevalidatesOriginalBeforeSwapAndPreservesConcurrentEdit(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	writeManagedFixtureVersion(t, skillDir, "0.9.0", []bundleFile{{name: "SKILL.md", data: []byte("old\n")}})

	_, err := upgrade(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}, upgradeControl{
		beforeSwap: func(_ int, _ UpgradeEntry) {
			if writeErr := os.WriteFile(skillPath, []byte("concurrent edit\n"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "changed after preflight") {
		t.Fatalf("Upgrade(race) error = %v", err)
	}
	data, readErr := os.ReadFile(skillPath)
	if readErr != nil || string(data) != "concurrent edit\n" {
		t.Fatalf("concurrent edit not preserved: data=%q error=%v", data, readErr)
	}
	if matches, _ := filepath.Glob(filepath.Join(home, ".claude", ".hextap-transactions", "backup-*")); len(matches) != 0 {
		t.Fatalf("race created backup before revalidation: %v", matches)
	}
}

func TestUpgradeDetectsMutationCapturedDuringSourceRename(t *testing.T) {
	home := t.TempDir()
	target := targetByIDForTest(t, "claude-code")
	skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
	writeManagedFixtureVersion(t, skillDir, "0.9.0", []bundleFile{{name: "SKILL.md", data: []byte("old\n")}})
	var backupPath string
	_, err := upgrade(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}, upgradeControl{
		afterSourceRename: func(_ int, _ UpgradeEntry, backup string) {
			backupPath = backup
			if writeErr := os.WriteFile(filepath.Join(backup, "SKILL.md"), []byte("concurrent edit\n"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	var partial *PartialUpgradeError
	if !errors.As(err, &partial) || !strings.Contains(err.Error(), "changed during swap") {
		t.Fatalf("Upgrade(rename mutation) error = %T %v", err, err)
	}
	if backupPath == "" || !containsPath(partial.RecoveryPaths, backupPath) {
		t.Fatalf("recovery paths = %v, want backup %q", partial.RecoveryPaths, backupPath)
	}
	data, readErr := os.ReadFile(filepath.Join(backupPath, "SKILL.md"))
	if readErr != nil || string(data) != "concurrent edit\n" {
		t.Fatalf("concurrent backup edit not preserved: data=%q error=%v", data, readErr)
	}
	if _, statErr := os.Lstat(skillDir); !os.IsNotExist(statErr) {
		t.Fatalf("failed swap published discovery target: %v", statErr)
	}
}

func containsPath(paths []string, wanted string) bool {
	for _, path := range paths {
		if path == wanted {
			return true
		}
	}
	return false
}
