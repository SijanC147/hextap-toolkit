package release

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunProfileXcodeQualityNeedsNoBun reproduces a-bar release run
// 36321016505: the quality phase of a schema 3 manifest must run its commands
// without looking for bun (SB23-3101 follow-up).
func TestRunProfileXcodeQualityNeedsNoBun(t *testing.T) {
	source, manifestPath := writeXcodeBuildFixture(t, xcodeAdapter)
	script := filepath.Join(source, "scripts", "check-test-membership.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'membership ok\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := RunProfile(ProfileOptions{ManifestPath: manifestPath, SourceDir: source, Phase: ProfileQuality, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatalf("RunProfile() error = %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "bun-version") || !strings.Contains(stdout.String(), "CHECK test-membership") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if err := RunProfile(ProfileOptions{ManifestPath: manifestPath, SourceDir: source, Phase: ProfileBuild}); err == nil {
		t.Fatal("RunProfile accepted the build phase for an Xcode profile")
	}
}
