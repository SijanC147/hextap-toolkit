package formula

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/atomicfile"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

var (
	architectureLine   = regexp.MustCompile(`^([ \t]*)if Hardware::CPU\.arm\?$`)
	heredocStart       = regexp.MustCompile(`<<[~-]?([A-Z][A-Z0-9_]*)\b`)
	formulaVersionLine = regexp.MustCompile(`^[ \t]*version\b.*$`)
	formulaURLLine     = regexp.MustCompile(`^[ \t]*url\b.*$`)
	formulaSHALine     = regexp.MustCompile(`^[ \t]*sha256\b.*$`)
	urlLine            = regexp.MustCompile(`^[ \t]*url "([^"]+)"[ \t]*$`)
	shaLine            = regexp.MustCompile(`^[ \t]*sha256 "([^"]+)"[ \t]*$`)
)

// UpdateResult describes a validated Formula metadata transition.
type UpdateResult struct {
	PreviousVersion string
	Version         string
	Changed         bool
}

type line struct {
	start int
	end   int
	body  string
}

type metadata struct {
	version string
	armURL  valueSpan
	armSHA  valueSpan
	amdURL  valueSpan
	amdSHA  valueSpan
}

type valueSpan struct {
	start int
	end   int
	value string
}

type replacement struct {
	start int
	end   int
	value string
}

// Update validates the complete architecture metadata structure and changes
// only the two URL values and two SHA-256 values.
func Update(original []byte, project manifest.Manifest, version, arm64SHA, amd64SHA string) ([]byte, UpdateResult, error) {
	if err := project.Validate(); err != nil {
		return nil, UpdateResult{}, err
	}
	if err := validateReleaseMetadata(version, arm64SHA, amd64SHA); err != nil {
		return nil, UpdateResult{}, err
	}
	current, err := inspect(original, project)
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

	replacements := []replacement{
		{current.armURL.start, current.armURL.end, releaseURL(project, version, project.Formula.Assets.DarwinARM64)},
		{current.armSHA.start, current.armSHA.end, arm64SHA},
		{current.amdURL.start, current.amdURL.end, releaseURL(project, version, project.Formula.Assets.DarwinAMD64)},
		{current.amdSHA.start, current.amdSHA.end, amd64SHA},
	}
	updated := replaceValues(original, replacements)
	verified, err := inspect(updated, project)
	if err != nil {
		return nil, UpdateResult{}, fmt.Errorf("verify updated Formula: %w", err)
	}
	if verified.version != version || verified.armSHA.value != arm64SHA || verified.amdSHA.value != amd64SHA {
		return nil, UpdateResult{}, errors.New("verify updated Formula: release metadata does not match request")
	}
	return updated, UpdateResult{
		PreviousVersion: current.version,
		Version:         version,
		Changed:         !bytes.Equal(original, updated),
	}, nil
}

// UpdateFile atomically updates a Formula and preserves its file mode.
func UpdateFile(path string, project manifest.Manifest, version, arm64SHA, amd64SHA string) (UpdateResult, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("update Formula: inspect %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return UpdateResult{}, fmt.Errorf("update Formula: %q is not a regular file", path)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("update Formula: read %q: %w", path, err)
	}
	updated, result, err := Update(original, project, version, arm64SHA, amd64SHA)
	if err != nil {
		return UpdateResult{}, err
	}
	if !result.Changed {
		return result, nil
	}
	if err := atomicfile.Write(path, updated, info.Mode().Perm()); err != nil {
		return UpdateResult{}, fmt.Errorf("update Formula: %w", err)
	}
	return result, nil
}

func inspect(data []byte, project manifest.Manifest) (metadata, error) {
	lines := splitLines(data)
	if len(lines) == 0 {
		return metadata{}, errors.New("inspect Formula: file is empty")
	}
	codeLines, err := formulaCodeLines(lines)
	if err != nil {
		return metadata{}, err
	}
	architectureIndexes := make([]int, 0, 1)
	for index, current := range lines {
		if !codeLines[index] {
			continue
		}
		if architectureLine.MatchString(current.body) {
			architectureIndexes = append(architectureIndexes, index)
		}
	}
	if len(architectureIndexes) != 1 {
		return metadata{}, fmt.Errorf("inspect Formula: expected exactly one Hardware::CPU.arm? conditional, found %d", len(architectureIndexes))
	}
	start := architectureIndexes[0]
	if start+6 >= len(lines) {
		return metadata{}, errors.New("inspect Formula: architecture metadata block is incomplete")
	}
	indent := architectureLine.FindStringSubmatch(lines[start].body)[1]
	urlCount := 0
	shaCount := 0
	for index, current := range lines {
		if !codeLines[index] {
			continue
		}
		if formulaVersionLine.MatchString(current.body) {
			return metadata{}, errors.New("inspect Formula: explicit version stanza is not allowed")
		}
		if formulaURLLine.MatchString(current.body) {
			urlCount++
		}
		if formulaSHALine.MatchString(current.body) {
			shaCount++
		}
	}
	if urlCount != 2 || shaCount != 2 {
		return metadata{}, fmt.Errorf("inspect Formula: expected exactly two url and two sha256 declarations, found %d and %d", urlCount, shaCount)
	}
	if lines[start+3].body != indent+"else" || lines[start+6].body != indent+"end" {
		return metadata{}, errors.New("inspect Formula: architecture block must be if/url/sha256/else/url/sha256/end")
	}
	armURL, err := quotedValue(lines[start+1], urlLine, "arm64 URL")
	if err != nil {
		return metadata{}, err
	}
	armSHA, err := quotedValue(lines[start+2], shaLine, "arm64 SHA-256")
	if err != nil {
		return metadata{}, err
	}
	amdURL, err := quotedValue(lines[start+4], urlLine, "amd64 URL")
	if err != nil {
		return metadata{}, err
	}
	amdSHA, err := quotedValue(lines[start+5], shaLine, "amd64 SHA-256")
	if err != nil {
		return metadata{}, err
	}
	if !sha256Pattern.MatchString(armSHA.value) || !sha256Pattern.MatchString(amdSHA.value) {
		return metadata{}, errors.New("inspect Formula: sha256 values must contain exactly 64 lowercase hexadecimal characters")
	}

	armVersion, err := canonicalURLVersion(armURL.value, project, project.Formula.Assets.DarwinARM64)
	if err != nil {
		return metadata{}, fmt.Errorf("inspect Formula: arm64 URL: %w", err)
	}
	amdVersion, err := canonicalURLVersion(amdURL.value, project, project.Formula.Assets.DarwinAMD64)
	if err != nil {
		return metadata{}, fmt.Errorf("inspect Formula: amd64 URL: %w", err)
	}
	if armVersion != amdVersion {
		return metadata{}, fmt.Errorf("inspect Formula: arm64 and amd64 URLs use different versions %s and %s", armVersion, amdVersion)
	}
	return metadata{version: armVersion, armURL: armURL, armSHA: armSHA, amdURL: amdURL, amdSHA: amdSHA}, nil
}

func formulaCodeLines(lines []line) ([]bool, error) {
	result := make([]bool, len(lines))
	inHeredoc := false
	terminator := ""
	for index, current := range lines {
		if inHeredoc {
			if strings.TrimSpace(current.body) == terminator {
				inHeredoc = false
				terminator = ""
			}
			continue
		}
		result[index] = true
		match := heredocStart.FindStringSubmatch(current.body)
		if match != nil {
			inHeredoc = true
			terminator = match[1]
		}
	}
	if inHeredoc {
		return nil, fmt.Errorf("inspect Formula: unterminated %s heredoc", terminator)
	}
	return result, nil
}

func splitLines(data []byte) []line {
	result := make([]line, 0, bytes.Count(data, []byte("\n"))+1)
	for start := 0; start < len(data); {
		relativeEnd := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if relativeEnd >= 0 {
			end = start + relativeEnd + 1
		}
		bodyEnd := end
		if bodyEnd > start && data[bodyEnd-1] == '\n' {
			bodyEnd--
		}
		if bodyEnd > start && data[bodyEnd-1] == '\r' {
			bodyEnd--
		}
		result = append(result, line{start: start, end: end, body: string(data[start:bodyEnd])})
		start = end
	}
	return result
}

func quotedValue(current line, pattern *regexp.Regexp, label string) (valueSpan, error) {
	matches := pattern.FindStringSubmatchIndex(current.body)
	if matches == nil {
		return valueSpan{}, fmt.Errorf("inspect Formula: expected %s immediately in architecture block", label)
	}
	return valueSpan{
		start: current.start + matches[2],
		end:   current.start + matches[3],
		value: current.body[matches[2]:matches[3]],
	}, nil
}

func canonicalURLVersion(value string, project manifest.Manifest, asset string) (string, error) {
	prefix := "https://github.com/" + project.RepositorySlug() + "/releases/download/v"
	suffix := "/" + asset
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return "", fmt.Errorf("must be the canonical %s release URL for asset %s", project.RepositorySlug(), asset)
	}
	version := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	if err := manifest.ValidateStableVersion(version); err != nil {
		return "", err
	}
	if value != releaseURL(project, version, asset) {
		return "", errors.New("URL is not canonical")
	}
	return version, nil
}

func replaceValues(original []byte, replacements []replacement) []byte {
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	result := append([]byte(nil), original...)
	for _, replacement := range replacements {
		updated := make([]byte, 0, len(result)-(replacement.end-replacement.start)+len(replacement.value))
		updated = append(updated, result[:replacement.start]...)
		updated = append(updated, replacement.value...)
		updated = append(updated, result[replacement.end:]...)
		result = updated
	}
	return result
}
