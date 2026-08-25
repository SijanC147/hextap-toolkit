package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
