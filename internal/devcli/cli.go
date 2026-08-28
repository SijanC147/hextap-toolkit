package devcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/commandmeta"
	"github.com/SijanC147/hextap-toolkit/internal/release"
)

// Run executes the installed developer command surface.
func Run(args []string, stdout, stderr io.Writer, version, commit string) int {
	return RunNamed("brew-hextap", args, stdout, stderr, version, commit)
}

// RunNamed executes the developer surface with invocation-aware help.
func RunNamed(invocation string, args []string, stdout, stderr io.Writer, version, commit string) int {
	service := Service{Stdout: stdout, Stderr: stderr, Invocation: invocation, Version: version, Commit: commit}
	return service.runCLI(context.Background(), args, stdout, stderr)
}

func (service Service) runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 && (isHelpToken(args[0]) || args[0] == "help") {
		service.writeHelp(stdout, []string{"dev"})
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
	binder := commandmeta.Bind(flags, "dev", "release")
	project := binder.String("project", ".")
	bump := binder.String("bump", "")
	confirmTag := binder.String("confirm-tag", "")
	execute := binder.Bool("execute", false)
	install := binder.Bool("install", false)
	jsonOutput := binder.Bool("json", false)
	var skillAgents stringList
	binder.Var(&skillAgents, "skill-agent")
	if isHelp(args) {
		service.writeHelp(stdout, []string{"dev", "release"})
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
	binder := commandmeta.Bind(flags, "dev", "deploy")
	project := binder.String("project", ".")
	bump := binder.String("bump", "")
	confirmTag := binder.String("confirm-tag", "")
	execute := binder.Bool("execute", false)
	install := binder.Bool("install", false)
	prTitle := binder.String("pr-title", "")
	jsonOutput := binder.Bool("json", false)
	var skillAgents stringList
	binder.Var(&skillAgents, "skill-agent")
	if isHelp(args) {
		service.writeHelp(stdout, []string{"dev", "deploy"})
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
	binder := commandmeta.Bind(flags, "dev", "install")
	project := binder.String("project", ".")
	tag := binder.String("tag", "")
	commit := binder.String("commit", "")
	execute := binder.Bool("execute", false)
	jsonOutput := binder.Bool("json", false)
	var skillAgents stringList
	binder.Var(&skillAgents, "skill-agent")
	if isHelp(args) {
		service.writeHelp(stdout, []string{"dev", "install"})
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
	binder := commandmeta.Bind(flags, "dev", "status")
	project := binder.String("project", ".")
	jsonOutput := binder.Bool("json", false)
	if isHelp(args) {
		service.writeHelp(stdout, []string{"dev", "status"})
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
	binder := commandmeta.Bind(flags, "dev", "validate")
	project := binder.String("project", ".")
	quick := binder.Bool("quick", false)
	jsonOutput := binder.Bool("json", false)
	if isHelp(args) {
		service.writeHelp(stdout, []string{"dev", "validate"})
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
	binder := commandmeta.Bind(flags, "dev", "plan")
	project := binder.String("project", ".")
	bump := binder.String("bump", "")
	jsonOutput := binder.Bool("json", false)
	if isHelp(args) {
		service.writeHelp(stdout, []string{"dev", "plan"})
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

func (service Service) writeHelp(output io.Writer, path []string) {
	invocation := service.Invocation
	if invocation == "" {
		invocation = "brew-hextap"
	}
	_, _ = io.WriteString(output, commandmeta.Help(invocation, path))
}

func cliFail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+strings.TrimSpace(format)+"\n", args...)
	return 2
}
