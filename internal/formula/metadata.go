package formula

import (
	"errors"
	"regexp"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateReleaseMetadata(version, arm64SHA, amd64SHA string) error {
	if err := manifest.ValidateStableVersion(version); err != nil {
		return err
	}
	if !sha256Pattern.MatchString(arm64SHA) {
		return errors.New("arm64 SHA-256 must contain exactly 64 lowercase hexadecimal characters")
	}
	if !sha256Pattern.MatchString(amd64SHA) {
		return errors.New("amd64 SHA-256 must contain exactly 64 lowercase hexadecimal characters")
	}
	return nil
}
