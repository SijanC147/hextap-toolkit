// Package inventory builds a read-only, offline report of the local Hextap
// command, Homebrew tap, registered projects, packages, and managed skills.
package inventory

import (
	"context"
	"io/fs"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

const (
	// TapName is the canonical Homebrew tap that registers Hextap projects.
	TapName = "sean/hextap"
	// ToolkitFormula is the Formula that owns the installed Hextap CLI.
	ToolkitFormula = "sean/hextap/hextap"
)

// Command is one shell-free, read-only external command invocation.
type Command struct {
	Name    string
	Args    []string
	Env     map[string]string
	Timeout time.Duration
}

// Result contains bounded command output.
type Result struct {
	Stdout string
	Stderr string
}

// Runner executes an external command without shell interpolation.
type Runner interface {
	Run(context.Context, Command) (Result, error)
}

// FileSystem is the minimal read-only filesystem surface used by inventory.
type FileSystem interface {
	Lstat(string) (fs.FileInfo, error)
	ReadDir(string) ([]fs.DirEntry, error)
	ReadFile(string) ([]byte, error)
}

// SkillStatusFunc invokes the existing marker-backed skill inventory logic.
type SkillStatusFunc func(skillinstall.Options) (skillinstall.StatusResult, error)

// Options selects optional local-project inventory.
type Options struct {
	Project string
}

// Service owns one dependency-injected inventory collection session.
type Service struct {
	Runner         Runner
	FileSystem     FileSystem
	Version        string
	Commit         string
	Executable     string
	HomeDir        string
	BrewCandidates []string
	ResolvePath    func(string) (string, error)
	LookPath       func(string) (string, error)
	SkillStatus    SkillStatusFunc
}

// Report is the versioned, lossless JSON inventory document.
type Report struct {
	Schema       int           `json:"schema"`
	CLI          CLIInfo       `json:"cli"`
	Homebrew     HomebrewInfo  `json:"homebrew"`
	Tap          TapInfo       `json:"tap"`
	Projects     []ProjectInfo `json:"projects"`
	Formulae     []FormulaInfo `json:"formulae"`
	Casks        []CaskInfo    `json:"casks"`
	Skills       []SkillInfo   `json:"skills"`
	LocalProject *ProjectInfo  `json:"local_project,omitempty"`
	Warnings     []Warning     `json:"warnings"`
}

// CLIInfo identifies the running Hextap binary.
type CLIInfo struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Executable string `json:"executable"`
}

// HomebrewInfo identifies the Homebrew installation that owns the CLI.
type HomebrewInfo struct {
	Executable string `json:"executable,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
}

// TapInfo records the canonical tap checkout and local Git identity.
type TapInfo struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Remote    string `json:"remote,omitempty"`
}

// ProjectInfo is the safe structural subset of one validated Hextap manifest.
type ProjectInfo struct {
	Name              string `json:"name"`
	Repository        string `json:"repository"`
	Binary            string `json:"binary"`
	Schema            int    `json:"schema"`
	ServiceEnabled    bool   `json:"service_enabled"`
	ManifestPath      string `json:"manifest_path"`
	RegistrationState string `json:"registration_state"`
}

// FormulaInfo records local availability, installation, and service metadata.
type FormulaInfo struct {
	Name              string      `json:"name"`
	FullName          string      `json:"full_name"`
	AvailableVersion  string      `json:"available_version,omitempty"`
	Installed         bool        `json:"installed"`
	InstalledVersions []string    `json:"installed_versions"`
	Outdated          bool        `json:"outdated"`
	Pinned            bool        `json:"pinned"`
	Service           ServiceInfo `json:"service"`
}

// ServiceInfo excludes environment values while retaining useful definition
// metadata from Homebrew's local JSON output.
type ServiceInfo struct {
	Defined              bool     `json:"defined"`
	RunType              string   `json:"run_type,omitempty"`
	KeepAlive            []string `json:"keep_alive"`
	EnvironmentVariables []string `json:"environment_variables"`
	RestartDelay         int      `json:"restart_delay,omitempty"`
}

// CaskInfo records local Cask availability and installation state.
type CaskInfo struct {
	Name             string `json:"name"`
	FullName         string `json:"full_name"`
	AvailableVersion string `json:"available_version,omitempty"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	Outdated         bool   `json:"outdated"`
	AutoUpdates      *bool  `json:"auto_updates,omitempty"`
}

// SkillInfo annotates the existing managed-skill status with its scope.
type SkillInfo struct {
	Scope skillinstall.Scope `json:"scope"`
	skillinstall.StatusEntry
}

// Warning identifies one unavailable or invalid report component. Messages are
// intentionally generic so dependency stderr and credential values stay opaque.
type Warning struct {
	Component string `json:"component"`
	Message   string `json:"message"`
}

// Kind selects a detailed inventory category.
type Kind string

const (
	// AllKind includes every inventory category.
	AllKind Kind = "all"
	// ProjectKind includes registered and optional local projects.
	ProjectKind Kind = "project"
	// FormulaKind includes Homebrew Formulae.
	FormulaKind Kind = "formula"
	// CaskKind includes Homebrew Casks.
	CaskKind Kind = "cask"
	// SkillKind includes managed Hextap skill targets.
	SkillKind Kind = "skill"
)
