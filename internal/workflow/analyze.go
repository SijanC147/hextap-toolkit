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
	RuleUnpinnedContainer      = "unpinned-container-image"
	RuleMutableSourceRef       = "mutable-source-ref"
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
	// HextapCaller reports whether every job in this file calls the Hextap
	// reusable release workflow by its exact external reference, pinned to a
	// full commit SHA. This is the only form an adopter may use.
	HextapCaller bool
	// SelfCaller reports whether every job in this file calls the reusable
	// release workflow through the relative self-call the toolkit's own
	// release uses. A directory cannot show which repository it belongs to,
	// so this form earns the exemption only under Policy.SelfRelease.
	SelfCaller bool
	// ReleaseCapable reports whether the file can reach a release. It is true
	// unless the workflow is proven unable to: the GITHUB_TOKEN scopes that
	// reach a release are explicitly withheld, no job passes secrets on, and
	// no expression reads a secret other than GITHUB_TOKEN. Absent permissions
	// are not proof, because the effective default is a repository setting
	// this analysis cannot see; a read-only token is not proof either, because
	// a secret can hold a token the permissions block never bounds. Only these
	// files are pin-audited.
	ReleaseCapable bool

	document *node
	watches  []string
	// chained records that the classification came from a workflow_run
	// trigger resolved against a tag responder rather than from the file's
	// own events.
	chained bool
}

// Report is the result of analysing one workflow directory.
type Report struct {
	Directory string
	Workflows []Workflow
}

// Policy states what the caller of the analysis knows that the files cannot
// tell it. Every field defaults to the strict reading.
type Policy struct {
	// CallerFile selects the Hextap caller by base name. Empty selects
	// DefaultCallerFile.
	CallerFile string
	// SelfRelease states that the analysed repository is the toolkit itself,
	// the only repository whose caller may use the relative self-call
	// ./.github/workflows/release-go.yml. A directory cannot show which
	// repository it belongs to, so whoever calls the analysis must state it
	// from evidence of their own: the reusable release workflow can take it
	// from github.repository once it calls this package (SB23-831), and an
	// adopter-side check leaves it false, under which an adopter-authored
	// release-go.yml at that path earns nothing.
	SelfRelease bool
	// DefaultBranchDirectory is the workflow directory of the repository's
	// default branch. GitHub runs a workflow_run workflow from the default
	// branch's definition, and only when the file exists there, so a chained
	// publisher can live on the default branch and nowhere in the tagged
	// tree, and the tagged copy of one can differ from what actually runs.
	// Preflight requires this field; pass the analysed directory itself when
	// the tagged tree is the default branch.
	DefaultBranchDirectory string
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
		if entry.IsDir() {
			// GitHub loads workflow files, and a directory has no contents to
			// parse whatever it is named.
			report.Workflows = append(report.Workflows, Workflow{
				File:           name,
				InactiveReason: "GitHub loads only files, so a directory never runs whatever its name",
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
		workflow.HextapCaller, workflow.SelfCaller = callerForms(document)
		workflow.ReleaseCapable = workflow.TagTrigger.RespondsToTagPush() || !provablyUnableToRelease(document)
		report.Workflows = append(report.Workflows, workflow)
	}

	sort.Slice(report.Workflows, func(first, second int) bool {
		return report.Workflows[first].File < report.Workflows[second].File
	})
	resolveWorkflowRunChains(report, nil)
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
// link. Classifications only ever escalate, so the loop terminates. external
// supplies tag responders from another tree: the default branch's chained
// workflows fire off the runs the tagged tree's workflows produce.
func resolveWorkflowRunChains(report *Report, external []Workflow) {
	for changed := true; changed; {
		changed = false
		responders := make(map[string]bool)
		for _, tree := range [][]Workflow{report.Workflows, external} {
			for _, workflow := range tree {
				if !workflow.Active || !workflow.TagTrigger.RespondsToTagPush() {
					continue
				}
				for _, name := range workflow.triggerNames() {
					responders[name] = true
				}
			}
		}
		for index := range report.Workflows {
			workflow := &report.Workflows[index]
			if !workflow.Active || len(workflow.watches) == 0 || workflow.chained {
				continue
			}
			for _, watched := range workflow.watches {
				if !responders[watched] {
					continue
				}
				reason := fmt.Sprintf("workflow_run chains from %q, which itself starts on a tag push", watched)
				// A workflow that already responds through its own events
				// keeps that classification; the chain is recorded alongside
				// it, because on the default branch the chain is the only
				// route by which the file runs at all.
				if workflow.TagTrigger.RespondsToTagPush() {
					workflow.TriggerReason += "; " + reason
				} else {
					workflow.TagTrigger = TagTriggerAny
					workflow.TriggerReason = reason
				}
				workflow.ReleaseCapable = true
				workflow.chained = true
				changed = true
				break
			}
		}
	}
}

// callerForms reports whether the document is nothing but a call into the
// Hextap reusable release workflow, and in which form. This is what earns the
// caller exemption: the file name alone never does, so a hostile file named
// hextap-release.yml is refused like any other competing workflow.
//
// Every job must be such a call, not merely one of them, and every job must
// use the same form. The exemption is granted to the whole file, so a caller
// carrying the genuine release job plus a second job that uploads an asset of
// its own would otherwise be waved through while recreating the exact race
// this package exists to remove.
func callerForms(document *node) (external, relative bool) {
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping || len(jobs.keys) == 0 {
		return false, false
	}
	external, relative = true, true
	for _, key := range jobs.keys {
		job := jobs.values[key]
		if job == nil || job.kind != nodeMapping {
			return false, false
		}
		uses := job.child("uses")
		if uses == nil || uses.kind != nodeScalar || uses.style == scalarBlock {
			return false, false
		}
		reference := strings.TrimSpace(uses.value)
		external = external && isExternalHextapReference(reference)
		relative = relative && reference == hextapRelativeReusableWorkflow
	}
	return external, relative
}

// isExternalHextapReference reports whether a uses: value is the Hextap
// reusable release workflow by its exact external reference. It is compared
// exactly and must carry a full commit SHA: a suffix match would accept any
// repository that happens to publish a file at the same path, which would hand
// the tag-trigger exemption to attacker-controlled workflow code.
func isExternalHextapReference(reference string) bool {
	at := strings.LastIndex(reference, "@")
	if at < 0 {
		return false
	}
	return reference[:at] == hextapReusableWorkflow && isHexadecimal(reference[at+1:], 40)
}

// provablyUnableToRelease reports whether the document demonstrably cannot
// reach a release: nothing in it holds a credential that could create or
// modify one.
//
// Absent permissions are not proof. The effective default for GITHUB_TOKEN is a
// repository setting this directory-only analysis cannot see, and in a
// repository still configured for read/write a workflow with no permissions
// block can modify a release.
//
// An explicit permissions block is not proof on its own either, because it
// bounds only GITHUB_TOKEN. A workflow whose token is read-only can still
// publish with a personal access token held in a secret, can exchange an OIDC
// id-token for credentials this analysis never sees, and can start a
// release-capable sibling through actions: write. A token can also arrive as
// plain data: a workflow_call or workflow_dispatch input, or a dispatch
// payload, is a string the workflow cannot tell from a credential. Only a
// workflow that grants none of those scopes, passes no secrets to a called
// workflow, and reads neither the secrets context nor any of those input
// channels is exempt from the pin audit. A workflow_call callee is never
// exempt: it runs under whatever its caller grants, and this pass judges one
// file at a time rather than composing grants across files. Everything else is
// audited, which over-reports rather than certifying a mutable release path as
// pinned.
func provablyUnableToRelease(document *node) bool {
	permissions := document.child("permissions")
	if permissions == nil || grantsReleaseScope(permissions) {
		return false
	}
	if events, _, err := triggerEvents(document.child("on")); err == nil {
		for _, event := range events {
			if event == "workflow_call" {
				return false
			}
		}
	}
	if jobs := document.child("jobs"); jobs != nil && jobs.kind == nodeMapping {
		for _, key := range jobs.keys {
			job := jobs.values[key]
			if job == nil || job.kind != nodeMapping {
				continue
			}
			// A job may escalate beyond the workflow-level default, and a job
			// that calls a reusable workflow may hand it secrets.
			if jobPermissions := job.child("permissions"); jobPermissions != nil && grantsReleaseScope(jobPermissions) {
				return false
			}
			if job.has("secrets") {
				return false
			}
		}
	}
	return !readsCredentialChannel(document)
}

// releaseScopes are the GITHUB_TOKEN scopes through which a workflow can reach
// a release: contents writes it directly, actions can dispatch a workflow that
// does, and id-token can be exchanged for credentials outside this analysis.
var releaseScopes = map[string]struct{}{
	"actions":  {},
	"contents": {},
	"id-token": {},
}

// grantsReleaseScope reports whether an explicit permissions block leaves any
// release-reaching scope open. A mapping that omits a scope grants it nothing,
// which is a restriction; the read-all shorthand restricts every scope. A
// value other than read or none is not assumed to be a restriction.
func grantsReleaseScope(permissions *node) bool {
	if permissions.kind == nodeScalar {
		return permissions.style == scalarBlock || permissions.value != "read-all"
	}
	if permissions.kind != nodeMapping {
		return true
	}
	for _, scope := range permissions.keys {
		if _, reaches := releaseScopes[scope]; !reaches {
			continue
		}
		grant := permissions.values[scope]
		if grant == nil || grant.kind != nodeScalar || grant.style == scalarBlock {
			return true
		}
		if grant.value != "read" && grant.value != "none" {
			return true
		}
	}
	return false
}

// readsCredentialChannel reports whether any expression in the document reads
// a value that can carry a credential this analysis cannot see: the secrets
// context for anything other than GITHUB_TOKEN, or one of the input channels
// a caller or dispatcher fills. A single such read anywhere in the file makes
// the workflow release capable. GITHUB_TOKEN is the one exception, because the
// permissions block bounds it.
func readsCredentialChannel(document *node) bool {
	return anyScalar(document, func(value string) bool {
		for _, expression := range expressions(value) {
			if expressionReadsCredentialChannel(expression) {
				return true
			}
		}
		return false
	})
}

// anyScalar reports whether any scalar value in the tree satisfies the
// predicate, stopping at the first that does.
func anyScalar(current *node, predicate func(string) bool) bool {
	if current == nil {
		return false
	}
	switch current.kind {
	case nodeScalar:
		return predicate(current.value)
	case nodeSequence:
		for _, item := range current.items {
			if anyScalar(item, predicate) {
				return true
			}
		}
	case nodeMapping:
		for _, key := range current.keys {
			if anyScalar(current.values[key], predicate) {
				return true
			}
		}
	}
	return false
}

// expressions returns the body of every ${{ }} expression in a value. An
// unterminated expression is returned as far as it goes: for a caller deciding
// safety it is still an expression.
func expressions(text string) []string {
	var found []string
	for {
		start := strings.Index(text, "${{")
		if start < 0 {
			return found
		}
		rest := text[start+len("${{"):]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return append(found, rest)
		}
		found = append(found, rest[:end])
		text = rest[end+len("}}"):]
	}
}

// payloadChannels are the paths under the github context that carry values
// the triggering party supplies as free-form data: dispatch inputs, a
// repository_dispatch client payload, and a deployment payload. Every other
// event field is structured data GitHub itself produced.
var payloadChannels = []string{"event.inputs", "event.client_payload", "event.deployment.payload"}

// expressionReadsCredentialChannel reports whether an expression body reads the
// secrets context for anything but GITHUB_TOKEN, the inputs context, or a
// payload channel under the github context. Each context is matched as a whole
// identifier that is not itself a property, so needs.build.outputs.secrets is
// not mistaken for one, while toJSON(secrets), secrets[name] and
// github['event']['inputs'] count as reads because they reach the whole
// channel at once.
func expressionReadsCredentialChannel(expression string) bool {
	for start := 0; start < len(expression); {
		end := start
		for end < len(expression) && isIdentifierByte(expression[end]) {
			end++
		}
		if end == start {
			start++
			continue
		}
		word := expression[start:end]
		remainder := expression[end:]
		property := start > 0 && expression[start-1] == '.'
		switch {
		case property:
		case strings.EqualFold(word, "inputs"):
			return true
		case strings.EqualFold(word, "secrets"):
			if path := contextPath(remainder); !strings.EqualFold(path, "github_token") {
				return true
			}
		case strings.EqualFold(word, "github"):
			path := strings.ToLower(contextPath(remainder))
			for _, channel := range payloadChannels {
				if path == channel || strings.HasPrefix(path, channel+".") {
					return true
				}
			}
			if path == "event" || path == "" {
				// The whole event, or the whole context, reaches every
				// payload channel at once.
				return true
			}
		}
		start = end
	}
	return false
}

// contextPath reads the property chain that follows a context name, in either
// dotted or bracketed form, and returns it dot-joined. It stops at the first
// character that continues neither form, so github.event.inputs.token and
// github['event']["inputs"].token both yield event.inputs.token.
func contextPath(remainder string) string {
	var segments []string
	for remainder != "" {
		switch {
		case strings.HasPrefix(remainder, "."):
			segment := identifierPrefix(remainder[1:])
			if segment == "" {
				return strings.Join(segments, ".")
			}
			segments = append(segments, segment)
			remainder = remainder[1+len(segment):]
		case strings.HasPrefix(remainder, "['") || strings.HasPrefix(remainder, "[\""):
			quote := remainder[1]
			closing := strings.IndexByte(remainder[2:], quote)
			if closing < 0 || len(remainder) < 2+closing+2 || remainder[2+closing+1] != ']' {
				return strings.Join(segments, ".")
			}
			segments = append(segments, remainder[2:2+closing])
			remainder = remainder[2+closing+2:]
		default:
			return strings.Join(segments, ".")
		}
	}
	return strings.Join(segments, ".")
}

func identifierPrefix(text string) string {
	end := 0
	for end < len(text) && isIdentifierByte(text[end]) {
		end++
	}
	return text[:end]
}

func isIdentifier(text string) bool {
	return text != "" && identifierPrefix(text) == text
}

func isIdentifierByte(character byte) bool {
	switch {
	case character >= '0' && character <= '9':
	case character >= 'a' && character <= 'z':
	case character >= 'A' && character <= 'Z':
	case character == '_' || character == '-':
	default:
		return false
	}
	return true
}

// isVerifiedCaller reports whether a workflow earns the caller exemption: it
// sits at the caller path, every job of it calls the Hextap reusable release
// workflow in a form the policy accepts, and its trigger is readable and
// starts on a tag push. The last condition is what makes the caller the owner
// of the pushed tag. A pinned caller that starts only from a branch push or a
// manual dispatch calls the right workflow but owns no tag, and a preflight
// that certified such a source tree would certify one in which nothing
// recognised responds to the release tag at all.
func isVerifiedCaller(workflow Workflow, policy Policy) bool {
	if !workflow.Active || workflow.File != resolveCallerFile(policy.CallerFile) {
		return false
	}
	if !workflow.HextapCaller && !(policy.SelfRelease && workflow.SelfCaller) {
		return false
	}
	return workflow.TagTrigger == TagTriggerFiltered || workflow.TagTrigger == TagTriggerAny
}

// TagExclusivityFindings reports every active workflow, other than the verified
// Hextap caller, that can start automatically from a tag push. A file the
// reader could not understand is reported too: it has not been shown to be
// safe, and treating silence as safety is the defect this replaces.
//
// The policy names the caller file and states whether the relative self-call
// form is acceptable.
func (report *Report) TagExclusivityFindings(policy Policy) []Finding {
	callerFile := resolveCallerFile(policy.CallerFile)

	var findings []Finding
	for _, workflow := range report.Workflows {
		if !workflow.Active || workflow.TagTrigger == TagTriggerNone {
			continue
		}
		if isVerifiedCaller(workflow, policy) {
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
		switch {
		case workflow.File == callerFile && workflow.SelfCaller:
			detail = fmt.Sprintf(
				"%s calls the release workflow only through the relative self-call %s, which is the toolkit's own form and earns no exemption in an adopter repository: %s",
				workflow.File, hextapRelativeReusableWorkflow, workflow.TriggerReason)
		case workflow.File == callerFile:
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

// PinFindings audits action references, container images and runtime version
// inputs in every release-capable workflow. Only what a workflow states in its
// own source can be bounded, so a value that arrives through an expression is
// refused, with one exception: the output of a job or step in the same file is
// computed by the tagged source itself and cannot be changed without a new
// commit. That exception is accepted, not proven pinned, and the audit says so.
func (report *Report) PinFindings() []Finding {
	var findings []Finding
	for _, workflow := range report.Workflows {
		if !workflow.Active || !workflow.ReleaseCapable || workflow.document == nil {
			continue
		}
		findings = append(findings, report.auditPins(workflow.File, workflow.document)...)
	}
	return findings
}

// PreflightFindings is the complete refusal set for a source tree that is
// also the repository's default branch: tag exclusivity, the presence of a
// verified caller that owns the tag, and the pin audit. When the tagged tree
// and the default branch differ, use Preflight, which reads both; a policy
// naming a different default-branch directory is refused here rather than
// ignored, for the same reason Preflight refuses an unset one.
func (report *Report) PreflightFindings(policy Policy) ([]Finding, error) {
	if policy.DefaultBranchDirectory != "" &&
		filepath.Clean(policy.DefaultBranchDirectory) != filepath.Clean(report.Directory) {
		return nil, fmt.Errorf("preflight policy names the default-branch workflow directory %q, which is not the analysed directory %q; use Preflight to read both",
			policy.DefaultBranchDirectory, report.Directory)
	}
	return preflight(report, report, policy), nil
}

// Preflight is the check the reusable release workflow runs against a tagged
// source tree before publishing.
//
// It refuses to publish under an ambiguous workflow surface. It honestly cannot
// prevent a competing workflow from starting — by the time the tag exists, that
// workflow has already been dispatched. What it removes is Hextap's own
// contribution to the race: Hextap will not publish an immutable release into a
// repository where something else also claims the tag.
//
// The policy must name the default branch's workflow directory, because a
// workflow_run workflow runs from the default branch's definition rather than
// the tagged one. Leaving it unset is refused rather than read as "the same
// tree": a preflight that silently looked at one tree would certify exactly
// the surface it never inspected.
func Preflight(directory string, policy Policy) ([]Finding, error) {
	if policy.DefaultBranchDirectory == "" {
		return nil, fmt.Errorf("preflight policy names no default-branch workflow directory; pass %q itself when the tagged tree is the default branch", directory)
	}
	tagged, err := Analyze(directory)
	if err != nil {
		return nil, err
	}
	defaultBranch := tagged
	if filepath.Clean(policy.DefaultBranchDirectory) != filepath.Clean(directory) {
		defaultBranch, err = Analyze(policy.DefaultBranchDirectory)
		if err != nil {
			return nil, err
		}
	}
	return preflight(tagged, defaultBranch, policy), nil
}

// preflight combines the tagged tree, which GitHub reads for push and create
// events, with the default-branch tree, which it reads for workflow_run.
func preflight(tagged, defaultBranch *Report, policy Policy) []Finding {
	callerFile := resolveCallerFile(policy.CallerFile)

	findings := tagged.TagExclusivityFindings(policy)
	if detail, verified := tagged.callerVerification(policy); !verified {
		findings = append(findings, Finding{
			File:   callerFile,
			Rule:   RuleMissingHextapCaller,
			Detail: detail,
			Remedy: "restore the Hextap caller workflow generated by onboarding, including its push tag trigger, before publishing from this source",
		})
	}
	findings = append(findings, tagged.PinFindings()...)
	if defaultBranch != tagged {
		findings = append(findings, defaultBranchFindings(tagged, defaultBranch, findings)...)
	}
	return findings
}

// defaultBranchFindings reports what the default branch contributes to the
// tag's workflow surface. It first resolves workflow_run chains on the
// default-branch report against the tag responders of both trees, which
// mutates that report, then reports every chained workflow there, together
// with the pin audit of it and of every local reusable workflow it reaches,
// and every file there the reader could not understand. A push or create
// workflow that exists only on the default branch is not reported, because
// GitHub reads those from the tagged commit.
//
// A refusal the tagged tree already raised for the same file and rule is not
// raised again; a pin finding is repeated only when its detail differs, so a
// default-branch copy that is worse than the tagged one is still reported.
func defaultBranchFindings(tagged, defaultBranch *Report, raised []Finding) []Finding {
	resolveWorkflowRunChains(defaultBranch, tagged.Workflows)

	key := func(finding Finding) [3]string {
		switch finding.Rule {
		case RuleCompetingTagTrigger, RuleUnreadableWorkflow:
			return [3]string{finding.File, finding.Rule, ""}
		default:
			return [3]string{finding.File, finding.Rule, finding.Detail}
		}
	}
	seen := make(map[[3]string]bool)
	for _, finding := range raised {
		seen[key(finding)] = true
	}
	var findings []Finding
	add := func(finding Finding) {
		if seen[key(finding)] {
			return
		}
		seen[key(finding)] = true
		findings = append(findings, finding)
	}

	audited := make(map[string]bool)
	var auditReachable func(name string)
	auditReachable = func(name string) {
		if audited[name] {
			return
		}
		audited[name] = true
		workflow := defaultBranch.workflow(name)
		if workflow == nil || !workflow.Active || workflow.document == nil {
			return
		}
		for _, finding := range defaultBranch.auditPins(name, workflow.document) {
			add(finding)
		}
		for _, callee := range localReusableCallees(workflow.document) {
			auditReachable(callee)
		}
	}

	for _, workflow := range defaultBranch.Workflows {
		if !workflow.Active {
			continue
		}
		switch {
		case workflow.TagTrigger == TagTriggerUnknown:
			add(Finding{
				File:   workflow.File,
				Rule:   RuleUnreadableWorkflow,
				Detail: fmt.Sprintf("on the default branch, the file could not be read closely enough to prove it never chains from the tag's workflows: %s", workflow.TriggerReason),
				Remedy: "rewrite the named construct within the plain block YAML subset the analyser accepts, or rename the file to end in .disabled so GitHub stops loading it",
			})
		case workflow.chained:
			add(Finding{
				File:   workflow.File,
				Rule:   RuleCompetingTagTrigger,
				Detail: fmt.Sprintf("on the default branch, whose definition GitHub runs for workflow_run, %s", workflow.TriggerReason),
				Remedy: "remove the workflow_run trigger from the default branch's copy, or rename it there to end in .disabled",
			})
			auditReachable(workflow.File)
		}
	}
	return findings
}

// workflow returns the analysed file with the given base name, or nil.
func (report *Report) workflow(name string) *Workflow {
	for index := range report.Workflows {
		if report.Workflows[index].File == name {
			return &report.Workflows[index]
		}
	}
	return nil
}

// localReusableCallees lists the sibling workflow files a document's jobs call
// through the relative form.
func localReusableCallees(document *node) []string {
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping {
		return nil
	}
	var callees []string
	for _, key := range jobs.keys {
		job := jobs.values[key]
		if job == nil || job.kind != nodeMapping {
			continue
		}
		uses := job.child("uses")
		if uses == nil || uses.kind != nodeScalar || uses.style == scalarBlock {
			continue
		}
		if name, local := reusableWorkflowName(strings.TrimSpace(uses.value)); local {
			callees = append(callees, name)
		}
	}
	return callees
}

// callerVerification reports whether the source tree has a verified caller
// and, when it does not, states which condition failed so that the adopter
// can fix the right one.
func (report *Report) callerVerification(policy Policy) (string, bool) {
	callerFile := resolveCallerFile(policy.CallerFile)
	for _, workflow := range report.Workflows {
		if !workflow.Active || workflow.File != callerFile {
			continue
		}
		switch {
		case isVerifiedCaller(workflow, policy):
			return "", true
		case workflow.SelfCaller && !policy.SelfRelease:
			return fmt.Sprintf("%s calls the release workflow only through the relative self-call %s, which is the toolkit's own form; an adopter's caller must use %s pinned to a full commit SHA",
				callerFile, hextapRelativeReusableWorkflow, hextapReusableWorkflow), false
		case !workflow.HextapCaller && !workflow.SelfCaller:
			return fmt.Sprintf("%s does not call %s in every job, so it is not the recognised owner of the pushed tag",
				callerFile, hextapReusableWorkflow), false
		case workflow.TagTrigger == TagTriggerUnknown:
			return fmt.Sprintf("%s calls %s but its trigger could not be read (%s), so it cannot be shown to own the pushed tag",
				callerFile, hextapReusableWorkflow, workflow.TriggerReason), false
		default:
			return fmt.Sprintf("%s calls %s but never starts on a tag push (%s), so no recognised workflow owns the pushed tag",
				callerFile, hextapReusableWorkflow, workflow.TriggerReason), false
		}
	}
	return fmt.Sprintf("no active workflow named %s exists, so no workflow in this source tree is the recognised owner of the pushed tag", callerFile), false
}

func resolveCallerFile(callerFile string) string {
	if callerFile == "" {
		return DefaultCallerFile
	}
	return filepath.Base(callerFile)
}

// auditPins visits the executable positions of one document: every job's
// reusable workflow reference, every step's action reference, and every job or
// service container. Only those positions run code. A key named uses anywhere
// else, such as a workflow_dispatch input or a with: value, is data, and
// auditing it produced refusals against workflows that were fully pinned. A
// job or step list the reader cannot represent is reported rather than skipped.
func (report *Report) auditPins(file string, document *node) []Finding {
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping {
		return []Finding{unreadableForAudit(file, "the jobs: block is not a readable mapping, so nothing in it can be audited")}
	}

	var findings []Finding
	for _, name := range jobs.keys {
		job := jobs.values[name]
		if job == nil || job.kind != nodeMapping {
			findings = append(findings, unreadableForAudit(file, fmt.Sprintf("job %q is not a readable mapping, so nothing in it can be audited", name)))
			continue
		}
		if job.has("uses") {
			findings = append(findings, report.auditActionReference(file, job.child("uses"), true)...)
			findings = append(findings, auditRuntimeVersions(file, job.child("with"))...)
		}
		if job.has("container") {
			findings = append(findings, auditContainerImage(file, job.child("container"))...)
		}
		if services := job.child("services"); services != nil && services.kind != nodeNull {
			if services.kind != nodeMapping {
				findings = append(findings, unreadableForAudit(file, fmt.Sprintf("job %q declares services that are not a readable mapping, so they cannot be audited", name)))
			} else {
				for _, service := range services.keys {
					findings = append(findings, auditContainerImage(file, services.values[service])...)
				}
			}
		}
		steps := job.child("steps")
		if steps == nil || steps.kind == nodeNull {
			continue
		}
		if steps.kind != nodeSequence {
			findings = append(findings, unreadableForAudit(file, fmt.Sprintf("job %q declares steps that are not a readable sequence, so they cannot be audited", name)))
			continue
		}
		for index, step := range steps.items {
			if step == nil || step.kind != nodeMapping {
				findings = append(findings, unreadableForAudit(file, fmt.Sprintf("step %d of job %q is not a readable mapping, so it cannot be audited", index+1, name)))
				continue
			}
			if step.has("uses") {
				findings = append(findings, report.auditActionReference(file, step.child("uses"), false)...)
				findings = append(findings, auditRuntimeVersions(file, step.child("with"))...)
				findings = append(findings, auditSourceRef(file, step.child("uses"), step.child("with"))...)
			}
		}
	}
	return findings
}

// fetchesSource reports whether a readable uses: value names an action that
// clones a repository: actions/checkout, or any action whose name says
// checkout or clone. The name test is a heuristic that over-reports, chosen
// because forks of checkout keep its input names and an adopter switching to
// one would otherwise lose the source-ref guarantee with no signal. Action
// paths are compared case-insensitively, as GitHub resolves them.
func fetchesSource(uses *node) bool {
	if uses == nil || uses.kind != nodeScalar || uses.style == scalarBlock {
		return false
	}
	reference := strings.TrimSpace(uses.value)
	if at := strings.LastIndex(reference, "@"); at >= 0 {
		reference = reference[:at]
	}
	if strings.EqualFold(reference, "actions/checkout") {
		return true
	}
	name := strings.ToLower(reference[strings.LastIndex(reference, "/")+1:])
	return strings.Contains(name, "checkout") || strings.Contains(name, "clone")
}

// immutableRefExpressions name a commit GitHub fixed before the job started:
// the commit of the event that started the run, and the commit of the reusable
// workflow the caller pinned. Contexts are matched case-insensitively, as
// GitHub evaluates them.
var immutableRefExpressions = []string{"github.sha", "job.workflow_sha"}

// auditSourceRef requires a step that selects source of its own to select an
// immutable commit. A ref: input on any action must be immutable: a branch or
// a tag resolves to whatever it points at when the job runs, so rerunning the
// same release tag can build different source while every action stays
// pinned. On an action that clones a repository, a repository: with no ref:
// checks out that repository's default branch and is refused too; with
// neither input such a step checks out the commit of the event that started
// the run, which for a tag push is the tagged commit.
//
// Inputs are matched the way the runner exposes them, case-insensitively, so
// Ref: is ref:. Two spellings of one input in the same block are refused: the
// analysis cannot say which the runner would use, and reading the pinned one
// would certify the other.
func auditSourceRef(file string, uses, with *node) []Finding {
	if with == nil || with.kind != nodeMapping {
		// An unreadable with: block is reported by auditRuntimeVersions.
		return nil
	}
	mutable := func(detail string) []Finding {
		return []Finding{{
			File:   file,
			Rule:   RuleMutableSourceRef,
			Detail: detail,
			Remedy: "set ref: to a full 40-character commit SHA, to ${{ github.sha }}, to ${{ job.workflow_sha }}, or to the output of a job or step in this workflow",
		}}
	}
	ref, refSpellings := inputByName(with, "ref")
	_, repositorySpellings := inputByName(with, "repository")
	if refSpellings > 1 || repositorySpellings > 1 {
		return mutable("a step spells the same source input in more than one way, so the value the runner would use cannot be established")
	}
	if ref == nil {
		if repositorySpellings == 1 && fetchesSource(uses) {
			return mutable("a checkout step selects another repository without a ref:, so it checks out that repository's default branch at whatever commit it holds when the job runs")
		}
		return nil
	}
	if ref.kind != nodeScalar || ref.style == scalarBlock {
		return mutable("a ref: input is not a readable literal")
	}
	text := strings.TrimSpace(ref.value)
	if isHexadecimal(text, 40) || isImmutableRefExpression(text) || isStepOutputExpression(text) {
		return nil
	}
	return mutable(fmt.Sprintf("the ref %q is not an immutable commit, so rerunning the same release tag can build different source", text))
}

// inputByName finds a with: input the way the runner does, ignoring case, and
// reports how many keys in the block spell that input.
func inputByName(with *node, name string) (*node, int) {
	var found *node
	spellings := 0
	for _, key := range with.keys {
		if strings.EqualFold(key, name) {
			found = with.values[key]
			spellings++
		}
	}
	return found, spellings
}

// isImmutableRefExpression reports whether a value is exactly one expression
// naming a commit GitHub fixed before the job started.
func isImmutableRefExpression(text string) bool {
	if !strings.HasPrefix(text, "${{") || !strings.HasSuffix(text, "}}") || strings.Count(text, "${{") != 1 {
		return false
	}
	body := strings.TrimSpace(text[len("${{") : len(text)-len("}}")])
	for _, expression := range immutableRefExpressions {
		if strings.EqualFold(body, expression) {
			return true
		}
	}
	return false
}

func unreadableForAudit(file, detail string) Finding {
	return Finding{
		File:   file,
		Rule:   RuleUnreadableWorkflow,
		Detail: detail,
		Remedy: "rewrite the named construct within the plain block YAML subset the analyser accepts",
	}
}

// auditActionReference audits one uses: value. reusable reports whether the
// reference sits on a job, where it names a reusable workflow, rather than on
// a step, where it names an action; only the former can point at a workflow
// file this pass has read.
func (report *Report) auditActionReference(file string, uses *node, reusable bool) []Finding {
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
		if reusable && report.auditedReusableWorkflow(reference) {
			return nil
		}
		return []Finding{{
			File:   file,
			Rule:   RuleUnauditedLocalAction,
			Detail: fmt.Sprintf("the local reference %q is not a reusable workflow file in the analysed directory, so the external references in its own definition are not audited here", reference),
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

// auditedReusableWorkflow reports whether a local uses: reference names a
// reusable workflow file that this same pass has read and does audit in its
// own right. Only a direct child of the workflow directory qualifies. An action
// stored below it, such as ./.github/workflows/actions/package, lives in a
// directory this pass never descends into, and its action.yml can reference
// anything; sharing the directory prefix earns it nothing. The callee must
// also be release capable, because PinFindings audits only those files: a
// callee this pass has read but skipped has not been audited by anyone.
func (report *Report) auditedReusableWorkflow(reference string) bool {
	name, local := reusableWorkflowName(reference)
	if !local {
		return false
	}
	workflow := report.workflow(name)
	return workflow != nil && workflow.Active && workflow.document != nil && workflow.ReleaseCapable
}

// reusableWorkflowName returns the base name a relative uses: reference points
// at when, and only when, it names a direct child of the workflow directory
// with a workflow extension.
func reusableWorkflowName(reference string) (string, bool) {
	prefix := "./" + DefaultWorkflowDirectory + "/"
	if !strings.HasPrefix(reference, prefix) {
		return "", false
	}
	name := reference[len(prefix):]
	if name == "" || strings.Contains(name, "/") || !hasWorkflowExtension(name) {
		return "", false
	}
	return name, true
}

// auditContainerImage requires a job or service container to name an immutable
// image. A container is executed code exactly as an action is, so rebuilding the
// same tag against a mutable image runs something different. An image chosen by
// an expression is refused whatever the expression's root. A repository
// variable changes with no commit, and an image built and named by an earlier
// job is exactly a runtime-resolved artifact; the step-output exception that
// auditRuntimeVersions makes exists because the Hextap contract carries the
// runtime version through a job output, and no part of that contract names an
// image that way.
func auditContainerImage(file string, container *node) []Finding {
	if container == nil || container.kind == nodeNull {
		return nil
	}
	image := container
	if container.kind == nodeMapping {
		image = container.child("image")
	}
	if image == nil || image.kind != nodeScalar || image.style == scalarBlock {
		return []Finding{{
			File:   file,
			Rule:   RuleUnpinnedContainer,
			Detail: "a job or service container image is not a readable literal",
			Remedy: "name the image as a plain or quoted scalar pinned to an @sha256: digest",
		}}
	}
	reference := strings.TrimSpace(image.value)
	if strings.Contains(reference, "${{") {
		return []Finding{{
			File:   file,
			Rule:   RuleUnpinnedContainer,
			Detail: fmt.Sprintf("the container image %q resolves through an expression, so the image the tagged build runs in is decided outside the tagged source", reference),
			Remedy: "name the image literally, pinned to an immutable @sha256: digest",
		}}
	}
	if digest := strings.LastIndex(reference, "@sha256:"); digest >= 0 &&
		isHexadecimal(reference[digest+len("@sha256:"):], 64) {
		return nil
	}
	return []Finding{{
		File:   file,
		Rule:   RuleUnpinnedContainer,
		Detail: fmt.Sprintf("the container image %q carries no image digest, so the same tag can execute a different image later", reference),
		Remedy: "pin the image to an immutable @sha256: digest",
	}}
}

// auditRuntimeVersions audits the runtime selectors among a job's or step's
// with: inputs. A with: block the reader cannot represent is reported rather
// than skipped, since the selectors inside it cannot be seen.
func auditRuntimeVersions(file string, with *node) []Finding {
	if with == nil || with.kind == nodeNull {
		return nil
	}
	if with.kind != nodeMapping {
		return []Finding{unreadableForAudit(file, "a with: block is not a readable mapping, so the runtime inputs in it cannot be audited")}
	}
	var findings []Finding
	for _, key := range with.keys {
		if !isRuntimeSelector(key) {
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
			if isStepOutputExpression(text) {
				continue
			}
			findings = append(findings, Finding{
				File:   file,
				Rule:   RuleFloatingRuntimeVersion,
				Detail: fmt.Sprintf("the runtime input %q resolves through the expression %q, whose value is not fixed by the tagged workflow source", key, text),
				Remedy: fmt.Sprintf("set %s to an exact version, or to the output of a job or step in this workflow", key),
			})
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

// runtimeSelectors are the input names, beyond any *-version input, through
// which setup actions choose a toolchain: version for the many actions that
// install one tool, toolchain for the Rust setup actions, sdk for Dart.
var runtimeSelectors = map[string]struct{}{
	"sdk":       {},
	"toolchain": {},
	"version":   {},
}

// isRuntimeSelector reports whether a with: input selects a runtime version.
// Names are compared case-insensitively with underscores folded to hyphens, so
// terraform_version and Node-Version are audited alongside node-version.
//
// Two names are deliberately outside the set. A *-version-file input names a
// file in the tagged source whose content is fixed by the tag and outside this
// directory-only audit. channel is ambiguous: it selects a Flutter release
// channel in one action and a Slack channel in several others, and refusing a
// release notification step would be a refusal the adopter cannot justify.
func isRuntimeSelector(key string) bool {
	normalised := strings.ReplaceAll(strings.ToLower(key), "_", "-")
	if _, selector := runtimeSelectors[normalised]; selector {
		return true
	}
	return strings.HasSuffix(normalised, "-version")
}

// isStepOutputExpression reports whether a value is exactly one expression
// reading a job or step output: needs.<job>.outputs.<name> or
// steps.<id>.outputs.<name>. Such a value is produced by a step whose own
// inputs this audit already covers. What the step computes from them is not
// visible here and is not claimed to be pinned; the shape is accepted because
// the Hextap reusable workflow relies on it to carry the runtime version
// sealed in the adopter manifest. Every other expression root, including vars,
// inputs, env, github and matrix, would need the audit to evaluate
// expressions to bound, and it evaluates none, so it is refused.
func isStepOutputExpression(text string) bool {
	if !strings.HasPrefix(text, "${{") || !strings.HasSuffix(text, "}}") || strings.Count(text, "${{") != 1 {
		return false
	}
	body := strings.TrimSpace(text[len("${{") : len(text)-len("}}")])
	parts := strings.Split(body, ".")
	if len(parts) != 4 || (parts[0] != "needs" && parts[0] != "steps") || parts[2] != "outputs" {
		return false
	}
	return isIdentifier(parts[1]) && isIdentifier(parts[3])
}

// floatingVersionAliases are the runtime selectors that resolve to whatever is
// current at the moment the job runs.
var floatingVersionAliases = map[string]struct{}{
	"":        {},
	"*":       {},
	"beta":    {},
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

// isHexadecimal accepts either case, because Git resolves a commit identifier
// case-insensitively. Refusing an uppercase revision would report an already
// immutable reference as mutable.
func isHexadecimal(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for index := 0; index < len(value); index++ {
		switch character := value[index]; {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'f':
		case character >= 'A' && character <= 'F':
		default:
			return false
		}
	}
	return true
}
