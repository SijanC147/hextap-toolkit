// Package githuboutput writes validated scalar values to a GitHub Actions
// output file without exposing callers to the multiline output grammar.
package githuboutput

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
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

// Append atomically appends fields to path with one O_APPEND write after every
// new byte has been validated. The runner-created file must already exist.
func Append(path string, fields []Field) error {
	return appendWithWriter(path, fields, func(file *os.File, data []byte) (int, error) {
		return file.Write(data)
	})
}

func appendWithWriter(path string, fields []Field, write func(*os.File, []byte) (int, error)) (retErr error) {
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
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open GitHub output %q for append: %w", path, err)
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
		return fmt.Errorf("inspect opened GitHub output %q: %w", path, err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return fmt.Errorf("GitHub output %q changed while opening", path)
	}
	if openedInfo.Size() > maxExistingOutputSize {
		return fmt.Errorf("GitHub output %q exceeds %d bytes", path, maxExistingOutputSize)
	}
	if openedInfo.Size() != 0 {
		last := []byte{0}
		if _, err := file.ReadAt(last, openedInfo.Size()-1); err != nil {
			return fmt.Errorf("read final byte of GitHub output %q: %w", path, err)
		}
		if last[0] != '\n' {
			return fmt.Errorf("GitHub output %q does not end with a newline", path)
		}
	}
	data := []byte(result.String())
	written, err := write(file, data)
	if err != nil {
		return fmt.Errorf("append GitHub output %q atomically: %w", path, err)
	}
	if written != len(data) {
		return fmt.Errorf("append GitHub output %q atomically: short write %d of %d bytes", path, written, len(data))
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync GitHub output %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close GitHub output %q: %w", path, err)
	}
	closed = true
	return nil
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
