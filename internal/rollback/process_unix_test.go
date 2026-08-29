//go:build darwin || linux

package rollback

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOSRunnerTimeoutKillsTheCompleteProcessGroup(t *testing.T) {
	result, err := (osRunner{}).Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", `sleep 30 & child=$!; echo "$child"; wait "$child"`}, Timeout: 100 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if parseErr != nil || pid <= 0 {
		t.Fatalf("child PID output = %q, %v", result.Stdout, parseErr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		killErr := syscall.Kill(pid, 0)
		if errors.Is(killErr, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived the command timeout", pid)
}
