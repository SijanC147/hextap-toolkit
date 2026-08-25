// Package brewcli implements the installable brew-hextap command surface.
package brewcli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/onboard"
)

const (
	errorExit = 2
	usageText = `usage: brew-hextap <command> [options]

Commands:
  version     Print build version and commit
  onboard     Plan or create local Hextap onboarding artifacts
  validate    Validate local onboarding artifacts; optionally smoke-build
  doctor      Check local prerequisites; optionally inspect GitHub read-only
`
)

var (
	stableInjectedVersion = regexp.MustCompile(`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)
	fullInjectedCommit    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// Run executes one brew-hextap command and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer, version, commit string) int {
	if len(args) == 1 && args[0] == "--version" {
		writeVersion(stdout, version, commit)
		return 0
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		_, _ = io.WriteString(stdout, usageText)
		return 0
	}
	if len(args) == 0 {
		return fail(stderr, "command required; expected version, onboard, validate, or doctor")
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return fail(stderr, "version: unexpected arguments")
		}
		writeVersion(stdout, version, commit)
		return 0
	case "onboard":
		return runOnboard(args[1:], stdout, stderr, version, commit)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown command; expected version, onboard, validate, or doctor")
	}
}

func writeVersion(output io.Writer, version, commit string) {
	fmt.Fprintf(output, "brew-hextap %s (commit %s)\n", version, commit)
}

func runOnboard(args []string, stdout, stderr io.Writer, version, commit string) int {
	if isHelpRequest(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap onboard [options]\n")
		return 0
	}
	flags := newFlagSet("onboard")
	project := flags.String("project", ".", "Git project root")
	repository := flags.String("repository", "", "exact OWNER/REPO identity")
	formula := flags.String("formula", "", "Homebrew formula name")
	binary := flags.String("binary", "", "installed binary basename")
	description := flags.String("description", "", "one-line formula description")
	license := flags.String("license", "", "formula license identifier")
	goPackage := flags.String("go-package", "", "narrow Go main package")
	versionSymbol := flags.String("version-symbol", "main.version", "package-qualified version variable")
	commitSymbol := flags.String("commit-symbol", "main.commit", "package-qualified commit variable")
	defaultToolkitVersion := ""
	if stableInjectedVersion.MatchString(version) {
		defaultToolkitVersion = version
	}
	defaultToolkitSHA := ""
	if fullInjectedCommit.MatchString(commit) {
		defaultToolkitSHA = commit
	}
	toolkitVersion := flags.String("toolkit-version", defaultToolkitVersion, "stable toolkit tag vX.Y.Z")
	toolkitSHA := flags.String("toolkit-sha", defaultToolkitSHA, "full toolkit commit SHA")
	linux := flags.Bool("linux", true, "build Linux release assets")
	dryRun := flags.Bool("dry-run", false, "report without writing")
	var requiredChecks stringList
	flags.Var(&requiredChecks, "required-check", "required status-check context (repeatable)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return fail(stderr, "onboard: use brew-hextap --help for command usage")
		}
		return fail(stderr, "onboard: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "onboard: unexpected positional arguments")
	}
	visited := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })
	result, err := onboard.Onboard(onboard.Options{
		Project:          *project,
		Repository:       *repository,
		Formula:          *formula,
		Binary:           *binary,
		Description:      *description,
		License:          *license,
		GoPackage:        *goPackage,
		VersionSymbol:    *versionSymbol,
		CommitSymbol:     *commitSymbol,
		ToolkitVersion:   *toolkitVersion,
		ToolkitSHA:       *toolkitSHA,
		RequiredChecks:   requiredChecks,
		Linux:            *linux,
		DryRun:           *dryRun,
		FormulaSet:       visited["formula"],
		BinarySet:        visited["binary"],
		DescriptionSet:   visited["description"],
		LicenseSet:       visited["license"],
		GoPackageSet:     visited["go-package"],
		VersionSymbolSet: visited["version-symbol"],
		CommitSymbolSet:  visited["commit-symbol"],
		LinuxSet:         visited["linux"],
	})
	if err != nil {
		return fail(stderr, "onboard: %v", err)
	}
	for _, entry := range result.Entries {
		fmt.Fprintf(stdout, "%s %s\n", entry.Action, entry.Path)
	}
	return 0
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	if isHelpRequest(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap validate [--project PATH] [--build]\n")
		return 0
	}
	flags := newFlagSet("validate")
	project := flags.String("project", ".", "Git project root")
	build := flags.Bool("build", false, "execute bounded build and archive smoke")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "validate: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "validate: unexpected positional arguments")
	}
	result, err := onboard.Validate(onboard.ValidateOptions{Project: *project, Build: *build})
	if err != nil {
		return fail(stderr, "validate: %v", err)
	}
	fmt.Fprintf(stdout, "VALIDATED %s (%s, toolkit %s@%s)\n", result.Project, result.Manifest.RepositorySlug(), result.ToolkitVersion, result.ToolkitSHA)
	if result.BuildVerified {
		_, _ = io.WriteString(stdout, "VALIDATED build and archives\n")
	}
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	if isHelpRequest(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap doctor [--project PATH] [--online]\n")
		return 0
	}
	flags := newFlagSet("doctor")
	project := flags.String("project", ".", "Git project root")
	online := flags.Bool("online", false, "perform additional read-only GitHub checks")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "doctor: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "doctor: unexpected positional arguments")
	}
	result, err := onboard.Doctor(onboard.DoctorOptions{Project: *project, Online: *online})
	if err != nil {
		return fail(stderr, "doctor: %v", err)
	}
	for _, check := range result.Checks {
		fmt.Fprintf(stdout, "OK %s\n", check)
	}
	return 0
}

func newFlagSet(name string) *flag.FlagSet {
	result := flag.NewFlagSet(name, flag.ContinueOnError)
	result.SetOutput(io.Discard)
	return result
}

func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}

func fail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+format+"\n", args...)
	return errorExit
}
