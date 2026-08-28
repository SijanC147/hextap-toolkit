package inventory

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

const (
	errorExit   = 2
	statusUsage = "usage: brew-hextap status [-p|--project PATH] [-j|--json]\n"
	infoUsage   = "usage: brew-hextap info [-p|--project PATH] [-k|--kind all|project|formula|cask|skill] [-n|--name NAME] [-j|--json]\n"
)

// RunStatusCLI parses the read-only status command and writes an overview or
// the complete versioned JSON report.
func (service Service) RunStatusCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if isHelp(args) {
		_, _ = io.WriteString(stdout, statusUsage)
		return 0
	}
	flags := newFlagSet("status")
	project, jsonOutput := "", false
	flags.StringVar(&project, "project", "", "Hextap project root to inspect")
	flags.StringVar(&project, "p", "", "alias for --project")
	flags.BoolVar(&jsonOutput, "json", false, "emit versioned JSON")
	flags.BoolVar(&jsonOutput, "j", false, "alias for --json")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "status: invalid arguments")
	}
	report := service.Collect(ctx, Options{Project: project})
	if jsonOutput {
		return writeJSON(stdout, stderr, "status", report)
	}
	RenderStatus(stdout, report)
	return 0
}

// RunInfoCLI parses the read-only detailed inventory command and applies
// category and exact-name filters before rendering.
func (service Service) RunInfoCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if isHelp(args) {
		_, _ = io.WriteString(stdout, infoUsage)
		return 0
	}
	flags := newFlagSet("info")
	project, kind, name, jsonOutput := "", string(AllKind), "", false
	flags.StringVar(&project, "project", "", "Hextap project root to inspect")
	flags.StringVar(&project, "p", "", "alias for --project")
	flags.StringVar(&kind, "kind", string(AllKind), "inventory category")
	flags.StringVar(&kind, "k", string(AllKind), "alias for --kind")
	flags.StringVar(&name, "name", "", "exact package, project, or skill name")
	flags.StringVar(&name, "n", "", "alias for --name")
	flags.BoolVar(&jsonOutput, "json", false, "emit versioned JSON")
	flags.BoolVar(&jsonOutput, "j", false, "alias for --json")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "info: invalid arguments")
	}
	report, err := Filter(service.Collect(ctx, Options{Project: project}), Kind(kind), name)
	if err != nil {
		return cliFail(stderr, "info: --kind must be all, project, formula, cask, or skill")
	}
	if jsonOutput {
		return writeJSON(stdout, stderr, "info", report)
	}
	RenderInfo(stdout, report)
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

func writeJSON(stdout, stderr io.Writer, label string, value any) int {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return cliFail(stderr, "%s: encode JSON", label)
	}
	_, _ = stdout.Write(append(data, '\n'))
	return 0
}

func cliFail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+format+"\n", args...)
	return errorExit
}
