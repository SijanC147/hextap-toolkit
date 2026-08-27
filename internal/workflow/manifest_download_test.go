package workflow_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyValidatedManifestScript(t *testing.T) {
	script := filepath.Join(repositoryRoot(t), "scripts", "verify-validated-manifest.sh")
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "project-manifest.json")
	manifest := []byte("validated manifest bytes\n")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(manifest))

	command := exec.Command("bash", script, manifestPath, digest)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("verify valid manifest: %v\n%s", err, output)
	}

	symlinkPath := filepath.Join(directory, "manifest-link.json")
	if err := os.Symlink(manifestPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	hashFailureBin := t.TempDir()
	writeFile(t, filepath.Join(hashFailureBin, "sha256sum"), "#!/bin/sh\nexit 1\n", 0o700)
	tests := map[string]struct {
		arguments []string
		path      string
		want      string
	}{
		"missing arguments": {want: "expected manifest path and SHA-256 arguments"},
		"symlink":           {arguments: []string{symlinkPath, digest}, want: "downloaded manifest must not be a symlink"},
		"missing":           {arguments: []string{filepath.Join(directory, "missing.json"), digest}, want: "downloaded manifest is missing"},
		"not regular":       {arguments: []string{directory, digest}, want: "downloaded manifest is not a regular file"},
		"malformed digest":  {arguments: []string{manifestPath, "not-a-digest"}, want: "expected manifest SHA-256 is malformed"},
		"digest mismatch":   {arguments: []string{manifestPath, strings.Repeat("0", 64)}, want: "downloaded manifest SHA-256 does not match"},
		"hash failure":      {arguments: []string{manifestPath, digest}, path: hashFailureBin + string(os.PathListSeparator) + os.Getenv("PATH"), want: "downloaded manifest could not be hashed"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("bash", append([]string{script}, test.arguments...)...)
			if test.path != "" {
				command.Env = append(os.Environ(), "PATH="+test.path)
			}
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("verify manifest unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(string(output), "::error title=Validated manifest::"+test.want) {
				t.Fatalf("diagnostic = %q, want %q", output, test.want)
			}
		})
	}
}
