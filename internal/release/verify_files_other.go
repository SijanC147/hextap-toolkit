//go:build !(aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris || windows)

package release

import (
	"errors"
	"io/fs"
)

// Verification fails closed where the operating system does not expose the
// Unix link-count and allocation metadata required by the release contract.
func verifyOuterFileLayout(fs.FileInfo) error {
	return errors.New("release verification is unsupported on this platform")
}
