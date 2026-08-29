package rollback

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/commandmeta"
)

const cliErrorExit = 2

// RunCLI parses the rollback command, renders its default plan, and executes
// only when both execution authorization fields match the fresh plan.
func (service Service) RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	invocation := service.Invocation
	if invocation == "" {
		invocation = "brew-hextap"
	}
	if len(args) == 0 {
		return rollbackCLIFail(stderr, "rollback: subcommand required; expected formula or cask")
	}
	if isHelp(args[0]) {
		_, _ = io.WriteString(stdout, commandmeta.Help(invocation, []string{"rollback"}))
		return 0
	}
	kind := Kind(args[0])
	if kind != FormulaKind && kind != CaskKind {
		return rollbackCLIFail(stderr, "rollback: unknown subcommand %q; expected formula or cask", args[0])
	}
	if hasHelp(args[1:]) {
		_, _ = io.WriteString(stdout, commandmeta.Help(invocation, []string{"rollback", string(kind)}))
		return 0
	}
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return rollbackCLIFail(stderr, "rollback %s: require package NAME immediately after the subcommand", kind)
	}
	name := args[1]
	flags := flag.NewFlagSet("rollback "+string(kind), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	binder := commandmeta.Bind(flags, "rollback", string(kind))
	toCommit := binder.String("to-commit", "")
	toVersion := binder.String("to-version", "")
	mode := binder.String("mode", string(LocalMode))
	execute := binder.Bool("execute", false)
	confirm := binder.String("confirm", "")
	jsonOutput := binder.Bool("json", false)
	if flags.Parse(args[2:]) != nil || flags.NArg() != 0 {
		return rollbackCLIFail(stderr, "rollback %s: require exactly one package NAME and valid options", kind)
	}
	outcome, err := service.Run(ctx, Options{
		Kind: kind, Name: name, ToCommit: *toCommit, ToVersion: *toVersion,
		Mode: Mode(*mode), Execute: *execute, Confirm: *confirm,
	})
	if err != nil {
		return rollbackCLIFail(stderr, "rollback %s: %v", kind, err)
	}
	if *jsonOutput {
		data, marshalErr := json.MarshalIndent(outcome, "", "  ")
		if marshalErr != nil {
			return rollbackCLIFail(stderr, "rollback %s: encode JSON", kind)
		}
		_, _ = stdout.Write(append(data, '\n'))
		return 0
	}
	renderOutcome(stdout, outcome)
	return 0
}

func renderOutcome(output io.Writer, outcome Outcome) {
	plan := outcome.Plan
	fmt.Fprintf(output, "ROLLBACK mode=%s kind=%s package=%s\n", plan.Mode, plan.Kind, plan.FullName)
	fmt.Fprintf(output, "FROM version=%s commit=%s", plan.CurrentVersion, plan.OriginalCommit)
	if plan.CurrentVersionScheme != 0 {
		fmt.Fprintf(output, " version_scheme=%d", plan.CurrentVersionScheme)
	}
	_, _ = io.WriteString(output, "\n")
	fmt.Fprintf(output, "TO version=%s commit=%s", plan.TargetVersion, plan.TargetCommit)
	if plan.PlannedVersionScheme != 0 {
		fmt.Fprintf(output, " version_scheme=%d", plan.PlannedVersionScheme)
	}
	_, _ = io.WriteString(output, "\n")
	if plan.Branch != "" {
		fmt.Fprintf(output, "BRANCH %s\n", plan.Branch)
	}
	for _, action := range plan.Actions {
		fmt.Fprintf(output, "PLAN %s\n", action)
	}
	fmt.Fprintf(output, "CONVERGENCE %s\n", plan.Convergence)
	fmt.Fprintf(output, "CONFIRM %s\n", plan.Confirmation)
	if outcome.Executed {
		_, _ = io.WriteString(output, "EXECUTED true\n")
	}
	if outcome.Restored {
		fmt.Fprintf(output, "RESTORED true tap_clean=%t\n", outcome.TapClean)
	}
	if outcome.PullRequestURL != "" {
		fmt.Fprintf(output, "PULL_REQUEST %s\n", outcome.PullRequestURL)
	}
}

func isHelp(argument string) bool { return argument == "-h" || argument == "--help" }

func hasHelp(arguments []string) bool {
	for _, argument := range arguments {
		if isHelp(argument) {
			return true
		}
	}
	return false
}

func rollbackCLIFail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+strings.TrimSpace(format)+"\n", args...)
	return cliErrorExit
}
