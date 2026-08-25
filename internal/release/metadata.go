// Package release implements deterministic release metadata and artifact build
// contracts shared by Hextap workflows.
package release

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	releaseCorePattern       = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	prereleaseIdentifierExpr = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
)

const maxReleaseVersionLength = 255

// Metadata is the normalized, validated release-tag contract.
type Metadata struct {
	Tag        string
	Version    string
	Stable     bool
	Prerelease bool
	Mode       string
}

// ParseMetadata validates a strict v-prefixed SemVer tag and release mode.
// Build metadata is intentionally unsupported because release asset identity
// must map to exactly one tag and Formula version.
func ParseMetadata(tag, mode string) (Metadata, error) {
	if mode != "full" && mode != "homebrew-only" {
		return Metadata{}, fmt.Errorf("mode %q must be full or homebrew-only", mode)
	}
	if !strings.HasPrefix(tag, "v") {
		return Metadata{}, fmt.Errorf("tag %q must be v-prefixed SemVer", tag)
	}
	if len(tag) > maxReleaseVersionLength+1 {
		return Metadata{}, errors.New("release tag exceeds 256 bytes")
	}
	version := strings.TrimPrefix(tag, "v")
	if strings.Contains(version, "+") {
		return Metadata{}, fmt.Errorf("tag %q may not contain build metadata", tag)
	}
	core, suffix, hasPrerelease := strings.Cut(version, "-")
	if !releaseCorePattern.MatchString(core) {
		return Metadata{}, fmt.Errorf("tag %q must contain SemVer components without leading zeros", tag)
	}
	if hasPrerelease {
		if err := validatePrerelease(suffix); err != nil {
			return Metadata{}, fmt.Errorf("tag %q has invalid prerelease: %w", tag, err)
		}
	}
	if mode == "homebrew-only" && hasPrerelease {
		return Metadata{}, errors.New("homebrew-only mode requires a stable release tag")
	}
	return Metadata{
		Tag:        tag,
		Version:    version,
		Stable:     !hasPrerelease,
		Prerelease: hasPrerelease,
		Mode:       mode,
	}, nil
}

// ParseVersion validates a normalized SemVer version without a leading v.
// Stable and prerelease versions are accepted; build metadata is rejected.
func ParseVersion(version string) (Metadata, error) {
	if strings.HasPrefix(version, "v") {
		return Metadata{}, errors.New("release version must not have a leading v")
	}
	return ParseMetadata("v"+version, "full")
}

func validatePrerelease(suffix string) error {
	if suffix == "" {
		return errors.New("suffix must not be empty")
	}
	for _, identifier := range strings.Split(suffix, ".") {
		if identifier == "" || !prereleaseIdentifierExpr.MatchString(identifier) {
			return fmt.Errorf("identifier %q is unsafe", identifier)
		}
		if len(identifier) > 1 && identifier[0] == '0' && isDecimal(identifier) {
			return fmt.Errorf("numeric identifier %q has a leading zero", identifier)
		}
	}
	return nil
}

func isDecimal(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
