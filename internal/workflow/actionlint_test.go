package workflow_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedActionlintGateFailsClosed(t *testing.T) {
	queueDiagnostic := `.github/workflows/release-go.yml:1:1: unexpected key "queue" for "concurrency" section. expected one of "cancel-in-progress", "group" [syntax-check]`
	workflowDiagnostic := `.github/workflows/release-go.yml:1:1: property "workflow_sha" is not defined in object type {status: string} [expression]`
	exactDiagnostics := queueDiagnostic + "\n" + strings.Repeat(workflowDiagnostic+"\n", 5)

	tests := []struct {
		name        string
		diagnostics string
		exitCode    int
		wantSuccess bool
	}{
		{name: "exact six", diagnostics: exactDiagnostics, exitCode: 1, wantSuccess: true},
		{name: "clean no-op", exitCode: 0},
		{name: "silent failure", exitCode: 1},
		{name: "missing diagnostic", diagnostics: queueDiagnostic + "\n" + strings.Repeat(workflowDiagnostic+"\n", 4), exitCode: 1},
		{name: "extra diagnostic", diagnostics: exactDiagnostics + ".github/workflows/ci.yml:1:1: unexpected failure [syntax-check]\n", exitCode: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bin := t.TempDir()
			actionlint := filepath.Join(bin, "actionlint")
			script := "#!/bin/sh\n" +
				"if [ \"$1\" = -version ]; then printf '%s\\n' v1.7.12; exit 0; fi\n" +
				"printf '%s' \"$FAKE_ACTIONLINT_DIAGNOSTICS\" >&2\n" +
				"exit \"$FAKE_ACTIONLINT_EXIT\"\n"
			if err := os.WriteFile(actionlint, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}

			command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts", "check-actionlint.sh"))
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(),
				"PATH="+bin+":/usr/bin:/bin",
				"FAKE_ACTIONLINT_DIAGNOSTICS="+test.diagnostics,
				"FAKE_ACTIONLINT_EXIT="+string(rune('0'+test.exitCode)),
			)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			err := command.Run()
			if test.wantSuccess && err != nil {
				t.Fatalf("gate failed: %v\n%s", err, output.String())
			}
			if !test.wantSuccess && err == nil {
				t.Fatalf("gate accepted invalid checker behavior:\n%s", output.String())
			}
		})
	}
}
