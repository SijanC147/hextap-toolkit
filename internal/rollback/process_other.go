//go:build !darwin && !linux

package rollback

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
