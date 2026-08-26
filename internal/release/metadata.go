// Package release implements deterministic release metadata and artifact build
// contracts shared by Hextap workflows.
package release

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

// StableVersion is a parsed stable SemVer core used for deterministic release
// planning. Components are unsigned because negative versions are invalid.
type StableVersion struct {
	Major uint64
	Minor uint64
	Patch uint64
}

// Bump selects the stable SemVer component increment used by developer release
// planning.
type Bump string

const (
	PatchBump Bump = "patch"
	MinorBump Bump = "minor"
	MajorBump Bump = "major"
)

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

// ParseStableVersion parses a normalized stable SemVer version into numeric
// components. Prereleases and components that exceed uint64 are rejected.
func ParseStableVersion(version string) (StableVersion, error) {
	metadata, err := ParseVersion(version)
	if err != nil {
		return StableVersion{}, err
	}
	if !metadata.Stable {
		return StableVersion{}, fmt.Errorf("version %q must be stable SemVer", version)
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return StableVersion{}, fmt.Errorf("version %q must have three components", version)
	}
	values := make([]uint64, 3)
	for index, part := range parts {
		value, parseErr := strconv.ParseUint(part, 10, 64)
		if parseErr != nil {
			return StableVersion{}, fmt.Errorf("version %q component %q: %w", version, part, parseErr)
		}
		values[index] = value
	}
	return StableVersion{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

func (version StableVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch)
}

// CompareStableVersions compares two normalized stable SemVer values.
func CompareStableVersions(left, right string) (int, error) {
	leftVersion, err := ParseStableVersion(left)
	if err != nil {
		return 0, fmt.Errorf("parse left version: %w", err)
	}
	rightVersion, err := ParseStableVersion(right)
	if err != nil {
		return 0, fmt.Errorf("parse right version: %w", err)
	}
	leftParts := [...]uint64{leftVersion.Major, leftVersion.Minor, leftVersion.Patch}
	rightParts := [...]uint64{rightVersion.Major, rightVersion.Minor, rightVersion.Patch}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1, nil
		}
		if leftParts[index] > rightParts[index] {
			return 1, nil
		}
	}
	return 0, nil
}

// BumpStableVersion returns the next stable version for one explicit SemVer
// component. Lower-order components reset according to SemVer convention.
func BumpStableVersion(current string, bump Bump) (string, error) {
	version, err := ParseStableVersion(current)
	if err != nil {
		return "", err
	}
	const maxUint64 = ^uint64(0)
	switch bump {
	case PatchBump:
		if version.Patch == maxUint64 {
			return "", errors.New("patch version overflow")
		}
		version.Patch++
	case MinorBump:
		if version.Minor == maxUint64 {
			return "", errors.New("minor version overflow")
		}
		version.Minor++
		version.Patch = 0
	case MajorBump:
		if version.Major == maxUint64 {
			return "", errors.New("major version overflow")
		}
		version.Major++
		version.Minor = 0
		version.Patch = 0
	default:
		return "", fmt.Errorf("bump %q must be patch, minor, or major", bump)
	}
	return version.String(), nil
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
