//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package release

import "os/exec"

func prepareVerifiedCommand(*exec.Cmd) {}

func terminateVerifiedCommand(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
