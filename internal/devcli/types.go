// Package devcli implements the Hextap toolkit's self-development and release
// orchestration surface.
package devcli

import (
	"context"
	"fmt"
	"io"
	"time"
)

const (
	ToolkitOwner       = "SijanC147"
	ToolkitRepository  = "SijanC147/hextap-toolkit"
	ToolkitModule      = "github.com/SijanC147/hextap-toolkit"
	ToolkitOriginHTTPS = "https://github.com/SijanC147/hextap-toolkit.git"
)

// Command is one shell-free external process invocation.
type Command struct {
	Name    string
	Args    []string
	Dir     string
	Env     map[string]string
	Timeout time.Duration
}

// Result contains bounded process output.
type Result struct {
	Stdout string
	Stderr string
}

// Runner executes one external command without shell interpolation.
type Runner interface {
	Run(context.Context, Command) (Result, error)
}

// Service owns one developer orchestration session.
type Service struct {
	Runner          Runner
	Stdout          io.Writer
	Stderr          io.Writer
	Version         string
	Commit          string
	Executable      string
	Sleep           func(context.Context, time.Duration) error
	BrewCandidates  []string
	InstalledBinary string
}

func (service Service) runner() Runner {
	if service.Runner != nil {
		return service.Runner
	}
	return OSRunner{}
}

func (service Service) progress(format string, args ...any) {
	if service.Stdout != nil {
		fmt.Fprintf(service.Stdout, format+"\n", args...)
	}
}

// StatusResult is the stable read-only toolkit repository inventory.
type StatusResult struct {
	Schema              int    `json:"schema"`
	Project             string `json:"project"`
	Repository          string `json:"repository"`
	Branch              string `json:"branch"`
	Head                string `json:"head"`
	Clean               bool   `json:"clean"`
	GitHubUser          string `json:"github_user"`
	LatestStableTag     string `json:"latest_stable_tag"`
	LatestStableVersion string `json:"latest_stable_version"`
	NextPatch           string `json:"next_patch"`
	NextMinor           string `json:"next_minor"`
	NextMajor           string `json:"next_major"`
	CLIVersion          string `json:"cli_version"`
	CLICommit           string `json:"cli_commit"`
}

// ReleasePlan binds one explicit SemVer choice to repository evidence.
type ReleasePlan struct {
	Schema         int    `json:"schema"`
	Project        string `json:"project"`
	Repository     string `json:"repository"`
	CurrentTag     string `json:"current_tag"`
	CurrentVersion string `json:"current_version"`
	Bump           string `json:"bump"`
	Tag            string `json:"tag"`
	Version        string `json:"version"`
	Commit         string `json:"commit"`
}

// ValidateOptions controls the local toolkit validation ladder.
type ValidateOptions struct {
	Project string
	Full    bool
}

// ValidateResult records which expensive gate was included.
type ValidateResult struct {
	Project string `json:"project"`
	Race    bool   `json:"race"`
}

// ReleaseOptions controls a protected release from canonical main.
type ReleaseOptions struct {
	Project     string
	Bump        string
	ConfirmTag  string
	Execute     bool
	Install     bool
	SkillAgents []string
}

// DeployOptions controls the feature-branch PR-to-release workflow.
type DeployOptions struct {
	ReleaseOptions
	PRTitle string
}

// InstallOptions controls local installation of one already published release.
type InstallOptions struct {
	Project        string
	Tag            string
	ExpectedCommit string
	Execute        bool
	SkillAgents    []string
}

// Outcome is the final remote and optional local deployment evidence.
type Outcome struct {
	Schema        int    `json:"schema"`
	Tag           string `json:"tag"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	PRURL         string `json:"pr_url,omitempty"`
	MainRunURL    string `json:"main_run_url"`
	ReleaseRunURL string `json:"release_run_url"`
	TapRunURL     string `json:"tap_run_url"`
	ReleaseURL    string `json:"release_url"`
	Installed     bool   `json:"installed"`
}

// InstallResult identifies the exact Homebrew and binary selected locally.
type InstallResult struct {
	Brew    string `json:"brew"`
	Binary  string `json:"binary"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}
