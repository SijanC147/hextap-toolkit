package main

import (
	"bytes"
	"testing"
)

func TestRunUsesBuildInjectedVersionValues(t *testing.T) {
	oldVersion, oldCommit := version, commit
	t.Cleanup(func() {
		version, commit = oldVersion, oldCommit
	})
	version, commit = "1.2.3", "deadbee"

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"version"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("run() exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "hextapctl 1.2.3 (commit deadbee)\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
