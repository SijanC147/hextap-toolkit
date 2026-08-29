package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

func TestStatusAndInfoCLIExposeJSONSummaryDetailsAndShortFlags(t *testing.T) {
	service := cliFixtureService(t)
	var stdout, stderr bytes.Buffer
	if code := service.RunStatusCLI(context.Background(), []string{"-j", "-p", "/project"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("status -j -p exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode status JSON %q: %v", stdout.String(), err)
	}
	if report.Schema != 1 || len(report.Formulae) != 1 || report.LocalProject == nil {
		t.Fatalf("status JSON = %#v", report)
	}

	stdout.Reset()
	stderr.Reset()
	if code := service.RunInfoCLI(context.Background(), []string{"-k", "formula", "-n", "hextap", "-p", "/project"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("info filters exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, expected := range []string{"FORMULAE (1)", "hextap", "available=1.2.3", "installed=1.2.3"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("info output %q is missing %q", stdout.String(), expected)
		}
	}
	for _, unexpected := range []string{"REGISTERED PROJECTS", "CASKS", "SKILLS", "LOCAL PROJECT"} {
		if strings.Contains(stdout.String(), unexpected) {
			t.Errorf("filtered info output %q contains %q", stdout.String(), unexpected)
		}
	}
}

func TestStatusAndInfoCLIValidationAndHelp(t *testing.T) {
	service := Service{}
	for _, test := range []struct {
		name string
		run  func(context.Context, []string, io.Writer, io.Writer) int
	}{
		{name: "status", run: service.RunStatusCLI},
		{name: "info", run: service.RunInfoCLI},
	} {
		t.Run(test.name+" help aliases", func(t *testing.T) {
			for _, flag := range []string{"-h", "--help"} {
				var stdout, stderr bytes.Buffer
				if code := test.run(context.Background(), []string{flag}, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.HasPrefix(stdout.String(), "usage: brew-hextap "+test.name) {
					t.Fatalf("%s %s exit=%d stdout=%q stderr=%q", test.name, flag, code, stdout.String(), stderr.String())
				}
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := service.RunInfoCLI(context.Background(), []string{"--kind", "unknown"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--kind") {
		t.Fatalf("invalid kind exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := service.RunStatusCLI(context.Background(), []string{"--unknown"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "error: status:") {
		t.Fatalf("invalid status flag exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func cliFixtureService(t *testing.T) Service {
	t.Helper()
	runner := &fakeRunner{
		t: t,
		results: map[string]Result{
			"/brew --prefix sean/hextap/hextap":                 {Stdout: "/opt/hextap\n"},
			"/brew --prefix":                                    {Stdout: "/opt/homebrew\n"},
			"/brew --repo sean/hextap":                          {Stdout: "/tap\n"},
			"git -C /tap rev-parse HEAD":                        {Stdout: testTapRevision + "\n"},
			"git -C /tap symbolic-ref --quiet --short HEAD":     {Stdout: "main\n"},
			"git -C /tap remote get-url origin":                 {Stdout: "https://github.com/SijanC147/homebrew-hextap.git\n"},
			"/brew info --json=v2 --formula sean/hextap/hextap": {Stdout: `{"formulae":[{"name":"hextap","full_name":"sean/hextap/hextap","versions":{"stable":"1.2.3"},"installed":[{"version":"1.2.3"}],"outdated":false,"pinned":false,"service":null}],"casks":[]}`},
		},
		errors: map[string]error{},
	}
	return Service{
		Runner: runner,
		FileSystem: mapFileSystem{files: fstest.MapFS{
			"tap/Formula/hextap.rb":    &fstest.MapFile{Data: []byte("formula")},
			"tap/Projects/hextap.json": &fstest.MapFile{Data: []byte(testManifest("hextap", "Hextap", "hextap-toolkit", "brew-hextap", false))},
			"project/.hextap.json":     &fstest.MapFile{Data: []byte(testManifest("demo", "Demo", "demo", "demo", false))},
		}},
		Version:        "1.2.3",
		Commit:         testCommit,
		Executable:     "/active/brew-hextap",
		HomeDir:        "/home/tester",
		BrewCandidates: []string{"/brew"},
		ResolvePath: func(path string) (string, error) {
			if path == "/active/brew-hextap" || path == "/opt/hextap/bin/brew-hextap" {
				return "/cellar/hextap/bin/brew-hextap", nil
			}
			return path, nil
		},
		SkillStatus: func(options skillinstall.Options) (skillinstall.StatusResult, error) {
			return skillinstall.StatusResult{Entries: []skillinstall.StatusEntry{{
				State:            skillinstall.CurrentState,
				Agent:            "agents+codex",
				DiscoveredBy:     []string{"agents", "codex", "cursor"},
				Path:             "/skills/hextap",
				InstalledVersion: "1.2.1",
				AvailableVersion: "1.2.1",
				Recommendation:   skillinstall.NoRecommendation,
			}}}, nil
		},
	}
}
