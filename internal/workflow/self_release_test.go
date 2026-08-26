package workflow_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
	"github.com/SijanC147/hextap-toolkit/internal/release"
)

const selfReleaseCommit = "0123456789abcdef0123456789abcdef01234567"

func TestToolkitSelfManifestContract(t *testing.T) {
	data := []byte(readRepositoryFile(t, ".hextap.json"))
	project, err := manifest.Parse(data)
	if err != nil {
		t.Fatalf("parse toolkit self-manifest: %v", err)
	}

	if project.Schema != 1 || project.Formula.Name != "hextap" || project.Formula.Class != "Hextap" {
		t.Fatalf("formula identity = schema %d, %q, %q", project.Schema, project.Formula.Name, project.Formula.Class)
	}
	if got, want := project.RepositorySlug(), "SijanC147/hextap-toolkit"; got != want {
		t.Fatalf("repository = %q, want %q", got, want)
	}
	if got, want := project.Formula.Binary, "brew-hextap"; got != want {
		t.Fatalf("binary = %q, want %q", got, want)
	}
	if got, want := project.Formula.Assets.DarwinARM64, "hextap-darwin-arm64.tar.gz"; got != want {
		t.Fatalf("Darwin arm64 asset = %q, want %q", got, want)
	}
	if got, want := project.Formula.Assets.DarwinAMD64, "hextap-darwin-amd64.tar.gz"; got != want {
		t.Fatalf("Darwin amd64 asset = %q, want %q", got, want)
	}
	if project.Release.BuildScript != "scripts/hextap-build" || !project.Release.Linux {
		t.Fatalf("release contract = %#v", project.Release)
	}
	if len(project.Homebrew.TestArgs) != 1 || project.Homebrew.TestArgs[0] != "--version" {
		t.Fatalf("Homebrew test args = %v", project.Homebrew.TestArgs)
	}
	if project.Homebrew.Service == nil || project.Homebrew.Service.Enabled {
		t.Fatalf("Homebrew service must use the explicit disabled shape, got %#v", project.Homebrew.Service)
	}
	if project.Homebrew.Caveats != "" {
		t.Fatalf("Homebrew caveats = %q, want empty", project.Homebrew.Caveats)
	}
}

func TestToolkitSelfReleaseCallerContract(t *testing.T) {
	caller := readRepositoryFile(t, ".github/workflows/hextap-release.yml")
	want := `name: Hextap toolkit release

on:
  push:
    tags:
      - "v*"
  workflow_dispatch:
    inputs:
      tag:
        description: Existing stable release tag
        required: true
        type: string

permissions:
  attestations: write
  contents: write
  id-token: write

jobs:
  release:
    uses: ./.github/workflows/release-go.yml
    with:
      manifest_path: .hextap.json
      tag: ${{ github.event_name == 'workflow_dispatch' && inputs.tag || github.ref_name }}
      mode: ${{ github.event_name == 'workflow_dispatch' && 'homebrew-only' || 'full' }}
    secrets:
      op_service_account_token: ${{ secrets.OP_SERVICE_ACCOUNT_TOKEN }}
`
	if caller != want {
		t.Fatalf("self-release caller does not match the exact same-commit contract:\n%s", caller)
	}
	assertContains(t, caller, "uses: ./.github/workflows/release-go.yml")
	assertNotContains(t, caller, "@main")
	assertNotContains(t, caller, "secrets: inherit")
	assertCount(t, caller, "OP_SERVICE_ACCOUNT_TOKEN", 1)
}

func TestToolkitBuildAdapterRejectsInvalidReleaseEnvironment(t *testing.T) {
	root := repositoryRoot(t)
	adapter := filepath.Join(root, "scripts", "hextap-build")
	info, err := os.Stat(adapter)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("adapter mode = %04o, want 0755", got)
	}
	source := readRepositoryFile(t, "scripts/hextap-build")
	assertContains(t, source, "./cmd/brew-hextap")
	assertNotContains(t, source, "./cmd/hextapctl")
	assertNotContains(t, source, "go build ./...")

	tests := []struct {
		name, targetOS, targetArch, version, commit string
	}{
		{name: "target OS", targetOS: "windows", targetArch: "arm64", version: "0.1.0", commit: selfReleaseCommit},
		{name: "target architecture", targetOS: "darwin", targetArch: "386", version: "0.1.0", commit: selfReleaseCommit},
		{name: "leading v version", targetOS: "darwin", targetArch: "arm64", version: "v0.1.0", commit: selfReleaseCommit},
		{name: "version build metadata", targetOS: "darwin", targetArch: "arm64", version: "0.1.0+build", commit: selfReleaseCommit},
		{name: "uppercase commit", targetOS: "darwin", targetArch: "arm64", version: "0.1.0", commit: strings.ToUpper(selfReleaseCommit)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "brew-hextap")
			command := exec.Command(adapter)
			command.Dir = root
			command.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"HEXTAP_TARGET_OS=" + test.targetOS,
				"HEXTAP_TARGET_ARCH=" + test.targetArch,
				"HEXTAP_OUTPUT=" + output,
				"HEXTAP_VERSION=" + test.version,
				"HEXTAP_COMMIT=" + test.commit,
			}
			if err := command.Run(); err == nil {
				t.Fatal("adapter accepted invalid release environment")
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid adapter invocation created output: %v", err)
			}
		})
	}
}

func TestToolkitSelfReleaseBuildIsDeterministicAndVerifiable(t *testing.T) {
	root := repositoryRoot(t)
	manifestPath := filepath.Join(root, ".hextap.json")
	outputs := []string{t.TempDir(), t.TempDir()}
	wantAssets := []string{
		"hextap-darwin-amd64.tar.gz",
		"hextap-darwin-arm64.tar.gz",
		"hextap-linux-amd64.tar.gz",
		"hextap-linux-arm64.tar.gz",
	}

	for _, output := range outputs {
		result, err := release.Build(release.BuildOptions{
			ManifestPath: manifestPath,
			Version:      "0.1.0",
			Commit:       selfReleaseCommit,
			SourceDir:    root,
			OutputDir:    output,
		})
		if err != nil {
			t.Fatalf("build toolkit self-release: %v", err)
		}
		if got := strings.Join(result.Assets, "\n"); got != strings.Join(wantAssets, "\n") {
			t.Fatalf("release assets = %v, want %v", result.Assets, wantAssets)
		}
		for _, asset := range wantAssets {
			if got, want := archiveMembers(t, filepath.Join(output, asset)), []string{"brew-hextap", "LICENSE", "README.md"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("%s members = %v, want %v", asset, got, want)
			}
		}
	}

	firstEntries, err := os.ReadDir(outputs[0])
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(firstEntries))
	for _, entry := range firstEntries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		first, err := os.ReadFile(filepath.Join(outputs[0], name))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(filepath.Join(outputs[1], name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) {
			t.Errorf("self-release output %s is not deterministic", name)
		}
	}

	target := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	verified, err := release.Verify(release.VerifyOptions{
		ManifestPath:  manifestPath,
		Version:       "0.1.0",
		Commit:        selfReleaseCommit,
		Directory:     outputs[0],
		ExecuteTarget: target,
	})
	if err != nil {
		t.Fatalf("verify toolkit self-release: %v", err)
	}
	if verified.ExecutedTarget != target {
		t.Fatalf("executed target = %q, want %q", verified.ExecutedTarget, target)
	}
}

func archiveMembers(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var result []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, header.Name)
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
	}
}
