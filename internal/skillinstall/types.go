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
	NotInstalledState State = "NOT_INSTALLED"
	CurrentState      State = "CURRENT"
	DifferentState    State = "DIFFERENT"
	DriftedState      State = "DRIFTED"
	UnmanagedState    State = "UNMANAGED"
	InvalidState      State = "INVALID"
)

// StatusEntry records one resolved target's read-only state.
type StatusEntry struct {
	State State
	Agent string
	Path  string
}

// StatusResult contains deterministic read-only target states.
type StatusResult struct {
	Entries []StatusEntry
}

// PartialInstallError reports paths preserved after a later create-only
// publication failed. Recovery never removes published paths by pathname.
type PartialInstallError struct {
	Cause     error
	Published []string
}

func (err *PartialInstallError) Error() string {
	return fmt.Sprintf("skill install partially published %s: %v", strings.Join(err.Published, ", "), err.Cause)
}

func (err *PartialInstallError) Unwrap() error {
	return err.Cause
}
