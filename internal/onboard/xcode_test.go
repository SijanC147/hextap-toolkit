package onboard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

func writeXcodeProject(t *testing.T, origin string) string {
	t.Helper()
	root := t.TempDir()
	if root == "" {
		t.Fatal("t.TempDir() returned an empty path")
	}
	if live, err := os.Getwd(); err == nil && strings.HasPrefix(root, live) {
		t.Fatalf("fixture %q is inside the live worktree %q", root, live)
	}
	schemes := filepath.Join(root, "Foo.xcodeproj", "xcshareddata", "xcschemes")
	if err := os.MkdirAll(schemes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(schemes, "Foo.xcscheme"), []byte("<Scheme/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", origin},
	} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return root
}

func xcodeArtifact(t *testing.T, state onboardingState, path string) []byte {
	t.Helper()
	for _, item := range state.artifacts {
		if item.path == path {
			return item.data
		}
	}
	t.Fatalf("plan has no artifact %s", path)
	return nil
}

func TestXcodeProjectPlansSchema3Manifest(t *testing.T) {
	root := writeXcodeProject(t, "git@github.com:SijanC147/foo.git")
	options := validOptions(root)
	options.DryRun = true
	state, err := prepareOnboarding(options)
	if err != nil {
		t.Fatalf("prepareOnboarding() error = %v", err)
	}
	project, err := manifest.Parse(xcodeArtifact(t, state, manifestPath))
	if err != nil {
		t.Fatalf("manifest.Parse(generated) error = %v", err)
	}
	profile := project.Release.Profile
	if project.Schema != manifest.XcodeSchema || profile == nil || profile.Runtime != manifest.RuntimeXcode {
		t.Fatalf("generated manifest is not schema 3 xcode: %+v", project)
	}
	if profile.Project != "Foo.xcodeproj" || profile.Scheme != "Foo" || profile.Configuration != "Release" || profile.App != "foo.app" {
		t.Fatalf("profile = %+v", profile)
	}
	if project.Formula.Binary != "foo" || project.Formula.Assets.DarwinARM64 != "foo-darwin-arm64.zip" || project.Homebrew.CaskProfile != "foo" || !project.Homebrew.MacOSOnly {
		t.Fatalf("formula/homebrew = %+v / %+v", project.Formula, project.Homebrew)
	}
	if project.Release.LinuxEnabled() {
		t.Fatal("schema 3 manifest enables Linux")
	}
	adapter := xcodeArtifact(t, state, defaultAdapterPath)
	for _, required := range []string{"CI=1 xcodebuild build", "CODE_SIGNING_ALLOWED=NO", "-project 'Foo.xcodeproj'", "-scheme 'Foo'", "amd64) arch=\"x86_64\"", "/usr/bin/ditto", "codesign --verify --deep --strict", "trap 'rm -rf \"$derived\"' EXIT"} {
		if !bytes.Contains(adapter, []byte(required)) {
			t.Fatalf("adapter lacks %q:\n%s", required, adapter)
		}
	}
	if bytes.Contains(adapter, []byte("--entitlements")) {
		t.Fatalf("adapter names entitlements that do not exist:\n%s", adapter)
	}
}

func TestXcodeProjectRejectsExplicitLinux(t *testing.T) {
	root := writeXcodeProject(t, "git@github.com:SijanC147/foo.git")
	options := validOptions(root)
	options.DryRun = true
	options.LinuxSet = true
	options.Linux = true
	if _, err := prepareOnboarding(options); err == nil || !strings.Contains(err.Error(), "schema 3") {
		t.Fatalf("prepareOnboarding(--linux=true) error = %v, want schema 3 rejection", err)
	}
}

func TestLowercaseOwnerOriginRecordsCanonicalOwner(t *testing.T) {
	root := writeXcodeProject(t, "https://github.com/sijanc147/foo.git")
	options := validOptions(root)
	options.DryRun = true
	state, err := prepareOnboarding(options)
	if err != nil {
		t.Fatalf("prepareOnboarding(lowercase owner) error = %v", err)
	}
	project, err := manifest.Parse(xcodeArtifact(t, state, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if project.Formula.Repository.Owner != supportedOwner || project.Formula.Homepage != "https://github.com/SijanC147/foo" {
		t.Fatalf("manifest owner = %q homepage = %q, want canonical %s", project.Formula.Repository.Owner, project.Formula.Homepage, supportedOwner)
	}
}
