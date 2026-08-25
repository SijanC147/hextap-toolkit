package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
