//go:build unix

package release

import (
	"os/exec"
	"syscall"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	// Process.Kill checks os.Process's lifecycle state. If Wait has already
	// reaped the leader it returns os.ErrProcessDone, and we must not use the
	// numeric PID as a negative process-group ID because it may be stale.
	if err := command.Process.Kill(); err != nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
