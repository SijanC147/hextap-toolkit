package release

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const profileBuildManifest = `{
  "schema": 2,
  "formula": {
    "name": "better-ccflare",
    "class": "BetterCcflare",
    "description": "Claude API proxy with intelligent load balancing across multiple accounts",
    "homepage": "https://github.com/SijanC147/better-ccflare",
    "license": "MIT",
    "repository": {"owner": "SijanC147", "name": "better-ccflare"},
    "binary": "better-ccflare",
    "assets": {
      "darwin_arm64": "better-ccflare-macos-arm64.tar.gz",
      "darwin_amd64": "better-ccflare-macos-x86_64.tar.gz"
    }
  },
  "release": {
    "build_script": "scripts/hextap-build",
    "profile": {
      "runtime": "bun",
      "runtime_version": "1.3.14",
      "install": {"name": "install", "argv": ["bun", "install", "--frozen-lockfile"]},
      "quality": [{"name": "test", "argv": ["bun", "test"]}],
      "prepare": []
    },
    "targets": {
      "darwin_arm64": {
        "binary": "better-ccflare-macos-arm64",
        "archive": "better-ccflare-macos-arm64.tar.gz",
        "archive_contents": "binary"
      },
      "darwin_amd64": {
        "binary": "better-ccflare-macos-x86_64",
        "archive": "better-ccflare-macos-x86_64.tar.gz",
        "archive_contents": "binary"
      },
      "linux_arm64": {"binary": "better-ccflare-linux-arm64"},
      "linux_amd64": {"binary": "better-ccflare-linux-amd64"},
      "windows_amd64": {"binary": "better-ccflare-windows-x64.exe"}
    }
  },
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "formula_profile": "better-ccflare",
    "service_enabled": true
  }
}`

func writeProfileBuildFixture(t *testing.T) (source, manifestPath string) {
	t.Helper()
	source = t.TempDir()
	for name, contents := range map[string]string{
		".hextap.json": profileBuildManifest,
		"LICENSE":      "license contents\n",
		"README.md":    "readme contents\n",
		"scripts/hextap-build": `#!/bin/sh
set -eu
printf '%s|%s|%s|%s' "$HEXTAP_TARGET_OS" "$HEXTAP_TARGET_ARCH" "$HEXTAP_VERSION" "$HEXTAP_COMMIT" > "$HEXTAP_OUTPUT"
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
	return source, filepath.Join(source, ".hextap.json")
}

func TestProfileBuildPreservesExplicitRawAndSingleBinaryArchiveContract(t *testing.T) {
	source, manifestPath := writeProfileBuildFixture(t)
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Build(buildOptions(source, manifestPath, output))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{
		"better-ccflare-linux-amd64",
		"better-ccflare-linux-arm64",
		"better-ccflare-macos-arm64",
		"better-ccflare-macos-arm64.tar.gz",
		"better-ccflare-macos-x86_64",
		"better-ccflare-macos-x86_64.tar.gz",
		"better-ccflare-windows-x64.exe",
	}
	sort.Strings(want)
	if strings.Join(result.Assets, "\n") != strings.Join(want, "\n") {
		t.Fatalf("assets = %v, want %v", result.Assets, want)
	}

	pairs := [][2]string{
		{"better-ccflare-macos-arm64", "better-ccflare-macos-arm64.tar.gz"},
		{"better-ccflare-macos-x86_64", "better-ccflare-macos-x86_64.tar.gz"},
	}
	for _, pair := range pairs {
		raw, err := os.ReadFile(filepath.Join(output, pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		archive := readArchive(t, filepath.Join(output, pair[1]))
		if len(archive) != 1 {
			t.Fatalf("%s members = %v, want only the executable", pair[1], archive)
		}
		if !bytes.Equal(archive["better-ccflare"].data, raw) {
			t.Fatalf("%s executable differs from %s", pair[1], pair[0])
		}
	}
	assertChecksums(t, output)

	second := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(buildOptions(source, manifestPath, second)); err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	for _, name := range append(append([]string(nil), want...), "SHA256SUMS") {
		firstData, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		secondData, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstData, secondData) {
			t.Fatalf("%s differs across identical profile builds", name)
		}
	}
}

func TestProfileBuildRequiresFullSourceCommitIdentity(t *testing.T) {
	source, manifestPath := writeProfileBuildFixture(t)
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	options := buildOptions(source, manifestPath, output)
	options.Commit = "abcdef1"
	if _, err := Build(options); err == nil || !strings.Contains(err.Error(), "full 40- or 64-character") {
		t.Fatalf("Build(short profile commit) error = %v", err)
	}
}
