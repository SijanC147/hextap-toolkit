package manifest

import (
	"fmt"
	"io"
	"os"
)

// MaximumSize is the maximum accepted project-manifest input size.
const MaximumSize int64 = 1 << 20

// Load reads and parses one bounded regular non-symlink manifest while
// checking that its file identity and size remain stable.
func Load(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect manifest %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("manifest %q must be a regular non-symlink file", path)
	}
	if info.Size() < 0 || info.Size() > MaximumSize {
		return Manifest{}, fmt.Errorf("manifest %q exceeds %d bytes", path, MaximumSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open manifest %q: %w", path, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect opened manifest %q: %w", path, err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() {
		return Manifest{}, fmt.Errorf("manifest %q changed while opening", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaximumSize+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %q: %w", path, err)
	}
	if int64(len(data)) > MaximumSize || int64(len(data)) != openedInfo.Size() {
		return Manifest{}, fmt.Errorf("manifest %q changed size while reading", path)
	}
	return Parse(data)
}
