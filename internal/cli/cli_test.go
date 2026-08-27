package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/release"
)

const (
	cliArmSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cliAmdSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func exampleManifest(t *testing.T) string {
	t.Helper()
	source := filepath.Join("..", "..", "examples", "claude-rc-proxy.json")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile(example): %v", err)
	}
	path := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(example): %v", err)
	}
	return path
}

func execute(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(args, &stdout, &stderr, "0.9.0", "abc1234")
	return exitCode, stdout.String(), stderr.String()
}

func TestVersionCommand(t *testing.T) {
	exitCode, stdout, stderr := execute("version")
	if exitCode != 0 || stdout != "hextapctl 0.9.0 (commit abc1234)\n" || stderr != "" {
		t.Fatalf("Run(version) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestReleaseVerifyCommandSmoke(t *testing.T) {
	source := t.TempDir()
	manifest := filepath.Join(source, ".hextap.json")
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"linux": true`), []byte(`"linux": false`), 1)
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "LICENSE"), []byte("license\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("readme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(source, "scripts", "hextap-build")
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\nset -eu\nGOOS=darwin GOARCH=\"$HEXTAP_TARGET_ARCH\" go build -o \"$HEXTAP_OUTPUT\" ./main.go\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Build(release.BuildOptions{ManifestPath: manifest, Version: "1.2.3", Commit: "0123456", SourceDir: source, OutputDir: directory}); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("release", "verify", "--manifest", manifest, "--version", "1.2.3", "--commit", "0123456", "--dir", directory)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "release verified: "+directory) {
		t.Fatalf("Run(verify) = code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestManifestValidateCommand(t *testing.T) {
	path := exampleManifest(t)
	exitCode, stdout, stderr := execute("manifest", "validate", "--file", path)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(validate) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := "manifest valid: " + path + " (schema 1, formula claude-rc-proxy)\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestManifestValidateReportsActionableError(t *testing.T) {
	path := exampleManifest(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"schema": 1,`), []byte(`"schema": 1, "unknown": true,`), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute("manifest", "validate", "--file", path)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "error: validate manifest") || !strings.Contains(stderr, "unknown field") {
		t.Fatalf("Run(invalid validate) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestReleaseMetadataCommandAndGitHubOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(output, []byte("existing=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute(
		"release", "metadata",
		"--tag", "v1.2.3-rc.1",
		"--mode", "full",
		"--github-output", output,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(release metadata) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if want := "release metadata: v1.2.3-rc.1 (version 1.2.3-rc.1, stable false, prerelease true, mode full)\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := "existing=value\ntag=v1.2.3-rc.1\nversion=1.2.3-rc.1\nstable=false\nprerelease=true\nmode=full\n"
	if string(data) != wantOutput {
		t.Fatalf("GitHub output = %q, want %q", data, wantOutput)
	}
}

func TestReleaseMetadataFailureDoesNotAppend(t *testing.T) {
	output := filepath.Join(t.TempDir(), "github-output")
	const sentinel = "existing=value\n"
	if err := os.WriteFile(output, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute(
		"release", "metadata",
		"--tag", "v1.2.3\ninjected=true",
		"--mode", "full",
		"--github-output", output,
	)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "error: release metadata") {
		t.Fatalf("Run(invalid metadata) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != sentinel {
		t.Fatalf("GitHub output = %q, error = %v", data, err)
	}
}

func TestReleaseBuildCommand(t *testing.T) {
	source := t.TempDir()
	manifestData, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestData = bytes.Replace(manifestData, []byte(`"linux": true`), []byte(`"linux": false`), 1)
	manifestPath := filepath.Join(source, ".hextap.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"LICENSE": "license\n", "README.md": "readme\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	adapter := filepath.Join(source, "scripts", "hextap-build")
	if err := os.MkdirAll(filepath.Dir(adapter), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\nset -eu\nprintf '%s-%s' \"$HEXTAP_TARGET_OS\" \"$HEXTAP_TARGET_ARCH\" > \"$HEXTAP_OUTPUT\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute(
		"release", "build",
		"--manifest", manifestPath,
		"--version", "1.2.3",
		"--commit", "0123456789abcdef",
		"--source", source,
		"--output", output,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(release build) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := "release built: " + output + " (claude-rc-proxy 1.2.3, 2 assets)\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	for _, name := range []string{"claude-rc-proxy-darwin-arm64.tar.gz", "claude-rc-proxy-darwin-amd64.tar.gz", "SHA256SUMS"} {
		if info, err := os.Stat(filepath.Join(output, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("output %s missing or nonregular: %v", name, err)
		}
	}
}

func TestManifestExportCommand(t *testing.T) {
	manifestPath := exampleManifest(t)
	output := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute(
		"manifest", "export",
		"--file", manifestPath,
		"--repository", "SijanC147/claude-rc-proxy",
		"--github-output", output,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(manifest export) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if want := "manifest exported: claude-rc-proxy (SijanC147/claude-rc-proxy)\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := "formula=claude-rc-proxy\nbinary=claude-rc-proxy\nowner=SijanC147\nrepository_name=claude-rc-proxy\nrepository=SijanC147/claude-rc-proxy\narm64_asset=claude-rc-proxy-darwin-arm64.tar.gz\namd64_asset=claude-rc-proxy-darwin-amd64.tar.gz\nbuild_script=scripts/hextap-build\nlinux=true\nruntime=go\nnative_matrix={\"include\":[{\"runner\":\"ubuntu-24.04\",\"target\":\"linux-amd64\"},{\"runner\":\"ubuntu-24.04-arm\",\"target\":\"linux-arm64\"},{\"runner\":\"macos-15\",\"target\":\"darwin-arm64\"},{\"runner\":\"macos-15-intel\",\"target\":\"darwin-amd64\"}]}\n"
	if string(data) != wantOutput {
		t.Fatalf("GitHub output = %q, want %q", data, wantOutput)
	}
}

func TestManifestExportBunProfileIncludesPinnedRuntimeAndWindowsMatrix(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "examples", "better-ccflare.json")
	output := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, _, stderr := execute(
		"manifest", "export",
		"--file", manifestPath,
		"--repository", "SijanC147/better-ccflare",
		"--github-output", output,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("manifest export = code %d, stderr %q", exitCode, stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"runtime=bun\n",
		"runtime_version=1.3.14\n",
		`{"runner":"windows-2025","target":"windows-amd64"}`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("GitHub output %q lacks %q", data, expected)
		}
	}
}

func TestManifestExportRepositoryMismatchDoesNotAppend(t *testing.T) {
	manifestPath := exampleManifest(t)
	output := filepath.Join(t.TempDir(), "github-output")
	const sentinel = "existing=value\n"
	if err := os.WriteFile(output, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute(
		"manifest", "export",
		"--file", manifestPath,
		"--repository", "SijanC147/other",
		"--github-output", output,
	)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "does not match") {
		t.Fatalf("Run(mismatched export) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != sentinel {
		t.Fatalf("GitHub output = %q, error = %v", data, err)
	}
}

func TestFormulaRenderAndUpdateCommands(t *testing.T) {
	manifestPath := exampleManifest(t)
	formulaPath := filepath.Join(t.TempDir(), "ClaudeRcProxy.rb")
	exitCode, stdout, stderr := execute(
		"formula", "render",
		"--manifest", manifestPath,
		"--version", "0.1.0",
		"--arm64-sha", cliArmSHA,
		"--amd64-sha", cliAmdSHA,
		"--output", formulaPath,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(render) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if want := "formula rendered: " + formulaPath + " (claude-rc-proxy 0.1.0)\n"; stdout != want {
		t.Fatalf("render stdout = %q, want %q", stdout, want)
	}
	if data, err := os.ReadFile(formulaPath); err != nil || !bytes.Contains(data, []byte("/v0.1.0/")) {
		t.Fatalf("rendered Formula = %q, error = %v", data, err)
	}

	newArm := strings.Repeat("c", 64)
	newAMD := strings.Repeat("d", 64)
	exitCode, stdout, stderr = execute(
		"formula", "update",
		"--manifest", manifestPath,
		"--formula", formulaPath,
		"--version", "0.2.0",
		"--arm64-sha", newArm,
		"--amd64-sha", newAMD,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(update) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if want := "formula updated: " + formulaPath + " (0.1.0 -> 0.2.0)\n"; stdout != want {
		t.Fatalf("update stdout = %q, want %q", stdout, want)
	}

	exitCode, stdout, stderr = execute(
		"formula", "update",
		"--manifest", manifestPath,
		"--formula", formulaPath,
		"--version", "0.2.0",
		"--arm64-sha", newArm,
		"--amd64-sha", newAMD,
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(idempotent update) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if want := "formula unchanged: " + formulaPath + " (0.2.0)\n"; stdout != want {
		t.Fatalf("idempotent stdout = %q, want %q", stdout, want)
	}
}

func TestFormulaUpdateProfileRequiresAndUsesTapTemplate(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "examples", "better-ccflare.json")
	template := `class BetterCcflare < Formula
  if Hardware::CPU.arm?
    url "@ARM64_URL@"
    sha256 "@ARM64_SHA256@"
  else
    url "@AMD64_URL@"
    sha256 "@AMD64_SHA256@"
  end
  marker = "#{send(:url, 'tap-owned-code')}"
end
`
	formula := strings.NewReplacer(
		"@ARM64_URL@", "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz",
		"@ARM64_SHA256@", cliArmSHA,
		"@AMD64_URL@", "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-x86_64.tar.gz",
		"@AMD64_SHA256@", cliAmdSHA,
	).Replace(template)
	directory := t.TempDir()
	formulaPath := filepath.Join(directory, "better-ccflare.rb")
	templatePath := filepath.Join(directory, "better-ccflare.rb.tmpl")
	if err := os.WriteFile(formulaPath, []byte(formula), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	common := []string{
		"formula", "update",
		"--manifest", manifestPath,
		"--formula", formulaPath,
		"--version", "3.8.2",
		"--arm64-sha", strings.Repeat("c", 64),
		"--amd64-sha", strings.Repeat("d", 64),
	}
	if code, _, stderr := execute(common...); code == 0 || !strings.Contains(stderr, "--template is required") {
		t.Fatalf("profile update without template = code %d, stderr %q", code, stderr)
	}
	code, stdout, stderr := execute(append(common, "--template", templatePath)...)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "formula updated") {
		t.Fatalf("profile update = code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	updated, err := os.ReadFile(formulaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "/v3.8.2/") || !strings.Contains(string(updated), strings.Repeat("c", 64)) || !strings.Contains(string(updated), strings.Repeat("d", 64)) {
		t.Fatalf("updated profile Formula metadata is wrong:\n%s", updated)
	}
}

func TestFormulaRenderCLIErrorDoesNotMutateDestination(t *testing.T) {
	manifestPath := exampleManifest(t)
	formulaPath := filepath.Join(t.TempDir(), "ClaudeRcProxy.rb")
	const sentinel = "do not replace\n"
	if err := os.WriteFile(formulaPath, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := execute(
		"formula", "render",
		"--manifest", manifestPath,
		"--version", "v0.1.0",
		"--arm64-sha", cliArmSHA,
		"--amd64-sha", cliAmdSHA,
		"--output", formulaPath,
	)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "error: render Formula") {
		t.Fatalf("Run(invalid render) = code %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	data, err := os.ReadFile(formulaPath)
	if err != nil || string(data) != sentinel {
		t.Fatalf("destination = %q, error = %v", data, err)
	}
}

func TestCLIRejectsMissingUnknownAndExtraArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"version", "extra"},
		{"manifest"},
		{"manifest", "unknown"},
		{"manifest", "validate"},
		{"formula"},
		{"formula", "unknown"},
		{"release"},
		{"release", "unknown"},
		{"release", "metadata"},
		{"release", "build", "--manifest", "x"},
		{"formula", "render", "--manifest", "x"},
		{"formula", "update", "extra"},
	}
	for _, args := range tests {
		exitCode, stdout, stderr := execute(args...)
		if exitCode == 0 || stdout != "" || !strings.HasPrefix(stderr, "error: ") || !strings.HasSuffix(stderr, "\n") {
			t.Errorf("Run(%q) = code %d, stdout %q, stderr %q", args, exitCode, stdout, stderr)
		}
	}
}
