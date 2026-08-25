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
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "existing=value\ntag=v1.2.3\nstable=true\n"; got != want {
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

func TestAppendWriterFailureLeavesOriginalUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-output")
	const sentinel = "existing=value\n"
	if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	err := appendWithWriter(path, []Field{{Key: "tag", Value: "v1.2.3"}}, func(*os.File, []byte) (int, error) {
		return 0, errors.New("injected atomic write failure")
	})
	if err == nil || !strings.Contains(err.Error(), "injected atomic write failure") {
		t.Fatalf("appendWithWriter() error = %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != sentinel {
		t.Fatalf("failed append changed output to %q", data)
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
