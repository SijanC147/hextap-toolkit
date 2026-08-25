package githuboutput

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendPreservesExistingOutputAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(path, []byte("existing=value\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	err := Append(path, []Field{
		{Key: "tag", Value: "v1.2.3"},
		{Key: "stable", Value: "true"},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := Append(path, []Field{{Key: "mode", Value: "full"}}); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "existing=value\ntag=v1.2.3\nstable=true\nmode=full\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %#o, want 0640", got)
	}
}

func TestAppendRejectsInjectionWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-output")
	const sentinel = "existing=value\n"
	if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, field := range []Field{
		{Key: "bad-key", Value: "value"},
		{Key: "tag", Value: "v1.2.3\ninjected=true"},
		{Key: "tag", Value: "v1.2.3\rhidden=true"},
		{Key: "tag", Value: "v1.2.3\x00hidden"},
	} {
		if err := Append(path, []Field{field}); err == nil {
			t.Fatalf("Append(%#v) unexpectedly succeeded", field)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != sentinel {
			t.Fatalf("failed append changed output to %q", data)
		}
	}
}

func TestAppendReplacementFailuresLeaveOriginalUnchanged(t *testing.T) {
	for _, failure := range []string{
		"partial write",
		"short write",
		"temporary sync",
		"temporary close",
		"rename",
	} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "github-output")
			const sentinel = "existing=value\n"
			if err := os.WriteFile(path, []byte(sentinel), 0o640); err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected " + failure + " failure")
			err := appendWithReplace(path, []Field{{Key: "tag", Value: "v1.2.3"}}, func(target string, data []byte, mode os.FileMode) error {
				if target != path {
					t.Fatalf("replacement target = %q, want %q", target, path)
				}
				if got, want := string(data), sentinel+"tag=v1.2.3\n"; got != want {
					t.Fatalf("replacement data = %q, want %q", got, want)
				}
				if mode.Perm() != 0o640 {
					t.Fatalf("replacement mode = %#o, want 0640", mode.Perm())
				}
				return injected
			})
			if err == nil || !strings.Contains(err.Error(), injected.Error()) {
				t.Fatalf("appendWithReplace() error = %v", err)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(data) != sentinel {
				t.Fatalf("failed append changed output to %q", data)
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0o640 {
				t.Fatalf("original mode = %#o, want 0640", info.Mode().Perm())
			}
		})
	}
}

func TestAppendRejectsMissingAndUnterminatedOutput(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing")
	if err := Append(missing, []Field{{Key: "tag", Value: "v1.2.3"}}); err == nil {
		t.Fatal("Append(missing) unexpectedly succeeded")
	}
	unterminated := filepath.Join(directory, "unterminated")
	if err := os.WriteFile(unterminated, []byte("existing=value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(unterminated, []Field{{Key: "tag", Value: "v1.2.3"}}); err == nil {
		t.Fatal("Append(unterminated) unexpectedly succeeded")
	}
	data, err := os.ReadFile(unterminated)
	if err != nil || string(data) != "existing=value" {
		t.Fatalf("unterminated output = %q, error = %v", data, err)
	}
}

func TestAppendRejectsSymlinkAndDuplicateKeys(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "output")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := Append(symlink, []Field{{Key: "tag", Value: "v1.2.3"}}); err == nil {
		t.Fatal("Append(symlink) unexpectedly succeeded")
	}

	path := filepath.Join(directory, "regular")
	if err := Append(path, []Field{{Key: "tag", Value: "v1.2.3"}, {Key: "tag", Value: "v1.2.4"}}); err == nil {
		t.Fatal("Append(duplicate key) unexpectedly succeeded")
	}
}
