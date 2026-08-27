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
	arm64URLToken                    = "@ARM64_URL@"
	arm64SHAToken                    = "@ARM64_SHA256@"
	amd64URLToken                    = "@AMD64_URL@"
	amd64SHAToken                    = "@AMD64_SHA256@"
	profileArchitectureTemplateBlock = `  if Hardware::CPU.arm?
    url "@ARM64_URL@"
    sha256 "@ARM64_SHA256@"
  else
    url "@AMD64_URL@"
    sha256 "@AMD64_SHA256@"
  end`
)

var profileTokenPattern = regexp.MustCompile(`@[^@\t \r\n]+@`)

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
	if count := bytes.Count(template, []byte(profileArchitectureTemplateBlock)); count != 1 {
		return nil, fmt.Errorf("tap-owned Formula template must contain exactly one canonical architecture metadata block, found %d", count)
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
