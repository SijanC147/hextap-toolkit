package manifest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
)

// MaximumSize is the maximum accepted project-manifest input size.
const MaximumSize int64 = 1 << 20

// Load reads and parses one bounded regular non-symlink manifest while
// checking that its file identity and size remain stable.
func Load(path string) (Manifest, error) {
	return loadWithHook(path, nil)
}

func loadWithHook(path string, afterFirstRead func()) (Manifest, error) {
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
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect opened manifest %q: %w", path, err)
	}
	if !stableFileInfo(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("manifest %q changed while opening", path)
	}
	first, err := io.ReadAll(io.LimitReader(file, MaximumSize+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %q: %w", path, err)
	}
	if int64(len(first)) > MaximumSize || int64(len(first)) != openedInfo.Size() {
		return Manifest{}, fmt.Errorf("manifest %q changed size while reading", path)
	}
	if afterFirstRead != nil {
		afterFirstRead()
	}
	middleInfo, err := file.Stat()
	if err != nil || !stableFileInfo(openedInfo, middleInfo) {
		return Manifest{}, fmt.Errorf("manifest %q changed after its first read", path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, fmt.Errorf("rewind manifest %q: %w", path, err)
	}
	second, err := io.ReadAll(io.LimitReader(file, MaximumSize+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("reread manifest %q: %w", path, err)
	}
	finalInfo, err := file.Stat()
	pathInfo, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !stableFileInfo(middleInfo, finalInfo) || !stableFileInfo(finalInfo, pathInfo) || !bytes.Equal(first, second) {
		return Manifest{}, fmt.Errorf("manifest %q changed during stable read", path)
	}
	if err := file.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close manifest %q: %w", path, err)
	}
	closed = true
	return Parse(first)
}

func stableFileInfo(before, after os.FileInfo) bool {
	if before == nil || after == nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return false
	}
	beforeLinks, beforeLinksOK := statUintField(before, "Nlink")
	afterLinks, afterLinksOK := statUintField(after, "Nlink")
	if beforeLinksOK != afterLinksOK || beforeLinksOK && beforeLinks != afterLinks {
		return false
	}
	beforeChange, beforeChangeOK := statTimeField(before, "Ctim", "Ctimespec", "ChangeTime")
	afterChange, afterChangeOK := statTimeField(after, "Ctim", "Ctimespec", "ChangeTime")
	return beforeChangeOK == afterChangeOK && (!beforeChangeOK || beforeChange == afterChange)
}

func statUintField(info os.FileInfo, name string) (uint64, bool) {
	value := statStruct(info)
	if !value.IsValid() {
		return 0, false
	}
	field := value.FieldByName(name)
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int()), true
	default:
		return 0, false
	}
}

func statTimeField(info os.FileInfo, names ...string) (string, bool) {
	value := statStruct(info)
	if !value.IsValid() {
		return "", false
	}
	for _, name := range names {
		field := value.FieldByName(name)
		if field.IsValid() {
			return fmt.Sprint(field.Interface()), true
		}
	}
	return "", false
}

func statStruct(info os.FileInfo) reflect.Value {
	value := reflect.ValueOf(info.Sys())
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return value
}
