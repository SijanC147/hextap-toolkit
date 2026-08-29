package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeRealVerifyFixture(t *testing.T, linux bool) (source, manifestPath string) {
	t.Helper()
	source = t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !linux {
		data = []byte(strings.Replace(string(data), `"linux": true`, `"linux": false`, 1))
	}
	manifestPath = filepath.Join(source, ".hextap.json")
	for name, contents := range map[string]string{
		".hextap.json": string(data),
		"LICENSE":      "license\n",
		"README.md":    "readme\n",
		"main.go": `package main

import "fmt"

var version = "dev"
var commit = "unknown"

func main() { fmt.Printf("claude-rc-proxy %s (commit %s)\n", version, commit) }
`,
		"scripts/hextap-build": `#!/bin/sh
set -eu
case "$HEXTAP_TARGET_ARCH" in
  amd64) arch=amd64 ;;
  arm64) arch=arm64 ;;
  *) exit 2 ;;
esac
GOOS="$HEXTAP_TARGET_OS" GOARCH="$arch" go build -trimpath -ldflags "-X main.version=$HEXTAP_VERSION -X main.commit=$HEXTAP_COMMIT" -o "$HEXTAP_OUTPUT" ./main.go
`,
	} {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if name == "scripts/hextap-build" {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	return source, manifestPath
}

func buildRealVerifyFixture(t *testing.T, linux bool) (manifestPath, output string) {
	t.Helper()
	source, manifestPath := writeRealVerifyFixture(t, linux)
	output = filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(buildOptions(source, manifestPath, output)); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return manifestPath, output
}

func buildRealProfileVerifyFixture(t *testing.T) (manifestPath, output string) {
	t.Helper()
	source := t.TempDir()
	for name, contents := range map[string]string{
		".hextap.json": profileBuildManifest,
		"LICENSE":      "license\n",
		"README.md":    "readme\n",
		"go.mod":       "module example.com/profile-verify\n\ngo 1.26\n",
		"main.go": `package main

import "fmt"

var version = "dev"
var commit = "unknown"

func main() { fmt.Printf("better-ccflare %s (commit %s)\n", version, commit) }
`,
		"scripts/hextap-build": `#!/bin/sh
set -eu
CGO_ENABLED=0 GOOS="$HEXTAP_TARGET_OS" GOARCH="$HEXTAP_TARGET_ARCH" go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$HEXTAP_VERSION -X main.commit=$HEXTAP_COMMIT" -o "$HEXTAP_OUTPUT" ./main.go
`,
	} {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if name == "scripts/hextap-build" {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	output = filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath = filepath.Join(source, ".hextap.json")
	options := buildOptions(source, manifestPath, output)
	options.BunCacheDir = t.TempDir()
	if _, err := Build(options); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return manifestPath, output
}

func TestVerifyProfileRawArchivesAndWindowsPE(t *testing.T) {
	manifestPath, output := buildRealProfileVerifyFixture(t)
	executeTarget := ""
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		executeTarget = runtime.GOOS + "-" + runtime.GOARCH
	}
	result, err := Verify(VerifyOptions{
		ManifestPath:  manifestPath,
		Version:       "1.2.3",
		Commit:        testCommit,
		Directory:     output,
		ExecuteTarget: executeTarget,
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(result.Assets) != 7 || result.ExecutedTarget != executeTarget {
		t.Fatalf("Verify() = %#v", result)
	}
}

func TestVerifyAcceptsBuildOutputAndRejectsDirectoryMutations(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	options := VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}
	if _, err := Verify(options); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := os.Remove(filepath.Join(output, "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(options); err == nil {
		t.Fatal("Verify() accepted missing SHA256SUMS")
	}
	if err := os.WriteFile(filepath.Join(output, "extra"), []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(options); err == nil {
		t.Fatal("Verify() accepted extra output")
	}
}

func TestVerifyAcceptsDeclaredZshCompletionBundle(t *testing.T) {
	source, manifestPath := writeRealVerifyFixture(t, false)
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
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err != nil {
		t.Fatalf("Verify() rejected completion bundle: %v", err)
	}
}

func TestVerifyRejectsChecksumMutation(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	path := filepath.Join(output, "SHA256SUMS")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = '0'
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted wrong checksum")
	}
}

func TestVerifyUsesChecksummedArchiveBytesAfterPathReplacement(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	asset := "claude-rc-proxy-darwin-arm64.tar.gz"
	path := filepath.Join(output, asset)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifyWithHook(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}, func() {
		if err := os.WriteFile(path, bytes.Repeat([]byte{0}, len(original)), 0o644); err != nil {
			t.Fatalf("replace archive: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("verifyWithHook() reread checksummed path: %v", err)
	}
}

func TestVerifyExecuteTargetOnHost(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, true)
	target := runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "darwin" {
		// Darwin's runtime architecture maps directly to the manifest target.
	} else if runtime.GOOS == "linux" {
		// Go and release target names use amd64/arm64 consistently.
	} else {
		t.Skip("host is not a supported release target")
	}
	result, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output, ExecuteTarget: target})
	if err != nil {
		t.Fatalf("Verify(execute) error = %v", err)
	}
	if result.ExecutedTarget != target {
		t.Fatalf("ExecutedTarget = %q, want %q", result.ExecutedTarget, target)
	}
}

func TestVerifyExecuteTargetMismatchBeforeExtraction(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, true)
	other := "darwin-arm64"
	if other == runtime.GOOS+"-"+runtime.GOARCH {
		other = "linux-amd64"
	}
	_, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output, ExecuteTarget: other})
	if err == nil || !strings.Contains(err.Error(), "does not match runtime") {
		t.Fatalf("Verify(mismatched execute target) error = %v", err)
	}
}

func TestVerifyRejectsNoncanonicalTarLinkname(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	asset := "claude-rc-proxy-darwin-arm64.tar.gz"
	archive := readArchive(t, filepath.Join(output, asset))
	archive["claude-rc-proxy"].header.Linkname = "unexpected"
	writeVerifyArchive(t, filepath.Join(output, asset), archive)
	updateVerifyChecksum(t, output, asset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted tar Linkname")
	}
}

func TestVerifyRejectsNoncanonicalGzipHeader(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	asset := "claude-rc-proxy-darwin-arm64.tar.gz"
	path := filepath.Join(output, asset)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[8] = 0
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	updateVerifyChecksum(t, output, asset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted noncanonical gzip XFL")
	}
}

func TestVerifyRejectsETDynAndMachOSubtypes(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, true)
	linuxAsset := "claude-rc-proxy-linux-amd64.tar.gz"
	linuxArchive := readArchive(t, filepath.Join(output, linuxAsset))
	linuxBinary := linuxArchive["claude-rc-proxy"].data
	linuxBinary[16] = 3 // ELF e_type = ET_DYN, little endian.
	linuxArchive["claude-rc-proxy"] = archiveFileForVerify(linuxArchive["claude-rc-proxy"].header, linuxBinary)
	writeVerifyArchive(t, filepath.Join(output, linuxAsset), linuxArchive)
	updateVerifyChecksum(t, output, linuxAsset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted ET_DYN ELF")
	}

	// Restore the Linux archive, then mutate the Darwin arm64 subtype to arm64e.
	_, output = buildRealVerifyFixture(t, false)
	asset := "claude-rc-proxy-darwin-arm64.tar.gz"
	archive := readArchive(t, filepath.Join(output, asset))
	binary := archive["claude-rc-proxy"].data
	if len(binary) >= 12 && binary[0] == 0xcf && binary[1] == 0xfa {
		binary[8] = 2
		binary[9], binary[10], binary[11] = 0, 0, 0
	}
	archive["claude-rc-proxy"] = archiveFileForVerify(archive["claude-rc-proxy"].header, binary)
	writeVerifyArchive(t, filepath.Join(output, asset), archive)
	updateVerifyChecksum(t, output, asset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted arm64e Mach-O")
	}
}

func TestVerifyRejectsHardlinkedAndSparseOuterFiles(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	asset := "claude-rc-proxy-darwin-arm64.tar.gz"
	assetPath := filepath.Join(output, asset)
	backup := filepath.Join(t.TempDir(), "archive")
	if err := os.Link(assetPath, backup); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	updateVerifyChecksum(t, output, asset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted hardlinked outer archive")
	}

	manifestPath, output = buildRealVerifyFixture(t, false)
	assetPath = filepath.Join(output, asset)
	file, err := os.OpenFile(assetPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(16 << 20); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	updateVerifyChecksum(t, output, asset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted sparse outer archive")
	}
}

func TestVerifyRejectsPerMemberLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		size int64
	}{
		{name: "LICENSE", size: maxLicenseSize + 1},
		{name: "README.md", size: maxReadmeSize + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifestPath, output := buildRealVerifyFixture(t, false)
			asset := "claude-rc-proxy-darwin-arm64.tar.gz"
			archive := readArchive(t, filepath.Join(output, asset))
			archive[test.name] = archiveFileForVerify(archive[test.name].header, make([]byte, test.size))
			writeVerifyArchive(t, filepath.Join(output, asset), archive)
			updateVerifyChecksum(t, output, asset)
			_, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output})
			if err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("Verify() error = %v, want per-member size rejection", err)
			}
		})
	}
}

func TestVerifyManifestBoundedRegularIdentity(t *testing.T) {
	source, manifestPath := writeRealVerifyFixture(t, false)
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := bytes.Repeat([]byte{'x'}, 1<<20+1)
	if err := os.WriteFile(manifestPath, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted oversized manifest")
	}
	validManifest := filepath.Join(source, "valid-manifest")
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validManifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(source, "manifest-link")
	if err := os.Symlink(validManifest, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Verify(VerifyOptions{ManifestPath: symlink, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted symlink manifest")
	}
}

func TestVerifyRejectsTarPaddingByCanonicalByteComparison(t *testing.T) {
	manifestPath, output := buildRealVerifyFixture(t, false)
	asset := "claude-rc-proxy-darwin-arm64.tar.gz"
	path := filepath.Join(output, asset)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatal(err)
	}
	if err := gzipReader.Close(); err != nil {
		t.Fatal(err)
	}
	payload = append(payload, make([]byte, 512)...)
	var mutated bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&mutated, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.Header.ModTime = time.Time{}
	gzipWriter.Header.OS = 255
	if _, err := gzipWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mutated.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	updateVerifyChecksum(t, output, asset)
	if _, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: output}); err == nil {
		t.Fatal("Verify() accepted padded tar payload")
	}
}

func TestExecuteUsesPrivateWorkingDirectory(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cwd")
	script := []byte("#!/bin/sh\npwd > " + marker + "\nprintf 'tool 1.2.3 (commit " + testCommit + ")\\n'\n")
	if err := executeVerifiedBinaryWithTimeout("tool", "tool", "1.2.3", testCommit, "tool.tar.gz", script, time.Second); err != nil {
		t.Fatalf("executeVerifiedBinaryWithTimeout() error = %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(strings.TrimSpace(string(data))) == filepath.Clean(current) {
		t.Fatal("execution did not use a private working directory")
	}
	if strings.Contains(string(data), "hextap-toolkit-worktrees") {
		t.Fatalf("execution cwd leaked repository path: %q", data)
	}
}

func TestExecuteSeparatesPlatformFilenameFromVersionIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix script fixture")
	}
	script := []byte("#!/bin/sh\nprintf 'tool 1.2.3 (commit " + testCommit + ")\\n'\n")
	if err := executeVerifiedBinaryWithTimeout("tool.exe", "tool", "1.2.3", testCommit, "tool.exe", script, time.Second); err != nil {
		t.Fatalf("executeVerifiedBinaryWithTimeout() error = %v", err)
	}
}

func TestNormalizeVersionOutputAllowsOnlyWindowsTerminalCRLF(t *testing.T) {
	if got := string(normalizeVersionOutput("windows", []byte("tool 1.2.3\r\n"))); got != "tool 1.2.3\n" {
		t.Fatalf("normalized output = %q", got)
	}
	for _, test := range []struct {
		os    string
		input string
		want  string
	}{
		{"linux", "tool 1.2.3\r\n", "tool 1.2.3\r\n"},
		{"windows", "tool\r\n1.2.3\r\n", "tool\r\n1.2.3\n"},
		{"windows", "tool 1.2.3\n\n", "tool 1.2.3\n\n"},
	} {
		if got := string(normalizeVersionOutput(test.os, []byte(test.input))); got != test.want {
			t.Fatalf("normalizeVersionOutput(%q, %q) = %q", test.os, test.input, got)
		}
	}
}

func TestExecuteTimeoutDoesNotWaitForDescendantOutputDescriptor(t *testing.T) {
	script := []byte("#!/bin/sh\n(sleep 1) &\nsleep 5\n")
	started := time.Now()
	err := executeVerifiedBinaryWithTimeout("tool", "tool", "1.2.3", testCommit, "tool.tar.gz", script, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("execute timeout error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout waited for descendant: %s", elapsed)
	}
}

func TestExecuteStopsAtLiveOutputLimit(t *testing.T) {
	script := []byte("#!/bin/sh\nyes x | head -c 200000\nsleep 2\n")
	started := time.Now()
	err := executeVerifiedBinaryWithTimeout("tool", "tool", "1.2.3", testCommit, "tool.tar.gz", script, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("output limit error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("live output overflow was not stopped promptly: %s", elapsed)
	}
}

func archiveFileForVerify(header *tar.Header, data []byte) archiveFile {
	copyHeader := *header
	return archiveFile{header: &copyHeader, data: data}
}

func writeVerifyArchive(t *testing.T, path string, files map[string]archiveFile) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.Header.ModTime = time.Time{}
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"claude-rc-proxy", "LICENSE", "README.md"} {
		header := *files[name].header
		header.Size = int64(len(files[name].data))
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(files[name].data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func updateVerifyChecksum(t *testing.T, output, asset string) {
	t.Helper()
	path := filepath.Join(output, asset)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	checksumsPath := filepath.Join(output, "SHA256SUMS")
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(checksums), "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, "  "+asset) {
			lines[i] = hex.EncodeToString(sum[:]) + "  " + asset
		}
	}
	if err := os.WriteFile(checksumsPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}
