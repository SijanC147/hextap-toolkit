//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package release

import (
	"os/exec"
	"syscall"
)

func prepareVerifiedCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateVerifiedCommand(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	_ = command.Process.Kill()
}
