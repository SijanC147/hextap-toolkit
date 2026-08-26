package devcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/release"
)

const usageText = `usage: brew-hextap dev <command> [options]

Commands:
  status      Inspect toolkit repository and release state
  validate    Run local toolkit validation gates
  plan        Compute the next patch, minor, or major release
  release     Publish a confirmed release from canonical main
  deploy      Run protected PR, release, and optional install workflow
  install     Install and verify one released Hextap version
`

// Run executes the installed developer command surface.
func Run(args []string, stdout, stderr io.Writer, version, commit string) int {
	service := Service{Stdout: stdout, Stderr: stderr, Version: version, Commit: commit}
	return service.runCLI(context.Background(), args, stdout, stderr)
}

func (service Service) runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		_, _ = io.WriteString(stdout, usageText)
		return 0
	}
	if len(args) == 0 {
		return cliFail(stderr, "dev command required")
	}
	switch args[0] {
	case "status":
		return service.runStatusCLI(ctx, args[1:], stdout, stderr)
	case "validate":
		return service.runValidateCLI(ctx, args[1:], stdout, stderr)
	case "plan":
		return service.runPlanCLI(ctx, args[1:], stdout, stderr)
	case "release":
		return service.runReleaseCLI(ctx, args[1:], stdout, stderr)
	case "deploy":
		return service.runDeployCLI(ctx, args[1:], stdout, stderr)
	case "install":
		return service.runInstallCLI(ctx, args[1:], stdout, stderr)
	default:
		return cliFail(stderr, "unknown dev command %q", args[0])
	}
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func (service Service) runReleaseCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("dev release")
	project := flags.String("project", ".", "toolkit Git project")
	bump := flags.String("bump", "", "required release bump")
	confirmTag := flags.String("confirm-tag", "", "exact computed tag confirmation")
	execute := flags.Bool("execute", false, "authorize remote release mutation")
	install := flags.Bool("install", false, "upgrade local Hextap after remote proof")
	jsonOutput := flags.Bool("json", false, "emit versioned JSON")
	var skillAgents stringList
	flags.Var(&skillAgents, "skill-agent", "concrete user skill target to reconcile after install (repeatable)")
	if isHelp(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap dev release --bump patch|minor|major --confirm-tag vX.Y.Z --execute [--project PATH] [--install] [--skill-agent ID ...] [--json]\n")
		return 0
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "dev release: invalid arguments")
	}
	outcome, err := service.Release(ctx, ReleaseOptions{Project: *project, Bump: *bump, ConfirmTag: *confirmTag, Execute: *execute, Install: *install, SkillAgents: skillAgents})
	if err != nil {
		return cliFail(stderr, "dev release: %v", err)
	}
	return writeOutcome(stdout, stderr, *jsonOutput, outcome)
}

func (service Service) runDeployCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("dev deploy")
	project := flags.String("project", ".", "toolkit Git project")
	bump := flags.String("bump", "", "required release bump")
	confirmTag := flags.String("confirm-tag", "", "exact computed tag confirmation")
	execute := flags.Bool("execute", false, "authorize protected PR and release mutation")
	install := flags.Bool("install", false, "upgrade local Hextap after remote proof")
	prTitle := flags.String("pr-title", "", "pull request title; defaults to latest commit subject")
	jsonOutput := flags.Bool("json", false, "emit versioned JSON")
	var skillAgents stringList
	flags.Var(&skillAgents, "skill-agent", "concrete user skill target to reconcile after install (repeatable)")
	if isHelp(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap dev deploy --bump patch|minor|major --confirm-tag vX.Y.Z --execute [--project PATH] [--pr-title TEXT] [--install] [--skill-agent ID ...] [--json]\n")
		return 0
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "dev deploy: invalid arguments")
	}
	outcome, err := service.Deploy(ctx, DeployOptions{ReleaseOptions: ReleaseOptions{Project: *project, Bump: *bump, ConfirmTag: *confirmTag, Execute: *execute, Install: *install, SkillAgents: skillAgents}, PRTitle: *prTitle})
	if err != nil {
		return cliFail(stderr, "dev deploy: %v", err)
	}
	return writeOutcome(stdout, stderr, *jsonOutput, outcome)
}

func (service Service) runInstallCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("dev install")
	project := flags.String("project", ".", "toolkit Git project")
	tag := flags.String("tag", "", "published stable tag")
	commit := flags.String("commit", "", "exact released commit")
	execute := flags.Bool("execute", false, "authorize local Hextap mutation")
	jsonOutput := flags.Bool("json", false, "emit versioned JSON")
	var skillAgents stringList
	flags.Var(&skillAgents, "skill-agent", "concrete user skill target to reconcile (repeatable)")
	if isHelp(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap dev install --tag vX.Y.Z --commit FULL_SHA --execute [--project PATH] [--skill-agent ID ...] [--json]\n")
		return 0
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "dev install: invalid arguments")
	}
	result, err := service.Install(ctx, InstallOptions{Project: *project, Tag: *tag, ExpectedCommit: *commit, Execute: *execute, SkillAgents: skillAgents})
	if err != nil {
		return cliFail(stderr, "dev install: %v", err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, "dev install", struct {
			Schema int `json:"schema"`
			InstallResult
		}{Schema: 1, InstallResult: result})
	}
	fmt.Fprintf(stdout, "INSTALLED hextap %s commit=%s brew=%s binary=%s\n", result.Version, result.Commit, result.Brew, result.Binary)
	return 0
}

func writeOutcome(stdout, stderr io.Writer, jsonOutput bool, outcome Outcome) int {
	if jsonOutput {
		return writeJSON(stdout, stderr, "dev deployment", outcome)
	}
	fmt.Fprintf(stdout, "RELEASED %s commit=%s %s\n", outcome.Tag, outcome.Commit, outcome.ReleaseURL)
	fmt.Fprintf(stdout, "EVIDENCE main=%s release=%s tap=%s installed=%t\n", outcome.MainRunURL, outcome.ReleaseRunURL, outcome.TapRunURL, outcome.Installed)
	if outcome.PRURL != "" {
		fmt.Fprintf(stdout, "PR %s\n", outcome.PRURL)
	}
	return 0
}

func (service Service) runStatusCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("dev status")
	project := flags.String("project", ".", "toolkit Git project")
	jsonOutput := flags.Bool("json", false, "emit versioned JSON")
	if isHelp(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap dev status [--project PATH] [--json]\n")
		return 0
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "dev status: invalid arguments")
	}
	status, err := service.Status(ctx, *project)
	if err != nil {
		return cliFail(stderr, "dev status: %v", err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, "dev status", status)
	}
	fmt.Fprintf(stdout, "TOOLKIT %s %s\n", status.Repository, status.Project)
	fmt.Fprintf(stdout, "SOURCE branch=%s head=%s clean=%t origin=%s\n", status.Branch, status.Head, status.Clean, ToolkitOriginHTTPS)
	fmt.Fprintf(stdout, "GITHUB user=%s latest=%s\n", status.GitHubUser, status.LatestStableTag)
	fmt.Fprintf(stdout, "NEXT patch=%s minor=%s major=%s\n", status.NextPatch, status.NextMinor, status.NextMajor)
	fmt.Fprintf(stdout, "CLI version=%s commit=%s\n", status.CLIVersion, status.CLICommit)
	return 0
}

func (service Service) runValidateCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("dev validate")
	project := flags.String("project", ".", "toolkit Git project")
	quick := flags.Bool("quick", false, "skip the race detector for an iteration check")
	jsonOutput := flags.Bool("json", false, "emit versioned JSON")
	if isHelp(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap dev validate [--project PATH] [--quick] [--json]\n")
		return 0
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "dev validate: invalid arguments")
	}
	result, err := service.Validate(ctx, ValidateOptions{Project: *project, Full: !*quick})
	if err != nil {
		return cliFail(stderr, "dev validate: %v", err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, "dev validate", struct {
			Schema int `json:"schema"`
			ValidateResult
		}{Schema: 1, ValidateResult: result})
	}
	fmt.Fprintf(stdout, "VALIDATED toolkit %s race=%t\n", result.Project, result.Race)
	return 0
}

func (service Service) runPlanCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("dev plan")
	project := flags.String("project", ".", "toolkit Git project")
	bump := flags.String("bump", "", "required release bump: patch, minor, or major")
	jsonOutput := flags.Bool("json", false, "emit versioned JSON")
	if isHelp(args) {
		_, _ = io.WriteString(stdout, "usage: brew-hextap dev plan --bump patch|minor|major [--project PATH] [--json]\n")
		return 0
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "dev plan: invalid arguments")
	}
	selected := release.Bump(*bump)
	if selected != release.PatchBump && selected != release.MinorBump && selected != release.MajorBump {
		return cliFail(stderr, "dev plan: --bump must be patch, minor, or major")
	}
	plan, err := service.Plan(ctx, *project, selected)
	if err != nil {
		return cliFail(stderr, "dev plan: %v", err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, "dev plan", plan)
	}
	fmt.Fprintf(stdout, "RELEASE %s -> %s bump=%s commit=%s\n", plan.CurrentTag, plan.Tag, plan.Bump, plan.Commit)
	fmt.Fprintf(stdout, "CONFIRM --execute --confirm-tag %s\n", plan.Tag)
	return 0
}

func writeJSON(stdout, stderr io.Writer, label string, value any) int {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return cliFail(stderr, "%s: encode JSON: %v", label, err)
	}
	_, _ = stdout.Write(append(data, '\n'))
	return 0
}

func newFlagSet(name string) *flag.FlagSet {
	result := flag.NewFlagSet(name, flag.ContinueOnError)
	result.SetOutput(io.Discard)
	return result
}

func isHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}

func cliFail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+strings.TrimSpace(format)+"\n", args...)
	return 2
}
