//go:build !windows

package release

import (
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
			err := runAdapterWithControl(adapter, t.TempDir(), filepath.Join(t.TempDir(), "output"), target{OS: "darwin", Arch: "arm64"}, "1.2.3", testCommit, "", timeout, func(*exec.Cmd) {
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

func TestRunAdapterKillsAndReapsLeaderOnlyOnTimeout(t *testing.T) {
	adapter := writeAdapterScript(t, "#!/bin/sh\nexec sleep 30\n")
	var terminateCalls atomic.Int32
	var killedCommand *exec.Cmd
	timeout := make(chan time.Time, 1)
	timeout <- time.Now()
	err := runAdapterWithControl(adapter, t.TempDir(), filepath.Join(t.TempDir(), "output"), target{OS: "darwin", Arch: "arm64"}, "1.2.3", testCommit, "", timeout, func(command *exec.Cmd) {
		terminateCalls.Add(1)
		killedCommand = command
		if command.Process == nil {
			t.Error("terminate callback received command without a process")
			return
		}
		if command.SysProcAttr != nil {
			t.Errorf("adapter process has process-group attributes: %#v", command.SysProcAttr)
		}
		killAdapterLeader(command.Process)
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("runAdapterWithControl() error = %v, want timeout", err)
	}
	if got := terminateCalls.Load(); got != 1 {
		t.Fatalf("terminate callback calls = %d, want 1", got)
	}
	if killedCommand == nil || killedCommand.ProcessState == nil {
		t.Fatal("timeout adapter leader was not reaped before return")
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
