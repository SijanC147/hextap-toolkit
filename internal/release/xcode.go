package release

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	artifactAppZip = "app-zip"
	// maxAppBundleSize bounds the bytes an adapter may place in one .app.
	maxAppBundleSize = int64(2 << 30)
	maxAppEntries    = 100000
)

// zipEpoch is the earliest time a zip DOS timestamp can carry; every entry
// uses it so the archive bytes depend only on the bundle contents.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

type appEntry struct {
	name   string
	mode   fs.FileMode
	target string
	path   string
}

// validateAppOutput requires the adapter to leave exactly one real directory,
// the declared .app, holding Contents/Info.plist and the executable
// Contents/MacOS/<binary>.
func validateAppOutput(stageDir, appName, binary string) error {
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return fmt.Errorf("read staging directory: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != appName {
		return fmt.Errorf("adapter must create exactly %s", appName)
	}
	appPath := filepath.Join(stageDir, appName)
	info, err := os.Lstat(appPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be a directory, not %s", appName, info.Mode().Type())
	}
	plist, err := os.Lstat(filepath.Join(appPath, "Contents", "Info.plist"))
	if err != nil || !plist.Mode().IsRegular() {
		return fmt.Errorf("%s is missing a regular Contents/Info.plist", appName)
	}
	executable, err := os.Lstat(filepath.Join(appPath, "Contents", "MacOS", binary))
	if err != nil || !executable.Mode().IsRegular() || executable.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is missing executable Contents/MacOS/%s", appName, binary)
	}
	return nil
}

func collectAppEntries(appPath, appName string) ([]appEntry, error) {
	var entries []appEntry
	var total int64
	err := filepath.WalkDir(appPath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(appPath, current)
		if err != nil {
			return err
		}
		name := appName
		if relative != "." {
			name = path.Join(appName, filepath.ToSlash(relative))
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := appEntry{name: name, path: current}
		switch {
		case info.IsDir():
			item.mode = fs.ModeDir | 0o755
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(current)
			if err != nil {
				return err
			}
			resolved := path.Clean(path.Join(path.Dir(name), filepath.ToSlash(target)))
			if filepath.IsAbs(target) || (resolved != appName && !strings.HasPrefix(resolved, appName+"/")) {
				return fmt.Errorf("symlink %s escapes the app bundle", name)
			}
			item.mode = fs.ModeSymlink | 0o755
			item.target = filepath.ToSlash(target)
		case info.Mode().IsRegular():
			if hardLinked(info) {
				return fmt.Errorf("%s is hard-linked", name)
			}
			item.mode = 0o644
			if info.Mode().Perm()&0o111 != 0 {
				item.mode = 0o755
			}
			total += info.Size()
			if total > maxAppBundleSize {
				return errors.New("app bundle exceeds the size limit")
			}
		default:
			return fmt.Errorf("%s has unsupported file type %s", name, info.Mode().Type())
		}
		entries = append(entries, item)
		if len(entries) > maxAppEntries {
			return errors.New("app bundle has too many entries")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

// writeAppZip writes a deterministic zip whose single top-level member is the
// .app bundle, preserving executable bits and in-bundle relative symlinks.
func writeAppZip(destination, appPath, appName string) (retErr error) {
	entries, err := collectAppEntries(appPath, appName)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if retErr != nil {
			_ = os.Remove(destination)
		}
	}()
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: zipEpoch}
		if entry.mode.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		}
		header.SetMode(entry.mode)
		output, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		switch {
		case entry.mode.IsDir():
		case entry.mode&fs.ModeSymlink != 0:
			if _, err := io.WriteString(output, entry.target); err != nil {
				return err
			}
		default:
			if err := copyRegularFile(output, entry.path); err != nil {
				return err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	closed = true
	return file.Close()
}

func copyRegularFile(output io.Writer, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	_, err = io.Copy(output, input)
	return err
}

// verifyAppZip validates a schema 3 app archive: every entry sits under the
// single .app root, symlinks stay inside it, Contents/Info.plist exists, and
// Contents/MacOS/<binary> is a Mach-O executable for the target. It returns
// the executable bytes so cross-asset comparison keeps working.
func verifyAppZip(raw []byte, appName, binary, targetOS, targetArch string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	var executable []byte
	var total uint64
	seen := make(map[string]bool, len(reader.File))
	plist := false
	for _, file := range reader.File {
		name := strings.TrimSuffix(file.Name, "/")
		if name != appName && !strings.HasPrefix(name, appName+"/") || path.Clean(name) != name || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("zip entry %q is outside %s", file.Name, appName)
		}
		if seen[name] {
			return nil, fmt.Errorf("zip entry %q is duplicated", name)
		}
		seen[name] = true
		total += file.UncompressedSize64
		if total > uint64(maxAppBundleSize) {
			return nil, errors.New("zip exceeds the app bundle size limit")
		}
		mode := file.Mode()
		switch {
		case mode.IsDir():
		case mode&fs.ModeSymlink != 0:
			target, err := readZipEntry(file, 4096)
			if err != nil {
				return nil, err
			}
			resolved := path.Clean(path.Join(path.Dir(name), string(target)))
			if path.IsAbs(string(target)) || (resolved != appName && !strings.HasPrefix(resolved, appName+"/")) {
				return nil, fmt.Errorf("zip symlink %q escapes %s", name, appName)
			}
		case mode.IsRegular():
			if name == appName+"/Contents/Info.plist" {
				plist = true
			}
			if name == appName+"/Contents/MacOS/"+binary {
				if mode.Perm()&0o111 == 0 {
					return nil, fmt.Errorf("%s is not executable", name)
				}
				executable, err = readZipEntry(file, maxBinarySize)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("zip entry %q has unsupported type %s", name, mode.Type())
		}
	}
	if !plist {
		return nil, fmt.Errorf("%s is missing Contents/Info.plist", appName)
	}
	if executable == nil {
		return nil, fmt.Errorf("%s is missing Contents/MacOS/%s", appName, binary)
	}
	if err := verifyExecutable(executable, targetOS, targetArch); err != nil {
		return nil, err
	}
	return executable, nil
}

func readZipEntry(file *zip.File, maximum int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(maximum) {
		return nil, fmt.Errorf("zip entry %q is too large", file.Name)
	}
	input, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum || uint64(len(data)) != file.UncompressedSize64 {
		return nil, fmt.Errorf("zip entry %q size mismatch", file.Name)
	}
	return data, nil
}

// verifyAppSignature extracts a verified app zip into a private directory and
// runs codesign --verify --deep --strict. It replaces the --version execution
// used for command-line binaries: an app bundle has no version contract.
func verifyAppSignature(raw []byte, appName string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("verify app signature: codesign is available only on macOS")
	}
	directory, err := os.MkdirTemp("", "hextap-verify-app-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		name := strings.TrimSuffix(file.Name, "/")
		destination := filepath.Join(directory, filepath.FromSlash(name))
		mode := file.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			target, err := readZipEntry(file, 4096)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(target), destination); err != nil {
				return err
			}
		default:
			data, err := readZipEntry(file, maxAppBundleSize)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(destination, data, mode.Perm()); err != nil {
				return err
			}
		}
	}
	command := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", filepath.Join(directory, appName))
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codesign --verify %s: %w: %s", appName, err, strings.TrimSpace(string(output)))
	}
	return nil
}
