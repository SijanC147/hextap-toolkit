// Package cli implements hextapctl's deterministic command interface.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/formula"
	"github.com/SijanC147/hextap-toolkit/internal/githuboutput"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
	"github.com/SijanC147/hextap-toolkit/internal/release"
)

const errorExit = 2

// Run executes one hextapctl command and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer, version, commit string) int {
	if len(args) == 0 {
		return fail(stderr, "command required; expected version, manifest, formula, or release")
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
	case "release":
		return runRelease(args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown command %q; expected version, manifest, formula, or release", args[0])
	}
}

func runManifest(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, "manifest subcommand required; expected validate or export")
	}
	switch args[0] {
	case "validate":
		return runManifestValidate(args[1:], stdout, stderr)
	case "export":
		return runManifestExport(args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown manifest subcommand %q; expected validate or export", args[0])
	}
}

func runManifestValidate(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("manifest validate")
	file := flags.String("file", "", "project manifest path")
	if err := flags.Parse(args); err != nil {
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

func runManifestExport(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("manifest export")
	file := flags.String("file", "", "project manifest path")
	repository := flags.String("repository", "", "calling owner/name repository")
	githubOutput := flags.String("github-output", "", "GitHub Actions output path")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "manifest export: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "manifest export: unexpected positional arguments")
	}
	if missing := missingFlags([]namedValue{{"--file", *file}, {"--repository", *repository}, {"--github-output", *githubOutput}}); len(missing) != 0 {
		return fail(stderr, "manifest export: required flag missing: %s", strings.Join(missing, ", "))
	}
	project, err := readManifest(*file)
	if err != nil {
		return fail(stderr, "export manifest: %v", err)
	}
	values, err := project.WorkflowExport(*repository)
	if err != nil {
		return fail(stderr, "export manifest: %v", err)
	}
	fields := []githuboutput.Field{
		{Key: "formula", Value: values.Formula},
		{Key: "binary", Value: values.Binary},
		{Key: "owner", Value: values.Owner},
		{Key: "repository_name", Value: values.RepositoryName},
		{Key: "repository", Value: values.Repository},
		{Key: "arm64_asset", Value: values.ARM64Asset},
		{Key: "amd64_asset", Value: values.AMD64Asset},
		{Key: "build_script", Value: values.BuildScript},
		{Key: "linux", Value: strconv.FormatBool(values.Linux)},
	}
	if err := githuboutput.Append(*githubOutput, fields); err != nil {
		return fail(stderr, "export manifest: %v", err)
	}
	fmt.Fprintf(stdout, "manifest exported: %s (%s)\n", values.Formula, values.Repository)
	return 0
}

func runRelease(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, "release subcommand required; expected metadata, build, or verify")
	}
	switch args[0] {
	case "metadata":
		return runReleaseMetadata(args[1:], stdout, stderr)
	case "build":
		return runReleaseBuild(args[1:], stdout, stderr)
	case "verify":
		return runReleaseVerify(args[1:], stdout, stderr)
	default:
		return fail(stderr, "unknown release subcommand %q; expected metadata, build, or verify", args[0])
	}
}

func runReleaseVerify(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("release verify")
	manifestPath := flags.String("manifest", "", "project manifest path")
	version := flags.String("version", "", "normalized release version without v")
	commit := flags.String("commit", "", "lowercase source commit")
	directory := flags.String("dir", "", "release distribution directory")
	executeTarget := flags.String("execute-target", "", "optional target to execute, such as darwin-arm64")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "release verify: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "release verify: unexpected positional arguments")
	}
	if missing := missingFlags([]namedValue{
		{"--manifest", *manifestPath},
		{"--version", *version},
		{"--commit", *commit},
		{"--dir", *directory},
	}); len(missing) != 0 {
		return fail(stderr, "release verify: required flag missing: %s", strings.Join(missing, ", "))
	}
	result, err := release.Verify(release.VerifyOptions{
		ManifestPath:  *manifestPath,
		Version:       *version,
		Commit:        *commit,
		Directory:     *directory,
		ExecuteTarget: *executeTarget,
	})
	if err != nil {
		return fail(stderr, "release verify: %v", err)
	}
	if result.ExecutedTarget != "" {
		fmt.Fprintf(stdout, "release verified: %s (%s %s, %d archives, executed %s)\n", *directory, result.Formula, result.Version, len(result.Assets), result.ExecutedTarget)
	} else {
		fmt.Fprintf(stdout, "release verified: %s (%s %s, %d archives)\n", *directory, result.Formula, result.Version, len(result.Assets))
	}
	return 0
}

func runReleaseBuild(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("release build")
	manifestPath := flags.String("manifest", "", "project manifest path")
	version := flags.String("version", "", "normalized release version without v")
	commit := flags.String("commit", "", "lowercase source commit")
	source := flags.String("source", "", "source directory")
	output := flags.String("output", "", "existing empty output directory")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "release build: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "release build: unexpected positional arguments")
	}
	if missing := missingFlags([]namedValue{
		{"--manifest", *manifestPath},
		{"--version", *version},
		{"--commit", *commit},
		{"--source", *source},
		{"--output", *output},
	}); len(missing) != 0 {
		return fail(stderr, "release build: required flag missing: %s", strings.Join(missing, ", "))
	}
	result, err := release.Build(release.BuildOptions{
		ManifestPath: *manifestPath,
		Version:      *version,
		Commit:       *commit,
		SourceDir:    *source,
		OutputDir:    *output,
	})
	if err != nil {
		return fail(stderr, "release build: %v", err)
	}
	fmt.Fprintf(stdout, "release built: %s (%s %s, %d archives)\n", *output, result.Formula, result.Version, len(result.Assets))
	return 0
}

func runReleaseMetadata(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("release metadata")
	tag := flags.String("tag", "", "v-prefixed release tag")
	mode := flags.String("mode", "", "full or homebrew-only")
	githubOutput := flags.String("github-output", "", "optional GitHub Actions output path")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "release metadata: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "release metadata: unexpected positional arguments")
	}
	if missing := missingFlags([]namedValue{{"--tag", *tag}, {"--mode", *mode}}); len(missing) != 0 {
		return fail(stderr, "release metadata: required flag missing: %s", strings.Join(missing, ", "))
	}
	metadata, err := release.ParseMetadata(*tag, *mode)
	if err != nil {
		return fail(stderr, "release metadata: %v", err)
	}
	if *githubOutput != "" {
		fields := []githuboutput.Field{
			{Key: "tag", Value: metadata.Tag},
			{Key: "version", Value: metadata.Version},
			{Key: "stable", Value: strconv.FormatBool(metadata.Stable)},
			{Key: "prerelease", Value: strconv.FormatBool(metadata.Prerelease)},
			{Key: "mode", Value: metadata.Mode},
		}
		if err := githuboutput.Append(*githubOutput, fields); err != nil {
			return fail(stderr, "release metadata: %v", err)
		}
	}
	fmt.Fprintf(stdout, "release metadata: %s (version %s, stable %t, prerelease %t, mode %s)\n", metadata.Tag, metadata.Version, metadata.Stable, metadata.Prerelease, metadata.Mode)
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

type namedValue struct {
	name  string
	value string
}

func missingFlags(values []namedValue) []string {
	missing := make([]string, 0)
	for _, value := range values {
		if value.value == "" {
			missing = append(missing, value.name)
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
