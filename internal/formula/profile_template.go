package formula

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	arm64URLToken = "@ARM64_URL@"
	arm64SHAToken = "@ARM64_SHA256@"
	amd64URLToken = "@AMD64_URL@"
	amd64SHAToken = "@AMD64_SHA256@"
	// rubyConstantPattern is a Ruby constant reference, optionally namespaced:
	// GitHubPrivateReleaseDownloadStrategy, or Hextap::PrivateAsset. It is
	// deliberately narrower than Ruby allows, because the only thing that
	// belongs here is a download strategy class name.
	rubyConstantPattern = `[A-Z][A-Za-z0-9_]*(?:::[A-Z][A-Za-z0-9_]*)*`
)

var profileTokenPattern = regexp.MustCompile(`@[^@\t \r\n]+@`)

// profileArchitectureBlockPattern is the canonical architecture block, with one
// thing allowed to vary: each url may carry a Homebrew download strategy.
//
// The block used to be an exact literal, which made a whole class of adopter
// impossible to package. Homebrew attaches a download strategy in exactly one
// way, a `using:` argument on the url call, and a formula whose release asset
// lives in a private repository cannot be installed without one: the rendered
// release URL cannot be authenticated, so the strategy is the install route
// rather than a preference (SB23-2503). A template that needed one could not
// match the literal, so the tap-owned profile and the private-asset route were
// mutually exclusive (SB23-2540).
//
// The suffix is optional and it is the ONLY thing permitted after the URL
// token, so the block stays a fixed shape rather than a free-form match, and
// the four tokens stay the only variable parts of the rendering. Both urls are
// captured so the caller can require them to agree; see
// validateProfileTemplateTokens for why that matters.
var profileArchitectureBlockPattern = regexp.MustCompile(
	`  if Hardware::CPU\.arm\?\n` +
		`    url "` + regexp.QuoteMeta(arm64URLToken) + `"(, using: ` + rubyConstantPattern + `)?\n` +
		`    sha256 "` + regexp.QuoteMeta(arm64SHAToken) + `"\n` +
		`  else\n` +
		`    url "` + regexp.QuoteMeta(amd64URLToken) + `"(, using: ` + rubyConstantPattern + `)?\n` +
		`    sha256 "` + regexp.QuoteMeta(amd64SHAToken) + `"\n` +
		`  end`)

type profileTokenOccurrence struct {
	index int
	token string
}

type profileFormulaMetadata struct {
	version     string
	arm64SHA256 string
	amd64SHA256 string
}

// UpdateWithTemplate updates a schema-2 Formula only when its complete current
// bytes equal the tap-owned template rendered with the current release metadata.
func UpdateWithTemplate(original, template []byte, project manifest.Manifest, version, arm64SHA, amd64SHA string) ([]byte, UpdateResult, error) {
	if err := project.Validate(); err != nil {
		return nil, UpdateResult{}, err
	}
	if project.Homebrew.FormulaProfile == "" {
		return nil, UpdateResult{}, errors.New("tap-owned Formula template requires a schema 2 Formula profile")
	}
	if err := validateReleaseMetadata(version, arm64SHA, amd64SHA); err != nil {
		return nil, UpdateResult{}, err
	}
	current, err := profileMetadataFromTemplate(original, template, project)
	if err != nil {
		return nil, UpdateResult{}, err
	}
	comparison, err := manifest.CompareStableVersions(version, current.version)
	if err != nil {
		return nil, UpdateResult{}, fmt.Errorf("inspect Formula version: %w", err)
	}
	if comparison < 0 {
		return nil, UpdateResult{}, fmt.Errorf("refuse Formula downgrade from %s to %s", current.version, version)
	}
	updated, err := renderProfileTemplate(template, project, version, arm64SHA, amd64SHA)
	if err != nil {
		return nil, UpdateResult{}, err
	}
	verified, err := profileMetadataFromTemplate(updated, template, project)
	if err != nil {
		return nil, UpdateResult{}, fmt.Errorf("verify updated Formula: %w", err)
	}
	if verified.version != version || verified.arm64SHA256 != arm64SHA || verified.amd64SHA256 != amd64SHA {
		return nil, UpdateResult{}, errors.New("verify updated Formula: release metadata does not match request")
	}
	return updated, UpdateResult{PreviousVersion: current.version, Version: version, Changed: !bytes.Equal(original, updated)}, nil
}

func profileMetadataFromTemplate(formula, template []byte, project manifest.Manifest) (profileFormulaMetadata, error) {
	if len(template) == 0 || !utf8.Valid(template) || bytes.IndexByte(template, 0) >= 0 {
		return profileFormulaMetadata{}, errors.New("tap-owned Formula template must be nonempty UTF-8 text without NUL bytes")
	}
	if len(formula) == 0 || !utf8.Valid(formula) || bytes.IndexByte(formula, 0) >= 0 {
		return profileFormulaMetadata{}, errors.New("tap Formula must be nonempty UTF-8 text without NUL bytes")
	}
	occurrences, err := validateProfileTemplateTokens(template)
	if err != nil {
		return profileFormulaMetadata{}, err
	}
	var pattern strings.Builder
	pattern.WriteString(`(?s)^`)
	start := 0
	for _, occurrence := range occurrences {
		pattern.WriteString(regexp.QuoteMeta(string(template[start:occurrence.index])))
		if occurrence.token == arm64SHAToken || occurrence.token == amd64SHAToken {
			pattern.WriteString(`([0-9a-f]{64})`)
		} else {
			pattern.WriteString(`([^"\r\n]+)`)
		}
		start = occurrence.index + len(occurrence.token)
	}
	pattern.WriteString(regexp.QuoteMeta(string(template[start:])))
	pattern.WriteString(`\z`)
	compiled, err := regexp.Compile(pattern.String())
	if err != nil {
		return profileFormulaMetadata{}, fmt.Errorf("compile tap-owned Formula template: %w", err)
	}
	matches := compiled.FindSubmatch(formula)
	if len(matches) != len(occurrences)+1 {
		return profileFormulaMetadata{}, errors.New("tap Formula bytes do not equal the tap-owned template rendered with current metadata")
	}
	values := make(map[string]string, len(occurrences))
	for index, occurrence := range occurrences {
		values[occurrence.token] = string(matches[index+1])
	}
	armVersion, err := canonicalURLVersion(values[arm64URLToken], project, project.Formula.Assets.DarwinARM64)
	if err != nil {
		return profileFormulaMetadata{}, fmt.Errorf("tap template arm64 URL: %w", err)
	}
	amdVersion, err := canonicalURLVersion(values[amd64URLToken], project, project.Formula.Assets.DarwinAMD64)
	if err != nil {
		return profileFormulaMetadata{}, fmt.Errorf("tap template amd64 URL: %w", err)
	}
	if armVersion != amdVersion {
		return profileFormulaMetadata{}, errors.New("tap template release URLs use different versions")
	}
	rendered, err := renderProfileTemplate(template, project, armVersion, values[arm64SHAToken], values[amd64SHAToken])
	if err != nil {
		return profileFormulaMetadata{}, err
	}
	if !bytes.Equal(rendered, formula) {
		return profileFormulaMetadata{}, errors.New("tap Formula is not the exact current-metadata template rendering")
	}
	return profileFormulaMetadata{version: armVersion, arm64SHA256: values[arm64SHAToken], amd64SHA256: values[amd64SHAToken]}, nil
}

func renderProfileTemplate(template []byte, project manifest.Manifest, version, arm64SHA, amd64SHA string) ([]byte, error) {
	if _, err := validateProfileTemplateTokens(template); err != nil {
		return nil, err
	}
	replacements := map[string]string{
		arm64URLToken: releaseURL(project, version, project.Formula.Assets.DarwinARM64),
		arm64SHAToken: arm64SHA,
		amd64URLToken: releaseURL(project, version, project.Formula.Assets.DarwinAMD64),
		amd64SHAToken: amd64SHA,
	}
	result := append([]byte(nil), template...)
	for _, token := range []string{arm64URLToken, arm64SHAToken, amd64URLToken, amd64SHAToken} {
		result = bytes.Replace(result, []byte(token), []byte(replacements[token]), 1)
	}
	if profileTokenPattern.Match(result) {
		return nil, errors.New("tap-owned Formula template contains unresolved tokens")
	}
	return result, nil
}

func validateProfileTemplateTokens(template []byte) ([]profileTokenOccurrence, error) {
	blocks := profileArchitectureBlockPattern.FindAllSubmatch(template, -1)
	if len(blocks) != 1 {
		return nil, fmt.Errorf("tap-owned Formula template must contain exactly one canonical architecture metadata block, found %d", len(blocks))
	}
	// Both architectures must name the same strategy, or neither. A template
	// that authenticates the arm64 asset and not the x86_64 one installs on one
	// Mac and fails on the other, and the failure is a 404 on a private asset,
	// which reads like a typo in the formula name rather than a missing
	// credential. Accepting one suffix without the other would make the cheaper
	// half of that mistake silent.
	if !bytes.Equal(blocks[0][1], blocks[0][2]) {
		return nil, errors.New("tap-owned Formula template must name the same download strategy for both architectures")
	}
	tokens := []string{arm64URLToken, arm64SHAToken, amd64URLToken, amd64SHAToken}
	allowed := make(map[string]bool, len(tokens))
	occurrences := make([]profileTokenOccurrence, 0, len(tokens))
	for _, token := range tokens {
		allowed[token] = true
		if count := bytes.Count(template, []byte(token)); count != 1 {
			return nil, fmt.Errorf("tap-owned Formula template must contain exactly one %s, found %d", token, count)
		}
		occurrences = append(occurrences, profileTokenOccurrence{index: bytes.Index(template, []byte(token)), token: token})
	}
	for _, token := range profileTokenPattern.FindAllString(string(template), -1) {
		if !allowed[token] {
			return nil, fmt.Errorf("tap-owned Formula template contains unsupported token %s", token)
		}
	}
	sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].index < occurrences[j].index })
	return occurrences, nil
}
