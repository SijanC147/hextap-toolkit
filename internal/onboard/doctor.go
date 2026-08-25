package onboard

import (
	"errors"
	"fmt"
	"os/exec"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const onlineCommandTimeout = 15 * time.Second

// Doctor verifies required local tools and the complete local contract. With
// Online set, it adds only bounded read-only GitHub queries.
func Doctor(options DoctorOptions) (DoctorResult, error) {
	checks := make([]string, 0, 12)
	for _, tool := range []string{"git", "gh", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			return DoctorResult{}, fmt.Errorf("required tool %q was not found on PATH", tool)
		}
		checks = append(checks, "tool "+tool)
	}
	validated, err := Validate(ValidateOptions{Project: options.Project})
	if err != nil {
		return DoctorResult{}, err
	}
	checks = append(checks, "local onboarding contract")
	if !options.Online {
		return DoctorResult{Project: validated.Project, Checks: checks}, nil
	}
	online, err := doctorOnline(validated)
	if err != nil {
		return DoctorResult{}, err
	}
	checks = append(checks, online...)
	return DoctorResult{Project: validated.Project, Checks: checks}, nil
}

func doctorOnline(validated ValidateResult) ([]string, error) {
	repository := validated.Manifest.RepositorySlug()
	if _, err := runCommandOutput(onlineCommandTimeout, 4<<10, "gh", "auth", "status", "--hostname", "github.com"); err != nil {
		return nil, errors.New("online doctor: GitHub authentication check failed")
	}
	defaultBranch, err := ghRead(16<<10, "api", "repos/"+repository, "--jq", ".default_branch")
	if err != nil || strings.TrimSpace(defaultBranch) != "main" {
		return nil, errors.New("online doctor: repository default branch must be main")
	}
	immutable, err := ghRead(16<<10, "api", "repos/"+repository+"/immutable-releases", "--jq", ".enabled")
	if err != nil || strings.TrimSpace(immutable) != "true" {
		return nil, errors.New("online doctor: immutable releases are not enabled")
	}
	secretNames, err := ghRead(64<<10, "api", "--paginate", "repos/"+repository+"/actions/secrets", "--jq", ".secrets[].name")
	if err != nil || !lineSet(secretNames)["OP_SERVICE_ACCOUNT_TOKEN"] {
		return nil, errors.New("online doctor: required Actions secret name OP_SERVICE_ACCOUNT_TOKEN is missing")
	}
	rulesetNames, err := ghRead(64<<10, "api", "--paginate", "repos/"+repository+"/rulesets", "--jq", `.[] | select(.enforcement == "active") | .name`)
	if err != nil {
		return nil, errors.New("online doctor: active repository rulesets could not be read")
	}
	rulesets := lineSet(rulesetNames)
	for _, name := range []string{"hextap/main", "hextap/release-tags"} {
		if !rulesets[name] {
			return nil, fmt.Errorf("online doctor: active owned ruleset %q is missing", name)
		}
	}
	resolved, err := ghRead(16<<10, "api", "repos/SijanC147/hextap-toolkit/commits/"+validated.ToolkitVersion, "--jq", ".sha")
	if err != nil || strings.TrimSpace(resolved) != validated.ToolkitSHA {
		return nil, errors.New("online doctor: stable toolkit tag does not resolve to the caller workflow SHA")
	}
	tapDestination := canonicalTapPath(validated.Manifest.Formula.Name)
	tapData, err := ghRead(maximumLocalFile, "api", "-H", "Accept: application/vnd.github.raw+json", "repos/SijanC147/homebrew-hextap/contents/"+tapDestination)
	if err != nil {
		return nil, fmt.Errorf("online doctor: canonical tap registration %s is missing", tapDestination)
	}
	tapManifest, err := manifest.Parse([]byte(tapData))
	if err != nil || !reflect.DeepEqual(validated.Manifest, tapManifest) {
		return nil, errors.New("online doctor: canonical tap registration does not semantically equal the source manifest")
	}
	formulaDestination := canonicalFormulaPath(validated.Manifest.Formula.Name)
	formulaData, err := ghRead(maximumLocalFile, "api", "-H", "Accept: application/vnd.github.raw+json", "repos/SijanC147/homebrew-hextap/contents/"+formulaDestination)
	if err != nil {
		return nil, fmt.Errorf("online doctor: canonical tap Formula %s is missing", formulaDestination)
	}
	classLine := regexp.MustCompile(`(?m)^class ` + regexp.QuoteMeta(validated.Manifest.Formula.Class) + ` < Formula$`)
	if !classLine.MatchString(formulaData) {
		return nil, errors.New("online doctor: canonical tap Formula does not declare the registered class")
	}
	return []string{
		"GitHub authentication",
		"default branch main",
		"immutable releases",
		"Actions secret name",
		"owned active rulesets",
		"stable toolkit provenance",
		"canonical tap registration and Formula",
	}, nil
}

func ghRead(maximum int, args ...string) (string, error) {
	return runCommandOutput(onlineCommandTimeout, maximum, "gh", args...)
}

func lineSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result
}

func canonicalTapPath(formula string) string {
	return path.Join("Projects", formula+".json")
}

func canonicalFormulaPath(formula string) string {
	return path.Join("Formula", formula+".rb")
}

func validateTapIdentity(project manifest.Manifest) error {
	destination := canonicalTapPath(project.Formula.Name)
	if path.Dir(destination) != "Projects" || strings.TrimSuffix(path.Base(destination), ".json") != project.Formula.Name || project.Formula.Class != classForFormula(project.Formula.Name) {
		return errors.New("tap registration destination basename, formula name, or registered class is inconsistent")
	}
	return nil
}
