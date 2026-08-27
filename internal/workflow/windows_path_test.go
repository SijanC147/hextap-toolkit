package workflow_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRunnerTempScript(t *testing.T) {
	script := filepath.Join(repositoryRoot(t), "scripts", "normalize-runner-temp.sh")

	t.Run("passes through existing Unix directory", func(t *testing.T) {
		directory := t.TempDir()
		command := exec.Command("bash", script, "Linux", directory)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			t.Fatalf("normalize Linux runner temp: %v\n%s", err, stderr.String())
		}
		if got := strings.TrimSpace(string(output)); got != directory {
			t.Fatalf("normalized Linux path = %q, want %q", got, directory)
		}
	})

	t.Run("uses cygpath for native Windows directory", func(t *testing.T) {
		bin := t.TempDir()
		normalized := t.TempDir()
		logPath := filepath.Join(t.TempDir(), "cygpath.log")
		writeFile(t, filepath.Join(bin, "cygpath"), `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$CYGPATH_LOG"
printf '%s\n' "$NORMALIZED_RUNNER_TEMP"
`, 0o700)
		command := exec.Command("bash", script, "Windows", `D:\a\_temp`)
		command.Env = append(os.Environ(),
			"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"CYGPATH_LOG="+logPath,
			"NORMALIZED_RUNNER_TEMP="+normalized,
		)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			t.Fatalf("normalize Windows runner temp: %v\n%s", err, stderr.String())
		}
		if got := strings.TrimSpace(string(output)); got != normalized {
			t.Fatalf("normalized Windows path = %q, want %q", got, normalized)
		}
		arguments, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(arguments)); got != `-u D:\a\_temp` {
			t.Fatalf("cygpath arguments = %q", got)
		}
	})

	tests := map[string]struct {
		arguments []string
		path      string
		want      string
	}{
		"missing arguments":  {arguments: nil, want: "expected runner OS and runner temp arguments"},
		"unsupported OS":     {arguments: []string{"Other", "/tmp"}, want: "unsupported runner OS"},
		"relative Unix path": {arguments: []string{"macOS", "relative"}, want: "normalized runner temp is not an absolute POSIX path"},
		"missing cygpath":    {arguments: []string{"Windows", `D:\a\_temp`}, path: t.TempDir(), want: "cygpath is required on Windows"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("bash", append([]string{script}, test.arguments...)...)
			if test.path != "" {
				command.Env = append(os.Environ(), "PATH="+test.path)
			}
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("normalize runner temp unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(string(output), "::error title=Runner temp path::"+test.want) {
				t.Fatalf("diagnostic = %q, want %q", output, test.want)
			}
		})
	}
}
