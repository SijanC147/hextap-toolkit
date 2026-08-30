package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

// homebrewLayout describes one installed-Homebrew shape: a prefix, the CLI path
// a user invokes, and the Cellar path that invocation really resolves to.
type homebrewLayout struct {
	name       string
	prefix     string
	executable string
	cellar     string
}

// ownershipService builds a Service that can only discover Homebrew the way
// production does: no injected candidates, and no brew on PATH.
func ownershipService(runner Runner, layout homebrewLayout) Service {
	return Service{
		Runner:     runner,
		Executable: layout.executable,
		LookPath:   func(string) (string, error) { return "", errors.New("no brew on PATH") },
		ResolvePath: func(path string) (string, error) {
			switch path {
			case layout.executable, layout.prefix + "/opt/hextap/bin/brew-hextap":
				return layout.cellar, nil
			default:
				return path, nil
			}
		},
		SkillStatus: func(skillinstall.Options) (skillinstall.StatusResult, error) {
			return skillinstall.StatusResult{}, nil
		},
	}
}

func TestFindOwningHomebrewProvesOwnershipAcrossEveryPrefixLayout(t *testing.T) {
	layouts := []homebrewLayout{
		{
			name:       "apple silicon",
			prefix:     "/opt/homebrew",
			executable: "/opt/homebrew/bin/hextap",
			cellar:     "/opt/homebrew/Cellar/hextap/1.2.3/bin/brew-hextap",
		},
		{
			name:       "intel",
			prefix:     "/usr/local",
			executable: "/usr/local/bin/hextap",
			cellar:     "/usr/local/Cellar/hextap/1.2.3/bin/brew-hextap",
		},
		{
			name:       "non-standard prefix",
			prefix:     "/Users/tester/homebrew",
			executable: "/Users/tester/homebrew/bin/hextap",
			cellar:     "/Users/tester/homebrew/Cellar/hextap/1.2.3/bin/brew-hextap",
		},
		{
			name:       "prefix nested under an unrelated Cellar directory",
			prefix:     "/Cellar/vendor/homebrew",
			executable: "/Cellar/vendor/homebrew/bin/hextap",
			cellar:     "/Cellar/vendor/homebrew/Cellar/hextap/1.2.3/bin/brew-hextap",
		},
	}
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			owningBrew := layout.prefix + "/bin/brew"
			runner := &fakeRunner{
				t: t,
				results: map[string]Result{
					owningBrew + " --prefix sean/hextap/hextap": {Stdout: layout.prefix + "/opt/hextap\n"},
				},
				errors: map[string]error{},
			}
			// Every conventional candidate other than the owner is unreachable,
			// so only discovery from the running binary can find this install.
			for _, conventional := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
				if conventional != owningBrew {
					runner.errors[conventional+" --prefix sean/hextap/hextap"] = errors.New("unreachable")
				}
			}
			ownership := ownershipService(runner, layout).findOwningHomebrew(context.Background())
			if !ownership.Owned || ownership.Executable != owningBrew {
				t.Fatalf("ownership = %#v, want the Homebrew at %q", ownership, owningBrew)
			}
			if ownership.Reason != "" {
				t.Fatalf("a proved ownership carried a failure reason: %q", ownership.Reason)
			}
			if len(runner.commands) == 0 || runner.commands[0].Name != owningBrew {
				t.Fatalf("the owning Homebrew was not examined first: %#v", runner.commands)
			}
		})
	}
}

func TestFindOwningHomebrewSelectsTheOwnerRatherThanTheFirstReachableHomebrew(t *testing.T) {
	// Both Homebrew installations answer and the running binary is not inside a
	// Cellar, so no candidate can be derived from it and the conventional list
	// is examined in order. The first reachable brew must not be assumed to own
	// the CLI just because it answered first.
	const active = "/usr/local/opt/hextap/bin/brew-hextap"
	runner := &fakeRunner{
		t: t,
		results: map[string]Result{
			"/opt/homebrew/bin/brew --prefix sean/hextap/hextap": {Stdout: "/opt/homebrew/opt/hextap\n"},
			"/usr/local/bin/brew --prefix sean/hextap/hextap":    {Stdout: "/usr/local/opt/hextap\n"},
		},
	}
	service := Service{
		Runner:      runner,
		Executable:  active,
		LookPath:    func(string) (string, error) { return "/opt/homebrew/bin/brew", nil },
		ResolvePath: func(path string) (string, error) { return path, nil },
	}

	ownership := service.findOwningHomebrew(context.Background())

	if !ownership.Owned || ownership.Executable != "/usr/local/bin/brew" {
		t.Fatalf("ownership = %#v, want the Homebrew at \"/usr/local/bin/brew\"", ownership)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "/opt/homebrew/bin/brew" {
		t.Fatalf("the conventional candidates were not examined in order: %#v", runner.commands)
	}
}

func TestFindOwningHomebrewRejectsADerivedCandidateThatCannotProveOwnership(t *testing.T) {
	// The running binary sits in a Cellar, so its prefix is derived and
	// examined — but that Homebrew installs a different brew-hextap, so
	// ownership must still be refused rather than inferred from the layout.
	layout := homebrewLayout{
		prefix:     "/impostor",
		executable: "/impostor/Cellar/hextap/1.2.3/bin/brew-hextap",
		cellar:     "/impostor/Cellar/hextap/1.2.3/bin/brew-hextap",
	}
	runner := &fakeRunner{
		t: t,
		results: map[string]Result{
			"/impostor/bin/brew --prefix sean/hextap/hextap": {Stdout: "/impostor/opt/hextap\n"},
		},
		errors: map[string]error{
			"/opt/homebrew/bin/brew --prefix sean/hextap/hextap": errors.New("unreachable"),
			"/usr/local/bin/brew --prefix sean/hextap/hextap":    errors.New("unreachable"),
		},
	}
	service := ownershipService(runner, layout)
	// The impostor prefix resolves to a brew-hextap that is not the running one.
	service.ResolvePath = func(path string) (string, error) { return path, nil }

	ownership := service.findOwningHomebrew(context.Background())

	if ownership.Owned {
		t.Fatalf("a derived candidate was granted ownership without proving it: %#v", ownership)
	}
	if runner.commands[0].Name != "/impostor/bin/brew" {
		t.Fatalf("the derived candidate was not examined: %#v", runner.commands)
	}
	if !strings.Contains(ownership.Reason, `"/impostor/bin/brew" (owns a different Hextap)`) {
		t.Fatalf("reason did not record the derived candidate's rejection: %q", ownership.Reason)
	}
}

func TestFindOwningHomebrewReportsWhyEveryCandidateWasRejected(t *testing.T) {
	const dependencySecret = "ops_ownership_error_secret_1234567890"
	layout := homebrewLayout{
		prefix:     "/unused",
		executable: "/build/bin/brew-hextap",
		cellar:     "/build/bin/brew-hextap",
	}
	runner := &fakeRunner{
		t: t,
		results: map[string]Result{
			"/opt/homebrew/bin/brew --prefix sean/hextap/hextap": {Stdout: "/opt/homebrew/opt/hextap\n"},
			"/usr/local/bin/brew --prefix sean/hextap/hextap":    {Stdout: "not a single line\nof output\n"},
		},
		errors: map[string]error{
			"/elsewhere/bin/brew --prefix sean/hextap/hextap": errors.New(dependencySecret),
		},
	}
	service := ownershipService(runner, layout)
	service.LookPath = func(string) (string, error) { return "/elsewhere/bin/brew", nil }

	ownership := service.findOwningHomebrew(context.Background())

	if ownership.Owned {
		t.Fatalf("ownership was granted to a binary no Homebrew installs: %#v", ownership)
	}
	for _, expected := range []string{
		`"/build/bin/brew-hextap"`,
		`"/opt/homebrew/bin/brew" (owns a different Hextap)`,
		`"/usr/local/bin/brew" (returned an invalid prefix record)`,
		`"/elsewhere/bin/brew" (unavailable)`,
	} {
		if !strings.Contains(ownership.Reason, expected) {
			t.Fatalf("reason %q did not contain %q", ownership.Reason, expected)
		}
	}
	if strings.Contains(ownership.Reason, dependencySecret) {
		t.Fatalf("reason leaked a dependency error: %q", ownership.Reason)
	}
}

func TestFindOwningHomebrewReportsAnUnresolvableExecutableWithoutClaimingAbsence(t *testing.T) {
	service := Service{
		Runner:      &fakeRunner{t: t},
		Executable:  "/vanished/hextap",
		ResolvePath: func(string) (string, error) { return "", errors.New("no such file") },
	}

	ownership := service.findOwningHomebrew(context.Background())

	if ownership.Owned {
		t.Fatalf("ownership = %#v, want an unproved result", ownership)
	}
	if !strings.Contains(ownership.Reason, `"/vanished/hextap"`) || !strings.Contains(ownership.Reason, "symlink chain") {
		t.Fatalf("reason did not identify the unresolvable executable: %q", ownership.Reason)
	}
}

func TestCollectReportsUnknownHomebrewRatherThanAnEmptySystem(t *testing.T) {
	layout := homebrewLayout{
		prefix:     "/unused",
		executable: "/build/bin/brew-hextap",
		cellar:     "/build/bin/brew-hextap",
	}
	runner := &fakeRunner{
		t: t,
		errors: map[string]error{
			"/opt/homebrew/bin/brew --prefix sean/hextap/hextap": errors.New("unreachable"),
			"/usr/local/bin/brew --prefix sean/hextap/hextap":    errors.New("unreachable"),
		},
	}
	service := ownershipService(runner, layout)
	service.Version = "1.2.3"
	service.Commit = testCommit

	report := service.Collect(context.Background(), Options{})

	if report.Tap.Installed || len(report.Projects) != 0 || len(report.Formulae) != 0 || len(report.Casks) != 0 {
		t.Fatalf("unproved ownership emitted Homebrew-derived inventory: %#v", report)
	}
	if report.Homebrew.Executable != "" || report.Homebrew.Prefix != "" {
		t.Fatalf("unproved ownership emitted a Homebrew record: %#v", report.Homebrew)
	}
	if report.CLI.Executable != layout.executable {
		t.Fatalf("the report did not identify the binary it inspected: %#v", report.CLI)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Component != "homebrew" {
		t.Fatalf("warnings = %#v", report.Warnings)
	}
	message := report.Warnings[0].Message
	for _, expected := range []string{
		"could not identify the Homebrew installation that owns the active Hextap CLI",
		`"/build/bin/brew-hextap"`,
		`"/opt/homebrew/bin/brew" (unavailable)`,
		`"/usr/local/bin/brew" (unavailable)`,
		"unknown here, not proven absent",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("warning %q did not contain %q", message, expected)
		}
	}
}

func TestHomebrewPrefixOfCellarPathAcceptsOnlyRealCellarPaths(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		prefix string
	}{
		{name: "apple silicon", path: "/opt/homebrew/Cellar/hextap/0.6.0/bin/brew-hextap", prefix: "/opt/homebrew"},
		{name: "intel", path: "/usr/local/Cellar/hextap/0.6.0/bin/brew-hextap", prefix: "/usr/local"},
		{name: "non-standard prefix", path: "/Users/tester/brew/Cellar/hextap/0.6.0/bin/brew-hextap", prefix: "/Users/tester/brew"},
		{name: "linuxbrew", path: "/home/linuxbrew/.linuxbrew/Cellar/hextap/0.6.0/bin/brew-hextap", prefix: "/home/linuxbrew/.linuxbrew"},
		{name: "innermost Cellar wins", path: "/Cellar/vendor/Cellar/hextap/0.6.0/bin/brew-hextap", prefix: "/Cellar/vendor"},
		{name: "no Cellar component", path: "/build/bin/brew-hextap", prefix: ""},
		{name: "lowercase cellar is a different directory", path: "/opt/homebrew/cellar/hextap/0.6.0/bin/brew-hextap", prefix: ""},
		{name: "relative path", path: "opt/homebrew/Cellar/hextap/0.6.0/bin/brew-hextap", prefix: ""},
		{name: "empty path", path: "", prefix: ""},
		{name: "root", path: "/", prefix: ""},
		{name: "Cellar is the file itself, not a directory above it", path: "/opt/homebrew/Cellar", prefix: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if prefix := homebrewPrefixOfCellarPath(testCase.path); prefix != testCase.prefix {
				t.Fatalf("homebrewPrefixOfCellarPath(%q) = %q, want %q", testCase.path, prefix, testCase.prefix)
			}
		})
	}
}

func TestBrewCandidatesHonourInjectedCandidatesAndDeduplicate(t *testing.T) {
	service := Service{BrewCandidates: []string{"/opt/homebrew/bin/brew", "/brew", "/brew"}}

	candidates := service.brewCandidates("/opt/homebrew/Cellar/hextap/0.6.0/bin/brew-hextap")

	want := []string{"/opt/homebrew/bin/brew", "/brew"}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", candidates, want)
	}
	for index, candidate := range candidates {
		if candidate != want[index] {
			t.Fatalf("candidates = %#v, want %#v", candidates, want)
		}
	}
}

func TestReportablePathRefusesUnsafeAndOversizedPaths(t *testing.T) {
	if got := reportablePath("/opt/homebrew/bin/brew"); got != `"/opt/homebrew/bin/brew"` {
		t.Fatalf("reportablePath = %q", got)
	}
	for _, path := range []string{"", "/opt/\x1b[31mhomebrew", "/opt/\nhomebrew", "/" + strings.Repeat("a", maximumWarningPathLength)} {
		if got := reportablePath(path); got != "an unreportable path" {
			t.Fatalf("reportablePath(%q) = %q, want the placeholder", path, got)
		}
	}
}
