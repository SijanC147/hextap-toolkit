package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRenameFailurePreservesOriginalAndRemovesTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "Formula.rb")
	original := []byte("original contents\n")
	replacement := []byte("replacement contents\n")
	if err := os.WriteFile(destination, original, 0o640); err != nil {
		t.Fatalf("WriteFile(original): %v", err)
	}

	renameObservedWrittenTemporaryFile := false
	rename := func(oldPath, newPath string) error {
		if newPath != destination {
			t.Fatalf("rename destination = %q, want %q", newPath, destination)
		}
		got, err := os.ReadFile(oldPath)
		if err != nil {
			t.Fatalf("ReadFile(temporary): %v", err)
		}
		if string(got) != string(replacement) {
			t.Fatalf("temporary contents = %q, want %q", got, replacement)
		}
		renameObservedWrittenTemporaryFile = true
		return errors.New("injected rename failure")
	}

	err := write(destination, replacement, 0o600, rename)
	if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("write() error = %v, want injected rename failure", err)
	}
	if !renameObservedWrittenTemporaryFile {
		t.Fatal("rename injection was not reached after the temporary write")
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatalf("ReadFile(destination): %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("destination changed after rename failure: %q", got)
	}
	temporaryFiles, globErr := filepath.Glob(filepath.Join(directory, ".Formula.rb.tmp-*"))
	if globErr != nil {
		t.Fatalf("Glob(temporary): %v", globErr)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files remain after failure: %v", temporaryFiles)
	}
	info, statErr := os.Stat(destination)
	if statErr != nil {
		t.Fatalf("Stat(destination): %v", statErr)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o640 {
		t.Fatalf("destination mode changed to %#o", gotMode)
	}
}
