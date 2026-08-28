// Package brewcli implements the installable brew-hextap command surface.
package brewcli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/commandmeta"
	"github.com/SijanC147/hextap-toolkit/internal/devcli"
	"github.com/SijanC147/hextap-toolkit/internal/onboard"
	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

const errorExit = 2

var (
	stableRuntimeVersion = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)
	fullInjectedCommit   = regexp.MustCompile(`^[0-9a-f]{40}$`)
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
	return RunNamed("brew-hextap", args, stdout, stderr, version, commit)
}

// RunNamed executes the installed command using the actual invocation name in
// help and build-identity output.
func RunNamed(invocation string, args []string, stdout, stderr io.Writer, version, commit string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-V") {
		writeVersion(stdout, invocation, version, commit)
		return 0
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		writeHelp(stdout, invocation, nil)
		return 0
	}
	if len(args) == 0 {
		return fail(stderr, "command required; expected version, onboard, validate, doctor, skills, dev, or completion")
	}
	switch args[0] {
	case "version":
		if hasHelpRequest(args[1:]) {
			writeHelp(stdout, invocation, []string{"version"})
			return 0
		}
		if len(args) != 1 {
			return fail(stderr, "version: unexpected arguments")
		}
		writeVersion(stdout, invocation, version, commit)
		return 0
	case "onboard":
		return runOnboard(invocation, args[1:], stdout, stderr, version, commit)
	case "validate":
		return runValidate(invocation, args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(invocation, args[1:], stdout, stderr)
	case "skills":
		return runSkills(invocation, args[1:], stdout, stderr)
	case "dev":
		return devcli.RunNamed(invocation, args[1:], stdout, stderr, version, commit)
	case "completion":
		return runCompletion(invocation, args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown command; expected version, onboard, validate, doctor, skills, dev, or completion")
	}
}

func writeVersion(output io.Writer, invocation, version, commit string) {
	fmt.Fprintf(output, "%s %s (commit %s)\n", invocation, version, commit)
}

func runOnboard(invocation string, args []string, stdout, stderr io.Writer, version, commit string) int {
	if hasHelpRequest(args) {
		writeHelp(stdout, invocation, []string{"onboard"})
		return 0
	}
	flags := newFlagSet("onboard")
	binder := commandmeta.Bind(flags, "onboard")
	project := binder.String("project", ".")
	repository := binder.String("repository", "")
	formula := binder.String("formula", "")
	binary := binder.String("binary", "")
	description := binder.String("description", "")
	license := binder.String("license", "")
	goPackage := binder.String("go-package", "")
	versionSymbol := binder.String("version-symbol", "main.version")
	commitSymbol := binder.String("commit-symbol", "main.commit")
	defaultToolkitVersion := ""
	if stableRuntimeVersion.MatchString(version) {
		defaultToolkitVersion = "v" + version
	}
	defaultToolkitSHA := ""
	if fullInjectedCommit.MatchString(commit) {
		defaultToolkitSHA = commit
	}
	toolkitVersion := binder.String("toolkit-version", defaultToolkitVersion)
	toolkitSHA := binder.String("toolkit-sha", defaultToolkitSHA)
	linux := binder.Bool("linux", true)
	dryRun := binder.Bool("dry-run", false)
	var requiredChecks stringList
	binder.Var(&requiredChecks, "required-check")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "onboard: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "onboard: unexpected positional arguments")
	}
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
		FormulaSet:       binder.WasSet("formula"),
		BinarySet:        binder.WasSet("binary"),
		DescriptionSet:   binder.WasSet("description"),
		LicenseSet:       binder.WasSet("license"),
		GoPackageSet:     binder.WasSet("go-package"),
		VersionSymbolSet: binder.WasSet("version-symbol"),
		CommitSymbolSet:  binder.WasSet("commit-symbol"),
		LinuxSet:         binder.WasSet("linux"),
	})
	if err != nil {
		return fail(stderr, "onboard: %v", err)
	}
	for _, entry := range result.Entries {
		fmt.Fprintf(stdout, "%s %s\n", entry.Action, entry.Path)
	}
	return 0
}

func runValidate(invocation string, args []string, stdout, stderr io.Writer) int {
	if hasHelpRequest(args) {
		writeHelp(stdout, invocation, []string{"validate"})
		return 0
	}
	flags := newFlagSet("validate")
	binder := commandmeta.Bind(flags, "validate")
	project := binder.String("project", ".")
	build := binder.Bool("build", false)
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

func runDoctor(invocation string, args []string, stdout, stderr io.Writer) int {
	if hasHelpRequest(args) {
		writeHelp(stdout, invocation, []string{"doctor"})
		return 0
	}
	flags := newFlagSet("doctor")
	binder := commandmeta.Bind(flags, "doctor")
	project := binder.String("project", ".")
	online := binder.Bool("online", false)
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

func runSkills(invocation string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 && isHelpToken(args[0]) {
		writeHelp(stdout, invocation, []string{"skills"})
		return 0
	}
	if len(args) == 0 {
		return fail(stderr, "skills: subcommand required; expected install, status, targets, or upgrade")
	}
	switch args[0] {
	case "install":
		return runSkillsInstall(invocation, args[1:], stdout, stderr)
	case "status":
		return runSkillsStatus(invocation, args[1:], stdout, stderr)
	case "upgrade":
		return runSkillsUpgrade(invocation, args[1:], stdout, stderr)
	case "targets":
		if hasHelpRequest(args[1:]) {
			writeHelp(stdout, invocation, []string{"skills", "targets"})
			return 0
		}
		if len(args) != 1 {
			return fail(stderr, "skills targets: unexpected arguments")
		}
		for _, target := range skillinstall.Targets() {
			if target.Virtual {
				fmt.Fprintf(stdout, "TARGET %s virtual\n", target.ID)
			} else {
				fmt.Fprintf(stdout, "TARGET %s user=%s project=%s\n", target.ID, target.UserSkillsDir, target.ProjectSkillsDir)
			}
		}
		return 0
	default:
		return fail(stderr, "skills: unknown subcommand %q; expected install, status, targets, or upgrade", args[0])
	}
}

func runCompletion(invocation string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 && isHelpToken(args[0]) {
		writeHelp(stdout, invocation, []string{"completion"})
		return 0
	}
	if len(args) == 0 {
		return fail(stderr, "completion: subcommand required; expected zsh")
	}
	switch args[0] {
	case "zsh":
		if hasHelpRequest(args[1:]) {
			writeHelp(stdout, invocation, []string{"completion", "zsh"})
			return 0
		}
		if len(args) != 1 {
			return fail(stderr, "completion zsh: unexpected arguments")
		}
		_, _ = io.WriteString(stdout, commandmeta.Zsh())
		return 0
	default:
		return fail(stderr, "completion: unknown subcommand %q; expected zsh", args[0])
	}
}

func runSkillsUpgrade(invocation string, args []string, stdout, stderr io.Writer) int {
	if hasHelpRequest(args) {
		writeHelp(stdout, invocation, []string{"skills", "upgrade"})
		return 0
	}
	flags := newFlagSet("skills upgrade")
	binder := commandmeta.Bind(flags, "skills", "upgrade")
	var agents stringList
	binder.Var(&agents, "agent")
	scope := binder.String("scope", "")
	project := binder.String("project", ".")
	dryRun := binder.Bool("dry-run", false)
	allowOverlap := binder.Bool("allow-overlapping-discovery", false)
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "skills upgrade: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "skills upgrade: unexpected positional arguments")
	}
	selectedScope := skillinstall.Scope(*scope)
	home, projectRoot, err := resolveSkillsRoots(selectedScope, *project)
	if err != nil {
		return fail(stderr, "skills upgrade: %v", err)
	}
	result, err := skillinstall.Upgrade(skillinstall.Options{
		Agents:                    agents,
		Scope:                     selectedScope,
		HomeDir:                   home,
		ProjectDir:                projectRoot,
		DryRun:                    *dryRun,
		AllowOverlappingDiscovery: *allowOverlap,
	})
	if err != nil {
		return fail(stderr, "skills upgrade: %v", err)
	}
	for _, entry := range result.Entries {
		fmt.Fprintf(stdout, "%s %s from=%s to=%s %s", entry.Action, entry.Agent, entry.FromVersion, entry.ToVersion, entry.Path)
		if entry.BackupPath != "" {
			fmt.Fprintf(stdout, " backup=%s", entry.BackupPath)
		}
		_, _ = io.WriteString(stdout, "\n")
	}
	return 0
}

func runSkillsInstall(invocation string, args []string, stdout, stderr io.Writer) int {
	if hasHelpRequest(args) {
		writeHelp(stdout, invocation, []string{"skills", "install"})
		return 0
	}
	flags := newFlagSet("skills install")
	binder := commandmeta.Bind(flags, "skills", "install")
	var agents stringList
	binder.Var(&agents, "agent")
	scope := binder.String("scope", "")
	project := binder.String("project", ".")
	dryRun := binder.Bool("dry-run", false)
	allowOverlap := binder.Bool("allow-overlapping-discovery", false)
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "skills install: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "skills install: unexpected positional arguments")
	}
	selectedScope := skillinstall.Scope(*scope)
	home, projectRoot, err := resolveSkillsRoots(selectedScope, *project)
	if err != nil {
		return fail(stderr, "skills install: %v", err)
	}
	result, err := skillinstall.Install(skillinstall.Options{
		Agents:                    agents,
		Scope:                     selectedScope,
		HomeDir:                   home,
		ProjectDir:                projectRoot,
		DryRun:                    *dryRun,
		AllowOverlappingDiscovery: *allowOverlap,
	})
	if err != nil {
		return fail(stderr, "skills install: %v", err)
	}
	for _, entry := range result.Entries {
		fmt.Fprintf(stdout, "%s %s %s\n", entry.Action, entry.Agent, entry.Path)
	}
	return 0
}

func runSkillsStatus(invocation string, args []string, stdout, stderr io.Writer) int {
	if hasHelpRequest(args) {
		writeHelp(stdout, invocation, []string{"skills", "status"})
		return 0
	}
	flags := newFlagSet("skills status")
	binder := commandmeta.Bind(flags, "skills", "status")
	var agents stringList
	binder.Var(&agents, "agent")
	scope := binder.String("scope", string(skillinstall.UserScope))
	project := binder.String("project", ".")
	jsonOutput := binder.Bool("json", false)
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "skills status: invalid arguments")
	}
	if flags.NArg() != 0 {
		return fail(stderr, "skills status: unexpected positional arguments")
	}
	selectedScope := skillinstall.Scope(*scope)
	home, projectRoot, err := resolveSkillsRoots(selectedScope, *project)
	if err != nil {
		return fail(stderr, "skills status: %v", err)
	}
	result, err := skillinstall.Status(skillinstall.Options{
		Agents:     agents,
		Scope:      selectedScope,
		HomeDir:    home,
		ProjectDir: projectRoot,
	})
	if err != nil {
		return fail(stderr, "skills status: %v", err)
	}
	if *jsonOutput {
		document := struct {
			Schema  int                        `json:"schema"`
			Scope   skillinstall.Scope         `json:"scope"`
			Entries []skillinstall.StatusEntry `json:"entries"`
		}{Schema: 1, Scope: selectedScope, Entries: result.Entries}
		data, marshalErr := json.MarshalIndent(document, "", "  ")
		if marshalErr != nil {
			return fail(stderr, "skills status: encode JSON: %v", marshalErr)
		}
		_, _ = stdout.Write(append(data, '\n'))
		return 0
	}
	for _, entry := range result.Entries {
		installed := entry.InstalledVersion
		if installed == "" {
			installed = "-"
		}
		fmt.Fprintf(stdout, "%s %s discovered_by=%s installed=%s available=%s action=%s %s\n", entry.State, entry.Agent, strings.Join(entry.DiscoveredBy, ","), installed, entry.AvailableVersion, entry.Recommendation, entry.Path)
	}
	return 0
}

func resolveSkillsRoots(scope skillinstall.Scope, project string) (home, projectRoot string, err error) {
	switch scope {
	case skillinstall.UserScope:
		home, err = os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("resolve user home: %w", err)
		}
		return home, "", nil
	case skillinstall.ProjectScope:
		command := exec.Command("git", "-C", project, "rev-parse", "--show-toplevel")
		command.Stdin = nil
		command.Stderr = io.Discard
		output, commandErr := command.Output()
		if commandErr != nil {
			return "", "", fmt.Errorf("resolve project Git top-level")
		}
		projectRoot, commandErr = parseGitTopLevelRecord(output)
		if commandErr != nil {
			return "", "", fmt.Errorf("resolve project Git top-level: %w", commandErr)
		}
		return "", projectRoot, nil
	default:
		return "", project, nil
	}
}

func parseGitTopLevelRecord(output []byte) (string, error) {
	if len(output) == 0 || output[len(output)-1] != '\n' {
		return "", fmt.Errorf("missing record terminator")
	}
	record := output[:len(output)-1]
	if len(record) == 0 {
		return "", fmt.Errorf("empty record")
	}
	if bytes.ContainsAny(record, "\x00\n") {
		return "", fmt.Errorf("unexpected extra or invalid record")
	}
	return string(record), nil
}

func newFlagSet(name string) *flag.FlagSet {
	result := flag.NewFlagSet(name, flag.ContinueOnError)
	result.SetOutput(io.Discard)
	return result
}

func hasHelpRequest(args []string) bool {
	for _, argument := range args {
		if isHelpToken(argument) {
			return true
		}
	}
	return false
}

func isHelpToken(argument string) bool {
	return argument == "--help" || argument == "-h"
}

func writeHelp(output io.Writer, invocation string, path []string) {
	_, _ = io.WriteString(output, commandmeta.Help(invocation, path))
}

func fail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+format+"\n", args...)
	return errorExit
}
