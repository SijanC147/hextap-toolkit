package release

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func writeBuildFixture(t *testing.T, linux bool, script string) (string, string) {
	t.Helper()
	source := t.TempDir()
	examplePath := filepath.Join("..", "..", "examples", "claude-rc-proxy.json")
	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("ReadFile(example): %v", err)
	}
	if !linux {
		data = []byte(strings.Replace(string(data), `"linux": true`, `"linux": false`, 1))
	}
	manifestPath := filepath.Join(source, ".hextap.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "LICENSE"), []byte("license contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("readme contents\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(source, "scripts", "hextap-build")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return source, manifestPath
}

func buildOptions(source, manifestPath, output string) BuildOptions {
	return BuildOptions{
		ManifestPath: manifestPath,
		Version:      "1.2.3",
		Commit:       testCommit,
		SourceDir:    source,
		OutputDir:    output,
	}
}

const successfulAdapter = `#!/bin/sh
set -eu
if [ "${SECRET_SHOULD_NOT_LEAK+x}" = x ]; then
  exit 91
fi
printf '%s|%s|%s|%s\n' "$HEXTAP_TARGET_OS" "$HEXTAP_TARGET_ARCH" "$HEXTAP_VERSION" "$HEXTAP_COMMIT" > "$HEXTAP_OUTPUT"
`

func TestBuildTargetMatrixAndLinuxToggle(t *testing.T) {
	t.Setenv("SECRET_SHOULD_NOT_LEAK", "do-not-inherit")
	for _, linux := range []bool{true, false} {
		t.Run(fmt.Sprintf("linux=%t", linux), func(t *testing.T) {
			source, manifestPath := writeBuildFixture(t, linux, successfulAdapter)
			output := filepath.Join(t.TempDir(), "dist")
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatal(err)
			}
			result, err := Build(buildOptions(source, manifestPath, output))
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			wantAssets := []string{
				"claude-rc-proxy-darwin-amd64.tar.gz",
				"claude-rc-proxy-darwin-arm64.tar.gz",
			}
			if linux {
				wantAssets = append(wantAssets,
					"claude-rc-proxy-linux-amd64.tar.gz",
					"claude-rc-proxy-linux-arm64.tar.gz",
				)
			}
			sort.Strings(wantAssets)
			if strings.Join(result.Assets, "\n") != strings.Join(wantAssets, "\n") {
				t.Fatalf("assets = %v, want %v", result.Assets, wantAssets)
			}
			entries, err := os.ReadDir(output)
			if err != nil {
				t.Fatal(err)
			}
			names := entryNames(entries)
			if len(entries) != len(wantAssets)+1 || !containsString(names, "SHA256SUMS") {
				t.Fatalf("output entries = %v", entryNames(entries))
			}
			for _, asset := range wantAssets {
				files := readArchive(t, filepath.Join(output, asset))
				binary := files["claude-rc-proxy"].data
				parts := strings.Split(strings.TrimSpace(string(binary)), "|")
				if len(parts) != 4 || parts[2] != "1.2.3" || parts[3] != testCommit {
					t.Fatalf("%s binary contents = %q", asset, binary)
				}
			}
		})
	}
}

func TestBuildEnvironmentAllowsDedicatedBunRuntimeCacheButNotSecrets(t *testing.T) {
	t.Setenv("SECRET_SHOULD_NOT_LEAK", "hidden")
	environment := buildEnvironment(target{OS: "windows", Arch: "amd64"}, "/tmp/output.exe", "1.2.3", testCommit, "/tmp/hextap-bun-runtime-cache")
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "BUN_INSTALL_CACHE_DIR=/tmp/hextap-bun-runtime-cache") {
		t.Fatalf("build environment lacks Bun cache: %v", environment)
	}
	if strings.Contains(joined, "SECRET_SHOULD_NOT_LEAK") || strings.Contains(joined, "hidden") {
		t.Fatalf("build environment leaked secret: %v", environment)
	}
}

func TestBuildPropagatesPrereleaseVersion(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, false, successfulAdapter)
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	options := buildOptions(source, manifestPath, output)
	options.Version = "1.2.3-rc.1"
	if _, err := Build(options); err != nil {
		t.Fatalf("Build(prerelease) error = %v", err)
	}
	for _, asset := range []string{"claude-rc-proxy-darwin-arm64.tar.gz", "claude-rc-proxy-darwin-amd64.tar.gz"} {
		binary := readArchive(t, filepath.Join(output, asset))["claude-rc-proxy"].data
		if !strings.Contains(string(binary), "|1.2.3-rc.1|") {
			t.Fatalf("%s did not receive prerelease version: %q", asset, binary)
		}
	}
}

func TestBuildIsByteDeterministicAndHeadersAreCanonical(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, true, successfulAdapter)
	outputs := make([]string, 2)
	for index := range outputs {
		if index == 1 {
			if err := os.Chmod(filepath.Join(source, "LICENSE"), 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(source, "README.md"), 0o400); err != nil {
				t.Fatal(err)
			}
			changedTime := time.Unix(1_700_000_000, 0)
			if err := os.Chtimes(filepath.Join(source, "LICENSE"), changedTime, changedTime); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TZ", "Pacific/Honolulu")
		}
		outputs[index] = filepath.Join(t.TempDir(), "dist")
		if err := os.Mkdir(outputs[index], 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Build(buildOptions(source, manifestPath, outputs[index])); err != nil {
			t.Fatalf("Build(%d) error = %v", index, err)
		}
	}
	firstEntries, err := os.ReadDir(outputs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range firstEntries {
		first, err := os.ReadFile(filepath.Join(outputs[0], entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(filepath.Join(outputs[1], entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(second) {
			t.Errorf("%s differs across deterministic builds", entry.Name())
		}
	}

	archivePath := filepath.Join(outputs[0], "claude-rc-proxy-darwin-arm64.tar.gz")
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	wantGzipHeader := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff}
	if len(archiveBytes) < len(wantGzipHeader) || !bytes.Equal(archiveBytes[:len(wantGzipHeader)], wantGzipHeader) {
		t.Fatalf("gzip header = %x, want %x", archiveBytes[:min(len(archiveBytes), 10)], wantGzipHeader)
	}
	if got, want := archiveMemberOrder(t, archivePath), []string{"claude-rc-proxy", "LICENSE", "README.md"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("archive member order = %v, want %v", got, want)
	}
	files := readArchive(t, archivePath)
	if len(files) != 3 {
		t.Fatalf("archive members = %v", files)
	}
	for name, wantMode := range map[string]int64{
		"claude-rc-proxy": 0o755,
		"LICENSE":         0o644,
		"README.md":       0o644,
	} {
		file := files[name]
		if file.header == nil {
			t.Fatalf("archive missing %s", name)
		}
		if file.header.Mode != wantMode || file.header.Size != int64(len(file.data)) || file.header.Uid != 0 || file.header.Gid != 0 || file.header.Uname != "" || file.header.Gname != "" || file.header.Typeflag != tar.TypeReg || file.header.Linkname != "" || file.header.Devmajor != 0 || file.header.Devminor != 0 || len(file.header.PAXRecords) != 0 || len(file.header.Xattrs) != 0 || !file.header.ModTime.Equal(time.Unix(0, 0).UTC()) || !file.header.AccessTime.IsZero() || !file.header.ChangeTime.IsZero() || file.header.Format != tar.FormatUSTAR {
			t.Errorf("%s header is not canonical: %#v", name, file.header)
		}
	}
	if got := string(files["LICENSE"].data); got != "license contents\n" {
		t.Fatalf("LICENSE = %q", got)
	}
	if got := string(files["README.md"].data); got != "readme contents\n" {
		t.Fatalf("README.md = %q", got)
	}
	for _, name := range []string{"LICENSE", "README.md"} {
		if info, err := os.Stat(filepath.Join(source, name)); err != nil || info.Mode().Perm() == 0o644 {
			// Source modes intentionally differ; archive modes are owned by the
			// toolkit rather than inherited from the checkout.
			t.Fatalf("source fixture mode for %s did not differ as expected", name)
		}
	}
	assertChecksums(t, outputs[0])
}

func TestBuildBundlesDeclaredZshCompletionWithCanonicalPathAndMode(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, false, successfulAdapter)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestData = bytes.Replace(manifestData, []byte(`"macos_only": true,`), []byte(`"macos_only": true,
    "binary_aliases": ["hextap"],
    "zsh_completion": "completions/_hextap",`), 1)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	completionPath := filepath.Join(source, "completions", "_hextap")
	if err := os.MkdirAll(filepath.Dir(completionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(completionPath, []byte("#compdef hextap brew-hextap\n_hextap() { : }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(buildOptions(source, manifestPath, output)); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	archivePath := filepath.Join(output, "claude-rc-proxy-darwin-arm64.tar.gz")
	if got, want := archiveMemberOrder(t, archivePath), []string{"claude-rc-proxy", "LICENSE", "README.md", "completions/_hextap"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("archive member order = %v, want %v", got, want)
	}
	completion := readArchive(t, archivePath)["completions/_hextap"]
	if completion.header == nil || completion.header.Mode != 0o644 || string(completion.data) != "#compdef hextap brew-hextap\n_hextap() { : }\n" {
		t.Fatalf("completion archive member = %#v", completion)
	}
}

func TestBuildRejectsZshCompletionSymlinkedParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks requires privileges on Windows")
	}
	source, manifestPath := writeBuildFixture(t, false, successfulAdapter)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestData = bytes.Replace(manifestData, []byte(`"macos_only": true,`), []byte(`"macos_only": true,
    "binary_aliases": ["hextap"],
    "zsh_completion": "completions/_hextap",`), 1)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "_hextap"), []byte("#compdef hextap brew-hextap\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "completions")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(buildOptions(source, manifestPath, output)); err == nil || !strings.Contains(err.Error(), "Zsh completion path must not contain symlinks") {
		t.Fatalf("Build() completion-parent error = %v", err)
	}
}

func TestBuildRejectsAdapterAndStagingViolationsAndCleansOutput(t *testing.T) {
	tests := map[string]struct {
		script string
		setup  func(t *testing.T, source string)
	}{
		"adapter failure after partial matrix": {
			script: `#!/bin/sh
set -eu
if [ "$HEXTAP_TARGET_ARCH" = amd64 ]; then exit 42; fi
printf binary > "$HEXTAP_OUTPUT"
`,
		},
		"missing binary": {
			script: "#!/bin/sh\nexit 0\n",
		},
		"symlink binary": {
			script: "#!/bin/sh\nln -s /bin/sh \"$HEXTAP_OUTPUT\"\n",
		},
		"hardlink binary": {
			script: "#!/bin/sh\nln \"$0\" \"$HEXTAP_OUTPUT\"\n",
		},
		"extra staged file": {
			script: "#!/bin/sh\nprintf binary > \"$HEXTAP_OUTPUT\"\nprintf extra > \"$(dirname \"$HEXTAP_OUTPUT\")/extra\"\n",
		},
		"non executable adapter": {
			script: successfulAdapter,
			setup: func(t *testing.T, source string) {
				t.Helper()
				if err := os.Chmod(filepath.Join(source, "scripts", "hextap-build"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		"symlink adapter": {
			script: successfulAdapter,
			setup: func(t *testing.T, source string) {
				t.Helper()
				path := filepath.Join(source, "scripts", "hextap-build")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/bin/sh", path); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source, manifestPath := writeBuildFixture(t, true, test.script)
			if test.setup != nil {
				test.setup(t, source)
			}
			output := filepath.Join(t.TempDir(), "dist")
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := Build(buildOptions(source, manifestPath, output)); err == nil {
				t.Fatal("Build() unexpectedly succeeded")
			}
			entries, err := os.ReadDir(output)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed build left partial output: %v", entryNames(entries))
			}
		})
	}
}

func TestBuildRejectsInvalidInputsBeforeAdapter(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, true, successfulAdapter)
	tests := map[string]func(t *testing.T, options *BuildOptions){
		"invalid version": func(t *testing.T, options *BuildOptions) { options.Version = "v1.2.3" },
		"invalid commit":  func(t *testing.T, options *BuildOptions) { options.Commit = "ABC1234" },
		"nonempty output": func(t *testing.T, options *BuildOptions) {
			if err := os.WriteFile(filepath.Join(options.OutputDir, "existing"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"manifest outside source": func(t *testing.T, options *BuildOptions) {
			data, err := os.ReadFile(options.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			options.ManifestPath = filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(options.ManifestPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"missing license": func(t *testing.T, options *BuildOptions) {
			if err := os.Remove(filepath.Join(options.SourceDir, "LICENSE")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			caseSource := source
			caseManifest := manifestPath
			if name == "missing license" {
				caseSource, caseManifest = writeBuildFixture(t, true, successfulAdapter)
			}
			output := filepath.Join(t.TempDir(), "dist")
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatal(err)
			}
			options := buildOptions(caseSource, caseManifest, output)
			mutate(t, &options)
			if _, err := Build(options); err == nil {
				t.Fatal("Build() unexpectedly succeeded")
			}
		})
	}
}

func TestAcquireReleaseLockRejectsCompetingLock(t *testing.T) {
	output := t.TempDir()
	lockPath := filepath.Join(output, releaseLockName)
	if err := os.WriteFile(lockPath, []byte("another build\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := acquireReleaseLock(output); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("acquireReleaseLock() error = %v, want an existing-lock error", err)
	}
	if got, err := os.ReadFile(lockPath); err != nil {
		t.Fatal(err)
	} else if string(got) != "another build\n" {
		t.Fatalf("competing lock contents = %q, want it preserved", got)
	}
}

func TestBuildCleanupPreservesReplacementOutput(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, false, successfulAdapter)
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	options := buildOptions(source, manifestPath, output)
	var replacementPath string
	_, err := build(options, buildHooks{
		afterPublish: func(name, path string) error {
			if replacementPath != "" {
				return nil
			}
			replacementPath = path
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("replacement owned by another writer\n"), 0o600); err != nil {
				return err
			}
			return errors.New("stop after replacement")
		},
	})
	if err == nil {
		t.Fatal("build() unexpectedly succeeded")
	}
	if replacementPath == "" {
		t.Fatal("build hook did not publish an output")
	}
	if got, readErr := os.ReadFile(replacementPath); readErr != nil {
		t.Fatalf("replacement output was removed: %v", readErr)
	} else if string(got) != "replacement owned by another writer\n" {
		t.Fatalf("replacement output = %q", got)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if names := entryNames(entries); len(names) != 1 || names[0] != filepath.Base(replacementPath) {
		t.Fatalf("failed build output entries = %v, want only the replacement", names)
	}
}

func TestBuildRejectsAndPreservesExtraFinalOutput(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, false, successfulAdapter)
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	extraPath := filepath.Join(output, "written-by-another-build")
	_, err := build(buildOptions(source, manifestPath, output), buildHooks{
		beforeOutputCommit: func(string) error {
			return os.WriteFile(extraPath, []byte("do not remove\n"), 0o600)
		},
	})
	if err == nil {
		t.Fatal("build() unexpectedly succeeded with an extra final output")
	}
	if got, readErr := os.ReadFile(extraPath); readErr != nil {
		t.Fatalf("extra output was removed: %v", readErr)
	} else if string(got) != "do not remove\n" {
		t.Fatalf("extra output = %q", got)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if names := entryNames(entries); len(names) != 1 || names[0] != filepath.Base(extraPath) {
		t.Fatalf("failed build output entries = %v, want only the extra output", names)
	}
}

type archiveFile struct {
	header *tar.Header
	data   []byte
}

func readArchive(t *testing.T, path string) map[string]archiveFile {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	result := make(map[string]archiveFile)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		copyHeader := *header
		result[header.Name] = archiveFile{header: &copyHeader, data: data}
	}
	return result
}

func archiveMemberOrder(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var result []string
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, header.Name)
		if _, err := io.Copy(io.Discard, tarReader); err != nil {
			t.Fatal(err)
		}
	}
}

func assertChecksums(t *testing.T, directory string) {
	t.Helper()
	file, err := os.Open(filepath.Join(directory, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	previous := ""
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 || parts[1] <= previous {
			t.Fatalf("invalid checksum line/order: %q", scanner.Text())
		}
		data, err := os.ReadFile(filepath.Join(directory, parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != parts[0] {
			t.Fatalf("checksum for %s = %s, want %s", parts[1], parts[0], got)
		}
		previous = parts[1]
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("SHA256SUMS was empty")
	}
}

func entryNames(entries []os.DirEntry) []string {
	result := make([]string, len(entries))
	for index, entry := range entries {
		result[index] = entry.Name()
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
