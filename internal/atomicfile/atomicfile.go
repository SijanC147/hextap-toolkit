// Package atomicfile provides same-directory temporary-file replacement.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type temporaryFile interface {
	Name() string
	Chmod(fs.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type createTemporaryFile func(string, string) (temporaryFile, error)

// Write replaces path with data using a fully written and synced temporary
// file in the destination directory. The caller supplies the final mode.
func Write(path string, data []byte, mode fs.FileMode) error {
	return write(path, data, mode, os.Rename, syncDirectory)
}

func write(path string, data []byte, mode fs.FileMode, rename func(string, string) error, syncParent func(string) error) (retErr error) {
	return writeWithTemporary(path, data, mode, func(directory, pattern string) (temporaryFile, error) {
		return os.CreateTemp(directory, pattern)
	}, rename, syncParent)
}

func writeWithTemporary(path string, data []byte, mode fs.FileMode, createTemp createTemporaryFile, rename func(string, string) error, syncParent func(string) error) (retErr error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	temp, err := createTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tempName := temp.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temp.Close(); retErr == nil && closeErr != nil {
				retErr = fmt.Errorf("close temporary file for %q: %w", path, closeErr)
			}
		}
		if retErr != nil {
			_ = os.Remove(tempName)
		}
	}()

	if err := temp.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("set temporary file mode for %q: %w", path, err)
	}
	written, err := temp.Write(data)
	if err != nil {
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if written != len(data) {
		return fmt.Errorf("write temporary file for %q: short write %d of %d bytes", path, written, len(data))
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file for %q: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	closed = true
	if err := rename(tempName, path); err != nil {
		return fmt.Errorf("replace %q atomically: %w", path, err)
	}
	if err := syncParent(dir); err != nil {
		return fmt.Errorf("sync parent directory for %q after rename; replacement may already be visible: %w", path, err)
	}
	return nil
}

func syncDirectory(path string) (retErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory %q: %w", path, err)
	}
	defer func() {
		if closeErr := directory.Close(); retErr == nil && closeErr != nil {
			retErr = fmt.Errorf("close directory %q: %w", path, closeErr)
		}
	}()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync directory %q: %w", path, err)
	}
	return nil
}
