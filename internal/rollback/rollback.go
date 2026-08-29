package rollback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultTapName          = "sean/hextap"
	defaultOwnedRemote      = "https://github.com/SijanC147/homebrew-hextap.git"
	toolkitFormula          = "sean/hextap/hextap"
	maximumHistoryRevisions = 512
)

var (
	packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9@+._-]*$`)
	fullCommitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	releaseURLPattern  = regexp.MustCompile(`/releases/download/v?([^/"']+)/`)
	tagURLPattern      = regexp.MustCompile(`/refs/tags/v?([^/"']+?)(?:\.tar\.[A-Za-z0-9.]+|\.zip)?["']`)
	explicitVersion    = regexp.MustCompile(`(?m)^\s*version\s+["']([^"']+)["']\s*$`)
)

type preparedPlan struct {
	plan            Plan
	brew            string
	tapPath         string
	branch          string
	remote          string
	definition      string
	currentBytes    []byte
	historicalBytes []byte
	remoteFiles     map[string][]byte
	currentMode     fs.FileMode
}

// Run builds a fresh plan and optionally executes it after exact confirmation.
func (service Service) Run(ctx context.Context, options Options) (Outcome, error) {
	prepared, err := service.prepare(ctx, options)
	if err != nil {
		return Outcome{}, err
	}
	outcome := Outcome{Plan: prepared.plan}
	if !options.Execute {
		return outcome, nil
	}
	if options.Confirm == "" || options.Confirm != prepared.plan.Confirmation {
		return outcome, fmt.Errorf("refusing execution: --execute requires the exact --confirm value from this fresh plan")
	}
	if options.Mode == RemoteMode {
		return service.executeRemote(ctx, prepared, outcome)
	}
	return service.executeLocal(ctx, prepared, outcome)
}

func (service Service) prepare(ctx context.Context, options Options) (preparedPlan, error) {
	if options.Mode == "" {
		options.Mode = LocalMode
	}
	if options.Kind != FormulaKind && options.Kind != CaskKind {
		return preparedPlan{}, fmt.Errorf("kind must be formula or cask")
	}
	if options.Mode != LocalMode && options.Mode != RemoteMode {
		return preparedPlan{}, fmt.Errorf("mode must be local or remote")
	}
	if !packageNamePattern.MatchString(options.Name) {
		return preparedPlan{}, fmt.Errorf("package name must match %s", packageNamePattern)
	}
	if (options.ToCommit == "") == (options.ToVersion == "") {
		return preparedPlan{}, fmt.Errorf("select exactly one historical revision with --to-commit or --to-version")
	}
	if options.ToCommit != "" && !fullCommitPattern.MatchString(options.ToCommit) {
		return preparedPlan{}, fmt.Errorf("--to-commit must be a full 40-character lowercase commit SHA")
	}

	brew, tapPath, err := service.resolveHomebrew(ctx)
	if err != nil {
		return preparedPlan{}, err
	}
	definition := definitionPath(options.Kind, options.Name)
	if err := validateDefinitionPath(tapPath, definition); err != nil {
		return preparedPlan{}, err
	}
	runner := service.runner()
	head, err := runLine(ctx, runner, service.command("git", "-C", tapPath, "rev-parse", "HEAD"))
	if err != nil || !fullCommitPattern.MatchString(head) {
		return preparedPlan{}, fmt.Errorf("could not resolve the tap's exact HEAD")
	}
	branch, err := runLine(ctx, runner, service.command("git", "-C", tapPath, "symbolic-ref", "--quiet", "--short", "HEAD"))
	if err != nil || branch != "main" {
		return preparedPlan{}, fmt.Errorf("tap must be on its canonical main branch")
	}
	status, err := runner.Run(ctx, service.command("git", "-C", tapPath, "status", "--porcelain=v1", "--untracked-files=all"))
	if err != nil {
		return preparedPlan{}, fmt.Errorf("could not inspect tap cleanliness")
	}
	if status.Stdout != "" {
		return preparedPlan{}, fmt.Errorf("tap must be clean before rollback planning or execution")
	}
	remote, err := runLine(ctx, runner, service.command("git", "-C", tapPath, "remote", "get-url", "origin"))
	if err != nil || !service.isOwnedRemote(remote) {
		return preparedPlan{}, fmt.Errorf("tap origin is not the exact owned homebrew-hextap repository")
	}
	currentBytes, err := service.gitShow(ctx, tapPath, head, definition)
	if err != nil {
		return preparedPlan{}, fmt.Errorf("current %s definition is not tracked at tap HEAD", options.Kind)
	}
	diskBytes, err := os.ReadFile(filepath.Join(tapPath, filepath.FromSlash(definition)))
	if err != nil || !bytes.Equal(diskBytes, currentBytes) {
		return preparedPlan{}, fmt.Errorf("tap definition differs from the exact HEAD bytes")
	}
	diskInfo, err := os.Lstat(filepath.Join(tapPath, filepath.FromSlash(definition)))
	if err != nil || !diskInfo.Mode().IsRegular() {
		return preparedPlan{}, fmt.Errorf("tap definition mode is unavailable")
	}

	targetCommit := options.ToCommit
	if targetCommit == "" {
		targetCommit, err = service.resolveVersion(ctx, tapPath, definition, options.Kind, options.ToVersion)
		if err != nil {
			return preparedPlan{}, err
		}
	}
	ancestorCommand := service.command("git", "-C", tapPath, "merge-base", "--is-ancestor", targetCommit, head)
	if _, err := runner.Run(ctx, ancestorCommand); err != nil {
		return preparedPlan{}, fmt.Errorf("historical commit must be an ancestor of the unchanged tap HEAD")
	}
	if targetCommit == head {
		return preparedPlan{}, fmt.Errorf("historical commit must differ from the current tap HEAD")
	}
	historicalBytes, err := service.gitShow(ctx, tapPath, targetCommit, definition)
	if err != nil {
		return preparedPlan{}, fmt.Errorf("historical commit does not contain the selected %s definition", options.Kind)
	}
	targetVersion, err := versionFromDefinition(options.Kind, historicalBytes)
	if err != nil {
		return preparedPlan{}, fmt.Errorf("historical definition version is not safely derivable")
	}
	if options.ToVersion != "" && targetVersion != options.ToVersion {
		return preparedPlan{}, fmt.Errorf("historical version selector became inconsistent")
	}

	packageState, err := service.inspectPackage(ctx, brew, options.Kind, options.Name)
	if err != nil {
		return preparedPlan{}, err
	}
	if !packageState.installed {
		return preparedPlan{}, fmt.Errorf("selected %s must already be installed", options.Kind)
	}
	if packageState.pinned {
		return preparedPlan{}, fmt.Errorf("selected %s is pinned and Homebrew refuses reinstall", options.Kind)
	}
	currentVersion, versionErr := versionFromDefinition(options.Kind, currentBytes)
	if versionErr != nil || currentVersion != packageState.version {
		return preparedPlan{}, fmt.Errorf("current tap definition version does not match owning Homebrew state")
	}
	if options.Mode == LocalMode && options.Kind == FormulaKind {
		if err := service.requireInactiveService(ctx, brew, options.Name); err != nil {
			return preparedPlan{}, err
		}
	}

	tapName := service.tapName()
	fullName := tapName + "/" + options.Name
	plan := Plan{
		Schema: 1, Mode: options.Mode, Kind: options.Kind, Name: options.Name, FullName: fullName,
		TapPath: tapPath, OriginalCommit: head, TargetCommit: targetCommit,
		CurrentVersion: packageState.version, TargetVersion: targetVersion,
		CurrentVersionScheme: packageState.versionScheme,
		Actions:              []string{},
	}
	prepared := preparedPlan{
		plan: plan, brew: brew, tapPath: tapPath, branch: branch, remote: remote,
		definition: definition, currentBytes: currentBytes, historicalBytes: historicalBytes,
		currentMode: diskInfo.Mode().Perm(),
	}
	if options.Mode == RemoteMode {
		prepared.remoteFiles, prepared.plan.PlannedVersionScheme, err = service.reconcileRemoteFiles(options.Kind, options.Name, tapPath, definition, currentBytes, historicalBytes, packageState.versionScheme)
		if err != nil {
			return preparedPlan{}, fmt.Errorf("cannot safely reconcile historical and canonical definitions: %w", err)
		}
		prepared.plan.Paths = sortedFileNames(prepared.remoteFiles)
		prepared.plan.Branch = remoteBranch(options.Kind, options.Name, targetVersion, targetCommit)
		prepared.plan.Actions = []string{
			"clone unchanged canonical main into a temporary directory",
			"write only " + strings.Join(prepared.plan.Paths, ", ") + " with historical release metadata and current canonical runtime structure",
			"commit and push only " + prepared.plan.Branch,
			"open a protected pull request targeting main; do not merge it",
		}
		prepared.plan.Convergence = convergence(options.Kind, fullName, packageState.autoUpdates, prepared.plan.PlannedVersionScheme)
	} else {
		prepared.plan.Actions = []string{
			"recheck unchanged clean tap HEAD and inactive service state",
			"temporarily check out only " + definition + " from " + targetCommit,
			"run bounded brew reinstall for " + fullName,
			"restore exact original HEAD bytes and index, then prove the tap clean",
		}
		prepared.plan.Convergence = "local mode installs the selected historical definition once; a later brew update and upgrade may return to the canonical tap version"
	}
	prepared.plan.Confirmation = confirmation(prepared.plan)
	return prepared, nil
}

type packageState struct {
	version       string
	versionScheme int
	installed     bool
	pinned        bool
	autoUpdates   bool
}

func (service Service) inspectPackage(ctx context.Context, brew string, kind Kind, name string) (packageState, error) {
	flag := "--formula"
	if kind == CaskKind {
		flag = "--cask"
	}
	command := service.command(brew, "info", "--json=v2", flag, service.tapName()+"/"+name)
	command.Env = homebrewEnvironment()
	result, err := service.runner().Run(ctx, command)
	if err != nil {
		return packageState{}, fmt.Errorf("Homebrew could not inspect the selected %s", kind)
	}
	if kind == FormulaKind {
		var document struct {
			Formulae []struct {
				Name     string `json:"name"`
				FullName string `json:"full_name"`
				Versions struct {
					Stable string `json:"stable"`
				} `json:"versions"`
				Installed []struct {
					Version string `json:"version"`
				} `json:"installed"`
				Pinned        bool `json:"pinned"`
				VersionScheme int  `json:"version_scheme"`
			} `json:"formulae"`
		}
		if json.Unmarshal([]byte(result.Stdout), &document) != nil || len(document.Formulae) != 1 || document.Formulae[0].Name != name || document.Formulae[0].FullName != service.tapName()+"/"+name || document.Formulae[0].Versions.Stable == "" {
			return packageState{}, fmt.Errorf("Homebrew returned an invalid Formula record")
		}
		entry := document.Formulae[0]
		return packageState{version: entry.Versions.Stable, versionScheme: entry.VersionScheme, installed: len(entry.Installed) != 0, pinned: entry.Pinned}, nil
	}
	var document struct {
		Casks []struct {
			Token       string          `json:"token"`
			FullToken   string          `json:"full_token"`
			Version     string          `json:"version"`
			Installed   json.RawMessage `json:"installed"`
			AutoUpdates *bool           `json:"auto_updates"`
			Pinned      bool            `json:"pinned"`
		} `json:"casks"`
	}
	if json.Unmarshal([]byte(result.Stdout), &document) != nil || len(document.Casks) != 1 || document.Casks[0].Token != name || document.Casks[0].FullToken != service.tapName()+"/"+name || document.Casks[0].Version == "" {
		return packageState{}, fmt.Errorf("Homebrew returned an invalid Cask record")
	}
	entry := document.Casks[0]
	var installedVersion string
	_ = json.Unmarshal(entry.Installed, &installedVersion)
	autoUpdates := entry.AutoUpdates != nil && *entry.AutoUpdates
	return packageState{version: entry.Version, installed: installedVersion != "", pinned: entry.Pinned, autoUpdates: autoUpdates}, nil
}

func (service Service) requireInactiveService(ctx context.Context, brew, name string) error {
	command := service.command(brew, "services", "list", "--json")
	command.Env = homebrewEnvironment()
	result, err := service.runner().Run(ctx, command)
	if err != nil {
		return fmt.Errorf("could not prove the selected Formula service inactive")
	}
	var entries []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(result.Stdout), &entries) != nil {
		return fmt.Errorf("could not prove the selected Formula service inactive")
	}
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		if entry.Status != "none" && entry.Status != "stopped" {
			return fmt.Errorf("refusing rollback while %s has an active Homebrew service (%s); Hextap will not stop it", name, entry.Status)
		}
	}
	return nil
}

func (service Service) resolveVersion(ctx context.Context, tapPath, definition string, kind Kind, version string) (string, error) {
	result, err := service.runner().Run(ctx, service.command("git", "-C", tapPath, "log", "--format=%H", "--", definition))
	if err != nil {
		return "", fmt.Errorf("could not inspect definition history")
	}
	commits := strings.Fields(result.Stdout)
	if len(commits) > maximumHistoryRevisions {
		return "", fmt.Errorf("definition history exceeds the bounded search; use --to-commit")
	}
	matches := make([]string, 0, 1)
	for _, commit := range commits {
		if !fullCommitPattern.MatchString(commit) {
			return "", fmt.Errorf("definition history contains an invalid commit record")
		}
		data, showErr := service.gitShow(ctx, tapPath, commit, definition)
		if showErr != nil {
			continue
		}
		candidate, parseErr := versionFromDefinition(kind, data)
		if parseErr == nil && candidate == version {
			matches = append(matches, commit)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no historical definition has exact version %q", version)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("historical version %q is ambiguous across %d commits; use --to-commit", version, len(matches))
	}
	return matches[0], nil
}

func (service Service) gitShow(ctx context.Context, tapPath, commit, definition string) ([]byte, error) {
	result, err := service.runner().Run(ctx, service.command("git", "-C", tapPath, "show", commit+":"+definition))
	if err != nil || result.Stdout == "" || !strings.HasSuffix(result.Stdout, "\n") {
		return nil, fmt.Errorf("invalid Git blob")
	}
	return []byte(result.Stdout), nil
}

func (service Service) resolveHomebrew(ctx context.Context) (string, string, error) {
	if service.Brew != "" && service.TapPath != "" {
		return service.Brew, service.TapPath, nil
	}
	executable := service.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return "", "", fmt.Errorf("could not resolve active Hextap executable")
		}
	}
	resolve := service.ResolvePath
	if resolve == nil {
		resolve = filepath.EvalSymlinks
	}
	active, err := resolve(executable)
	if err != nil {
		return "", "", fmt.Errorf("could not resolve active Hextap executable")
	}
	candidates := append([]string(nil), service.BrewCandidates...)
	if len(candidates) == 0 {
		candidates = []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"}
		if path, lookErr := exec.LookPath("brew"); lookErr == nil {
			candidates = append(candidates, path)
		}
	}
	for _, candidate := range candidates {
		command := service.command(candidate, "--prefix", toolkitFormula)
		command.Env = homebrewEnvironment()
		prefix, lineErr := runLine(ctx, service.runner(), command)
		if lineErr != nil {
			continue
		}
		owned, resolveErr := resolve(filepath.Join(prefix, "bin", "brew-hextap"))
		if resolveErr == nil && owned == active {
			tapCommand := service.command(candidate, "--repo", service.tapName())
			tapCommand.Env = homebrewEnvironment()
			tapPath, tapErr := runLine(ctx, service.runner(), tapCommand)
			if tapErr != nil {
				return "", "", fmt.Errorf("owning Homebrew could not resolve the Hextap tap")
			}
			return candidate, tapPath, nil
		}
	}
	return "", "", fmt.Errorf("could not identify the Homebrew installation that owns active Hextap")
}

func validateDefinitionPath(tapPath, definition string) error {
	originalInfo, err := os.Lstat(tapPath)
	if err != nil || !originalInfo.IsDir() || originalInfo.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("tap must be a regular directory")
	}
	root, err := filepath.EvalSymlinks(tapPath)
	if err != nil {
		return fmt.Errorf("tap path is unavailable")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("tap must be a regular directory")
	}
	path := filepath.Join(root, filepath.FromSlash(definition))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return fmt.Errorf("definition path must be a regular file contained by the tap")
	}
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("definition path must be a regular file contained by the tap")
	}
	return nil
}

func versionFromDefinition(kind Kind, data []byte) (string, error) {
	if kind == CaskKind {
		match := explicitVersion.FindSubmatch(data)
		if len(match) != 2 || len(explicitVersion.FindAllSubmatch(data, -1)) != 1 {
			return "", fmt.Errorf("Cask must contain one literal version")
		}
		return checkedVersion(string(match[1]))
	}
	if matches := explicitVersion.FindAllSubmatch(data, -1); len(matches) == 1 {
		return checkedVersion(string(matches[0][1]))
	} else if len(matches) > 1 {
		return "", fmt.Errorf("Formula has multiple explicit versions")
	}
	versions := uniqueCaptures(releaseURLPattern, data)
	if len(versions) == 0 {
		versions = uniqueCaptures(tagURLPattern, data)
	}
	if len(versions) != 1 {
		return "", fmt.Errorf("Formula release URLs do not identify one version")
	}
	return checkedVersion(versions[0])
}

func checkedVersion(value string) (string, error) {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("version is empty, oversized, or padded")
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", fmt.Errorf("version contains whitespace or control characters")
		}
	}
	return value, nil
}

func uniqueCaptures(pattern *regexp.Regexp, data []byte) []string {
	seen := make(map[string]bool)
	var values []string
	for _, match := range pattern.FindAllSubmatch(data, -1) {
		value := string(match[1])
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	return values
}

func definitionPath(kind Kind, name string) string {
	if kind == CaskKind {
		return "Casks/" + name + ".rb"
	}
	return "Formula/" + name + ".rb"
}

func (service Service) tapName() string {
	if service.TapName != "" {
		return service.TapName
	}
	return defaultTapName
}

func (service Service) ownedRemote() string {
	if service.OwnedRemote != "" {
		return service.OwnedRemote
	}
	return defaultOwnedRemote
}

func (service Service) isOwnedRemote(candidate string) bool {
	owned := service.ownedRemote()
	if owned != defaultOwnedRemote {
		return candidate == owned
	}
	if candidate == defaultOwnedRemote || candidate == "git@github.com:SijanC147/homebrew-hextap.git" {
		return true
	}
	parsed, err := url.Parse(candidate)
	return err == nil && parsed.User == nil && parsed.Scheme == "https" && parsed.Host == "github.com" && parsed.RawQuery == "" && parsed.Fragment == "" && strings.TrimSuffix(parsed.Path, ".git") == "/SijanC147/homebrew-hextap"
}

func (service Service) runner() Runner {
	if service.Runner != nil {
		return service.Runner
	}
	return osRunner{}
}

func (service Service) command(name string, args ...string) Command {
	timeout := service.CommandTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return Command{Name: name, Args: args, Timeout: timeout}
}

func runLine(ctx context.Context, runner Runner, command Command) (string, error) {
	result, err := runner.Run(ctx, command)
	if err != nil {
		return "", err
	}
	if result.Stdout == "" || !strings.HasSuffix(result.Stdout, "\n") || strings.Count(result.Stdout, "\n") != 1 || strings.ContainsRune(result.Stdout, '\x00') {
		return "", fmt.Errorf("invalid command record")
	}
	return strings.TrimSuffix(result.Stdout, "\n"), nil
}

func confirmation(plan Plan) string {
	value := strings.Join([]string{"ROLLBACK", string(plan.Mode), string(plan.Kind), plan.FullName, plan.TargetCommit}, " ")
	if plan.Mode == RemoteMode {
		value += " VIA " + plan.Branch
	}
	return value
}

func remoteBranch(kind Kind, name, version, commit string) string {
	safeVersion := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(version, "-")
	safeVersion = strings.Trim(safeVersion, "-.")
	if safeVersion == "" {
		safeVersion = commit[:12]
	}
	if len(safeVersion) > 64 {
		safeVersion = safeVersion[:64]
	}
	return "codex/hextap-rollback-" + string(kind) + "-" + name + "-to-" + safeVersion
}

func convergence(kind Kind, fullName string, autoUpdates bool, scheme int) string {
	if kind == FormulaKind {
		return "yes: brew update && brew upgrade " + fullName + " converges because published version_scheme " + strconv.Itoa(scheme) + " is newer than the installed scheme even though the package version decreases"
	}
	if autoUpdates {
		return "not by default: this Cask declares auto_updates; after merge use brew update && brew upgrade --cask --greedy " + fullName + " to converge by replacement"
	}
	return "yes: brew update && brew upgrade --cask " + fullName + " converges because Homebrew Cask treats any installed/current version mismatch as outdated and replaces it"
}
