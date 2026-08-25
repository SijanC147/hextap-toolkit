// Package githuboutput writes validated scalar values to a GitHub Actions
// output file without exposing callers to the multiline output grammar.
package githuboutput

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/SijanC147/hextap-toolkit/internal/atomicfile"
)

const maxExistingOutputSize = 1 << 20

const (
	maxFieldCount       = 64
	maxKeyLength        = 128
	maxAppendRecordSize = 64 << 10
)

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Field is one single-line GitHub Actions output.
type Field struct {
	Key   string
	Value string
}

// Append atomically replaces path with its existing contents plus fields after
// every new byte has been validated. The runner-created file must already
// exist. A parent-directory sync failure can occur after the complete
// replacement becomes visible.
func Append(path string, fields []Field) error {
	return appendWithReplace(path, fields, atomicfile.Write)
}

type replaceFile func(string, []byte, fs.FileMode) error

func appendWithReplace(path string, fields []Field, replace replaceFile) error {
	if path == "" {
		return errors.New("GitHub output path must not be empty")
	}
	if len(fields) == 0 {
		return errors.New("at least one GitHub output field is required")
	}
	if len(fields) > maxFieldCount {
		return fmt.Errorf("GitHub output append exceeds %d fields", maxFieldCount)
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field.Key) > maxKeyLength || !keyPattern.MatchString(field.Key) {
			return fmt.Errorf("GitHub output key %q is invalid", field.Key)
		}
		if _, exists := seen[field.Key]; exists {
			return fmt.Errorf("GitHub output key %q is duplicated", field.Key)
		}
		seen[field.Key] = struct{}{}
		if err := validateValue(field.Key, field.Value); err != nil {
			return err
		}
	}

	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect GitHub output %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("GitHub output %q must be a regular non-symlink file", path)
	}
	if info.Size() > maxExistingOutputSize {
		return fmt.Errorf("GitHub output %q exceeds %d bytes", path, maxExistingOutputSize)
	}

	var result strings.Builder
	result.Grow(len(fields) * 32)
	for _, field := range fields {
		result.WriteString(field.Key)
		result.WriteByte('=')
		result.WriteString(field.Value)
		result.WriteByte('\n')
	}
	if result.Len() > maxAppendRecordSize {
		return fmt.Errorf("GitHub output append exceeds %d bytes", maxAppendRecordSize)
	}
	existing, mode, err := readExisting(path, info)
	if err != nil {
		return err
	}
	combined := make([]byte, 0, len(existing)+result.Len())
	combined = append(combined, existing...)
	combined = append(combined, result.String()...)
	if err := replace(path, combined, mode); err != nil {
		return fmt.Errorf("replace GitHub output %q atomically: %w", path, err)
	}
	return nil
}

func readExisting(path string, info fs.FileInfo) (data []byte, mode fs.FileMode, retErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open GitHub output %q: %w", path, err)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); retErr == nil && closeErr != nil {
				retErr = fmt.Errorf("close GitHub output %q: %w", path, closeErr)
			}
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("inspect opened GitHub output %q: %w", path, err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("GitHub output %q changed while opening", path)
	}
	if openedInfo.Size() > maxExistingOutputSize {
		return nil, 0, fmt.Errorf("GitHub output %q exceeds %d bytes", path, maxExistingOutputSize)
	}
	data, err = io.ReadAll(io.LimitReader(file, maxExistingOutputSize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read GitHub output %q: %w", path, err)
	}
	if len(data) > maxExistingOutputSize || int64(len(data)) != openedInfo.Size() {
		return nil, 0, fmt.Errorf("GitHub output %q changed size while reading", path)
	}
	if len(data) != 0 && data[len(data)-1] != '\n' {
		return nil, 0, fmt.Errorf("GitHub output %q does not end with a newline", path)
	}
	if err := file.Close(); err != nil {
		return nil, 0, fmt.Errorf("close GitHub output %q: %w", path, err)
	}
	closed = true
	return data, openedInfo.Mode().Perm(), nil
}

func validateValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("GitHub output %q must not be empty", key)
	}
	if len(value) > 4096 {
		return fmt.Errorf("GitHub output %q exceeds 4096 bytes", key)
	}
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("GitHub output %q is not a safe single-line value", key)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("GitHub output %q contains a control character", key)
		}
	}
	return nil
}
