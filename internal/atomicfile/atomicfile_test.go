package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type faultTemporaryFile struct {
	*os.File
	writeFault func([]byte) (int, error)
	syncFault  func() error
	closeFault func() error
}

func (file *faultTemporaryFile) Write(data []byte) (int, error) {
	if file.writeFault != nil {
		return file.writeFault(data)
	}
	return file.File.Write(data)
}

func (file *faultTemporaryFile) Sync() error {
	if file.syncFault != nil {
		return file.syncFault()
	}
	return file.File.Sync()
}

func (file *faultTemporaryFile) Close() error {
	if file.closeFault != nil {
		return file.closeFault()
	}
	return file.File.Close()
}

func TestWritePreRenameFailuresPreserveOriginal(t *testing.T) {
	tests := map[string]func(*faultTemporaryFile){
		"partial write": func(file *faultTemporaryFile) {
			file.writeFault = func(data []byte) (int, error) {
				written, err := file.File.Write(data[:len(data)/2])
				if err != nil {
					return written, err
				}
				return written, errors.New("injected partial write failure")
			}
		},
		"short write": func(file *faultTemporaryFile) {
			file.writeFault = func(data []byte) (int, error) {
				return file.File.Write(data[:len(data)/2])
			}
		},
		"sync": func(file *faultTemporaryFile) {
			file.syncFault = func() error {
				return errors.New("injected temporary sync failure")
			}
		},
		"close": func(file *faultTemporaryFile) {
			file.closeFault = func() error {
				if err := file.File.Close(); err != nil {
					return err
				}
				return errors.New("injected temporary close failure")
			}
		},
	}
	for name, inject := range tests {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			destination := filepath.Join(directory, "github-output")
			original := []byte("existing=value\n")
			replacement := []byte("existing=value\ntag=v1.2.3\n")
			if err := os.WriteFile(destination, original, 0o640); err != nil {
				t.Fatal(err)
			}

			createTemp := func(directory, pattern string) (temporaryFile, error) {
				created, err := os.CreateTemp(directory, pattern)
				if err != nil {
					return nil, err
				}
				wrapped := &faultTemporaryFile{File: created}
				inject(wrapped)
				return wrapped, nil
			}
			renameCalled := false
			err := writeWithTemporary(destination, replacement, fs.FileMode(0o640), createTemp, func(string, string) error {
				renameCalled = true
				return nil
			}, func(string) error {
				t.Fatal("directory sync called before a successful rename")
				return nil
			})
			if err == nil {
				t.Fatal("writeWithTemporary() unexpectedly succeeded")
			}
			if renameCalled {
				t.Fatal("rename ran after a pre-rename temporary-file failure")
			}
			got, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(original) {
				t.Fatalf("destination changed to %q", got)
			}
			info, statErr := os.Stat(destination)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if gotMode := info.Mode().Perm(); gotMode != 0o640 {
				t.Fatalf("destination mode = %#o, want 0640", gotMode)
			}
			temporaryFiles, globErr := filepath.Glob(filepath.Join(directory, ".github-output.tmp-*"))
			if globErr != nil {
				t.Fatal(globErr)
			}
			if len(temporaryFiles) != 0 {
				t.Fatalf("temporary files remain: %v", temporaryFiles)
			}
		})
	}
}

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

	syncCalled := false
	err := write(destination, replacement, 0o600, rename, func(string) error {
		syncCalled = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("write() error = %v, want injected rename failure", err)
	}
	if !renameObservedWrittenTemporaryFile {
		t.Fatal("rename injection was not reached after the temporary write")
	}
	if syncCalled {
		t.Fatal("directory sync ran even though rename failed")
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

func TestWriteDirectorySyncFailureReturnsErrorAfterReplacement(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "Formula.rb")
	original := []byte("original contents\n")
	replacement := []byte("replacement contents\n")
	if err := os.WriteFile(destination, original, 0o640); err != nil {
		t.Fatalf("WriteFile(original): %v", err)
	}

	syncCalls := 0
	err := write(destination, replacement, 0o600, os.Rename, func(path string) error {
		syncCalls++
		if path != directory {
			t.Fatalf("sync directory = %q, want %q", path, directory)
		}
		return errors.New("injected directory sync failure")
	})
	if err == nil || !strings.Contains(err.Error(), "replacement may already be visible") || !strings.Contains(err.Error(), "injected directory sync failure") {
		t.Fatalf("write() error = %v, want post-rename durability error", err)
	}
	if syncCalls != 1 {
		t.Fatalf("directory sync calls = %d, want 1", syncCalls)
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatalf("ReadFile(destination): %v", readErr)
	}
	if string(got) != string(replacement) {
		t.Fatalf("replacement is not visible after rename: %q", got)
	}
	info, statErr := os.Stat(destination)
	if statErr != nil {
		t.Fatalf("Stat(destination): %v", statErr)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("replacement mode = %#o, want 0600", gotMode)
	}
	temporaryFiles, globErr := filepath.Glob(filepath.Join(directory, ".Formula.rb.tmp-*"))
	if globErr != nil {
		t.Fatalf("Glob(temporary): %v", globErr)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files remain after post-rename sync failure: %v", temporaryFiles)
	}
}
