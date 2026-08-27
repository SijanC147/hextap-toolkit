package formula

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

// CanonicalMetadata is the constrained release metadata extracted from an
// otherwise byte-canonical generated Formula.
type CanonicalMetadata struct {
	Version     string
	ARM64SHA256 string
	AMD64SHA256 string
}

// ValidateCanonical extracts validated stable release metadata. Schema-1
// manifests require exact toolkit-rendered bytes. Schema-2 Formula profiles
// must use ValidateCanonicalWithTemplate.
func ValidateCanonical(data []byte, project manifest.Manifest) (CanonicalMetadata, error) {
	if err := project.Validate(); err != nil {
		return CanonicalMetadata{}, err
	}
	if project.Homebrew.FormulaProfile != "" {
		return CanonicalMetadata{}, errors.New("tap-owned Formula profile template is required")
	}
	current, err := inspect(data, project)
	if err != nil {
		return CanonicalMetadata{}, err
	}
	expected, err := Render(project, current.version, current.armSHA.value, current.amdSHA.value)
	if err != nil {
		return CanonicalMetadata{}, fmt.Errorf("render canonical Formula: %w", err)
	}
	if !bytes.Equal(data, expected) {
		return CanonicalMetadata{}, errors.New("Formula bytes are not the canonical manifest rendering")
	}
	return CanonicalMetadata{
		Version:     current.version,
		ARM64SHA256: current.armSHA.value,
		AMD64SHA256: current.amdSHA.value,
	}, nil
}

// ValidateCanonicalWithTemplate requires complete schema-2 Formula equality
// with the tap-owned template rendered using its current release metadata.
func ValidateCanonicalWithTemplate(data, template []byte, project manifest.Manifest) (CanonicalMetadata, error) {
	if err := project.Validate(); err != nil {
		return CanonicalMetadata{}, err
	}
	if project.Homebrew.FormulaProfile == "" {
		return CanonicalMetadata{}, errors.New("tap-owned Formula template requires a schema 2 Formula profile")
	}
	current, err := profileMetadataFromTemplate(data, template, project)
	if err != nil {
		return CanonicalMetadata{}, err
	}
	return CanonicalMetadata{Version: current.version, ARM64SHA256: current.arm64SHA256, AMD64SHA256: current.amd64SHA256}, nil
}
