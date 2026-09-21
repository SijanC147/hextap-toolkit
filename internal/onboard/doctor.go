package onboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"

	formulaengine "github.com/SijanC147/hextap-toolkit/internal/formula"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	onlineCommandTimeout = 15 * time.Second
	maximumTagPeelDepth  = 8
)

type remoteRulesetSummary struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Target      string `json:"target"`
	SourceType  string `json:"source_type"`
	Source      string `json:"source"`
	Enforcement string `json:"enforcement"`
}

type remoteRulesetDetail struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Target       string          `json:"target"`
	SourceType   string          `json:"source_type"`
	Source       string          `json:"source"`
	Enforcement  string          `json:"enforcement"`
	BypassActors json.RawMessage `json:"bypass_actors"`
	Conditions   json.RawMessage `json:"conditions"`
	Rules        json.RawMessage `json:"rules"`
}

type normalizedBypassActor struct {
	ActorID    int64  `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

type normalizedRuleset struct {
	Name         string
	Target       string
	Enforcement  string
	BypassActors []normalizedBypassActor
	Conditions   any
	Rules        any
}

type gitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type gitRefResponse struct {
	Ref    string    `json:"ref"`
	Object gitObject `json:"object"`
}

type gitTagResponse struct {
	Tag    string    `json:"tag"`
	Object gitObject `json:"object"`
}

// Doctor verifies required local tools and the complete local contract. With
// Online set, it adds only bounded read-only GitHub queries.
func Doctor(options DoctorOptions) (DoctorResult, error) {
	checks := make([]string, 0, 12)
	for _, tool := range []string{"git", "gh"} {
		if _, err := exec.LookPath(tool); err != nil {
			return DoctorResult{}, fmt.Errorf("required tool %q was not found on PATH", tool)
		}
		checks = append(checks, "tool "+tool)
	}
	validated, err := Validate(ValidateOptions{Project: options.Project})
	if err != nil {
		return DoctorResult{}, err
	}
	runtimeTool := "go"
	if validated.Manifest.Release.Profile != nil {
		runtimeTool = validated.Manifest.Release.Profile.Runtime
	}
	if _, err := exec.LookPath(runtimeTool); err != nil {
		return DoctorResult{}, fmt.Errorf("required tool %q was not found on PATH", runtimeTool)
	}
	checks = append(checks, "tool "+runtimeTool, "local onboarding contract")
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
	if _, err := runCommandOutput(onlineCommandTimeout, 4<<10, "gh", "auth", "status", "--active", "--hostname", "github.com"); err != nil {
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
	// A listing that could not be read is not a listing without the name in
	// it. A network failure, a token without the scope, or a response over
	// ghRead's cap would otherwise tell an adopter whose secret exists that it
	// does not, which is the same class of wrong report as the one below.
	if err != nil {
		return nil, errors.New("online doctor: the Actions secret listing could not be read")
	}
	declaredNames := lineSet(secretNames)
	if !declaredNames["OP_SERVICE_ACCOUNT_TOKEN"] {
		return nil, errors.New("online doctor: required Actions secret name OP_SERVICE_ACCOUNT_TOKEN is missing")
	}
	submoduleCredential := submoduleCredentialCheck(validated.Manifest.Release.SubmodulesMode(), declaredNames)
	if err := validateOnlineRulesets(repository, validated.RequiredChecks); err != nil {
		return nil, err
	}
	if !selfCallerPin(validated) {
		resolved, err := resolveStableToolkitTag(validated.ToolkitVersion)
		if err != nil || resolved != validated.ToolkitSHA {
			return nil, errors.New("online doctor: stable toolkit tag does not resolve to the caller workflow SHA")
		}
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
	if err := validateFormulaClass([]byte(formulaData), validated.Manifest.Formula.Class); err != nil {
		return nil, errors.New("online doctor: tap Formula fails canonical class validation")
	}
	if validated.Manifest.Homebrew.FormulaProfile != "" {
		templateDestination := path.Join("packaging", validated.Manifest.Homebrew.FormulaProfile+".rb.tmpl")
		templateType, err := ghRead(4<<10, "api", "repos/SijanC147/homebrew-hextap/contents/"+templateDestination, "--jq", ".type")
		if err != nil || strings.TrimSpace(templateType) != "file" {
			return nil, fmt.Errorf("online doctor: tap-owned Formula template %s is not a regular repository file", templateDestination)
		}
		templateData, err := ghRead(maximumLocalFile, "api", "-H", "Accept: application/vnd.github.raw+json", "repos/SijanC147/homebrew-hextap/contents/"+templateDestination)
		if err != nil {
			return nil, fmt.Errorf("online doctor: tap-owned Formula template %s is missing", templateDestination)
		}
		if _, err := formulaengine.ValidateCanonicalWithTemplate([]byte(formulaData), []byte(templateData), validated.Manifest); err != nil {
			return nil, errors.New("online doctor: tap Formula does not equal its tap-owned template rendering")
		}
	} else if _, err := formulaengine.ValidateCanonical([]byte(formulaData), validated.Manifest); err != nil {
		return nil, errors.New("online doctor: tap Formula does not satisfy the manifest Formula contract")
	}
	// Name the provenance line for what actually happened. Reporting "stable
	// toolkit provenance" after skipping it would claim a check that never ran,
	// which is the failure this whole family of checks exists to prevent.
	provenance := "stable toolkit provenance"
	if selfCallerPin(validated) {
		provenance = "toolkit self-caller: no external pin to verify"
	}
	return []string{
		"GitHub authentication",
		"default branch main",
		"immutable releases",
		"Actions secret name",
		submoduleCredential,
		"owned active ruleset bodies",
		provenance,
		"canonical tap registration and Formula contract",
	}, nil
}

func validateOnlineRulesets(repository string, checks []string) error {
	data, err := ghRead(maximumLocalFile, "api", "--paginate", "--slurp", "repos/"+repository+"/rulesets?per_page=100")
	if err != nil {
		return errors.New("online doctor: repository rulesets could not be read")
	}
	var pages [][]remoteRulesetSummary
	if err := decodeJSON([]byte(data), &pages); err != nil {
		return errors.New("online doctor: repository ruleset listing is malformed")
	}
	wanted := map[string]remoteRulesetSummary{}
	for _, page := range pages {
		for _, summary := range page {
			if summary.Name != "hextap/main" && summary.Name != "hextap/release-tags" {
				continue
			}
			if summary.SourceType != "Repository" || summary.Source != repository || summary.Enforcement != "active" {
				continue
			}
			if summary.ID <= 0 || summary.Target == "" {
				return fmt.Errorf("online doctor: owned ruleset %q has malformed identity", summary.Name)
			}
			if _, duplicate := wanted[summary.Name]; duplicate {
				return fmt.Errorf("online doctor: owned ruleset %q is ambiguous", summary.Name)
			}
			wanted[summary.Name] = summary
		}
	}
	mainBytes, err := mainRulesetBytes(checks)
	if err != nil {
		return err
	}
	tagBytes, err := tagRulesetBytes()
	if err != nil {
		return err
	}
	expected := map[string][]byte{
		"hextap/main":         mainBytes,
		"hextap/release-tags": tagBytes,
	}
	for _, name := range []string{"hextap/main", "hextap/release-tags"} {
		summary, exists := wanted[name]
		if !exists {
			return fmt.Errorf("online doctor: exact active repository-owned ruleset %q is missing", name)
		}
		detailData, err := ghRead(maximumLocalFile, "api", "repos/"+repository+"/rulesets/"+strconv.FormatInt(summary.ID, 10))
		if err != nil {
			return fmt.Errorf("online doctor: owned ruleset %q body could not be read", name)
		}
		if err := compareRemoteRuleset([]byte(detailData), expected[name], repository, summary); err != nil {
			return fmt.Errorf("online doctor: owned ruleset %q drifted: %w", name, err)
		}
	}
	return nil
}

func compareRemoteRuleset(remoteData, expectedData []byte, repository string, summary remoteRulesetSummary) error {
	var remote remoteRulesetDetail
	if err := decodeJSON(remoteData, &remote); err != nil {
		return errors.New("malformed remote body")
	}
	if remote.ID != summary.ID || remote.Name != summary.Name || remote.Target != summary.Target || remote.SourceType != "Repository" || remote.Source != repository || remote.Enforcement != "active" {
		return errors.New("identity, source, target, or enforcement mismatch")
	}
	if len(remote.BypassActors) == 0 || bytes.Equal(bytes.TrimSpace(remote.BypassActors), []byte("null")) {
		return errors.New("bypass actors were not returned")
	}
	var remoteActors []normalizedBypassActor
	if err := decodeJSON(remote.BypassActors, &remoteActors); err != nil {
		return errors.New("malformed bypass actors")
	}
	if remoteActors == nil {
		return errors.New("bypass actors must be an explicit array")
	}
	var expectedBody remoteRulesetDetail
	if err := decodeJSON(expectedData, &expectedBody); err != nil {
		return fmt.Errorf("decode local ruleset: %w", err)
	}
	remoteNormalized, err := normalizeRuleset(remote, remoteActors)
	if err != nil {
		return err
	}
	expectedNormalized, err := normalizeRuleset(expectedBody, []normalizedBypassActor{})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(remoteNormalized, expectedNormalized) {
		return errors.New("bypass actors, conditions, or rules mismatch")
	}
	return nil
}

func normalizeRuleset(body remoteRulesetDetail, actors []normalizedBypassActor) (normalizedRuleset, error) {
	var conditions any
	if err := decodeJSON(body.Conditions, &conditions); err != nil {
		return normalizedRuleset{}, errors.New("malformed ruleset conditions")
	}
	var rules any
	if err := decodeJSON(body.Rules, &rules); err != nil {
		return normalizedRuleset{}, errors.New("malformed ruleset rules")
	}
	if body.Target == "tag" {
		restoreTagUpdateDefault(rules)
	}
	if actors == nil {
		actors = []normalizedBypassActor{}
	}
	return normalizedRuleset{
		Name:         body.Name,
		Target:       body.Target,
		Enforcement:  body.Enforcement,
		BypassActors: actors,
		Conditions:   conditions,
		Rules:        rules,
	}, nil
}

// submoduleCredentialCheck names what happened to the conditional Actions
// secret, in the three states it actually has. SB23-873 fixed the same defect
// on the provenance check: a check that was skipped, reported as one that
// passed. SB23-2505 is that defect wearing a new field, so the fix takes the
// same shape, a line that says which of the three happened.
//
// The absent name is reported as not verified rather than as a failure. The
// reusable workflow falls back to github.token, which is the correct
// credential for public submodules, and nothing readable from here says
// whether a submodule is private. Failing the run would also stop every check
// that follows for an adopter whose configuration is correct.
//
// Only the name is ever read, from the listing the OP token already uses.
// GitHub does not serve an Actions secret value, and no call here asks for one.
func submoduleCredentialCheck(mode string, declaredNames map[string]bool) string {
	if mode == manifest.SubmodulesNone {
		return "submodule credential: not required, this manifest checks out no submodules"
	}
	if declaredNames["SUBMODULES_TOKEN"] {
		return "Actions secret name SUBMODULES_TOKEN for the declared submodule checkout"
	}
	return "submodule credential SUBMODULES_TOKEN is absent: private submodules NOT verified"
}

// restoreTagUpdateDefault puts back the one parameter GitHub declines to store.
// A tag-target `update` rule submitted with
// `{"update_allows_fetch_and_merge": false}` is accepted and then stored with
// no `parameters` object at all, measured on 2026-09-21 across three live
// rulesets: the two written in August carry the parameter, the one written in
// September does not, and a PUT of the generated body neither restores it nor
// records a new version in the ruleset's history. The generated body is what
// GitHub normalizes away, so re-applying the file cannot fix the drift.
//
// The absent parameter is GitHub's own default, so the two shapes describe the
// same protection and only the comparison needs to know that. Generation keeps
// writing the explicit form: dropping it there would make every ruleset written
// before the change drift instead.
//
// Restoring the default rather than stripping the parameter is what keeps a
// stored `true` reported as drift, which is a real weakening of tag protection
// and the thing this check exists to catch. The tolerance is confined to a
// tag-target `update` rule: a branch-target ruleset round-trips every
// parameter, so an absent `parameters` there stays drift.
func restoreTagUpdateDefault(rules any) {
	entries, ok := rules.([]any)
	if !ok {
		return
	}
	for _, entry := range entries {
		rule, ok := entry.(map[string]any)
		if !ok || rule["type"] != "update" {
			continue
		}
		if _, present := rule["parameters"]; present {
			continue
		}
		rule["parameters"] = map[string]any{"update_allows_fetch_and_merge": false}
	}
}

// selfCallerPin reports the toolkit's own relative same-repository caller, by
// the only signature it has: validateWorkflow returns an empty toolkit version
// and SHA for it, and for nothing else. An external caller cannot reach here
// with an empty pin, because validateWorkflow fails locally with "caller
// workflow lacks an exact stable toolkit version and full SHA pin" before any
// online check runs.
//
// The pair is tested rather than a path, so widening this to any other caller
// shape would take a deliberate change to validateWorkflow rather than a
// filename that happens to match. One half empty is not a self-caller: that is
// a malformed pin, and it still fails the provenance check below.
func selfCallerPin(validated ValidateResult) bool {
	return validated.ToolkitVersion == "" && validated.ToolkitSHA == ""
}

func resolveStableToolkitTag(version string) (string, error) {
	encoded := url.PathEscape(version)
	data, err := ghRead(16<<10, "api", "repos/SijanC147/hextap-toolkit/git/ref/tags/"+encoded)
	if err != nil {
		return "", errors.New("stable toolkit tag reference is missing")
	}
	var reference gitRefResponse
	if err := decodeJSON([]byte(data), &reference); err != nil || reference.Ref != "refs/tags/"+version {
		return "", errors.New("stable toolkit tag reference is malformed")
	}
	object := reference.Object
	seen := make(map[string]struct{})
	for depth := 0; ; depth++ {
		if !fullCommitPattern.MatchString(object.SHA) {
			return "", errors.New("stable toolkit tag object has an invalid SHA")
		}
		switch object.Type {
		case "commit":
			return object.SHA, nil
		case "tag":
			if depth >= maximumTagPeelDepth {
				return "", errors.New("stable toolkit annotated tag exceeds peel depth")
			}
			if _, duplicate := seen[object.SHA]; duplicate {
				return "", errors.New("stable toolkit annotated tag contains a cycle")
			}
			seen[object.SHA] = struct{}{}
			data, err := ghRead(16<<10, "api", "repos/SijanC147/hextap-toolkit/git/tags/"+object.SHA)
			if err != nil {
				return "", errors.New("stable toolkit annotated tag object is missing")
			}
			var tag gitTagResponse
			if err := decodeJSON([]byte(data), &tag); err != nil || tag.Tag == "" || len(tag.Tag) > 256 || strings.ContainsAny(tag.Tag, "\r\n\x00") {
				return "", errors.New("stable toolkit annotated tag object is malformed")
			}
			if depth == 0 && tag.Tag != version {
				return "", errors.New("stable toolkit annotated tag name mismatches the exact ref")
			}
			object = tag.Object
		default:
			return "", errors.New("stable toolkit tag does not peel to a commit")
		}
	}
}

func decodeJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func ghRead(maximum int, args ...string) (string, error) {
	if len(args) == 0 || args[0] != "api" {
		return "", errors.New("gh read requires the api command")
	}
	pinned := make([]string, 0, len(args)+2)
	pinned = append(pinned, "api", "--hostname", "github.com")
	pinned = append(pinned, args[1:]...)
	return runCommandOutput(onlineCommandTimeout, maximum, "gh", pinned...)
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
