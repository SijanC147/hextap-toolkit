package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// RuntimeXcode selects the schema 3 xcodebuild application profile.
	RuntimeXcode = "xcode"
	// ArchiveContentsApp is a zip holding exactly one .app bundle. It is
	// deliberately distinct from "bundle", which means executable plus
	// LICENSE and README.md.
	ArchiveContentsApp = "app"
)

var (
	xcodeNamePattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+-]*$`)
	xcodeConfigurationPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

func (p ReleaseProfile) validateXcode(binary string) error {
	if p.Runtime != RuntimeXcode {
		return errors.New("validate manifest: schema 3 release.profile.runtime must be xcode")
	}
	if p.RuntimeVersion != "" || p.Install.Name != "" || p.Install.Argv != nil || p.Prepare != nil {
		return errors.New("validate manifest: Xcode profile does not accept Bun runtime fields")
	}
	if err := validateRelativePath("release.profile.project", p.Project); err != nil {
		return err
	}
	if !strings.HasSuffix(p.Project, ".xcodeproj") {
		return errors.New("validate manifest: release.profile.project must name a .xcodeproj")
	}
	if len(p.Scheme) > maxPathComponentBytes || !xcodeNamePattern.MatchString(p.Scheme) {
		return errors.New("validate manifest: release.profile.scheme is not a safe scheme name")
	}
	if len(p.Configuration) > 64 || !xcodeConfigurationPattern.MatchString(p.Configuration) {
		return errors.New("validate manifest: release.profile.configuration is not a safe build configuration")
	}
	stem := strings.TrimSuffix(p.App, ".app")
	if len(p.App) > maxPathComponentBytes || !strings.HasSuffix(p.App, ".app") || stem == "" || !xcodeNamePattern.MatchString(stem) {
		return errors.New("validate manifest: release.profile.app must be a safe .app basename")
	}
	if stem != binary {
		return errors.New("validate manifest: release.profile.app must be formula.binary + .app")
	}
	if p.Quality == nil {
		return errors.New("validate manifest: release.profile.quality must be an array")
	}
	return validateCommandList("release.profile.quality", p.Quality)
}

func validateXcodeTargets(formulaAssets Assets, targets map[string]TargetArtifacts) error {
	if len(targets) != 2 {
		return errors.New("validate manifest: schema 3 declares exactly darwin_arm64 and darwin_amd64")
	}
	for name, asset := range map[string]string{"darwin_arm64": formulaAssets.DarwinARM64, "darwin_amd64": formulaAssets.DarwinAMD64} {
		target, exists := targets[name]
		if !exists {
			return fmt.Errorf("validate manifest: required release target %q is missing", name)
		}
		if target.Binary != "" {
			return fmt.Errorf("validate manifest: release.targets.%s.binary is not supported for an Xcode app", name)
		}
		if target.Archive != asset {
			return errors.New("validate manifest: Darwin target archives must equal formula.assets")
		}
		if target.ArchiveContents != ArchiveContentsApp {
			return fmt.Errorf("validate manifest: release.targets.%s.archive_contents must be app", name)
		}
	}
	return nil
}
