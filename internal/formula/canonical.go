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

// ValidateCanonical extracts validated stable release metadata, renders the
// only Formula accepted for that metadata and manifest, and requires exact
// byte equality with the supplied Formula.
func ValidateCanonical(data []byte, project manifest.Manifest) (CanonicalMetadata, error) {
	if err := project.Validate(); err != nil {
		return CanonicalMetadata{}, err
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
