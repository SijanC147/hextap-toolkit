package release

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
