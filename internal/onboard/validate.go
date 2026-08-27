package onboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
	"github.com/SijanC147/hextap-toolkit/internal/release"
)

const (
	smokeVersion = "1.2.3-rc.1"
	smokeCommit  = "0123456789abcdef0123456789abcdef01234567"
)

var workflowPinPattern = regexp.MustCompile(`(?m)^    uses: SijanC147/hextap-toolkit/\.github/workflows/release-go\.yml@([0-9a-f]{40}) # (v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$`)

type decodedRulesetRule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type decodedRuleset struct {
	Name        string               `json:"name"`
	Target      string               `json:"target"`
	Enforcement string               `json:"enforcement"`
	Conditions  rulesetConditions    `json:"conditions"`
	Rules       []decodedRulesetRule `json:"rules"`
}

// Validate checks the complete local onboarding contract without executing
// project code unless Build is explicitly true.
func Validate(options ValidateOptions) (ValidateResult, error) {
	root, repository, err := resolveProject(options.Project)
	if err != nil {
		return ValidateResult{}, err
	}
	owner, _, err := parseRepository(repository)
	if err != nil {
		return ValidateResult{}, err
	}
	if owner != supportedOwner {
		return ValidateResult{}, fmt.Errorf("repository owner %q is unsupported; the current publisher contract supports only %s", owner, supportedOwner)
	}
	manifestData, manifestInfo, err := readLocalFile(filepath.Join(root, manifestPath), "manifest", maximumLocalFile, true)
	if err != nil {
		return ValidateResult{}, err
	}
	if !exactFileMode(manifestInfo, 0o644) {
		return ValidateResult{}, errors.New("manifest mode must be 0644")
	}
	project, err := parseManifestBytes(manifestData)
	if err != nil {
		return ValidateResult{}, err
	}
	if manifestContainsCredential(project) {
		return ValidateResult{}, errors.New("manifest metadata appears to contain a credential and was rejected")
	}
	if project.RepositorySlug() != repository {
		return ValidateResult{}, fmt.Errorf("manifest repository %q does not match Git remote origin identity %q", project.RepositorySlug(), repository)
	}
	if err := validateRequiredProjectFile(filepath.Join(root, "LICENSE"), "LICENSE"); err != nil {
		return ValidateResult{}, err
	}
	if err := validateRequiredProjectFile(filepath.Join(root, "README.md"), "README.md"); err != nil {
		return ValidateResult{}, err
	}
	if err := validateAdapter(root, project.Release.BuildScript); err != nil {
		return ValidateResult{}, err
	}

	workflow, info, err := readManagedArtifact(root, workflowPath)
	if err != nil {
		return ValidateResult{}, err
	}
	if !exactFileMode(info, 0o644) {
		return ValidateResult{}, errors.New("caller workflow mode must be 0644")
	}
	toolkitVersion, toolkitSHA, err := validateWorkflow(workflow)
	if err != nil {
		return ValidateResult{}, err
	}

	tapData, tapInfo, err := readManagedArtifact(root, tapPath)
	if err != nil {
		return ValidateResult{}, err
	}
	if !exactFileMode(tapInfo, 0o644) {
		return ValidateResult{}, errors.New("tap registration mode must be 0644")
	}
	tapManifest, err := parseManifestBytes(tapData)
	if err != nil {
		return ValidateResult{}, fmt.Errorf("validate tap registration: %w", err)
	}
	if !reflect.DeepEqual(project, tapManifest) {
		return ValidateResult{}, errors.New("tap registration is not semantically equal to the source manifest")
	}
	if err := validateTapIdentity(project); err != nil {
		return ValidateResult{}, err
	}

	mainData, mainInfo, err := readManagedArtifact(root, mainRulesetPath)
	if err != nil {
		return ValidateResult{}, err
	}
	if !exactFileMode(mainInfo, 0o644) {
		return ValidateResult{}, errors.New("main ruleset mode must be 0644")
	}
	checks, err := validateMainRuleset(mainData)
	if err != nil {
		return ValidateResult{}, err
	}
	tagData, tagInfo, err := readManagedArtifact(root, tagRulesetPath)
	if err != nil {
		return ValidateResult{}, err
	}
	if !exactFileMode(tagInfo, 0o644) {
		return ValidateResult{}, errors.New("release-tags ruleset mode must be 0644")
	}
	wantTag, _ := tagRulesetBytes()
	if !bytes.Equal(tagData, wantTag) {
		return ValidateResult{}, errors.New("release-tags ruleset does not match the owned active tag policy")
	}

	setup, setupInfo, err := readManagedArtifact(root, setupPath)
	if err != nil {
		return ValidateResult{}, err
	}
	if !exactFileMode(setupInfo, 0o644) || !bytes.Equal(setup, setupDocument(repository, project.Formula.Name, toolkitVersion, toolkitSHA)) {
		return ValidateResult{}, errors.New("SETUP.md does not match the exact safe follow-up instructions")
	}

	result := ValidateResult{
		Project:        root,
		Manifest:       project,
		ToolkitVersion: toolkitVersion,
		ToolkitSHA:     toolkitSHA,
		RequiredChecks: checks,
	}
	if options.Build {
		if err := validateBuildSmoke(root, project); err != nil {
			return ValidateResult{}, err
		}
		result.BuildVerified = true
	}
	return result, nil
}

func validateRequiredProjectFile(path, label string) error {
	_, _, err := readLocalFile(path, label, maximumLocalFile, false)
	return err
}

func validateAdapter(root, relative string) error {
	if err := inspectArtifactParents(root, relative); err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	_, info, err := readLocalFile(path, "build adapter", maximumLocalFile, true)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 == 0 || hasSpecialFileMode(info) {
		return errors.New("build adapter must be executable")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve build adapter: %w", err)
	}
	relativeResolved, err := filepath.Rel(root, resolved)
	if err != nil || relativeResolved == ".." || strings.HasPrefix(relativeResolved, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeResolved) {
		return errors.New("build adapter must resolve within the project")
	}
	return nil
}

func readManagedArtifact(root, relative string) ([]byte, os.FileInfo, error) {
	if err := inspectArtifactParents(root, relative); err != nil {
		return nil, nil, err
	}
	data, info, err := readLocalFile(filepath.Join(root, filepath.FromSlash(relative)), "managed artifact", maximumLocalFile, true)
	if err != nil {
		return nil, nil, fmt.Errorf("validate %s: %w", relative, err)
	}
	return data, info, nil
}

func validateWorkflow(data []byte) (version, commit string, err error) {
	matches := workflowPinPattern.FindSubmatch(data)
	if len(matches) != 3 {
		return "", "", errors.New("caller workflow lacks an exact stable toolkit version and full SHA pin")
	}
	commit, version = string(matches[1]), string(matches[2])
	if err := validateToolkitPin(version, commit); err != nil {
		return "", "", err
	}
	if !bytes.Equal(data, workflowBytes(version, commit)) {
		return "", "", errors.New("caller workflow does not match the exact owned thin caller")
	}
	return version, commit, nil
}

func validateMainRuleset(data []byte) ([]string, error) {
	var decoded decodedRuleset
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode main ruleset: %w", err)
	}
	if len(decoded.Rules) != 4 {
		return nil, errors.New("main ruleset must contain the four owned rules")
	}
	var status requiredStatusParameters
	found := false
	for _, rule := range decoded.Rules {
		if rule.Type != "required_status_checks" {
			continue
		}
		if found {
			return nil, errors.New("main ruleset contains duplicate required status policy")
		}
		found = true
		parameters := json.NewDecoder(bytes.NewReader(rule.Parameters))
		parameters.DisallowUnknownFields()
		if err := parameters.Decode(&status); err != nil {
			return nil, fmt.Errorf("decode required status policy: %w", err)
		}
	}
	if !found || !status.StrictRequiredStatusChecksPolicy || len(status.RequiredStatusChecks) == 0 {
		return nil, errors.New("main ruleset requires a nonempty strict status-check policy")
	}
	checks := make([]string, len(status.RequiredStatusChecks))
	for index, check := range status.RequiredStatusChecks {
		checks[index] = check.Context
	}
	checks, err := validateRequiredChecks(checks)
	if err != nil {
		return nil, err
	}
	want, err := mainRulesetBytes(checks)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, want) {
		return nil, errors.New("main ruleset does not match the owned active default-branch policy")
	}
	return checks, nil
}

func validateBuildSmoke(root string, project manifest.Manifest) error {
	temporary, err := os.MkdirTemp("", ".brew-hextap-validate-*")
	if err != nil {
		return fmt.Errorf("create private build smoke directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return fmt.Errorf("secure build smoke directory: %w", err)
	}
	bunCacheDir := ""
	if project.Schema == manifest.ProfileSchema {
		bunCacheDir = filepath.Join(temporary, "bun-runtime-cache")
		if err := os.Mkdir(bunCacheDir, 0o700); err != nil {
			return fmt.Errorf("create private Bun runtime cache: %w", err)
		}
		if err := release.RunProfile(release.ProfileOptions{
			ManifestPath: filepath.Join(root, manifestPath),
			SourceDir:    root,
			Phase:        release.ProfileBuild,
			BunCacheDir:  bunCacheDir,
			Stdout:       io.Discard,
			Stderr:       io.Discard,
		}); err != nil {
			return fmt.Errorf("profile build preparation: %w", err)
		}
	}
	output := filepath.Join(temporary, "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		return fmt.Errorf("create build smoke output: %w", err)
	}
	manifestFile := filepath.Join(root, manifestPath)
	if _, err := release.Build(release.BuildOptions{
		ManifestPath: manifestFile,
		Version:      smokeVersion,
		Commit:       smokeCommit,
		SourceDir:    root,
		OutputDir:    output,
		BunCacheDir:  bunCacheDir,
	}); err != nil {
		return fmt.Errorf("adapter build smoke: %w", err)
	}
	executeTarget := hostDeclaredTarget(project)
	if _, err := release.Verify(release.VerifyOptions{
		ManifestPath:  manifestFile,
		Version:       smokeVersion,
		Commit:        smokeCommit,
		Directory:     output,
		ExecuteTarget: executeTarget,
	}); err != nil {
		return fmt.Errorf("archive verification smoke: %w", err)
	}
	return nil
}

func hostDeclaredTarget(project manifest.Manifest) string {
	if project.Schema == manifest.ProfileSchema {
		key := runtime.GOOS + "_" + runtime.GOARCH
		if _, exists := project.Release.Targets[key]; exists {
			return runtime.GOOS + "-" + runtime.GOARCH
		}
		return ""
	}
	if runtime.GOOS == "darwin" && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		return runtime.GOOS + "-" + runtime.GOARCH
	}
	if runtime.GOOS == "linux" && project.Release.LinuxEnabled() && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		return runtime.GOOS + "-" + runtime.GOARCH
	}
	return ""
}
