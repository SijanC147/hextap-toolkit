// Package rollback plans and executes fail-closed Homebrew Formula and Cask
// rollbacks without stopping services or mutating immutable releases.
package rollback

import (
	"context"
	"time"
)

// Kind selects the Homebrew definition type.
type Kind string

const (
	// FormulaKind selects Formula/<name>.rb.
	FormulaKind Kind = "formula"
	// CaskKind selects Casks/<name>.rb.
	CaskKind Kind = "cask"
)

// Mode selects a temporary local reinstall or protected remote pull request.
type Mode string

const (
	// LocalMode temporarily checks out one historical definition and reinstalls it.
	LocalMode Mode = "local"
	// RemoteMode publishes a metadata-only rollback through a feature branch and PR.
	RemoteMode Mode = "remote"
)

// Options is one explicit rollback request.
type Options struct {
	Kind      Kind
	Name      string
	ToCommit  string
	ToVersion string
	Mode      Mode
	Execute   bool
	Confirm   string
}

// Plan is the complete, versioned, secret-free rollback contract.
type Plan struct {
	Schema               int      `json:"schema"`
	Mode                 Mode     `json:"mode"`
	Kind                 Kind     `json:"kind"`
	Name                 string   `json:"name"`
	FullName             string   `json:"full_name"`
	TapPath              string   `json:"tap_path"`
	OriginalCommit       string   `json:"original_commit"`
	TargetCommit         string   `json:"target_commit"`
	CurrentVersion       string   `json:"current_version"`
	TargetVersion        string   `json:"target_version"`
	CurrentVersionScheme int      `json:"current_version_scheme,omitempty"`
	PlannedVersionScheme int      `json:"planned_version_scheme,omitempty"`
	Branch               string   `json:"branch,omitempty"`
	Confirmation         string   `json:"confirmation"`
	Convergence          string   `json:"convergence"`
	Paths                []string `json:"paths"`
	Actions              []string `json:"actions"`
}

// Outcome records plan or execution evidence.
type Outcome struct {
	Plan           Plan   `json:"plan"`
	Executed       bool   `json:"executed"`
	Restored       bool   `json:"restored,omitempty"`
	TapClean       bool   `json:"tap_clean,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
}

// Command is one direct, bounded child-process invocation.
type Command struct {
	Name    string
	Args    []string
	Env     map[string]string
	Dir     string
	Timeout time.Duration
}

// Result contains bounded dependency output. Callers never expose stderr.
type Result struct {
	Stdout string
	Stderr string
}

// Runner executes a command without a shell.
type Runner interface {
	Run(context.Context, Command) (Result, error)
}

// Service owns one dependency-injected rollback session.
type Service struct {
	Runner           Runner
	Invocation       string
	Executable       string
	BrewCandidates   []string
	Brew             string
	TapPath          string
	TapName          string
	OwnedRemote      string
	TempRoot         string
	CommandTimeout   time.Duration
	ReinstallTimeout time.Duration
	ResolvePath      func(string) (string, error)
}
