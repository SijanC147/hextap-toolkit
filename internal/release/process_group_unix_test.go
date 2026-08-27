//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteTimeoutKillsDescendantProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant-survived")
	script := []byte("#!/bin/sh\n(sleep 1; printf survived > \"" + marker + "\") &\nsleep 5\n")
	err := executeVerifiedBinaryWithTimeout("tool", "tool", "1.2.3", testCommit, "tool.tar.gz", script, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("execute timeout error = %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("descendant survived process-group termination: %v", err)
	}
}
