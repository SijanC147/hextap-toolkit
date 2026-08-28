package inventory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

const (
	testCommit      = "0123456789abcdef0123456789abcdef01234567"
	testTapRevision = "89abcdef0123456789abcdef0123456789abcdef"
)

type fakeRunner struct {
	t             *testing.T
	results       map[string]Result
	errors        map[string]error
	commands      []Command
	requireNoAuto bool
}

type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, _ Command) (Result, error) {
	<-ctx.Done()
	return Result{}, ctx.Err()
}

func (runner *fakeRunner) Run(_ context.Context, command Command) (Result, error) {
	runner.t.Helper()
	runner.commands = append(runner.commands, command)
	key := commandKey(command)
	if runner.requireNoAuto && strings.HasPrefix(key, "/brew ") && command.Env["HOMEBREW_NO_AUTO_UPDATE"] != "1" {
		runner.t.Fatalf("%s did not disable Homebrew auto-update", key)
	}
	if err, ok := runner.errors[key]; ok {
		return Result{}, err
	}
	result, ok := runner.results[key]
	if !ok {
		runner.t.Fatalf("unexpected command %q", key)
	}
	return result, nil
}

func commandKey(command Command) string {
	return strings.Join(append([]string{command.Name}, command.Args...), " ")
}

type mapFileSystem struct {
	files fstest.MapFS
}

func (filesystem mapFileSystem) Lstat(name string) (fs.FileInfo, error) {
	return fs.Stat(filesystem.files, mapPath(name))
}

func (filesystem mapFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(filesystem.files, mapPath(name))
}

func (filesystem mapFileSystem) ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(filesystem.files, mapPath(name))
}

func mapPath(name string) string {
	return strings.TrimPrefix(strings.TrimPrefix(name, "/"), "./")
}

func TestCollectReportsSystemInventoryWithoutLiveHomebrewState(t *testing.T) {
	runner := &fakeRunner{
		t:             t,
		requireNoAuto: true,
		results: map[string]Result{
			"/brew --prefix sean/hextap/hextap":             {Stdout: "/opt/hextap\n"},
			"/brew --prefix":                                {Stdout: "/opt/homebrew\n"},
			"/brew --repo sean/hextap":                      {Stdout: "/tap\n"},
			"git -C /tap rev-parse HEAD":                    {Stdout: testTapRevision + "\n"},
			"git -C /tap symbolic-ref --quiet --short HEAD": {Stdout: "main\n"},
			"git -C /tap remote get-url origin":             {Stdout: "https://token-must-not-appear@github.com/SijanC147/homebrew-hextap.git\n"},
			"/brew info --json=v2 --formula sean/hextap/hextap": {Stdout: `{
  "formulae": [{
    "name": "hextap",
    "full_name": "sean/hextap/hextap",
    "versions": {"stable": "1.2.3"},
    "installed": [{"version": "1.2.2"}, {"version": "1.2.3"}],
    "outdated": false,
    "pinned": true,
    "service": null
  }],
  "casks": []
}`},
			"/brew info --json=v2 --formula sean/hextap/worker": {Stdout: `{
  "formulae": [{
    "name": "worker",
    "full_name": "sean/hextap/worker",
    "versions": {"stable": "2.0.0"},
    "installed": [{"version": "1.9.0"}],
    "outdated": true,
    "pinned": false,
    "service": {
      "run": "/opt/homebrew/opt/worker/bin/worker",
      "run_type": "immediate",
      "keep_alive": {"crashed": true},
      "environment_variables": {"SAFE_SETTING": "visible", "SECRET_VALUE": "never-report-this"},
      "log_path": "/opt/homebrew/var/log/worker.log",
      "error_log_path": "/opt/homebrew/var/log/worker.err.log",
      "restart_delay": 5
    }
  }],
  "casks": []
}`},
			"/brew info --json=v2 --cask sean/hextap/desktop": {Stdout: `{
  "formulae": [],
  "casks": [{
    "token": "desktop",
    "full_token": "sean/hextap/desktop",
    "version": "3.4.5",
    "installed": "3.4.4",
    "outdated": true,
    "auto_updates": false
  }]
}`},
		},
		errors: map[string]error{},
	}
	filesystem := mapFileSystem{files: fstest.MapFS{
		"tap/Formula/hextap.rb":    &fstest.MapFile{Data: []byte("class Hextap < Formula\nend\n")},
		"tap/Formula/worker.rb":    &fstest.MapFile{Data: []byte("class Worker < Formula\nend\n")},
		"tap/Casks/desktop.rb":     &fstest.MapFile{Data: []byte("cask \"desktop\" do\nend\n")},
		"tap/Projects/hextap.json": &fstest.MapFile{Data: []byte(testManifest("hextap", "Hextap", "hextap-toolkit", "brew-hextap", false))},
		"tap/Projects/worker.json": &fstest.MapFile{Data: []byte(testManifest("worker", "Worker", "worker", "worker", true))},
		"project/.hextap.json":     &fstest.MapFile{Data: []byte(testManifest("demo", "Demo", "demo", "demo", false))},
	}}
	skillCalls := make([]skillinstall.Options, 0, 2)
	service := Service{
		Runner:         runner,
		FileSystem:     filesystem,
		Version:        "1.2.3",
		Commit:         testCommit,
		Executable:     "/active/brew-hextap",
		HomeDir:        "/home/tester",
		BrewCandidates: []string{"/brew"},
		ResolvePath: func(path string) (string, error) {
			switch path {
			case "/active/brew-hextap", "/opt/hextap/bin/brew-hextap":
				return "/cellar/hextap/1.2.3/bin/brew-hextap", nil
			default:
				return path, nil
			}
		},
		SkillStatus: func(options skillinstall.Options) (skillinstall.StatusResult, error) {
			skillCalls = append(skillCalls, options)
			entry := skillinstall.StatusEntry{
				State:            skillinstall.CurrentState,
				Agent:            "agents+codex",
				DiscoveredBy:     []string{"agents", "codex", "cursor"},
				Path:             "/skills/hextap",
				InstalledVersion: "1.2.1",
				AvailableVersion: "1.2.1",
				Recommendation:   skillinstall.NoRecommendation,
			}
			return skillinstall.StatusResult{Entries: []skillinstall.StatusEntry{entry}}, nil
		},
	}

	report := service.Collect(context.Background(), Options{Project: "/project"})

	if report.Schema != 1 || report.CLI.Version != "1.2.3" || report.CLI.Commit != testCommit || report.CLI.Executable != "/active/brew-hextap" {
		t.Fatalf("CLI inventory = %#v", report.CLI)
	}
	if report.Homebrew.Executable != "/brew" || report.Homebrew.Prefix != "/opt/homebrew" {
		t.Fatalf("Homebrew inventory = %#v", report.Homebrew)
	}
	if !report.Tap.Installed || report.Tap.Path != "/tap" || report.Tap.Revision != testTapRevision || report.Tap.Branch != "main" {
		t.Fatalf("tap inventory = %#v", report.Tap)
	}
	if report.Tap.Remote != "https://github.com/SijanC147/homebrew-hextap.git" || strings.Contains(report.Tap.Remote, "token-must-not-appear") {
		t.Fatalf("tap remote was not sanitized: %q", report.Tap.Remote)
	}
	if got := []string{report.Projects[0].Name, report.Projects[1].Name}; !slices.Equal(got, []string{"hextap", "worker"}) {
		t.Fatalf("registered projects = %v", got)
	}
	if len(report.Formulae) != 2 || report.Formulae[0].Name != "hextap" || !report.Formulae[0].Installed || !report.Formulae[0].Pinned || !slices.Equal(report.Formulae[0].InstalledVersions, []string{"1.2.2", "1.2.3"}) {
		t.Fatalf("Formula inventory = %#v", report.Formulae)
	}
	worker := report.Formulae[1]
	if !worker.Outdated || !worker.Service.Defined || worker.Service.RunType != "immediate" || !slices.Equal(worker.Service.KeepAlive, []string{"crashed=true"}) || !slices.Equal(worker.Service.EnvironmentVariables, []string{"SAFE_SETTING", "SECRET_VALUE"}) {
		t.Fatalf("service inventory = %#v", worker.Service)
	}
	if strings.Contains(strings.Join(worker.Service.EnvironmentVariables, " "), "never-report-this") {
		t.Fatalf("service inventory exposed an environment value: %#v", worker.Service)
	}
	if len(report.Casks) != 1 || report.Casks[0].Name != "desktop" || report.Casks[0].InstalledVersion != "3.4.4" || !report.Casks[0].Outdated {
		t.Fatalf("Cask inventory = %#v", report.Casks)
	}
	if len(report.Skills) != 2 || report.Skills[0].Scope != skillinstall.UserScope || report.Skills[1].Scope != skillinstall.ProjectScope {
		t.Fatalf("skill inventory = %#v", report.Skills)
	}
	if len(skillCalls) != 2 || skillCalls[0].HomeDir != "/home/tester" || skillCalls[1].ProjectDir != "/project" {
		t.Fatalf("skill status calls = %#v", skillCalls)
	}
	if report.LocalProject == nil || report.LocalProject.Name != "demo" || report.LocalProject.ManifestPath != "/project/.hextap.json" {
		t.Fatalf("local project = %#v", report.LocalProject)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("warnings = %#v", report.Warnings)
	}
}

func TestCollectPreservesPartialResultsAndNeverEchoesDependencyErrors(t *testing.T) {
	const dependencySecret = "ops_dependency_error_secret_1234567890"
	runner := &fakeRunner{
		t: t,
		results: map[string]Result{
			"/brew --prefix sean/hextap/hextap": {Stdout: "/opt/hextap\n"},
			"/brew --prefix":                    {Stdout: "/opt/homebrew\n"},
			"/brew --repo sean/hextap":          {Stdout: "/tap\n"},
		},
		errors: map[string]error{
			"git -C /tap rev-parse HEAD":                        errors.New(dependencySecret),
			"git -C /tap symbolic-ref --quiet --short HEAD":     errors.New(dependencySecret),
			"git -C /tap remote get-url origin":                 errors.New(dependencySecret),
			"/brew info --json=v2 --formula sean/hextap/hextap": errors.New(dependencySecret),
		},
	}
	service := Service{
		Runner: runner,
		FileSystem: mapFileSystem{files: fstest.MapFS{
			"tap/Formula/hextap.rb":    &fstest.MapFile{Data: []byte("formula")},
			"tap/Projects/broken.json": &fstest.MapFile{Data: []byte(`{"token":"` + dependencySecret + `"}`)},
		}},
		Version:        "dev",
		Commit:         "unknown",
		Executable:     "/active/brew-hextap",
		HomeDir:        "/home/tester",
		BrewCandidates: []string{"/brew"},
		ResolvePath: func(path string) (string, error) {
			if path == "/active/brew-hextap" || path == "/opt/hextap/bin/brew-hextap" {
				return "/cellar/hextap/bin/brew-hextap", nil
			}
			return path, nil
		},
		SkillStatus: func(skillinstall.Options) (skillinstall.StatusResult, error) {
			return skillinstall.StatusResult{}, errors.New(dependencySecret)
		},
	}

	report := service.Collect(context.Background(), Options{})

	if len(report.Formulae) != 1 || report.Formulae[0].Name != "hextap" {
		t.Fatalf("partial Formula inventory = %#v", report.Formulae)
	}
	if len(report.Warnings) < 5 {
		t.Fatalf("warnings = %#v", report.Warnings)
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning.Message, dependencySecret) {
			t.Fatalf("warning leaked dependency error: %#v", warning)
		}
	}
}

func TestCollectAppliesOneAggregateDeadline(t *testing.T) {
	service := Service{
		Runner:            blockingRunner{},
		Executable:        "/active/brew-hextap",
		HomeDir:           t.TempDir(),
		BrewCandidates:    []string{"/brew"},
		CollectionTimeout: 25 * time.Millisecond,
		ResolvePath:       func(path string) (string, error) { return path, nil },
		SkillStatus: func(skillinstall.Options) (skillinstall.StatusResult, error) {
			return skillinstall.StatusResult{}, nil
		},
	}
	started := time.Now()
	report := service.Collect(context.Background(), Options{})
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Collect() exceeded aggregate deadline: %s", elapsed)
	}
	if len(report.Warnings) == 0 || report.Warnings[0].Component != "homebrew" {
		t.Fatalf("deadline report warnings = %#v", report.Warnings)
	}
}

func TestPackageInventoryCapsRegistryEntries(t *testing.T) {
	files := make(fstest.MapFS, maximumRegistryEntries+1)
	for index := 0; index <= maximumRegistryEntries; index++ {
		name := fmt.Sprintf("tap/Formula/package-%04d.rb", index)
		files[name] = &fstest.MapFile{Data: []byte("formula\n")}
	}
	service := Service{FileSystem: mapFileSystem{files: files}}
	report := Report{Warnings: []Warning{}}
	names := service.collectPackageNames(&report, "/tap/Formula", "formula", true)
	if len(names) != maximumRegistryEntries {
		t.Fatalf("bounded Formula names = %d, want %d", len(names), maximumRegistryEntries)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Component != "formulae.limit" {
		t.Fatalf("registry-limit warnings = %#v", report.Warnings)
	}
}

func TestSanitizeRemoteRejectsCredentialsQueriesAndControlText(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"HTTPS credentials": {
			input: "https://token@github.com/SijanC147/homebrew-hextap.git",
			want:  "https://github.com/SijanC147/homebrew-hextap.git",
		},
		"HTTPS query and fragment": {
			input: "https://github.com/SijanC147/homebrew-hextap.git?token=secret#fragment",
			want:  "https://github.com/SijanC147/homebrew-hextap.git",
		},
		"safe SCP remote": {
			input: "git@github.com:SijanC147/homebrew-hextap.git",
			want:  "git@github.com:SijanC147/homebrew-hextap.git",
		},
		"SCP query":      {input: "git@github.com:repo.git?token=SECRET"},
		"SCP control":    {input: "git@github.com:repo.git\x1b[31m"},
		"SCP extra at":   {input: "git@evil@github.com:repo.git"},
		"unknown scheme": {input: "credential-store://secret@example"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := sanitizeRemote(test.input); got != test.want {
				t.Fatalf("sanitizeRemote(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestFilterSelectsKindsAndNamesWithoutMutatingSource(t *testing.T) {
	report := Report{
		Schema:       1,
		Projects:     []ProjectInfo{{Name: "alpha"}, {Name: "beta"}},
		Formulae:     []FormulaInfo{{Name: "alpha"}, {Name: "beta"}},
		Casks:        []CaskInfo{{Name: "desktop"}},
		Skills:       []SkillInfo{{StatusEntry: skillinstall.StatusEntry{Agent: "codex"}}},
		LocalProject: &ProjectInfo{Name: "local"},
	}

	filtered, err := Filter(report, FormulaKind, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Formulae) != 1 || filtered.Formulae[0].Name != "beta" || len(filtered.Projects) != 0 || len(filtered.Casks) != 0 || len(filtered.Skills) != 0 || filtered.LocalProject != nil {
		t.Fatalf("filtered report = %#v", filtered)
	}
	if len(report.Formulae) != 2 || len(report.Projects) != 2 {
		t.Fatalf("Filter mutated source: %#v", report)
	}
	if _, err := Filter(report, Kind("unknown"), ""); err == nil {
		t.Fatal("Filter accepted an unknown kind")
	}
}

func testManifest(name, class, repository, binary string, service bool) string {
	serviceDefinition := `{"enabled": false}`
	if service {
		serviceDefinition = `{
      "enabled": true,
      "run_args": ["` + binary + `"],
      "keep_alive": {"crashed": true},
      "restart_delay": 5,
      "environment": {},
      "log_path": "service.log",
      "error_log_path": "service.err.log"
    }`
	}
	return `{
  "schema": 1,
  "formula": {
    "name": "` + name + `",
    "class": "` + class + `",
    "description": "Fixture",
    "homepage": "https://github.com/SijanC147/` + repository + `",
    "license": "MIT",
    "repository": {"owner": "SijanC147", "name": "` + repository + `"},
    "binary": "` + binary + `",
    "assets": {
      "darwin_arm64": "` + name + `-darwin-arm64.tar.gz",
      "darwin_amd64": "` + name + `-darwin-amd64.tar.gz"
    }
  },
  "release": {"build_script": "scripts/hextap-build", "linux": true},
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "service": ` + serviceDefinition + `,
    "caveats": ""
  }
}`
}
