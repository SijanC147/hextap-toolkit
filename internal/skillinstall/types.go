// Package skillinstall installs and inspects embedded Hextap agent skills
// without reading credentials or invoking agents, package managers, or
// services.
package skillinstall

import (
	"fmt"
	"io/fs"
	"strings"
)

// Scope selects the filesystem root beneath which target paths are resolved.
type Scope string

const (
	// UserScope installs below a caller-supplied user home.
	UserScope Scope = "user"
	// ProjectScope installs below a caller-supplied project directory.
	ProjectScope Scope = "project"
)

// Options controls Hextap skill installation or inspection.
type Options struct {
	Agents                    []string
	Scope                     Scope
	HomeDir                   string
	ProjectDir                string
	DryRun                    bool
	AllowOverlappingDiscovery bool
}

// Action describes the deterministic operation planned for one managed file.
type Action string

const (
	// CreateAction publishes a file only when the destination is absent.
	CreateAction Action = "CREATE"
	// UnchangedAction leaves an exact marker-owned file untouched.
	UnchangedAction Action = "UNCHANGED"
	// UpgradeAction replaces an intact older marker-owned skill bundle.
	UpgradeAction Action = "UPGRADE"
)

const installedFileMode fs.FileMode = 0o644

// Entry records the operation for one target file.
type Entry struct {
	Action Action
	Agent  string
	Path   string
	Mode   fs.FileMode
	Size   int
}

// Result is a stable, target-then-bundle-order installation plan.
type Result struct {
	Entries []Entry
}

// State describes one target's marker-backed installation state.
type State string

const (
	NotInstalledState         State = "NOT_INSTALLED"
	CurrentState              State = "CURRENT"
	UpdateAvailableState      State = "UPDATE_AVAILABLE"
	NewerThanCLIState         State = "NEWER_THAN_CLI"
	SameVersionDifferentState State = "SAME_VERSION_DIFFERENT"
	DriftedState              State = "DRIFTED"
	UnmanagedState            State = "UNMANAGED"
	InvalidState              State = "INVALID"
)

// Recommendation is the safe next operation for one inspected target.
type Recommendation string

const (
	NoRecommendation      Recommendation = "NONE"
	InstallRecommendation Recommendation = "INSTALL"
	UpgradeRecommendation Recommendation = "UPGRADE"
	RefuseRecommendation  Recommendation = "REFUSE"
)

// StatusEntry records one resolved target's read-only state.
type StatusEntry struct {
	State            State          `json:"state"`
	Agent            string         `json:"agent"`
	DiscoveredBy     []string       `json:"discovered_by"`
	Path             string         `json:"path"`
	InstalledVersion string         `json:"installed_version,omitempty"`
	AvailableVersion string         `json:"available_version"`
	Recommendation   Recommendation `json:"recommendation"`
}

// StatusResult contains deterministic read-only target states.
type StatusResult struct {
	Entries []StatusEntry
}

// UpgradeEntry records one target-level managed bundle transition.
type UpgradeEntry struct {
	Action      Action
	Agent       string
	Path        string
	FromVersion string
	ToVersion   string
	BackupPath  string
}

// UpgradeResult contains deterministic target-level upgrade outcomes.
type UpgradeResult struct {
	Entries []UpgradeEntry
}

// PartialUpgradeError reports completed targets and exact recovery paths after
// a transaction stopped. Hextap never deletes these paths automatically.
type PartialUpgradeError struct {
	Cause         error
	Completed     []UpgradeEntry
	RecoveryPaths []string
}

func (err *PartialUpgradeError) Error() string {
	return fmt.Sprintf("skill upgrade partial state (recovery paths %s): %v", strings.Join(err.RecoveryPaths, ", "), err.Cause)
}

func (err *PartialUpgradeError) Unwrap() error {
	return err.Cause
}

// PartialInstallError reports durable paths preserved after a create-only
// claim or publication prefix succeeded. Recovery never removes these or any
// other paths by pathname.
type PartialInstallError struct {
	Cause     error
	Claimed   []string
	Published []string
}

func (err *PartialInstallError) Error() string {
	states := make([]string, 0, 2)
	if len(err.Claimed) != 0 {
		states = append(states, "claimed directories "+strings.Join(err.Claimed, ", "))
	}
	if len(err.Published) != 0 {
		states = append(states, "published files "+strings.Join(err.Published, ", "))
	}
	return fmt.Sprintf("skill install partial state (%s): %v", strings.Join(states, "; "), err.Cause)
}

func (err *PartialInstallError) Unwrap() error {
	return err.Cause
}
