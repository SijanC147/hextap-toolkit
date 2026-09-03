package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

const (
	maximumManifestSize      int64 = 1 << 20
	maximumRegistryEntries         = 1024
	maximumWarningPathLength       = 1024
	defaultCollectionTimeout       = 30 * time.Second
	homebrewCellarDirectory        = "Cellar"
	homebrewBinaryDirectory        = "bin"
	homebrewExecutableName         = "brew"
	toolkitExecutableName          = "brew-hextap"
)

var (
	gitRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9@+._-]*$`)
	environmentPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	scpRemotePattern   = regexp.MustCompile(`^git@[A-Za-z0-9.-]+:[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// Collect returns every safely available local result and records component
// warnings rather than failing the complete read-only report.
func (service Service) Collect(ctx context.Context, options Options) Report {
	timeout := service.CollectionTimeout
	if timeout <= 0 {
		timeout = defaultCollectionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	report := Report{
		Schema:   1,
		CLI:      CLIInfo{Version: service.Version, Commit: service.Commit, Executable: service.executable()},
		Tap:      TapInfo{Name: TapName},
		Projects: []ProjectInfo{},
		Formulae: []FormulaInfo{},
		Casks:    []CaskInfo{},
		Skills:   []SkillInfo{},
		Warnings: []Warning{},
	}
	service.collectSkills(&report, options.Project)

	ownership := service.findOwningHomebrew(ctx)
	if !ownership.Owned {
		addWarning(&report, "homebrew", "could not identify the Homebrew installation that owns the active Hextap CLI: "+ownership.Reason+"; Homebrew, tap and package inventory are unknown here, not proven absent")
		service.collectLocalProject(&report, options.Project, false)
		return report
	}
	brew := ownership.Executable
	report.Homebrew.Executable = brew
	if result, err := service.runner().Run(ctx, Command{Name: brew, Args: []string{"--prefix"}, Env: homebrewReadOnlyEnvironment()}); err == nil {
		if prefix, parseOK := parseLine(result.Stdout); parseOK {
			report.Homebrew.Prefix = prefix
		} else {
			addWarning(&report, "homebrew", "Homebrew returned an invalid prefix record")
		}
	} else {
		addWarning(&report, "homebrew", "could not inspect the Homebrew prefix")
	}

	tapPathResult, err := service.runner().Run(ctx, Command{Name: brew, Args: []string{"--repo", TapName}, Env: homebrewReadOnlyEnvironment()})
	if err != nil {
		addWarning(&report, "tap", "could not locate the canonical Hextap tap")
		service.collectLocalProject(&report, options.Project, false)
		return report
	}
	tapPath, ok := parseLine(tapPathResult.Stdout)
	if !ok {
		addWarning(&report, "tap", "Homebrew returned an invalid tap path record")
		service.collectLocalProject(&report, options.Project, false)
		return report
	}
	report.Tap.Path = tapPath
	info, statErr := service.fileSystem().Lstat(tapPath)
	if statErr != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		addWarning(&report, "tap", "the canonical Hextap tap is not installed as a regular directory")
		service.collectLocalProject(&report, options.Project, false)
		return report
	}
	report.Tap.Installed = true
	service.collectTapGit(ctx, &report)
	var registryComplete bool
	report.Projects, registryComplete = service.collectRegisteredProjects(ctx, &report, tapPath)
	formulaNames := service.collectPackageNames(ctx, &report, filepath.Join(tapPath, "Formula"), "formula", true)
	caskNames := service.collectCaskNames(ctx, &report, tapPath)
	for _, name := range formulaNames {
		report.Formulae = append(report.Formulae, service.collectFormula(ctx, &report, brew, name))
	}
	for _, name := range caskNames {
		report.Casks = append(report.Casks, service.collectCask(ctx, &report, brew, name))
	}
	service.collectLocalProject(&report, options.Project, registryComplete)
	return report
}

func (service Service) runner() Runner {
	if service.Runner != nil {
		return service.Runner
	}
	return osRunner{}
}

func (service Service) fileSystem() FileSystem {
	if service.FileSystem != nil {
		return service.FileSystem
	}
	return osFileSystem{}
}

func (service Service) executable() string {
	if service.Executable != "" {
		return service.Executable
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return executable
}

// homebrewOwnership is the outcome of proving which Homebrew installation owns
// the active Hextap CLI. Reason explains a failure precisely enough that an
// unproven ownership can never be read as an empty system.
type homebrewOwnership struct {
	Executable string
	Owned      bool
	Reason     string
}

// findOwningHomebrew proves which Homebrew installation owns the running Hextap
// CLI. Ownership is granted only by asking a candidate Homebrew for the toolkit
// Formula prefix and requiring that the brew-hextap beneath it resolve to the
// same file as the running executable. Candidate discovery widens where that
// proof is attempted; it never widens what the proof accepts, so a candidate
// derived from the running executable is rejected like any other when it fails.
func (service Service) findOwningHomebrew(ctx context.Context) homebrewOwnership {
	executable := service.executable()
	if executable == "" {
		return homebrewOwnership{Reason: "the path of the running executable is unavailable"}
	}
	resolve := service.ResolvePath
	if resolve == nil {
		resolve = filepath.EvalSymlinks
	}
	active, err := resolve(executable)
	if err != nil {
		return homebrewOwnership{Reason: fmt.Sprintf("the running executable %s could not be resolved through its symlink chain", reportablePath(executable))}
	}
	candidates := service.brewCandidates(active)
	rejections := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result, runErr := service.runner().Run(ctx, Command{Name: candidate, Args: []string{"--prefix", ToolkitFormula}, Env: homebrewReadOnlyEnvironment()})
		if runErr != nil {
			rejections = append(rejections, fmt.Sprintf("%s (unavailable)", reportablePath(candidate)))
			continue
		}
		prefix, parseOK := parseLine(result.Stdout)
		if !parseOK {
			rejections = append(rejections, fmt.Sprintf("%s (returned an invalid prefix record)", reportablePath(candidate)))
			continue
		}
		owned, resolveErr := resolve(filepath.Join(prefix, homebrewBinaryDirectory, toolkitExecutableName))
		if resolveErr != nil {
			rejections = append(rejections, fmt.Sprintf("%s (installs no resolvable %s)", reportablePath(candidate), toolkitExecutableName))
			continue
		}
		if owned != active {
			rejections = append(rejections, fmt.Sprintf("%s (owns a different Hextap)", reportablePath(candidate)))
			continue
		}
		return homebrewOwnership{Executable: candidate, Owned: true}
	}
	if len(rejections) == 0 {
		return homebrewOwnership{Reason: fmt.Sprintf("no Homebrew installation could be located to examine for ownership of %s", reportablePath(active))}
	}
	return homebrewOwnership{Reason: fmt.Sprintf("no examined Homebrew installation owns %s: %s", reportablePath(active), strings.Join(rejections, "; "))}
}

// brewCandidates lists the Homebrew executables worth asking about ownership,
// most specific first. The installation that owns the running CLI names itself
// in that CLI's resolved path, so it is examined before the two conventional
// prefixes and the PATH lookup, which between them cannot see a Homebrew
// installed anywhere else. Explicitly supplied candidates are still honoured
// exactly, and every candidate remains subject to the same ownership proof.
func (service Service) brewCandidates(active string) []string {
	candidates := make([]string, 0, 4)
	seen := make(map[string]bool)
	appendCandidate := func(candidate string) {
		if candidate == "" || seen[candidate] {
			return
		}
		seen[candidate] = true
		candidates = append(candidates, candidate)
	}
	if prefix := homebrewPrefixOfCellarPath(active); prefix != "" {
		appendCandidate(filepath.Join(prefix, homebrewBinaryDirectory, homebrewExecutableName))
	}
	if len(service.BrewCandidates) != 0 {
		for _, candidate := range service.BrewCandidates {
			appendCandidate(candidate)
		}
		return candidates
	}
	appendCandidate("/opt/homebrew/bin/brew")
	appendCandidate("/usr/local/bin/brew")
	lookPath := service.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if path, lookErr := lookPath(homebrewExecutableName); lookErr == nil {
		appendCandidate(path)
	}
	return candidates
}

// homebrewPrefixOfCellarPath returns the Homebrew prefix that owns a resolved
// Cellar path such as /opt/homebrew/Cellar/hextap/0.6.0/bin/brew-hextap, or an
// empty string when the path is not inside a Cellar. Homebrew keeps the Cellar
// immediately beneath its prefix, so every installed binary's real path names
// its own prefix no matter where that prefix lives. The innermost Cellar wins,
// which is the correct answer when a prefix is itself nested under one.
func homebrewPrefixOfCellarPath(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	directory := filepath.Dir(path)
	for {
		parent := filepath.Dir(directory)
		if parent == directory {
			return ""
		}
		if filepath.Base(directory) == homebrewCellarDirectory {
			return parent
		}
		directory = parent
	}
}

// reportablePath renders a filesystem path for a warning message, substituting
// a fixed placeholder for any path that is empty, over-long, or carries control
// characters, so that a diagnostic can never smuggle terminal control bytes.
func reportablePath(path string) string {
	if !safeText(path, maximumWarningPathLength) {
		return "an unreportable path"
	}
	return strconv.Quote(path)
}

func (service Service) collectTapGit(ctx context.Context, report *Report) {
	commands := []struct {
		component string
		args      []string
		assign    func(string) bool
	}{
		{
			component: "tap.git.revision",
			args:      []string{"-C", report.Tap.Path, "rev-parse", "HEAD"},
			assign: func(value string) bool {
				if !gitRevisionPattern.MatchString(value) {
					return false
				}
				report.Tap.Revision = value
				return true
			},
		},
		{
			component: "tap.git.branch",
			args:      []string{"-C", report.Tap.Path, "symbolic-ref", "--quiet", "--short", "HEAD"},
			assign: func(value string) bool {
				if value == "" || strings.ContainsAny(value, "\x00\r\n") {
					return false
				}
				report.Tap.Branch = value
				return true
			},
		},
		{
			component: "tap.git.remote",
			args:      []string{"-C", report.Tap.Path, "remote", "get-url", "origin"},
			assign: func(value string) bool {
				report.Tap.Remote = sanitizeRemote(value)
				return report.Tap.Remote != ""
			},
		},
	}
	for _, command := range commands {
		result, err := service.runner().Run(ctx, Command{Name: "git", Args: command.args})
		if err != nil {
			addWarning(report, command.component, "tap Git metadata is unavailable")
			continue
		}
		value, ok := parseLine(result.Stdout)
		if !ok || !command.assign(value) {
			addWarning(report, command.component, "tap Git metadata is invalid")
		}
	}
}

func (service Service) collectRegisteredProjects(ctx context.Context, report *Report, tapPath string) ([]ProjectInfo, bool) {
	directory := filepath.Join(tapPath, "Projects")
	entries, truncated, err := service.fileSystem().ReadDir(ctx, directory, maximumRegistryEntries)
	if err != nil {
		addWarning(report, "projects", "the Hextap project registry is unavailable")
		return []ProjectInfo{}, false
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	complete := true
	if truncated {
		addWarning(report, "projects.limit", "the Hextap project registry exceeds the bounded inventory limit")
		complete = false
	}
	projects := make([]ProjectInfo, 0, len(entries))
	seen := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		manifestPath := filepath.Join(directory, name)
		manifestValue, loadErr := service.loadManifest(manifestPath)
		if loadErr != nil || strings.TrimSuffix(name, ".json") != manifestValue.Formula.Name || seen[manifestValue.Formula.Name] {
			addWarning(report, "projects.invalid", "a registered project manifest is invalid")
			complete = false
			continue
		}
		seen[manifestValue.Formula.Name] = true
		projects = append(projects, projectInfo(manifestValue, manifestPath, "REGISTERED"))
	}
	return projects, complete
}

func (service Service) collectPackageNames(ctx context.Context, report *Report, directory, component string, warnMissing bool) []string {
	entries, truncated, err := service.fileSystem().ReadDir(ctx, directory, maximumRegistryEntries)
	if err != nil {
		if warnMissing || !errors.Is(err, fs.ErrNotExist) {
			addWarning(report, pluralComponent(component), "the tap's "+component+" registry is unavailable")
		}
		return []string{}
	}
	names := make([]string, 0, len(entries))
	if truncated {
		addWarning(report, pluralComponent(component)+".limit", "the tap package registry exceeds the bounded inventory limit")
	}
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".rb" {
			continue
		}
		info, infoErr := entry.Info()
		packageName := strings.TrimSuffix(name, ".rb")
		if infoErr != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || !packageNamePattern.MatchString(packageName) {
			addWarning(report, pluralComponent(component)+".invalid", "a tap package entry is invalid")
			continue
		}
		names = append(names, packageName)
	}
	sort.Strings(names)
	return names
}

func (service Service) collectCaskNames(ctx context.Context, report *Report, tapPath string) []string {
	seen := make(map[string]bool)
	for _, directory := range []string{"Cask", "Casks"} {
		for _, name := range service.collectPackageNames(ctx, report, filepath.Join(tapPath, directory), "cask", false) {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (service Service) collectFormula(ctx context.Context, report *Report, brew, name string) FormulaInfo {
	fullName := TapName + "/" + name
	fallback := FormulaInfo{Name: name, FullName: fullName, InstalledVersions: []string{}, Service: ServiceInfo{KeepAlive: []string{}, EnvironmentVariables: []string{}}}
	result, err := service.runner().Run(ctx, Command{Name: brew, Args: []string{"info", "--json=v2", "--formula", fullName}, Env: homebrewReadOnlyEnvironment()})
	if err != nil {
		addWarning(report, "formula."+name, "Homebrew Formula metadata is unavailable")
		return fallback
	}
	var document brewInfoDocument
	if json.Unmarshal([]byte(result.Stdout), &document) != nil || len(document.Formulae) != 1 {
		addWarning(report, "formula."+name, "Homebrew returned invalid Formula metadata")
		return fallback
	}
	formula := document.Formulae[0]
	if formula.Name != name || formula.FullName != fullName || !safeVersion(formula.Versions.Stable) {
		addWarning(report, "formula."+name, "Homebrew returned mismatched Formula metadata")
		return fallback
	}
	fallback.AvailableVersion = formula.Versions.Stable
	for _, installed := range formula.Installed {
		if safeVersion(installed.Version) {
			fallback.InstalledVersions = append(fallback.InstalledVersions, installed.Version)
		}
	}
	sort.Strings(fallback.InstalledVersions)
	fallback.Installed = len(fallback.InstalledVersions) != 0
	fallback.Outdated = formula.Outdated
	fallback.Pinned = formula.Pinned
	if formula.Service != nil {
		fallback.Service.Defined = true
		if safeText(formula.Service.RunType, 64) {
			fallback.Service.RunType = formula.Service.RunType
		}
		for key, value := range formula.Service.KeepAlive {
			if safeText(key, 64) {
				fallback.Service.KeepAlive = append(fallback.Service.KeepAlive, key+"="+strconv.FormatBool(value))
			}
		}
		for key := range formula.Service.EnvironmentVariables {
			if environmentPattern.MatchString(key) {
				fallback.Service.EnvironmentVariables = append(fallback.Service.EnvironmentVariables, key)
			}
		}
		sort.Strings(fallback.Service.KeepAlive)
		sort.Strings(fallback.Service.EnvironmentVariables)
		fallback.Service.RestartDelay = formula.Service.RestartDelay
	}
	return fallback
}

func (service Service) collectCask(ctx context.Context, report *Report, brew, name string) CaskInfo {
	fullName := TapName + "/" + name
	fallback := CaskInfo{Name: name, FullName: fullName}
	result, err := service.runner().Run(ctx, Command{Name: brew, Args: []string{"info", "--json=v2", "--cask", fullName}, Env: homebrewReadOnlyEnvironment()})
	if err != nil {
		addWarning(report, "cask."+name, "Homebrew Cask metadata is unavailable")
		return fallback
	}
	var document brewInfoDocument
	if json.Unmarshal([]byte(result.Stdout), &document) != nil || len(document.Casks) != 1 {
		addWarning(report, "cask."+name, "Homebrew returned invalid Cask metadata")
		return fallback
	}
	cask := document.Casks[0]
	if cask.Token != name || cask.FullToken != fullName || !safeVersion(cask.Version) || cask.Installed != "" && !safeVersion(cask.Installed) {
		addWarning(report, "cask."+name, "Homebrew returned mismatched Cask metadata")
		return fallback
	}
	fallback.AvailableVersion = cask.Version
	fallback.InstalledVersion = cask.Installed
	fallback.Installed = cask.Installed != ""
	fallback.Outdated = cask.Outdated
	fallback.AutoUpdates = cask.AutoUpdates
	return fallback
}

func (service Service) collectSkills(report *Report, project string) {
	status := service.SkillStatus
	if status == nil {
		status = skillinstall.Status
	}
	home := service.HomeDir
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			addWarning(report, "skills.user", "could not resolve the user home for managed skill inventory")
		} else {
			home = resolved
		}
	}
	if home != "" {
		result, err := status(skillinstall.Options{Scope: skillinstall.UserScope, HomeDir: home})
		if err != nil {
			addWarning(report, "skills.user", "managed user-skill inventory is unavailable")
		} else {
			for _, entry := range result.Entries {
				report.Skills = append(report.Skills, SkillInfo{Scope: skillinstall.UserScope, StatusEntry: entry})
			}
		}
	}
	if project != "" {
		result, err := status(skillinstall.Options{Scope: skillinstall.ProjectScope, ProjectDir: project})
		if err != nil {
			addWarning(report, "skills.project", "managed project-skill inventory is unavailable")
		} else {
			for _, entry := range result.Entries {
				report.Skills = append(report.Skills, SkillInfo{Scope: skillinstall.ProjectScope, StatusEntry: entry})
			}
		}
	}
}

func (service Service) collectLocalProject(report *Report, project string, registryComplete bool) {
	if project == "" {
		return
	}
	manifestPath := filepath.Join(project, ".hextap.json")
	manifestValue, err := service.loadManifest(manifestPath)
	if err != nil {
		addWarning(report, "local_project", "the supplied project's .hextap.json is unavailable or invalid")
		return
	}
	state := "UNKNOWN"
	for _, registered := range report.Projects {
		if registered.Name == manifestValue.Formula.Name && registered.Repository == manifestValue.RepositorySlug() {
			state = "REGISTERED"
			break
		}
	}
	if state == "UNKNOWN" && registryComplete {
		state = "NOT_REGISTERED"
	}
	value := projectInfo(manifestValue, manifestPath, state)
	report.LocalProject = &value
}

func (service Service) loadManifest(path string) (manifest.Manifest, error) {
	if service.FileSystem == nil {
		return manifest.Load(path)
	}
	info, err := service.fileSystem().Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumManifestSize {
		return manifest.Manifest{}, fmt.Errorf("invalid manifest")
	}
	data, err := service.fileSystem().ReadFile(path)
	if err != nil || int64(len(data)) != info.Size() {
		return manifest.Manifest{}, fmt.Errorf("invalid manifest")
	}
	return manifest.Parse(data)
}

func projectInfo(value manifest.Manifest, path, registration string) ProjectInfo {
	serviceEnabled := value.Homebrew.Service != nil && value.Homebrew.Service.Enabled
	if value.Homebrew.ServiceEnabled != nil {
		serviceEnabled = *value.Homebrew.ServiceEnabled
	}
	return ProjectInfo{
		Name:              value.Formula.Name,
		Repository:        value.RepositorySlug(),
		Binary:            value.Formula.Binary,
		Schema:            value.Schema,
		ServiceEnabled:    serviceEnabled,
		ManifestPath:      path,
		RegistrationState: registration,
	}
}

type brewInfoDocument struct {
	Formulae []struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
		Installed []struct {
			Version string `json:"version"`
		} `json:"installed"`
		Outdated bool `json:"outdated"`
		Pinned   bool `json:"pinned"`
		Service  *struct {
			RunType              string                     `json:"run_type"`
			KeepAlive            map[string]bool            `json:"keep_alive"`
			EnvironmentVariables map[string]json.RawMessage `json:"environment_variables"`
			RestartDelay         int                        `json:"restart_delay"`
		} `json:"service"`
	} `json:"formulae"`
	Casks []struct {
		Token       string `json:"token"`
		FullToken   string `json:"full_token"`
		Version     string `json:"version"`
		Installed   string `json:"installed"`
		Outdated    bool   `json:"outdated"`
		AutoUpdates *bool  `json:"auto_updates"`
	} `json:"casks"`
}

func homebrewReadOnlyEnvironment() map[string]string {
	return map[string]string{
		"HOMEBREW_NO_ANALYTICS":        "1",
		"HOMEBREW_NO_AUTO_UPDATE":      "1",
		"HOMEBREW_NO_GITHUB_API":       "1",
		"HOMEBREW_NO_INSTALL_FROM_API": "1",
	}
}

func parseLine(output string) (string, bool) {
	if !strings.HasSuffix(output, "\n") || strings.Count(output, "\n") != 1 {
		return "", false
	}
	value := strings.TrimSuffix(output, "\n")
	if value == "" || strings.ContainsAny(value, "\x00\r") {
		return "", false
	}
	return value, true
}

func sanitizeRemote(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" && allowedRemoteScheme(parsed.Scheme) {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		sanitized := parsed.String()
		if safeText(sanitized, 2048) {
			return sanitized
		}
		return ""
	}
	if safeText(value, 2048) && scpRemotePattern.MatchString(value) && !strings.Contains(value, "..") {
		return value
	}
	return ""
}

func allowedRemoteScheme(value string) bool {
	switch strings.ToLower(value) {
	case "git", "http", "https", "ssh":
		return true
	default:
		return false
	}
}

func safeVersion(value string) bool { return safeText(value, 256) }

func safeText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.IndexFunc(value, unicode.IsControl) == -1
}

func pluralComponent(value string) string {
	if value == "formula" {
		return "formulae"
	}
	return value + "s"
}

func addWarning(report *Report, component, message string) {
	report.Warnings = append(report.Warnings, Warning{Component: component, Message: message})
}
