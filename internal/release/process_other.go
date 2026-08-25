//go:build !unix

package release

import "os/exec"

func configureProcess(_ *exec.Cmd) {}

func terminateProcessTree(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
