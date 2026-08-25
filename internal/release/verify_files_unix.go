//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package release

import (
	"fmt"
	"io/fs"
	"syscall"
)

// verifyOuterFileLayout enforces the filesystem properties that cannot be
// represented by fs.FileInfo's portable interface. Release verification is
// deliberately conservative on Unix filesystems: every outer artifact must
// be a single-link, fully allocated regular file.
func verifyOuterFileLayout(info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unsupported Unix file metadata for %q", info.Name())
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("release file %q must have exactly one link", info.Name())
	}
	if stat.Blocks < 0 || info.Size() > 0 && stat.Blocks < (info.Size()+511)/512 {
		return fmt.Errorf("release file %q is sparse", info.Name())
	}
	return nil
}
