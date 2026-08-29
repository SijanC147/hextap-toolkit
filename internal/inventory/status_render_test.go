package inventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

var ansiSequencePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestStatusHumanOutputContainsEveryFullyPopulatedJSONValue(t *testing.T) {
	report := fullyPopulatedStatusReport()
	var styled bytes.Buffer
	renderStatus(&styled, report, true)
	plain := stripStatusANSI(styled.String())

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, value := range jsonScalarValues(document) {
		if !strings.Contains(plain, value) {
			t.Errorf("plain status output is missing JSON scalar %q\noutput:\n%s", value, plain)
		}
	}
}

func TestStatusStylingChangesOnlyANSIBytes(t *testing.T) {
	report := fullyPopulatedStatusReport()
	var plain, styled bytes.Buffer
	renderStatus(&plain, report, false)
	renderStatus(&styled, report, true)
	if !strings.Contains(styled.String(), "\x1b[") {
		t.Fatalf("styled output contains no ANSI: %q", styled.String())
	}
	if got := stripStatusANSI(styled.String()); got != plain.String() {
		t.Fatalf("stripped styled output differs from plain\nstyled stripped:\n%s\nplain:\n%s", got, plain.String())
	}
}

func TestStatusOutputGoldens(t *testing.T) {
	warnings := emptyStatusReport()
	warnings.Warnings = []Warning{
		{Component: "tap.git.revision", Message: "tap Git metadata is unavailable"},
		{Component: "formula.hextap", Message: "Homebrew Formula metadata is unavailable"},
	}
	tests := []struct {
		name   string
		report Report
		styled bool
	}{
		{name: "plain", report: fullyPopulatedStatusReport()},
		{name: "styled", report: fullyPopulatedStatusReport(), styled: true},
		{name: "warnings", report: warnings},
		{name: "empty", report: emptyStatusReport()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			renderStatus(&output, test.report, test.styled)
			actual := strings.ReplaceAll(output.String(), "\x1b", "<ESC>")
			expected, err := os.ReadFile(filepath.Join("testdata", "status_"+test.name+".golden"))
			if err != nil {
				t.Fatalf("read golden: %v\nactual:\n%s", err, actual)
			}
			if actual != string(expected) {
				t.Fatalf("status %s golden mismatch\nactual:\n%s\nexpected:\n%s", test.name, actual, expected)
			}
		})
	}
}

func TestRenderStatusUsesNoANSIForOrdinaryWriters(t *testing.T) {
	var output bytes.Buffer
	RenderStatus(&output, fullyPopulatedStatusReport())
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("RenderStatus(buffer) emitted ANSI: %q", output.String())
	}
}

func TestStatusTerminalColorPolicy(t *testing.T) {
	tests := []struct {
		name  string
		goos  string
		isTTY bool
		env   map[string]string
		want  bool
	}{
		{name: "Darwin terminal", goos: "darwin", isTTY: true, env: map[string]string{"TERM": "xterm-256color"}, want: true},
		{name: "Linux terminal", goos: "linux", isTTY: true, env: map[string]string{"TERM": "screen"}, want: true},
		{name: "pipe", goos: "darwin", env: map[string]string{"TERM": "xterm-256color"}},
		{name: "Windows", goos: "windows", isTTY: true, env: map[string]string{"TERM": "xterm-256color"}},
		{name: "NO_COLOR set", goos: "darwin", isTTY: true, env: map[string]string{"NO_COLOR": "", "TERM": "xterm-256color"}},
		{name: "dumb terminal", goos: "darwin", isTTY: true, env: map[string]string{"TERM": "dumb"}},
		{name: "case-insensitive dumb terminal", goos: "darwin", isTTY: true, env: map[string]string{"TERM": "DUMB"}},
		{name: "TERM absent", goos: "darwin", isTTY: true, env: map[string]string{}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				value, ok := test.env[name]
				return value, ok
			}
			if got := statusColorEnabled(statusTerminalContext{GOOS: test.goos, IsTTY: test.isTTY, LookupEnv: lookup}); got != test.want {
				t.Fatalf("statusColorEnabled() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestWriterIsTerminalRejectsBuffersRegularFilesPipesAndDevNull(t *testing.T) {
	if writerIsTerminal(&bytes.Buffer{}) {
		t.Fatal("bytes.Buffer was classified as a terminal")
	}
	regular, err := os.CreateTemp(t.TempDir(), "status-output")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	if writerIsTerminal(regular) {
		t.Fatal("regular file was classified as a terminal")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if writerIsTerminal(writer) {
		t.Fatal("pipe was classified as a terminal")
	}
	if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		defer devNull.Close()
		if writerIsTerminal(devNull) {
			t.Fatal("null device was classified as a terminal")
		}
	}
}

func TestStatusEscapesTerminalControlCharacters(t *testing.T) {
	report := emptyStatusReport()
	report.CLI.Executable = "unsafe\x1b[31m\nnext"
	var output bytes.Buffer
	renderStatus(&output, report, true)
	if strings.Count(output.String(), "\x1b[") != statusStyleSequenceCount(output.String()) {
		t.Fatalf("status output contains an unowned ANSI sequence: %q", output.String())
	}
	plain := stripStatusANSI(output.String())
	if !strings.Contains(plain, `unsafe\x1b[31m\nnext`) {
		t.Fatalf("control characters were not rendered visibly: %q", plain)
	}
}

func fullyPopulatedStatusReport() Report {
	autoUpdates := false
	return Report{
		Schema: 1,
		CLI: CLIInfo{
			Version:    "9.8.7-status-version",
			Commit:     "1111111111111111111111111111111111111111",
			Executable: "/status/bin/hextap-cli",
		},
		Homebrew: HomebrewInfo{Executable: "/status/bin/brew-owner", Prefix: "/status/prefix"},
		Tap: TapInfo{
			Name:      "owner/status-tap",
			Installed: true,
			Path:      "/status/tap/path",
			Revision:  "2222222222222222222222222222222222222222",
			Branch:    "status-branch",
			Remote:    "https://example.invalid/status-tap.git",
		},
		Projects: []ProjectInfo{{
			Name:              "status-project",
			Repository:        "owner/status-project-repository",
			Binary:            "status-project-binary",
			Schema:            2,
			ServiceEnabled:    true,
			ManifestPath:      "/status/manifests/project.json",
			RegistrationState: "STATUS_REGISTERED",
		}},
		Formulae: []FormulaInfo{{
			Name:              "status-formula",
			FullName:          "owner/status-tap/status-formula-full",
			AvailableVersion:  "4.5.6-status-available",
			Installed:         true,
			InstalledVersions: []string{"4.5.4-status-installed-a", "4.5.5-status-installed-b"},
			Outdated:          true,
			Pinned:            false,
			Service: ServiceInfo{
				Defined:              true,
				RunType:              "status-run-type",
				KeepAlive:            []string{"status-keep-alive-a=true", "status-keep-alive-b=false"},
				EnvironmentVariables: []string{"STATUS_ENV_A", "STATUS_ENV_B"},
				RestartDelay:         17,
			},
		}},
		Casks: []CaskInfo{{
			Name:             "status-cask",
			FullName:         "owner/status-tap/status-cask-full",
			AvailableVersion: "7.8.9-status-cask-available",
			Installed:        true,
			InstalledVersion: "7.8.8-status-cask-installed",
			Outdated:         false,
			AutoUpdates:      &autoUpdates,
		}},
		Skills: []SkillInfo{{
			Scope: skillinstall.ProjectScope,
			StatusEntry: skillinstall.StatusEntry{
				State:            skillinstall.UpdateAvailableState,
				Agent:            "status-agent",
				DiscoveredBy:     []string{"status-discoverer-a", "status-discoverer-b"},
				Path:             "/status/skills/hextap",
				InstalledVersion: "1.2.2-status-skill-installed",
				AvailableVersion: "1.2.3-status-skill-available",
				Recommendation:   skillinstall.UpgradeRecommendation,
			},
		}},
		LocalProject: &ProjectInfo{
			Name:              "status-local-project",
			Repository:        "owner/status-local-repository",
			Binary:            "status-local-binary",
			Schema:            1,
			ServiceEnabled:    false,
			ManifestPath:      "/status/manifests/local.json",
			RegistrationState: "STATUS_NOT_REGISTERED",
		},
		Warnings: []Warning{{Component: "status.warning.component", Message: "status warning message"}},
	}
}

func emptyStatusReport() Report {
	return Report{
		Schema:   1,
		CLI:      CLIInfo{},
		Tap:      TapInfo{Name: TapName},
		Projects: []ProjectInfo{},
		Formulae: []FormulaInfo{},
		Casks:    []CaskInfo{},
		Skills:   []SkillInfo{},
		Warnings: []Warning{},
	}
}

func stripStatusANSI(value string) string {
	return ansiSequencePattern.ReplaceAllString(value, "")
}

func jsonScalarValues(value any) []string {
	var result []string
	var visit func(any)
	visit = func(candidate any) {
		switch typed := candidate.(type) {
		case map[string]any:
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case string:
			result = append(result, typed)
		case float64:
			result = append(result, strconv.FormatFloat(typed, 'f', -1, 64))
		case bool:
			result = append(result, strconv.FormatBool(typed))
		case nil:
			result = append(result, "null")
		default:
			panic(fmt.Sprintf("unexpected JSON scalar type %T", typed))
		}
	}
	visit(value)
	return result
}

func statusStyleSequenceCount(value string) int {
	owned := []string{
		statusANSIReset,
		statusANSITitle,
		statusANSISection,
		statusANSIWarning,
		statusANSIDim,
	}
	count := 0
	for _, sequence := range owned {
		count += strings.Count(value, sequence)
	}
	return count
}
