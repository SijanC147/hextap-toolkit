// Package cli implements hextapctl's deterministic command interface.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/formula"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const errorExit = 2

// Run executes one hextapctl command and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer, version, commit string) int {
	if len(args) == 0 {
		return fail(stderr, "command required; expected version, manifest, or formula")
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return fail(stderr, "version: unexpected arguments")
		}
		fmt.Fprintf(stdout, "hextapctl %s (commit %s)\n", version, commit)
		return 0
	case "manifest":
		return runManifest(args[1:], stdout, stderr)
	case "formula":
		return runFormula(args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown command %q; expected version, manifest, or formula", args[0])
	}
}

func runManifest(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, "manifest subcommand required; expected validate")
	}
	if args[0] != "validate" {
		return fail(stderr, "unknown manifest subcommand %q; expected validate", args[0])
	}
	flags := newFlagSet("manifest validate")
	file := flags.String("file", "", "project manifest path")
	if err := flags.Parse(args[1:]); err != nil {
		return fail(stderr, "manifest validate: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "manifest validate: unexpected positional arguments")
	}
	if *file == "" {
		return fail(stderr, "manifest validate: --file is required")
	}
	project, err := readManifest(*file)
	if err != nil {
		return fail(stderr, "validate manifest: %v", err)
	}
	fmt.Fprintf(stdout, "manifest valid: %s (schema %d, formula %s)\n", *file, project.Schema, project.Formula.Name)
	return 0
}

func runFormula(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, "formula subcommand required; expected render or update")
	}
	switch args[0] {
	case "render":
		return runFormulaRender(args[1:], stdout, stderr)
	case "update":
		return runFormulaUpdate(args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown formula subcommand %q; expected render or update", args[0])
	}
}

type formulaFlags struct {
	manifestPath *string
	version      *string
	arm64SHA     *string
	amd64SHA     *string
}

func addFormulaFlags(flags *flag.FlagSet) formulaFlags {
	return formulaFlags{
		manifestPath: flags.String("manifest", "", "project manifest path"),
		version:      flags.String("version", "", "stable release version without v"),
		arm64SHA:     flags.String("arm64-sha", "", "Darwin arm64 archive SHA-256"),
		amd64SHA:     flags.String("amd64-sha", "", "Darwin amd64 archive SHA-256"),
	}
}

func runFormulaRender(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("formula render")
	common := addFormulaFlags(flags)
	output := flags.String("output", "", "destination Formula path")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "formula render: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "formula render: unexpected positional arguments")
	}
	if missing := missingFormulaFlags(common, map[string]string{"--output": *output}); len(missing) != 0 {
		return fail(stderr, "formula render: required flag missing: %s", strings.Join(missing, ", "))
	}
	project, err := readManifest(*common.manifestPath)
	if err != nil {
		return fail(stderr, "render Formula: %v", err)
	}
	if err := formula.RenderFile(*output, project, *common.version, *common.arm64SHA, *common.amd64SHA); err != nil {
		return fail(stderr, "render Formula: %v", err)
	}
	fmt.Fprintf(stdout, "formula rendered: %s (%s %s)\n", *output, project.Formula.Name, *common.version)
	return 0
}

func runFormulaUpdate(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("formula update")
	common := addFormulaFlags(flags)
	formulaPath := flags.String("formula", "", "existing Formula path")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "formula update: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "formula update: unexpected positional arguments")
	}
	if missing := missingFormulaFlags(common, map[string]string{"--formula": *formulaPath}); len(missing) != 0 {
		return fail(stderr, "formula update: required flag missing: %s", strings.Join(missing, ", "))
	}
	project, err := readManifest(*common.manifestPath)
	if err != nil {
		return fail(stderr, "update Formula: %v", err)
	}
	result, err := formula.UpdateFile(*formulaPath, project, *common.version, *common.arm64SHA, *common.amd64SHA)
	if err != nil {
		return fail(stderr, "update Formula: %v", err)
	}
	if result.Changed {
		fmt.Fprintf(stdout, "formula updated: %s (%s -> %s)\n", *formulaPath, result.PreviousVersion, result.Version)
	} else {
		fmt.Fprintf(stdout, "formula unchanged: %s (%s)\n", *formulaPath, result.Version)
	}
	return 0
}

func missingFormulaFlags(common formulaFlags, extra map[string]string) []string {
	values := map[string]string{
		"--manifest":  *common.manifestPath,
		"--version":   *common.version,
		"--arm64-sha": *common.arm64SHA,
		"--amd64-sha": *common.amd64SHA,
	}
	for key, value := range extra {
		values[key] = value
	}
	order := []string{"--manifest", "--version", "--arm64-sha", "--amd64-sha", "--output", "--formula"}
	missing := make([]string, 0)
	for _, key := range order {
		if value, exists := values[key]; exists && value == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func readManifest(path string) (manifest.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("read %q: %w", path, err)
	}
	project, err := manifest.Parse(data)
	if err != nil {
		return manifest.Manifest{}, err
	}
	return project, nil
}

func newFlagSet(name string) *flag.FlagSet {
	result := flag.NewFlagSet(name, flag.ContinueOnError)
	result.SetOutput(io.Discard)
	return result
}

func fail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "error: "+format+"\n", args...)
	return errorExit
}
