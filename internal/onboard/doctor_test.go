package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeGH(t *testing.T, project string) (logPath string) {
	t.Helper()
	directory := t.TempDir()
	logPath = filepath.Join(directory, "gh.log")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_GH_LOG"
case "$*" in
  "auth status --hostname github.com") exit 0 ;;
  "api repos/SijanC147/example-tool --jq .default_branch") printf '%s\n' main ;;
  "api repos/SijanC147/example-tool/immutable-releases --jq .enabled")
    if [ "${FAKE_GH_MODE:-success}" = immutable ]; then printf '%s\n' false; else printf '%s\n' true; fi ;;
  "api --paginate repos/SijanC147/example-tool/actions/secrets --jq .secrets[].name")
    if [ "${FAKE_GH_MODE:-success}" != secret ]; then printf '%s\n' OP_SERVICE_ACCOUNT_TOKEN; fi ;;
  "api --paginate repos/SijanC147/example-tool/rulesets --jq "*)
    printf '%s\n' hextap/main
    if [ "${FAKE_GH_MODE:-success}" != ruleset ]; then printf '%s\n' hextap/release-tags; fi ;;
  "api repos/SijanC147/hextap-toolkit/commits/v1.2.3 --jq .sha")
    if [ "${FAKE_GH_MODE:-success}" = toolkit ]; then printf '%s\n' 1111111111111111111111111111111111111111; else printf '%s\n' ` + testToolkitSHA + `; fi ;;
  "api -H Accept: application/vnd.github.raw+json repos/SijanC147/homebrew-hextap/contents/Projects/example-tool.json")
    if [ "${FAKE_GH_MODE:-success}" = tap ]; then printf '%s\n' '{"schema":1}'; else cat "$FAKE_TAP_PATH"; fi ;;
  "api -H Accept: application/vnd.github.raw+json repos/SijanC147/homebrew-hextap/contents/Formula/example-tool.rb")
    if [ "${FAKE_GH_MODE:-success}" = formula ]; then printf '%s\n' 'class Wrong < Formula'; else printf '%s\n' 'class ExampleTool < Formula'; fi ;;
  *) exit 44 ;;
esac
`
	path := filepath.Join(directory, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GH_LOG", logPath)
	t.Setenv("FAKE_TAP_PATH", filepath.Join(project, ".hextap.json"))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func TestDoctorLocalMakesNoGHCallsAndOnlineIsReadOnly(t *testing.T) {
	project := writeGoProject(t)
	if _, err := Onboard(validOptions(project)); err != nil {
		t.Fatal(err)
	}
	logPath := writeFakeGH(t, project)
	local, err := Doctor(DoctorOptions{Project: project})
	if err != nil {
		t.Fatalf("Doctor(local) error = %v", err)
	}
	if len(local.Checks) != 4 {
		t.Fatalf("local checks = %v", local.Checks)
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("local doctor invoked gh: %s", data)
	}
	online, err := Doctor(DoctorOptions{Project: project, Online: true})
	if err != nil {
		t.Fatalf("Doctor(online) error = %v", err)
	}
	if len(online.Checks) != 11 {
		t.Fatalf("online checks = %v", online.Checks)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"--method", "POST", "PUT", "PATCH", "DELETE", "secret set"} {
		if strings.Contains(string(log), mutation) {
			t.Fatalf("online doctor used mutation verb %q:\n%s", mutation, log)
		}
	}
}

func TestDoctorOnlineReportsMissingRemoteInvariants(t *testing.T) {
	tests := map[string]string{
		"immutable releases": "immutable",
		"secret":             "secret",
		"ruleset":            "ruleset",
		"toolkit provenance": "toolkit",
		"tap mismatch":       "tap",
		"Formula class":      "formula",
	}
	for name, mode := range tests {
		t.Run(name, func(t *testing.T) {
			project := writeGoProject(t)
			if _, err := Onboard(validOptions(project)); err != nil {
				t.Fatal(err)
			}
			writeFakeGH(t, project)
			t.Setenv("FAKE_GH_MODE", mode)
			if _, err := Doctor(DoctorOptions{Project: project, Online: true}); err == nil {
				t.Fatal("Doctor(online) unexpectedly succeeded")
			}
		})
	}
}
