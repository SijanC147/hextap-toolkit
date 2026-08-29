package inventory

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/SijanC147/hextap-toolkit/internal/commandmeta"
)

const errorExit = 2

// RunStatusCLI parses the read-only status command and writes an overview or
// the complete versioned JSON report.
func (service Service) RunStatusCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if isHelp(args) {
		_, _ = io.WriteString(stdout, commandmeta.Help(service.Invocation, []string{"status"}))
		return 0
	}
	flags := newFlagSet("status")
	binder := commandmeta.Bind(flags, "status")
	project := binder.String("project", "")
	jsonOutput := binder.Bool("json", false)
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "status: invalid arguments")
	}
	report := service.Collect(ctx, Options{Project: *project})
	if *jsonOutput {
		return writeJSON(stdout, stderr, "status", report)
	}
	RenderStatus(stdout, report)
	return 0
}

// RunInfoCLI parses the read-only detailed inventory command and applies
// category and exact-name filters before rendering.
func (service Service) RunInfoCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if isHelp(args) {
		_, _ = io.WriteString(stdout, commandmeta.Help(service.Invocation, []string{"info"}))
		return 0
	}
	flags := newFlagSet("info")
	binder := commandmeta.Bind(flags, "info")
	project := binder.String("project", "")
	kind := binder.String("kind", string(AllKind))
	name := binder.String("name", "")
	jsonOutput := binder.Bool("json", false)
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return cliFail(stderr, "info: invalid arguments")
	}
	report, err := Filter(service.Collect(ctx, Options{Project: *project}), Kind(*kind), *name)
	if err != nil {
		return cliFail(stderr, "info: --kind must be all, project, formula, cask, or skill")
	}
	if *jsonOutput {
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
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			return true
		}
	}
	return false
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
