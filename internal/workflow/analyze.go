// Package workflow analyses the GitHub Actions workflow files that sit beside a
// Hextap adopter's release caller and enforces the trigger-exclusivity property
// immutable publication depends on.
//
// Hextap publishes an exact asset set to an immutable release (D-003). That
// guarantee assumes exactly one workflow owns a pushed release tag. A second
// tag-responsive workflow can add, replace or remove assets in the window
// between Hextap's final draft asset-set check and publication, and no ordering
// inside Hextap can close that window from the inside.
//
// The analysis is deliberately strict. Every file is read with the restricted
// YAML reader in this package, and anything the reader cannot represent exactly
// becomes a finding rather than a pass. A checker that guesses is precisely how
// the original defect let a competing workflow through: a quote-sensitive
// substring match for a double-quoted tag filter never matched the
// single-quoted, comment-trailing form an upstream merge introduced.
//
// The exemption granted to the Hextap caller is earned by content, never by
// file name. A file at the caller path that does not call the Hextap release
// workflow is treated exactly like any other competing workflow.
package workflow

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultWorkflowDirectory is the repository-relative directory GitHub loads
// workflows from.
const DefaultWorkflowDirectory = ".github/workflows"

// DefaultCallerFile is the file name Hextap onboarding gives the caller
// workflow. Only a file with this name that also verifies as a real caller is
// exempt from tag exclusivity.
const DefaultCallerFile = "hextap-release.yml"

// hextapReusableWorkflow is the exact external reference an adopter's caller
// must use. Comparing it exactly, rather than by path suffix, is what stops a
// repository that merely publishes a file at the same path from being mistaken
// for the toolkit.
const hextapReusableWorkflow = "SijanC147/hextap-toolkit/.github/workflows/release-go.yml"

// hextapRelativeReusableWorkflow is the toolkit's own same-repository call,
// which is deliberately unlike every adopter's full-SHA pin because it resolves
// inside the commit already under review.
const hextapRelativeReusableWorkflow = "./.github/workflows/release-go.yml"

// Rule names identify why a workflow was refused. They are stable strings so
// that callers and tests can assert on a specific refusal.
const (
	RuleCompetingTagTrigger    = "competing-tag-trigger"
	RuleUnreadableWorkflow     = "unreadable-workflow"
	RuleMissingHextapCaller    = "missing-hextap-caller"
	RuleUnpinnedAction         = "unpinned-action"
	RuleFloatingRuntimeVersion = "floating-runtime-version"
	RuleUnauditedLocalAction   = "unaudited-local-action"
)

// Finding is one refusal. Detail names the exact construct and, where the
// reader produced one, the line it sits on; Remedy states what to change. A
// refusal an adopter cannot diagnose gets worked around, which is worse than no
// check at all.
type Finding struct {
	File   string
	Rule   string
	Detail string
	Remedy string
}

func (finding Finding) String() string {
	return fmt.Sprintf("%s [%s]: %s. %s", finding.File, finding.Rule, finding.Detail, finding.Remedy)
}

// Workflow is one analysed file in the workflow directory.
type Workflow struct {
	// File is the base name as it appears on disk.
	File string
	// Name is the workflow's declared name, which is what a workflow_run
	// trigger in a sibling file refers to.
	Name string
	// Active reports whether GitHub loads the file at all.
	Active bool
	// InactiveReason explains an inactive file, and is empty otherwise.
	InactiveReason string
	// TagTrigger is the classification the exclusivity policy acts on.
	TagTrigger TagTrigger
	// TagPatterns holds the explicit tag filters, when the workflow has any.
	TagPatterns []string
	// Events lists the events declared under the on: key. It is populated only
	// when the document parsed; on a parse failure it is nil and TagTrigger is
	// TagTriggerUnknown. Any later check layered on this field must test for
	// that first, or an unreadable workflow will read as declaring no events.
	Events []string
	// TriggerReason explains the classification in the terms of the file.
	TriggerReason string
	// HextapCaller reports whether a job in this file calls the Hextap
	// reusable release workflow.
	HextapCaller bool
	// ReleaseCapable reports whether the file can reach a release: it either
	// responds to a tag push or grants itself write access to repository
	// contents. Only these files are pin-audited.
	ReleaseCapable bool

	document *node
	watches  []string
}

// Report is the result of analysing one workflow directory.
type Report struct {
	Directory string
	Workflows []Workflow
}

// Analyze reads and classifies every file in the given workflow directory.
func Analyze(directory string) (*Report, error) {
	return analyze(directory, os.ReadDir, os.ReadFile)
}

func analyze(
	directory string,
	readDirectory func(string) ([]fs.DirEntry, error),
	readFile func(string) ([]byte, error),
) (*Report, error) {
	entries, err := readDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("read workflow directory %q: %w", directory, err)
	}

	report := &Report{Directory: directory}
	for _, entry := range entries {
		name := entry.Name()
		if !hasWorkflowExtension(name) {
			report.Workflows = append(report.Workflows, Workflow{
				File:           name,
				InactiveReason: "GitHub loads only .yml and .yaml files, so this file never runs",
			})
			continue
		}
		if !entry.Type().IsRegular() {
			report.Workflows = append(report.Workflows, Workflow{
				File:          name,
				Active:        true,
				TagTrigger:    TagTriggerUnknown,
				TriggerReason: "the workflow path is not a regular file, so its contents cannot be established",
			})
			continue
		}

		path := filepath.Join(directory, name)
		data, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("read workflow %q: %w", path, err)
		}

		workflow := Workflow{File: name, Active: true}
		document, parseErr := parseWorkflowDocument(string(data))
		if parseErr != nil {
			workflow.TagTrigger = TagTriggerUnknown
			workflow.TriggerReason = parseErr.Error()
			report.Workflows = append(report.Workflows, workflow)
			continue
		}

		name, readable := declaredName(document)
		if !readable {
			workflow.TagTrigger = TagTriggerUnknown
			workflow.TriggerReason = "the workflow name is not a readable scalar, so a sibling workflow_run trigger cannot be matched against it"
			report.Workflows = append(report.Workflows, workflow)
			continue
		}

		analysis := analyzeTriggers(document)
		workflow.document = document
		workflow.Name = name
		workflow.TagTrigger = analysis.trigger
		workflow.TriggerReason = analysis.reason
		workflow.Events = analysis.events
		workflow.TagPatterns = analysis.patterns
		workflow.watches = analysis.watches
		workflow.HextapCaller = isHextapCaller(document)
		workflow.ReleaseCapable = workflow.TagTrigger.RespondsToTagPush() || grantsContentsWrite(document)
		report.Workflows = append(report.Workflows, workflow)
	}

	sort.Slice(report.Workflows, func(first, second int) bool {
		return report.Workflows[first].File < report.Workflows[second].File
	})
	resolveWorkflowRunChains(report)
	return report, nil
}

// hasWorkflowExtension reports whether GitHub loads the file as a workflow. The
// comparison is deliberately case-insensitive. Whether GitHub's loader accepts
// an uppercased extension is not established here, and the two possible errors
// are not symmetric: matching too strictly would treat a live workflow as inert,
// which under-reports and fails open, while matching too loosely can only
// over-report an inert file, which fails safe.
func hasWorkflowExtension(name string) bool {
	lowered := strings.ToLower(name)
	return strings.HasSuffix(lowered, ".yml") || strings.HasSuffix(lowered, ".yaml")
}

// declaredName reads the workflow's name. The second result is false when a
// name is present but cannot be represented exactly. A name that is only
// approximately known cannot be matched against a sibling workflow_run trigger,
// and treating it as absent would let a chained workflow escape the policy.
func declaredName(document *node) (string, bool) {
	if !document.has("name") {
		return "", true
	}
	name := document.child("name")
	if name == nil || name.kind != nodeScalar || name.style == scalarBlock {
		return "", false
	}
	return name.value, true
}

// triggerNames lists every name by which a sibling workflow_run trigger can
// refer to this workflow. GitHub falls back to the file path when a workflow
// declares no name, so both forms have to resolve.
func (workflow Workflow) triggerNames() []string {
	names := make([]string, 0, 2)
	if workflow.Name != "" {
		names = append(names, workflow.Name)
	}
	return append(names, DefaultWorkflowDirectory+"/"+workflow.File)
}

// resolveWorkflowRunChains upgrades every workflow_run trigger that chains from
// a workflow which itself responds to a tag push. It runs to a fixed point so
// that a chain of chained workflows is resolved rather than only its first
// link. Classifications only ever escalate, so the loop terminates.
func resolveWorkflowRunChains(report *Report) {
	for changed := true; changed; {
		changed = false
		responders := make(map[string]bool)
		for _, workflow := range report.Workflows {
			if !workflow.Active || !workflow.TagTrigger.RespondsToTagPush() {
				continue
			}
			for _, name := range workflow.triggerNames() {
				responders[name] = true
			}
		}
		for index := range report.Workflows {
			workflow := &report.Workflows[index]
			if !workflow.Active || len(workflow.watches) == 0 || workflow.TagTrigger.RespondsToTagPush() {
				continue
			}
			for _, watched := range workflow.watches {
				if !responders[watched] {
					continue
				}
				workflow.TagTrigger = TagTriggerAny
				workflow.TriggerReason = fmt.Sprintf(
					"workflow_run chains from %q, which itself starts on a tag push", watched)
				workflow.ReleaseCapable = true
				changed = true
				break
			}
		}
	}
}

// isHextapCaller reports whether the document is nothing but a call into the
// Hextap reusable release workflow. This is what earns the caller exemption:
// the file name alone never does, so a hostile file named hextap-release.yml is
// refused like any other competing workflow.
//
// Every job must be such a call, not merely one of them. The exemption is
// granted to the whole file, so a caller carrying the genuine release job plus a
// second job that uploads an asset of its own would otherwise be waved through
// while recreating the exact race this package exists to remove.
func isHextapCaller(document *node) bool {
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping || len(jobs.keys) == 0 {
		return false
	}
	for _, key := range jobs.keys {
		job := jobs.values[key]
		if job == nil || job.kind != nodeMapping {
			return false
		}
		uses := job.child("uses")
		if uses == nil || uses.kind != nodeScalar || uses.style == scalarBlock {
			return false
		}
		if !referencesHextapReleaseWorkflow(uses.value) {
			return false
		}
	}
	return true
}

// referencesHextapReleaseWorkflow reports whether a uses: value is the Hextap
// reusable release workflow itself. The external form is compared exactly and
// must carry a full commit SHA: a suffix match would accept any repository that
// happens to publish a file at the same path, which would hand the tag-trigger
// exemption to attacker-controlled workflow code.
func referencesHextapReleaseWorkflow(reference string) bool {
	reference = strings.TrimSpace(reference)
	if reference == hextapRelativeReusableWorkflow {
		return true
	}
	at := strings.LastIndex(reference, "@")
	if at < 0 {
		return false
	}
	return reference[:at] == hextapReusableWorkflow && isHexadecimal(reference[at+1:], 40)
}

// grantsContentsWrite reports whether the workflow or any of its jobs asks for
// write access to repository contents, which is what a competing workflow needs
// in order to alter a release.
func grantsContentsWrite(document *node) bool {
	if permissionsGrantContentsWrite(document.child("permissions")) {
		return true
	}
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping {
		return false
	}
	for _, key := range jobs.keys {
		job := jobs.values[key]
		if job == nil || job.kind != nodeMapping {
			continue
		}
		if permissionsGrantContentsWrite(job.child("permissions")) {
			return true
		}
	}
	return false
}

func permissionsGrantContentsWrite(permissions *node) bool {
	if permissions == nil {
		return false
	}
	if permissions.kind == nodeScalar {
		return permissions.style != scalarBlock && permissions.value == "write-all"
	}
	if permissions.kind != nodeMapping {
		return false
	}
	contents := permissions.child("contents")
	return contents != nil && contents.kind == nodeScalar &&
		contents.style != scalarBlock && contents.value == "write"
}

// TagExclusivityFindings reports every active workflow, other than the verified
// Hextap caller, that can start automatically from a tag push. A file the
// reader could not understand is reported too: it has not been shown to be
// safe, and treating silence as safety is the defect this replaces.
//
// callerFile selects the caller by base name; an empty value selects
// DefaultCallerFile.
func (report *Report) TagExclusivityFindings(callerFile string) []Finding {
	callerFile = resolveCallerFile(callerFile)

	var findings []Finding
	for _, workflow := range report.Workflows {
		if !workflow.Active || workflow.TagTrigger == TagTriggerNone {
			continue
		}
		if workflow.File == callerFile && workflow.HextapCaller {
			continue
		}
		if workflow.TagTrigger == TagTriggerUnknown {
			findings = append(findings, Finding{
				File:   workflow.File,
				Rule:   RuleUnreadableWorkflow,
				Detail: fmt.Sprintf("the file could not be read closely enough to prove it never starts on a tag push: %s", workflow.TriggerReason),
				Remedy: "rewrite the named construct within the plain block YAML subset the analyser accepts, or rename the file to end in .disabled so GitHub stops loading it",
			})
			continue
		}

		detail := fmt.Sprintf("an active workflow other than %s starts on a tag push: %s", callerFile, workflow.TriggerReason)
		if workflow.File == callerFile {
			detail = fmt.Sprintf(
				"%s sits at the Hextap caller path but no job of it calls %s, so it earns no exemption: %s",
				workflow.File, hextapReusableWorkflow, workflow.TriggerReason)
		}
		findings = append(findings, Finding{
			File:   workflow.File,
			Rule:   RuleCompetingTagTrigger,
			Detail: detail,
			Remedy: fmt.Sprintf("restrict its push trigger to branches, or rename it to end in .disabled; only %s may start from a tag push while Hextap publishes an immutable release", callerFile),
		})
	}
	return findings
}

// PinFindings audits action references and runtime version inputs in every
// release-capable workflow. A reference resolved through an expression is
// accepted because it cannot be judged statically; that is an honest limit of a
// source-only audit, not a proof of pinning.
func (report *Report) PinFindings() []Finding {
	var findings []Finding
	for _, workflow := range report.Workflows {
		if !workflow.Active || !workflow.ReleaseCapable || workflow.document == nil {
			continue
		}
		findings = append(findings, auditPins(workflow.File, workflow.document)...)
	}
	return findings
}

// PreflightFindings is the complete refusal set for a tagged source tree: tag
// exclusivity, the presence of a verified caller, and the pin audit.
func (report *Report) PreflightFindings(callerFile string) []Finding {
	callerFile = resolveCallerFile(callerFile)

	findings := report.TagExclusivityFindings(callerFile)
	if !report.hasVerifiedCaller(callerFile) {
		findings = append(findings, Finding{
			File:   callerFile,
			Rule:   RuleMissingHextapCaller,
			Detail: fmt.Sprintf("no active workflow named %s calls %s, so no workflow in this source tree is the recognised owner of the pushed tag", callerFile, hextapReusableWorkflow),
			Remedy: "restore the Hextap caller workflow generated by onboarding before publishing from this source",
		})
	}
	return append(findings, report.PinFindings()...)
}

// Preflight is the check the reusable release workflow runs against a tagged
// source tree before publishing.
//
// It refuses to publish under an ambiguous workflow surface. It honestly cannot
// prevent a competing workflow from starting — by the time the tag exists, that
// workflow has already been dispatched. What it removes is Hextap's own
// contribution to the race: Hextap will not publish an immutable release into a
// repository where something else also claims the tag.
func Preflight(directory, callerFile string) ([]Finding, error) {
	report, err := Analyze(directory)
	if err != nil {
		return nil, err
	}
	return report.PreflightFindings(callerFile), nil
}

func (report *Report) hasVerifiedCaller(callerFile string) bool {
	for _, workflow := range report.Workflows {
		if workflow.Active && workflow.File == callerFile && workflow.HextapCaller {
			return true
		}
	}
	return false
}

func resolveCallerFile(callerFile string) string {
	if callerFile == "" {
		return DefaultCallerFile
	}
	return filepath.Base(callerFile)
}

// auditPins walks every action reference and runtime version input in one
// document. Walking the whole tree rather than only known step lists means a
// reference introduced somewhere unexpected is still audited.
func auditPins(file string, document *node) []Finding {
	var findings []Finding
	walkNodes(document, func(current *node) {
		if current.kind != nodeMapping || !current.has("uses") {
			return
		}
		findings = append(findings, auditActionReference(file, current.child("uses"))...)
		findings = append(findings, auditRuntimeVersions(file, current.child("with"))...)
	})
	return findings
}

func walkNodes(current *node, visit func(*node)) {
	if current == nil {
		return
	}
	visit(current)
	switch current.kind {
	case nodeSequence:
		for _, item := range current.items {
			walkNodes(item, visit)
		}
	case nodeMapping:
		for _, key := range current.keys {
			walkNodes(current.values[key], visit)
		}
	}
}

func auditActionReference(file string, uses *node) []Finding {
	unpinned := func(detail, remedy string) []Finding {
		return []Finding{{File: file, Rule: RuleUnpinnedAction, Detail: detail, Remedy: remedy}}
	}
	if uses == nil || uses.kind != nodeScalar || uses.style == scalarBlock {
		return unpinned(
			"a uses: reference is not a readable literal",
			"write the reference as a plain or quoted scalar pinned to a full 40-character commit SHA")
	}

	reference := strings.TrimSpace(uses.value)
	switch {
	case strings.HasPrefix(reference, "./"):
		if strings.HasPrefix(reference, "./"+DefaultWorkflowDirectory+"/") {
			// A reusable workflow in the analysed directory resolves inside the
			// commit under review and is audited in its own right by this pass.
			return nil
		}
		return []Finding{{
			File:   file,
			Rule:   RuleUnauditedLocalAction,
			Detail: fmt.Sprintf("the local action %q resolves inside the reviewed commit, but the external references in its own definition live outside the workflow directory and are not audited here", reference),
			Remedy: "pin every external reference inside the local action definition to a full commit SHA, or inline the step so its dependencies are visible to this audit",
		}}
	case strings.Contains(reference, "${{"):
		return unpinned(
			fmt.Sprintf("the action reference %q resolves through an expression, so no fixed revision can be established from the source", reference),
			"replace the expression with a literal reference pinned to a full 40-character commit SHA")
	case strings.HasPrefix(reference, "docker://"):
		if digest := strings.LastIndex(reference, "@sha256:"); digest >= 0 && isHexadecimal(reference[digest+len("@sha256:"):], 64) {
			return nil
		}
		return unpinned(
			fmt.Sprintf("the container reference %q carries no image digest", reference),
			"pin the container to an immutable @sha256: digest")
	}

	at := strings.LastIndex(reference, "@")
	if at < 0 {
		return unpinned(
			fmt.Sprintf("the action reference %q carries no revision at all", reference),
			"pin the action to a full 40-character commit SHA, keeping the version tag as a trailing comment")
	}
	if revision := reference[at+1:]; !isHexadecimal(revision, 40) {
		return unpinned(
			fmt.Sprintf("the action reference %q is pinned to the mutable revision %q rather than a full commit SHA", reference, revision),
			"pin the action to a full 40-character commit SHA, keeping the version tag as a trailing comment")
	}
	return nil
}

func auditRuntimeVersions(file string, with *node) []Finding {
	if with == nil || with.kind != nodeMapping {
		return nil
	}
	var findings []Finding
	for _, key := range with.keys {
		if key != "version" && !strings.HasSuffix(key, "-version") {
			continue
		}
		value := with.values[key]
		if value == nil || value.kind != nodeScalar || value.style == scalarBlock {
			findings = append(findings, Finding{
				File:   file,
				Rule:   RuleFloatingRuntimeVersion,
				Detail: fmt.Sprintf("the runtime input %q is not a readable literal", key),
				Remedy: "give the runtime an exact version as a plain or quoted scalar",
			})
			continue
		}
		text := strings.TrimSpace(value.value)
		if strings.Contains(text, "${{") {
			// Resolved by the workflow at run time from a value this audit
			// cannot see. Reported as accepted rather than proven pinned.
			continue
		}
		if isFloatingVersion(text) {
			findings = append(findings, Finding{
				File:   file,
				Rule:   RuleFloatingRuntimeVersion,
				Detail: fmt.Sprintf("the runtime input %q is set to the floating value %q, so the same tag can build against different toolchains", key, text),
				Remedy: fmt.Sprintf("set %s to an exact version", key),
			})
		}
	}
	return findings
}

// floatingVersionAliases are the runtime selectors that resolve to whatever is
// current at the moment the job runs.
var floatingVersionAliases = map[string]struct{}{
	"":        {},
	"*":       {},
	"canary":  {},
	"current": {},
	"dev":     {},
	"edge":    {},
	"head":    {},
	"latest":  {},
	"lts":     {},
	"main":    {},
	"master":  {},
	"newest":  {},
	"nightly": {},
	"node":    {},
	"stable":  {},
}

// isFloatingVersion reports whether a runtime selector can resolve to different
// releases over time. A bare major or major.minor selector is a range rather
// than a pin: node-version "22" selects whichever 22.x is current when the job
// runs, so the same tag can build against a different toolchain later.
func isFloatingVersion(version string) bool {
	lowered := strings.ToLower(strings.TrimSpace(version))
	if _, floating := floatingVersionAliases[lowered]; floating {
		return true
	}
	if strings.ContainsAny(lowered, "^~><*") {
		return true
	}
	if strings.HasPrefix(lowered, "lts/") || strings.HasSuffix(lowered, ".x") {
		return true
	}
	return !isExactVersion(lowered)
}

// isExactVersion requires a complete major.minor.patch, optionally prefixed
// with v and optionally carrying a prerelease or build suffix.
func isExactVersion(version string) bool {
	version = strings.TrimPrefix(version, "v")
	if index := strings.IndexAny(version, "-+"); index >= 0 {
		version = version[:index]
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for index := 0; index < len(part); index++ {
			if part[index] < '0' || part[index] > '9' {
				return false
			}
		}
	}
	return true
}

func isHexadecimal(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
