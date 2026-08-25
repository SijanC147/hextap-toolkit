// Package atomicfile provides same-directory temporary-file replacement.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Write replaces path with data using a fully written and synced temporary
// file in the destination directory. The caller supplies the final mode.
func Write(path string, data []byte, mode fs.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	temp, err := os.CreateTemp(dir, "."+base+".tmp-*")
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
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file for %q: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	closed = true
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace %q atomically: %w", path, err)
	}
	return nil
}
