//go:build windows

package release

import (
	"errors"
	"io/fs"
	"syscall"
)

const windowsFileAttributeSparse = 0x00000200

func verifyOuterFileLayout(info fs.FileInfo) error {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return errors.New("unsupported Windows file metadata")
	}
	if attributes.FileAttributes&(syscall.FILE_ATTRIBUTE_REPARSE_POINT|windowsFileAttributeSparse) != 0 {
		return errors.New("release file must not be sparse or a reparse point")
	}
	return nil
}
