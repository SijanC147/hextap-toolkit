package rollback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

var (
	metadataDirective = regexp.MustCompile(`^(\s*)(version|url|sha256)\s+`)
	versionSchemeLine = regexp.MustCompile(`^  version_scheme\s+([0-9]+)\s*$`)
	formulaBoundary   = regexp.MustCompile(`^  (?:def install|service do|def caveats|test do|resource\s+)`)
	caskBoundary      = regexp.MustCompile(`^  (?:app|binary|pkg|installer|artifact|suite|qlplugin|prefpane|font|audio_unit_plugin|vst_plugin|vst3_plugin|screen_saver|dictionary|colorpicker|input_method|internet_plugin|keyboard_layout|mdimporter)\s+`)
	profileToken      = regexp.MustCompile(`@[^@\t \r\n]+@`)
	quotedURLLine     = regexp.MustCompile(`^\s*url "([^"]+)"\s*$`)
	quotedSHALine     = regexp.MustCompile(`^\s*sha256 "([0-9a-f]{64})"\s*$`)
)

var requiredProfileTokens = []string{"@ARM64_URL@", "@ARM64_SHA256@", "@AMD64_URL@", "@AMD64_SHA256@"}

type directive struct {
	start  int
	indent string
	name   string
	lines  []string
}

func reconcileRemoteDefinition(kind Kind, current, historical []byte, currentScheme int) ([]byte, int, error) {
	if !bytes.HasSuffix(current, []byte("\n")) || !bytes.HasSuffix(historical, []byte("\n")) {
		return nil, 0, fmt.Errorf("definitions must end with a newline")
	}
	currentLines := strings.Split(strings.TrimSuffix(string(current), "\n"), "\n")
	historicalLines := strings.Split(strings.TrimSuffix(string(historical), "\n"), "\n")
	currentBoundary := definitionBoundary(kind, currentLines)
	historicalBoundary := definitionBoundary(kind, historicalLines)
	if currentBoundary <= 0 || historicalBoundary <= 0 {
		return nil, 0, fmt.Errorf("canonical runtime boundary is not recognizable")
	}
	currentDirectives := releaseDirectives(currentLines[:currentBoundary])
	historicalDirectives := releaseDirectives(historicalLines[:historicalBoundary])
	if len(currentDirectives) == 0 || len(currentDirectives) != len(historicalDirectives) {
		return nil, 0, fmt.Errorf("release metadata directive counts differ")
	}
	for index := range currentDirectives {
		currentDirective := currentDirectives[index]
		historicalDirective := historicalDirectives[index]
		if currentDirective.name != historicalDirective.name || currentDirective.indent != historicalDirective.indent || len(currentDirective.lines) != len(historicalDirective.lines) {
			return nil, 0, fmt.Errorf("release metadata structure differs at directive %d", index+1)
		}
		for lineIndex := range currentDirective.lines {
			currentIndent := leadingWhitespace(currentDirective.lines[lineIndex])
			historicalIndent := leadingWhitespace(historicalDirective.lines[lineIndex])
			if currentIndent != historicalIndent {
				return nil, 0, fmt.Errorf("release metadata continuation structure differs at directive %d", index+1)
			}
			currentLines[currentDirective.start+lineIndex] = currentIndent + strings.TrimSpace(historicalDirective.lines[lineIndex])
		}
	}

	plannedScheme := 0
	if kind == FormulaKind {
		plannedScheme = currentScheme + 1
		schemeIndex := -1
		for index, line := range currentLines[:currentBoundary] {
			match := versionSchemeLine.FindStringSubmatch(line)
			if len(match) == 0 {
				continue
			}
			if schemeIndex != -1 {
				return nil, 0, fmt.Errorf("Formula contains multiple version_scheme directives")
			}
			value, err := strconv.Atoi(match[1])
			if err != nil || value != currentScheme {
				return nil, 0, fmt.Errorf("Formula version_scheme does not match Homebrew state")
			}
			schemeIndex = index
		}
		if schemeIndex >= 0 {
			currentLines[schemeIndex] = "  version_scheme " + strconv.Itoa(plannedScheme)
		} else {
			if currentScheme != 0 {
				return nil, 0, fmt.Errorf("Homebrew reports a nonzero version_scheme absent from the Formula")
			}
			licenseIndex := -1
			for index, line := range currentLines[:currentBoundary] {
				if strings.HasPrefix(line, "  license ") {
					licenseIndex = index
					break
				}
			}
			if licenseIndex == -1 {
				return nil, 0, fmt.Errorf("Formula lacks a canonical license insertion point")
			}
			currentLines = append(currentLines[:licenseIndex+1], append([]string{"  version_scheme " + strconv.Itoa(plannedScheme)}, currentLines[licenseIndex+1:]...)...)
		}
	}
	updated := []byte(strings.Join(currentLines, "\n") + "\n")
	version, err := versionFromDefinition(kind, updated)
	if err != nil {
		return nil, 0, fmt.Errorf("reconciled definition has no exact version")
	}
	historicalVersion, err := versionFromDefinition(kind, historical)
	if err != nil || version != historicalVersion {
		return nil, 0, fmt.Errorf("reconciled version does not equal the historical selection")
	}
	if bytes.Equal(updated, current) {
		return nil, 0, fmt.Errorf("reconciliation produced no change")
	}
	return updated, plannedScheme, nil
}

func (service Service) reconcileRemoteFiles(kind Kind, name, tapPath, definition string, current, historical []byte, currentScheme int) (map[string][]byte, int, error) {
	if kind == FormulaKind {
		templatePath, template, profile, err := schema2ProfileTemplate(tapPath, name)
		if err != nil {
			return nil, 0, err
		}
		if profile {
			formula, updatedTemplate, scheme, reconcileErr := reconcileProfileFormula(current, historical, template, currentScheme)
			if reconcileErr != nil {
				return nil, 0, reconcileErr
			}
			return map[string][]byte{definition: formula, templatePath: updatedTemplate}, scheme, nil
		}
	}
	updated, scheme, err := reconcileRemoteDefinition(kind, current, historical, currentScheme)
	if err != nil {
		return nil, 0, err
	}
	return map[string][]byte{definition: updated}, scheme, nil
}

func schema2ProfileTemplate(tapPath, name string) (string, []byte, bool, error) {
	registrationRelative := "Projects/" + name + ".json"
	registrationPath := filepath.Join(tapPath, filepath.FromSlash(registrationRelative))
	if _, err := os.Lstat(registrationPath); errors.Is(err, os.ErrNotExist) {
		return "", nil, false, nil
	} else if err != nil {
		return "", nil, false, fmt.Errorf("registered project is unreadable")
	}
	data, err := readRegularContained(tapPath, registrationRelative)
	if err != nil {
		return "", nil, false, fmt.Errorf("registered project is unreadable")
	}
	registration, err := manifest.Parse(data)
	if err != nil || registration.Formula.Name != name {
		return "", nil, false, fmt.Errorf("registered project identity is invalid")
	}
	if registration.Schema == manifest.LegacySchema {
		return "", nil, false, nil
	}
	if registration.Homebrew.FormulaProfile != name {
		return "", nil, false, fmt.Errorf("schema-2 Formula profile must exactly match the package name")
	}
	relative := "packaging/" + name + ".rb.tmpl"
	template, err := readRegularContained(tapPath, relative)
	if err != nil {
		return "", nil, false, fmt.Errorf("schema-2 Formula template is unavailable: %w", err)
	}
	if !bytes.HasSuffix(template, []byte("\n")) {
		return "", nil, false, fmt.Errorf("schema-2 Formula template lacks a final newline")
	}
	return relative, template, true, nil
}

func reconcileProfileFormula(current, historical, template []byte, currentScheme int) ([]byte, []byte, int, error) {
	if err := validateProfileTokens(template); err != nil {
		return nil, nil, 0, err
	}
	currentMetadata, err := profileMetadata(current)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("current profile Formula metadata: %w", err)
	}
	if rendered := renderProfile(template, currentMetadata); !bytes.Equal(rendered, current) {
		return nil, nil, 0, fmt.Errorf("current Formula is not byte-identical to its authoritative template rendering")
	}
	historicalMetadata, err := profileMetadata(historical)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("historical profile Formula metadata: %w", err)
	}
	updatedTemplate, scheme, err := bumpFormulaScheme(template, currentScheme)
	if err != nil {
		return nil, nil, 0, err
	}
	updatedFormula := renderProfile(updatedTemplate, historicalMetadata)
	targetVersion, targetErr := versionFromDefinition(FormulaKind, historical)
	updatedVersion, updatedErr := versionFromDefinition(FormulaKind, updatedFormula)
	if targetErr != nil || updatedErr != nil || updatedVersion != targetVersion {
		return nil, nil, 0, fmt.Errorf("profile Formula rendering did not preserve the historical version")
	}
	return updatedFormula, updatedTemplate, scheme, nil
}

type profileValues struct {
	armURL string
	armSHA string
	amdURL string
	amdSHA string
}

func profileMetadata(formula []byte) (profileValues, error) {
	var urls, checksums []string
	for _, line := range strings.Split(strings.TrimSuffix(string(formula), "\n"), "\n") {
		if match := quotedURLLine.FindStringSubmatch(line); len(match) == 2 {
			urls = append(urls, match[1])
		}
		if match := quotedSHALine.FindStringSubmatch(line); len(match) == 2 {
			checksums = append(checksums, match[1])
		}
	}
	if len(urls) != 2 || len(checksums) != 2 {
		return profileValues{}, fmt.Errorf("expected exactly two literal URL and checksum directives")
	}
	return profileValues{armURL: urls[0], armSHA: checksums[0], amdURL: urls[1], amdSHA: checksums[1]}, nil
}

func validateProfileTokens(template []byte) error {
	matches := profileToken.FindAllString(string(template), -1)
	if len(matches) != len(requiredProfileTokens) {
		return fmt.Errorf("authoritative template must contain exactly four release tokens")
	}
	sort.Strings(matches)
	expected := append([]string(nil), requiredProfileTokens...)
	sort.Strings(expected)
	for index := range expected {
		if matches[index] != expected[index] {
			return fmt.Errorf("authoritative template release tokens are invalid")
		}
	}
	return nil
}

func renderProfile(template []byte, values profileValues) []byte {
	replacer := strings.NewReplacer(
		"@ARM64_URL@", values.armURL,
		"@ARM64_SHA256@", values.armSHA,
		"@AMD64_URL@", values.amdURL,
		"@AMD64_SHA256@", values.amdSHA,
	)
	return []byte(replacer.Replace(string(template)))
}

func bumpFormulaScheme(data []byte, currentScheme int) ([]byte, int, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	boundary := definitionBoundary(FormulaKind, lines)
	if boundary <= 0 {
		return nil, 0, fmt.Errorf("Formula runtime boundary is not recognizable")
	}
	plannedScheme := currentScheme + 1
	schemeIndex := -1
	for index, line := range lines[:boundary] {
		match := versionSchemeLine.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		if schemeIndex != -1 {
			return nil, 0, fmt.Errorf("Formula contains multiple version_scheme directives")
		}
		value, err := strconv.Atoi(match[1])
		if err != nil || value != currentScheme {
			return nil, 0, fmt.Errorf("Formula version_scheme does not match Homebrew state")
		}
		schemeIndex = index
	}
	if schemeIndex >= 0 {
		lines[schemeIndex] = "  version_scheme " + strconv.Itoa(plannedScheme)
	} else {
		if currentScheme != 0 {
			return nil, 0, fmt.Errorf("Homebrew reports a nonzero version_scheme absent from the Formula")
		}
		licenseIndex := -1
		for index, line := range lines[:boundary] {
			if strings.HasPrefix(line, "  license ") {
				licenseIndex = index
				break
			}
		}
		if licenseIndex == -1 {
			return nil, 0, fmt.Errorf("Formula lacks a canonical license insertion point")
		}
		lines = append(lines[:licenseIndex+1], append([]string{"  version_scheme " + strconv.Itoa(plannedScheme)}, lines[licenseIndex+1:]...)...)
	}
	return []byte(strings.Join(lines, "\n") + "\n"), plannedScheme, nil
}

func readRegularContained(root, relative string) ([]byte, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path || !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("path is not a contained regular file")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a contained regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > maximumCommandOutput {
		return nil, fmt.Errorf("file is unreadable, empty, or oversized")
	}
	return data, nil
}

func sortedFileNames(files map[string][]byte) []string {
	result := make([]string, 0, len(files))
	for name := range files {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func definitionBoundary(kind Kind, lines []string) int {
	pattern := formulaBoundary
	if kind == CaskKind {
		pattern = caskBoundary
	}
	for index, line := range lines {
		if pattern.MatchString(line) {
			return index
		}
	}
	return -1
}

func releaseDirectives(lines []string) []directive {
	var result []directive
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		match := metadataDirective.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		end := index + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" && len(leadingWhitespace(lines[end])) > len(match[1]) {
			end++
		}
		result = append(result, directive{start: index, indent: match[1], name: match[2], lines: append([]string(nil), lines[index:end]...)})
		index = end - 1
	}
	return result
}

func leadingWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func (service Service) executeRemote(ctx context.Context, prepared preparedPlan, outcome Outcome) (Outcome, error) {
	if err := service.recheckTap(ctx, prepared, true); err != nil {
		return outcome, err
	}
	root := service.TempRoot
	temporary, err := os.MkdirTemp(root, "hextap-rollback-remote-*")
	if err != nil {
		return outcome, fmt.Errorf("could not create isolated rollback clone")
	}
	defer os.RemoveAll(temporary)
	clonePath := filepath.Join(temporary, "tap")
	runner := service.runner()
	clone := service.command("git", "clone", "--no-local", "--branch", "main", "--single-branch", prepared.remote, clonePath)
	if _, err := runner.Run(ctx, clone); err != nil {
		return outcome, fmt.Errorf("could not clone the owned tap for protected rollback")
	}
	cloneHead, err := runLine(ctx, runner, service.command("git", "-C", clonePath, "rev-parse", "HEAD"))
	if err != nil || cloneHead != prepared.plan.OriginalCommit {
		return outcome, fmt.Errorf("remote main changed after planning; no branch was pushed")
	}
	cloneRemote, err := runLine(ctx, runner, service.command("git", "-C", clonePath, "remote", "get-url", "origin"))
	if err != nil || !service.isOwnedRemote(cloneRemote) {
		return outcome, fmt.Errorf("isolated clone is not the exact owned tap")
	}
	if prepared.plan.Branch == "" || !strings.HasPrefix(prepared.plan.Branch, "codex/hextap-rollback-") || prepared.plan.Branch == "main" {
		return outcome, fmt.Errorf("generated rollback branch is unsafe")
	}
	if _, err := runner.Run(ctx, service.command("git", "-C", clonePath, "checkout", "-b", prepared.plan.Branch)); err != nil {
		return outcome, fmt.Errorf("could not create the isolated rollback feature branch")
	}
	paths := sortedFileNames(prepared.remoteFiles)
	for _, relative := range paths {
		path := filepath.Join(clonePath, filepath.FromSlash(relative))
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return outcome, fmt.Errorf("remote rollback target is not a regular tracked file")
		}
		if err := atomicWrite(path, prepared.remoteFiles[relative], info.Mode().Perm()); err != nil {
			return outcome, fmt.Errorf("could not write reconciled rollback files")
		}
	}
	if _, err := runner.Run(ctx, service.command("git", "-C", clonePath, "diff", "--check")); err != nil {
		return outcome, fmt.Errorf("reconciled rollback definition failed whitespace validation")
	}
	diff, err := runner.Run(ctx, service.command("git", "-C", clonePath, "diff", "--name-only"))
	if err != nil || diff.Stdout != strings.Join(paths, "\n")+"\n" {
		return outcome, fmt.Errorf("remote rollback changed files outside the selected definition")
	}
	addArgs := append([]string{"-C", clonePath, "add", "--"}, paths...)
	if _, err := runner.Run(ctx, service.command("git", addArgs...)); err != nil {
		return outcome, fmt.Errorf("could not stage the rollback definition")
	}
	message := "fix(tap): rollback " + string(prepared.plan.Kind) + " " + prepared.plan.Name + " to " + prepared.plan.TargetVersion
	commit := service.command("git", "-C", clonePath, "-c", "user.name=Hextap", "-c", "user.email=hextap@users.noreply.github.com", "commit", "-m", message)
	if _, err := runner.Run(ctx, commit); err != nil {
		return outcome, fmt.Errorf("could not commit the isolated rollback")
	}
	push := service.command("git", "-C", clonePath, "push", "--set-upstream", "origin", "HEAD:refs/heads/"+prepared.plan.Branch)
	if _, err := runner.Run(ctx, push); err != nil {
		return outcome, fmt.Errorf("feature-branch push failed; main and immutable releases were not changed")
	}
	body := remotePRBody(prepared.plan)
	pr := service.command("gh", "pr", "create", "--repo", "SijanC147/homebrew-hextap", "--base", "main", "--head", prepared.plan.Branch, "--title", message, "--body", body)
	prResult, err := runner.Run(ctx, pr)
	if err != nil {
		return outcome, fmt.Errorf("feature branch %s was pushed but protected pull-request creation failed; no merge or release was attempted", prepared.plan.Branch)
	}
	prURL := strings.TrimSpace(prResult.Stdout)
	if !regexp.MustCompile(`^https://github\.com/SijanC147/homebrew-hextap/pull/[0-9]+$`).MatchString(prURL) {
		return outcome, fmt.Errorf("feature branch %s was pushed but pull-request creation returned an invalid URL; no merge or release was attempted", prepared.plan.Branch)
	}
	outcome.Executed = true
	outcome.PullRequestURL = prURL
	return outcome, nil
}

func remotePRBody(plan Plan) string {
	return strings.Join([]string{
		"## Hextap rollback plan",
		"",
		"- Kind: `" + string(plan.Kind) + "`",
		"- Package: `" + plan.FullName + "`",
		"- Historical tap commit: `" + plan.TargetCommit + "`",
		"- Version: `" + plan.CurrentVersion + "` -> `" + plan.TargetVersion + "`",
		"- Convergence: " + plan.Convergence,
		"",
		"This PR preserves the current canonical service, caveat, test, and artifact structure. It does not move a tag, rewrite a release, force-push, merge, install, or stop a service.",
	}, "\n")
}
