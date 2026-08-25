package manifest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsSymlinkAndOversizedManifest(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte(validManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(target); err != nil {
		t.Fatalf("Load(valid manifest without final newline) = %v", err)
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("Load(symlink) unexpectedly succeeded")
	}
	oversized := filepath.Join(directory, "oversized.json")
	file, err := os.OpenFile(oversized, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaximumSize + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(oversized); err == nil {
		t.Fatal("Load(oversized) unexpectedly succeeded")
	}
}

func TestLoadRejectsSameSizeInPlaceRewriteDuringRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	original := []byte(validManifest)
	replacement := bytes.Replace(original, []byte("Selective"), []byte("Different"), 1)
	if len(replacement) != len(original) {
		t.Fatal("test replacement changed size")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWithHook(path, func() {
		if writeErr := os.WriteFile(path, replacement, 0o600); writeErr != nil {
			t.Fatalf("rewrite manifest: %v", writeErr)
		}
	}); err == nil {
		t.Fatal("Load() accepted a same-size in-place rewrite")
	}
}
