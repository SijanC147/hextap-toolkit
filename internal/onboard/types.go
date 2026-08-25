// Package onboard implements conflict-safe local Hextap project onboarding,
// validation, and read-only doctor checks.
package onboard

import "github.com/SijanC147/hextap-toolkit/internal/manifest"

const (
	manifestPath       = ".hextap.json"
	workflowPath       = ".github/workflows/hextap-release.yml"
	tapPath            = ".hextap/tap-registration.json"
	mainRulesetPath    = ".hextap/rulesets/main.json"
	tagRulesetPath     = ".hextap/rulesets/release-tags.json"
	setupPath          = ".hextap/SETUP.md"
	defaultAdapterPath = "scripts/hextap-build"
	maximumLocalFile   = 1 << 20
	supportedOwner     = "SijanC147"
)

// Options contains validated local onboarding inputs. The Set fields record
// whether a generation-only CLI option was explicitly supplied.
type Options struct {
	Project          string
	Repository       string
	Formula          string
	Binary           string
	Description      string
	License          string
	GoPackage        string
	VersionSymbol    string
	CommitSymbol     string
	ToolkitVersion   string
	ToolkitSHA       string
	RequiredChecks   []string
	Linux            bool
	DryRun           bool
	FormulaSet       bool
	BinarySet        bool
	DescriptionSet   bool
	LicenseSet       bool
	GoPackageSet     bool
	VersionSymbolSet bool
	CommitSymbolSet  bool
	LinuxSet         bool
}

// Action is one deterministic local onboarding plan action.
type Action string

const (
	// ActionCreate means an absent file will be created.
	ActionCreate Action = "CREATE"
	// ActionUnchanged means an owned file already has exact bytes and mode.
	ActionUnchanged Action = "UNCHANGED"
	// ActionValidated means a valid project-owned custom adapter is preserved.
	ActionValidated Action = "VALIDATED"
)

// Entry describes one artifact in a deterministic onboarding plan.
type Entry struct {
	Action Action
	Path   string
}

// Result describes a completed or dry-run onboarding plan.
type Result struct {
	Project string
	Entries []Entry
	DryRun  bool
}

// ValidateOptions selects local validation and the optional adapter smoke.
type ValidateOptions struct {
	Project string
	Build   bool
}

// ValidateResult contains the verified local onboarding identity.
type ValidateResult struct {
	Project        string
	Manifest       manifest.Manifest
	ToolkitVersion string
	ToolkitSHA     string
	RequiredChecks []string
	BuildVerified  bool
}

// DoctorOptions selects local-only or additional read-only online checks.
type DoctorOptions struct {
	Project string
	Online  bool
}

// DoctorResult lists deterministic checks that completed successfully.
type DoctorResult struct {
	Project string
	Checks  []string
}
