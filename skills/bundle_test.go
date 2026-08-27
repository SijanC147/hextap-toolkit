package skills

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

func TestHextapBundleContainsOnlySafeRelativeFiles(t *testing.T) {
	bundle := Hextap()
	if bundle.Name != "hextap" {
		t.Fatalf("bundle name = %q, want hextap", bundle.Name)
	}
	if bundle.Version != "1.1.1" {
		t.Fatalf("bundle version = %q, want 1.1.1", bundle.Version)
	}
	foundSkill := false
	err := fs.WalkDir(bundle.Files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || entry.IsDir() {
			return nil
		}
		if path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			t.Errorf("unsafe bundle path %q", name)
		}
		if name == "SKILL.md" {
			foundSkill = true
			data, err := fs.ReadFile(bundle.Files, name)
			if err != nil {
				return err
			}
			if len(data) == 0 {
				t.Error("SKILL.md is empty")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bundle: %v", err)
	}
	if !foundSkill {
		t.Fatal("bundle does not contain SKILL.md")
	}
}
