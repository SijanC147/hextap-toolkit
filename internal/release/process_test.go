//go:build !windows

package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunAdapterDoesNotKillCompletedAdapter(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{name: "success", script: "#!/bin/sh\nexit 0\n", want: ""},
		{name: "nonzero", script: "#!/bin/sh\nexit 23\n", want: "build adapter failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := writeAdapterScript(t, test.script)
			var terminateCalls atomic.Int32
			timeout := make(chan time.Time)
			err := runAdapterWithControl(adapter, t.TempDir(), filepath.Join(t.TempDir(), "output"), target{OS: "darwin", Arch: "arm64"}, "1.2.3", testCommit, timeout, func(*exec.Cmd) {
				terminateCalls.Add(1)
			})
			if test.want == "" {
				if err != nil {
					t.Fatalf("runAdapterWithControl() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runAdapterWithControl() error = %v, want substring %q", err, test.want)
			}
			if got := terminateCalls.Load(); got != 0 {
				t.Fatalf("terminate callback calls = %d, want 0 after adapter was reaped", got)
			}
		})
	}
}

func TestRunAdapterKillsOnlyOnTimeoutBeforeWait(t *testing.T) {
	adapter := writeAdapterScript(t, "#!/bin/sh\nsleep 30\n")
	var terminateCalls atomic.Int32
	timeout := make(chan time.Time, 1)
	timeout <- time.Now()
	err := runAdapterWithControl(adapter, t.TempDir(), filepath.Join(t.TempDir(), "output"), target{OS: "darwin", Arch: "arm64"}, "1.2.3", testCommit, timeout, func(command *exec.Cmd) {
		terminateCalls.Add(1)
		if command.Process == nil {
			t.Error("terminate callback received command without a process")
			return
		}
		terminateProcessTree(command)
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("runAdapterWithControl() error = %v, want timeout", err)
	}
	if got := terminateCalls.Load(); got != 1 {
		t.Fatalf("terminate callback calls = %d, want 1", got)
	}
}

func TestTerminateProcessTreeDoesNotSignalGroupAfterWait(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	adapter := writeAdapterScript(t, fmt.Sprintf("#!/bin/sh\n(sleep 0.2; printf survived > %q) &\nexit 0\n", marker))
	command := exec.Command(adapter)
	configureProcess(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}

	// The leader has been reaped. Its child remains in the process group and
	// must not be reached through a stale negative PGID signal.
	terminateProcessTree(command)
	time.Sleep(500 * time.Millisecond)
	if got, err := os.ReadFile(marker); err != nil {
		t.Fatalf("post-wait child marker was removed by termination: %v", err)
	} else if string(got) != "survived" {
		t.Fatalf("post-wait child marker = %q", got)
	}
}

func writeAdapterScript(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
