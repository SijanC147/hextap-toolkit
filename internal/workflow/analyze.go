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
	RuleUnpinnedRemoteScript   = "unpinned-remote-script"
	RuleUnpinnedRemoteInput    = "unpinned-remote-input"
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
	// ownTrigger and ownReason are the classification from the file's own
	// events, before any workflow_run chain was resolved, so that a second
	// resolution against another tree can start from them.
	ownTrigger TagTrigger
	ownReason  string
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
	// TrustJobOutputs accepts a runtime version or a source ref that arrives
	// as the output of a job or step in the same workflow, in the exact
	// needs.<job>.outputs.<name> or steps.<id>.outputs.<name> shape. The
	// audit cannot see what the producing step computed, and the shape alone
	// proves nothing: a step can derive its output from a variable, an
	// input, the network or the clock. The toolkit's own reusable release
	// workflow derives the runtime version from the manifest it has just
	// sealed and the source commit from the tag it has just verified against
	// the default branch, and that code is under review in this repository,
	// so the toolkit sets this for its own tree. Nothing else should. Under
	// the default, an output-shaped value is refused like any other
	// expression.
	TrustJobOutputs bool
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
				ownTrigger:    TagTriggerUnknown,
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
			workflow.ownTrigger = TagTriggerUnknown
			workflow.TriggerReason = parseErr.Error()
			report.Workflows = append(report.Workflows, workflow)
			continue
		}

		name, readable := declaredName(document)
		if !readable {
			workflow.TagTrigger = TagTriggerUnknown
			workflow.ownTrigger = TagTriggerUnknown
			workflow.TriggerReason = "the workflow name is not a readable scalar, so a sibling workflow_run trigger cannot be matched against it"
			report.Workflows = append(report.Workflows, workflow)
			continue
		}

		analysis := analyzeTriggers(document)
		workflow.document = document
		workflow.Name = name
		workflow.TagTrigger = analysis.trigger
		workflow.TriggerReason = analysis.reason
		workflow.ownTrigger = analysis.trigger
		workflow.ownReason = analysis.reason
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
	resolveWorkflowRunChains(report, nil, true)
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
//
// external supplies tag responders from another tree, through the events
// those files declare themselves: the default branch's chained workflows fire
// off the runs the tagged tree's workflows produce. selfSeeds says whether the
// report's own push and create responders count too. They do for a single
// tree; they do not for the default branch resolved beside a tagged tree,
// because GitHub reads push and create from the tagged commit, so a tag
// trigger a default-branch copy has gained since the tag produces no run and
// cannot start anything. Chained workflows in the report always count, since
// they run on the default branch by definition.
func resolveWorkflowRunChains(report *Report, external []Workflow, selfSeeds bool) {
	for changed := true; changed; {
		changed = false
		responders := make(map[string]bool)
		for _, workflow := range external {
			if !workflow.Active || !workflow.ownTrigger.RespondsToTagPush() {
				continue
			}
			for _, name := range workflow.triggerNames() {
				responders[name] = true
			}
		}
		for _, workflow := range report.Workflows {
			if !workflow.Active {
				continue
			}
			if !workflow.chained && !(selfSeeds && workflow.TagTrigger.RespondsToTagPush()) {
				continue
			}
			for _, name := range workflow.triggerNames() {
				responders[name] = true
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
// this package exists to remove. A job carrying an if: condition is not a
// call either: whether the condition holds for the pushed tag would need the
// expression evaluated, and a caller whose only job is skipped owns nothing.
// Nor is a job carrying a strategy: a matrix runs the release once per leg
// against the same tag, which is the shape this package exists to remove, and
// for the same reason the file must carry exactly one job: two calls, parallel
// or chained, are two release runs against one tag.
func callerForms(document *node) (external, relative bool) {
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping || len(jobs.keys) != 1 {
		return false, false
	}
	external, relative = true, true
	for _, key := range jobs.keys {
		job := jobs.values[key]
		if job == nil || job.kind != nodeMapping || job.has("if") || job.has("strategy") {
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
	if at < 0 || !isHexadecimal(reference[at+1:], 40) {
		return false
	}
	// GitHub resolves an owner and a repository name case-insensitively; a
	// path inside the repository is a Git path and is exact.
	path := reference[:at]
	split := strings.Index(path, "/.github/")
	if split < 0 {
		return false
	}
	expectedSplit := strings.Index(hextapReusableWorkflow, "/.github/")
	// GitHub owner and repository names are ASCII, and EqualFold is Unicode
	// simple folding, under which a long s or a Kelvin sign would fold onto
	// an ASCII letter; such a name resolves to no repository at all.
	if !isASCII(path[:split]) {
		return false
	}
	return strings.EqualFold(path[:split], hextapReusableWorkflow[:expectedSplit]) &&
		path[split:] == hextapReusableWorkflow[expectedSplit:]
}

func isASCII(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] >= 0x80 {
			return false
		}
	}
	return true
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

// readsCredentialChannel reports whether the document holds or reads a
// credential this analysis cannot see the scope of: a literal GitHub token
// anywhere in its text, a literal value under a credential-named key, or an
// expression reading the secrets context for anything other than
// GITHUB_TOKEN, one of the input channels a caller or dispatcher fills, a
// variable, the env or matrix contexts, or the output of a step or job. A
// single one anywhere in the file makes the workflow release capable.
// GITHUB_TOKEN is the one exception, because the permissions block bounds it.
func readsCredentialChannel(document *node) bool {
	if anyScalar(document, func(value string) bool {
		if looksLikeGitHubToken(value) {
			return true
		}
		for _, expression := range expressions(value) {
			if expressionReadsCredentialChannel(expression) {
				return true
			}
		}
		return false
	}) {
		return true
	}
	return holdsLiteralCredential(document)
}

// gitHubTokenPrefixes are the prefixes GitHub issues its tokens with. A value
// carrying one is a credential wherever it sits, and a workflow holding one
// in its own text can publish with it whatever its permissions block says.
var gitHubTokenPrefixes = []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"}

// looksLikeGitHubToken reports whether a value carries a GitHub token: one of
// the issued prefixes beginning a word and followed by a token-length run of
// letters, digits and underscores. The shortest issued tokens carry 36
// characters after the prefix, so twenty is a comfortable floor that still
// leaves $HIGHS_MAX and a documentation path such as /gho_guide alone.
func looksLikeGitHubToken(value string) bool {
	const tokenBodyFloor = 20
	lowered := strings.ToLower(value)
	for _, prefix := range gitHubTokenPrefixes {
		for start := strings.Index(lowered, prefix); start >= 0; {
			if start == 0 || !isTokenBodyByte(lowered[start-1]) {
				body := 0
				for index := start + len(prefix); index < len(lowered) && isTokenBodyByte(lowered[index]); index++ {
					body++
				}
				if body >= tokenBodyFloor {
					return true
				}
			}
			next := strings.Index(lowered[start+1:], prefix)
			if next < 0 {
				break
			}
			start += 1 + next
		}
	}
	return false
}

func isTokenBodyByte(character byte) bool {
	switch {
	case character >= '0' && character <= '9':
	case character >= 'a' && character <= 'z':
	case character >= 'A' && character <= 'Z':
	case character == '_':
	default:
		return false
	}
	return true
}

// credentialInputNames are the mapping keys, in env: and with: blocks alike,
// under which a workflow supplies a credential: each name on its own and as
// the suffix of a longer name, so token, github-token, private-key and
// app-private-key all count. A literal value under one of them is a
// credential this analysis cannot see the scope of; an expression under one
// is judged by the channel it reads. Any name ending in -key counts as well
// (ssh-key, deploy-key, service-account-key), while bare key is deliberately
// left out because actions/cache takes a literal one; auth, bearer, passwd
// and the aws_access_key_id spelling are left out too, each for sweeping in
// a common non-credential input or for not occurring as a literal in
// practice.
var credentialInputNames = []string{
	"access-key",
	"api-key",
	"credentials",
	"credentials-json",
	"password",
	"pat",
	"private-key",
	"secret",
	"token",
}

func isCredentialInputName(key string) bool {
	normalised := normaliseInputName(key)
	for _, name := range credentialInputNames {
		if normalised == name || strings.HasSuffix(normalised, "-"+name) {
			return true
		}
	}
	return strings.HasSuffix(normalised, "-key")
}

// holdsLiteralCredential reports whether any mapping in the document gives a
// credential-named key a literal, non-empty value with no expression in it.
func holdsLiteralCredential(document *node) bool {
	return anyMapping(document, func(mapping *node) bool {
		for _, key := range mapping.keys {
			if !isCredentialInputName(key) {
				continue
			}
			value := mapping.values[key]
			if value == nil || value.kind != nodeScalar {
				continue
			}
			text := strings.TrimSpace(value.value)
			if text == "" || strings.Contains(text, "${{") || isBooleanLiteral(text) {
				// persist-credentials: false is a switch, not a credential.
				continue
			}
			return true
		}
		return false
	})
}

func isBooleanLiteral(text string) bool {
	switch strings.ToLower(text) {
	case "true", "false", "yes", "no", "on", "off":
		return true
	}
	return false
}

// anyMapping reports whether any mapping node in the tree satisfies the
// predicate, stopping at the first that does.
func anyMapping(current *node, predicate func(*node) bool) bool {
	if current == nil {
		return false
	}
	switch current.kind {
	case nodeMapping:
		if predicate(current) {
			return true
		}
		for _, key := range current.keys {
			if anyMapping(current.values[key], predicate) {
				return true
			}
		}
	case nodeSequence:
		for _, item := range current.items {
			if anyMapping(item, predicate) {
				return true
			}
		}
	}
	return false
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

// structuredEventLeaves are the final path segments under github.event that
// carry an identifier, a number, a commit or an enumerated state GitHub itself
// produced. Every other field of the event, such as an issue body, a comment,
// a title or a name, is text the triggering party wrote and can hold a token
// as readily as a dispatch input, so a read of it is a credential channel.
// github.head_ref is the same kind of text, chosen by whoever opened the pull
// request, and counts too.
var structuredEventLeaves = map[string]struct{}{
	"action":      {},
	"after":       {},
	"before":      {},
	"conclusion":  {},
	"created":     {},
	"deleted":     {},
	"draft":       {},
	"forced":      {},
	"head_sha":    {},
	"id":          {},
	"merged":      {},
	"node_id":     {},
	"number":      {},
	"run_attempt": {},
	"run_id":      {},
	"run_number":  {},
	"sha":         {},
	"status":      {},
	"workflow_id": {},
}

// expressionReadsCredentialChannel reports whether an expression body reads the
// secrets context for anything but GITHUB_TOKEN, the inputs, vars or env
// contexts, or a payload channel under the github context. Each context is matched as a whole
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
		case strings.EqualFold(word, "inputs"), strings.EqualFold(word, "vars"), strings.EqualFold(word, "env"), strings.EqualFold(word, "matrix"):
			// Repository, organisation and environment variables are
			// externally mutable strings and can hold a token as readily as
			// a dispatch input; the env context can carry one set by an
			// earlier step from a source this analysis never sees; a matrix
			// can be built from a job output.
			return true
		case strings.EqualFold(word, "steps"), strings.EqualFold(word, "needs"):
			// An output of a step or a job is computed by code this
			// analysis does not read, which can fetch a token from the
			// network or a runner. Only the outputs are a channel; a
			// step's outcome or a job's result is an enumerated state.
			path := strings.ToLower(contextPath(remainder))
			if path == "" || strings.Contains("."+path+".", ".outputs.") {
				return true
			}
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
			if path == "event" || path == "" || path == "head_ref" {
				// The whole event, or the whole context, reaches every
				// payload channel at once.
				return true
			}
			if strings.HasPrefix(path, "event.") {
				leaf := path[strings.LastIndex(path, ".")+1:]
				if _, structured := structuredEventLeaves[leaf]; !structured {
					return true
				}
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
// refused, with one exception the policy has to switch on: under
// Policy.TrustJobOutputs, the output of a job or step in the same file is
// accepted, not proven pinned, and the audit says so.
func (report *Report) PinFindings(policy Policy) []Finding {
	var findings []Finding
	for _, workflow := range report.Workflows {
		if !workflow.Active || !workflow.ReleaseCapable || workflow.document == nil {
			continue
		}
		findings = append(findings, report.auditPins(workflow, policy)...)
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
	findings = append(findings, tagged.PinFindings(policy)...)
	if defaultBranch != tagged {
		findings = append(findings, defaultBranchFindings(tagged, defaultBranch, policy, findings)...)
	}
	return findings
}

// defaultBranchFindings reports what the default branch contributes to the
// tag's workflow surface. It first discards the chain resolution Analyze did
// within the default-branch tree alone and resolves workflow_run chains again
// from the tagged tree's own responders, which mutates that report: a push or
// create trigger the default-branch copy of a file has gained since the tag
// produces no run for the tag push, so it must not seed a chain. It then
// reports every chained workflow there, together with the pin audit of it and
// of every local reusable workflow it reaches, and every file there the
// reader could not understand. A push or create workflow that exists only on
// the default branch is not reported, because GitHub reads those from the
// tagged commit.
//
// A refusal the tagged tree already raised for the same file and rule is not
// raised again; a pin finding is repeated only when its detail differs, so a
// default-branch copy that is worse than the tagged one is still reported.
func defaultBranchFindings(tagged, defaultBranch *Report, policy Policy, raised []Finding) []Finding {
	for index := range defaultBranch.Workflows {
		workflow := &defaultBranch.Workflows[index]
		if !workflow.chained {
			continue
		}
		workflow.chained = false
		workflow.TagTrigger = workflow.ownTrigger
		workflow.TriggerReason = workflow.ownReason
		workflow.ReleaseCapable = workflow.ownTrigger.RespondsToTagPush() || !provablyUnableToRelease(workflow.document)
	}
	resolveWorkflowRunChains(defaultBranch, tagged.Workflows, false)

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
		for _, finding := range defaultBranch.auditPins(*workflow, policy) {
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
			if count := jobCount(workflow.document); count > 1 {
				return fmt.Sprintf("%s carries %d jobs, and every job of a caller is a release run against the same tag; the caller must carry exactly one",
					callerFile, count), false
			}
			if job, key, found := guardedJob(workflow.document); found {
				if key == "strategy" {
					return fmt.Sprintf("job %q of %s carries a strategy, so it would run the release once per matrix leg against the same tag",
						job, callerFile), false
				}
				return fmt.Sprintf("job %q of %s carries an if: condition, so it cannot be shown to run for the pushed tag, and a caller whose call is skipped owns nothing",
					job, callerFile), false
			}
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

// jobCount reports how many jobs the document declares, or zero when the
// jobs block is not a readable mapping.
func jobCount(document *node) int {
	if document == nil {
		return 0
	}
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping {
		return 0
	}
	return len(jobs.keys)
}

// guardedJob names the first job carrying an if: condition or a strategy,
// and which of the two, if any.
func guardedJob(document *node) (job, key string, found bool) {
	if document == nil {
		return "", "", false
	}
	jobs := document.child("jobs")
	if jobs == nil || jobs.kind != nodeMapping {
		return "", "", false
	}
	for _, name := range jobs.keys {
		current := jobs.values[name]
		if current == nil || current.kind != nodeMapping {
			continue
		}
		for _, guard := range []string{"if", "strategy"} {
			if current.has(guard) {
				return name, guard, true
			}
		}
	}
	return "", "", false
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
//
// Two boundaries are deliberate. A run: step is the tagged source's own shell
// code and is not read as a program; the one shape refused is a downloader
// piped into an interpreter, which fetches code the tag does not fix and is
// the idiom by which floating toolchains arrive. And the runner image a job
// selects with runs-on is the platform's chosen execution environment, revised
// by GitHub over time; Hextap's release contract builds natively on hosted
// runners for every target, so that drift is a platform decision recorded
// outside this package rather than a finding it can raise.
func (report *Report) auditPins(workflow Workflow, policy Policy) []Finding {
	file, document, events := workflow.File, workflow.document, workflow.Events
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
			findings = append(findings, auditRuntimeVersions(file, job.child("with"), policy)...)
			findings = append(findings, auditSourceRef(file, job.child("uses"), job.child("with"), events, true, policy)...)
			findings = append(findings, auditRemoteContexts(file, job.child("uses"), job.child("with"), policy)...)
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
				findings = append(findings, auditRuntimeVersions(file, step.child("with"), policy)...)
				findings = append(findings, auditRuntimeSetup(file, step.child("uses"), step.child("with"), policy)...)
				findings = append(findings, auditRemoteContexts(file, step.child("uses"), step.child("with"), policy)...)
				findings = append(findings, auditSourceRef(file, step.child("uses"), step.child("with"), events, false, policy)...)
			}
			if step.has("run") {
				findings = append(findings, auditRunStep(file, step.child("run"))...)
			}
		}
	}
	return findings
}

// auditRunStep refuses the one shape of shell code the audit reads: a
// downloader piped into an interpreter, or a download substituted into a
// command line. The script that arrives is fixed by nothing in the tagged
// source, so the same tag can run different code on a rerun while every
// action stays pinned, and the idiom is how floating toolchains are usually
// installed. Everything else in a run: body is the tagged source's own code
// and is left alone: a download saved to a file and executed afterwards, on
// the same line or a later one, is not caught, and this doc says so rather
// than implying a shell audit.
func auditRunStep(file string, run *node) []Finding {
	if run == nil || run.kind != nodeScalar {
		return nil
	}
	var findings []Finding
	for _, line := range strings.Split(run.value, "\n") {
		switch fetchesAndUses(line) {
		case downloadExecuted:
			findings = append(findings, Finding{
				File:   file,
				Rule:   RuleUnpinnedRemoteScript,
				Detail: fmt.Sprintf("a run step executes code it downloads while running: %q", strings.TrimSpace(line)),
				Remedy: "install the tool through a setup action pinned to a full commit SHA with an exact version, or commit the script to the tagged source",
			})
		case downloadCaptured:
			findings = append(findings, Finding{
				File:   file,
				Rule:   RuleUnpinnedRemoteInput,
				Detail: fmt.Sprintf("a run step captures a value it downloads while running, so the build depends on what the network returns when the job runs: %q", strings.TrimSpace(line)),
				Remedy: "commit the value to the tagged source, or compute it from data the tag fixes",
			})
		}
	}
	return findings
}

// downloadUse classifies what one line of shell does with a download.
type downloadUse int

const (
	downloadUnused downloadUse = iota
	// downloadCaptured means the download's text is captured into a variable
	// or an argument, so the build depends on it without executing it.
	downloadCaptured
	// downloadExecuted means the download's text is run as code.
	downloadExecuted
)

// downloaders and interpreters are the command names the run-step rule
// recognises: a downloader anywhere in a pipe segment or a substitution, an
// interpreter only in command position of a later pipe segment.
var (
	downloaders  = []string{"curl", "wget", "invoke-webrequest", "iwr"}
	interpreters = []string{"sh", "bash", "zsh", "dash", "ksh", "fish", "python", "python3", "node", "perl", "ruby", "pwsh", "powershell", "iex", "invoke-expression"}
	// commandPrefixes are the words that may precede the command in a pipe
	// segment without being it.
	commandPrefixes = map[string]struct{}{"sudo": {}, "env": {}, "exec": {}, "time": {}, "nice": {}, "command": {}}
	// executors are the words after which a substituted download is run as
	// code rather than captured: an interpreter, or eval, source, exec, the
	// dot builtin, or an interpreter's -c, -e and -Command flags.
	executors = map[string]struct{}{"eval": {}, "source": {}, ".": {}, "exec": {}, "-c": {}, "-e": {}, "-command": {}, "--command": {}}
)

// fetchesAndUses classifies one line of shell. A downloader's output piped
// into an interpreter that is the command of a later segment (curl … | sh,
// wget -qO- … | sudo bash, curl … | /usr/bin/env bash, iwr … |
// Invoke-Expression), or a download substituted where a command is expected
// (bash <(curl …), sh -c "$(curl …)", eval "$(curl …)", $(curl …) at the
// start of a line), is a download executed. A download substituted anywhere
// else (VERSION=$(curl …), an argument built from one) is a download
// captured. A substitution counts only when the downloader is inside it: a
// header built from $(cat token) on a curl line downloads nothing it then
// uses. A pipe counts only when the interpreter is the command of a later
// segment, so curl … | jq -r '.node' is a filter, and || is a fallback, not
// a pipe. A quoted pipeline handed to an interpreter (sh -c "curl … | sh")
// is read like an unquoted one.
func fetchesAndUses(line string) downloadUse {
	lowered := strings.ToLower(line)
	if !containsCommand(lowered, downloaders) {
		return downloadUnused
	}
	// Each substitution's body is scanned to its closing parenthesis, which
	// on a line of nested or unclosed openers reaches the end of the line
	// every time. A line carrying more substitutions than any real command
	// does is read as an execution rather than scanned quadratically.
	const maxSubstitutions = 64
	use := downloadUnused
	examined := 0
	for _, opener := range []string{"$(", "<("} {
		for start := strings.Index(lowered, opener); start >= 0; {
			if examined++; examined > maxSubstitutions {
				return downloadExecuted
			}
			if containsCommand(substitutionBody(lowered[start+len(opener):]), downloaders) {
				if executesSubstitution(lowered[:start], opener) {
					return downloadExecuted
				}
				use = downloadCaptured
			}
			next := strings.Index(lowered[start+1:], opener)
			if next < 0 {
				break
			}
			start += 1 + next
		}
	}
	segments := strings.Split(strings.ReplaceAll(lowered, "||", " ; "), "|")
	downloaded := false
	for _, segment := range segments {
		if downloaded && commandOf(segment, interpreters) {
			return downloadExecuted
		}
		if containsCommand(segment, downloaders) {
			downloaded = true
		}
	}
	return use
}

// executesSubstitution reports whether the text before a substitution puts
// the substituted download where a command is expected: at the start of a
// command, as the standard input redirected from a process substitution
// (< <(curl …), where the download itself is read; < $(curl …) names a file
// and is a capture), or as the operand of an interpreter or an executor
// word. A prefix that is nothing but whitespace the shell would not even
// split, such as a pasted non-breaking space, is read as a command start.
func executesSubstitution(prefix, opener string) bool {
	prefix = strings.TrimRight(prefix, " \t\"'`")
	if prefix == "" {
		return true
	}
	terminators := []string{"|", ";", "&&", "(", "{"}
	if opener == "<(" {
		terminators = append(terminators, "<")
	}
	for _, terminator := range terminators {
		if strings.HasSuffix(prefix, terminator) {
			return true
		}
	}
	words := strings.Fields(prefix)
	if len(words) == 0 {
		return true
	}
	last := strings.Trim(words[len(words)-1], "\"'`")
	if _, executor := executors[last]; executor {
		return true
	}
	command := last[strings.LastIndex(last, "/")+1:]
	for _, name := range interpreters {
		if command == name {
			return true
		}
	}
	return false
}

// substitutionBody returns the text of a substitution up to its closing
// parenthesis, or to the end of the line when none closes it.
func substitutionBody(text string) string {
	depth := 1
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[:index]
			}
		}
	}
	return text
}

// commandOf reports whether the command a pipe segment runs is one of the
// names. Each word is stripped of surrounding quotes and of any directory
// first, then sudo -E, /usr/bin/env -i, exec and a VAR=value assignment are
// skipped as prefixes, so that curl … | /usr/bin/env bash and a quoted
// pipeline such as sh -c "curl … | sh" both read their real command.
func commandOf(segment string, names []string) bool {
	for _, word := range strings.Fields(segment) {
		word = strings.Trim(word, "\"'`")
		if word == "" {
			continue
		}
		command := word[strings.LastIndex(word, "/")+1:]
		if _, prefix := commandPrefixes[command]; prefix || strings.HasPrefix(command, "-") || (strings.Contains(word, "=") && !strings.HasPrefix(word, "=")) {
			continue
		}
		for _, name := range names {
			if command == name {
				return true
			}
		}
		return false
	}
	return false
}

// containsCommand reports whether any of the names appears in the text as a
// whole word, so that curl matches and curly does not.
func containsCommand(text string, names []string) bool {
	for _, name := range names {
		for start := strings.Index(text, name); start >= 0; {
			end := start + len(name)
			before := start == 0 || !isIdentifierByte(text[start-1])
			after := end == len(text) || !isIdentifierByte(text[end])
			if before && after {
				return true
			}
			next := strings.Index(text[start+1:], name)
			if next < 0 {
				break
			}
			start += 1 + next
		}
	}
	return false
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

// auditRemoteContexts requires every with: input that names a remote Git
// repository, under whatever input name and in whatever position within the
// value, to name it at an immutable commit. BuildKit's build context accepts
// https://host/repo.git#ref and resolves the ref when the job runs; with no
// fragment it takes the default branch; and its named contexts and build
// arguments carry the same references as newline-separated name=value pairs,
// usually in a block scalar. Either way the same release tag can build
// different contents while the action itself stays pinned. Each line of a
// value is examined on its own, with a leading name= stripped; a line counts
// as a remote Git reference when it starts with a URL scheme or an SCP-like
// host address and carries a .git path or a #fragment; the fragment must be a
// full 40-character commit SHA, optionally followed by a :subdirectory. Prose
// that merely contains a URL is untouched, because the reference has to start
// the line; the cost is that a non-Git URL with a #route at the start of a
// value is refused too.
func auditRemoteContexts(file string, uses, with *node, policy Policy) []Finding {
	if with == nil || with.kind != nodeMapping {
		return nil
	}
	action := ""
	if uses != nil && uses.kind == nodeScalar && uses.style != scalarBlock {
		action = strings.ToLower(strings.TrimSpace(uses.value))
		if at := strings.LastIndex(action, "@"); at >= 0 {
			action = action[:at]
		}
	}
	var findings []Finding
	for _, key := range with.keys {
		value := with.values[key]
		if value == nil || value.kind != nodeScalar {
			continue
		}
		for _, line := range strings.Split(value.value, "\n") {
			text := strings.TrimSpace(line)
			if name := strings.Index(text, "="); name > 0 && isIdentifier(text[:name]) {
				text = strings.TrimSpace(text[name+1:])
			}
			if isBuildContextInput(action, key) && strings.Contains(text, "${{") {
				// A build context chosen by an expression can become a
				// remote Git reference on a moving branch with no commit
				// to the workflow, so it is refused unless it is a trusted
				// job output.
				if isStepOutputExpression(text) && policy.TrustJobOutputs {
					continue
				}
				findings = append(findings, Finding{
					File:   file,
					Rule:   RuleMutableSourceRef,
					Detail: fmt.Sprintf("the build context input %q resolves through the expression %q, so the source it builds from is decided outside the tagged workflow", key, text),
					Remedy: "name the build context literally: a path in the checkout, or a remote Git reference at a full 40-character commit SHA",
				})
				continue
			}
			if !isRemoteGitReference(text) {
				continue
			}
			fragment := ""
			if hash := strings.Index(text, "#"); hash >= 0 {
				fragment = text[hash+1:]
				if colon := strings.Index(fragment, ":"); colon >= 0 {
					fragment = fragment[:colon]
				}
			}
			if isHexadecimal(fragment, 40) {
				continue
			}
			findings = append(findings, Finding{
				File:   file,
				Rule:   RuleMutableSourceRef,
				Detail: fmt.Sprintf("the input %q names the remote Git source %q without an immutable commit, so the source it fetches is resolved when the job runs", key, text),
				Remedy: "append #<full 40-character commit SHA> to the remote Git reference",
			})
		}
	}
	return findings
}

// buildContextInputs lists, for the build actions that take source under a
// name other than context, which inputs do: docker/bake-action's source
// accepts the same https://host/repo.git#ref form as context does, and its
// files can name remote definitions. Gated on the action so that a generic
// source: input on another action is not swept in.
var buildContextInputs = map[string][]string{
	"docker/bake-action": {"source", "files"},
}

// isBuildContextInput reports whether a with: input names a build context:
// BuildKit's context and its named build-contexts on any action, and the
// listed inputs of the build actions that use other names.
func isBuildContextInput(action, key string) bool {
	normalised := normaliseInputName(key)
	if normalised == "context" || strings.HasSuffix(normalised, "-context") || strings.HasSuffix(normalised, "-contexts") {
		return true
	}
	return containsString(buildContextInputs[action], normalised)
}

// isRemoteGitReference reports whether a value names a Git repository: a URL
// under any scheme (https, ssh, git, ftps, and the git+ forms of each), or an
// SCP-like address of the form user@host:path or host:path with a dotted
// host, together with a .git path or a #fragment. A URL with neither, such as
// a package registry, is not a Git source.
func isRemoteGitReference(text string) bool {
	lowered := strings.ToLower(text)
	if !strings.Contains(lowered, ".git") && !strings.Contains(lowered, "#") {
		return false
	}
	if scheme := strings.Index(lowered, "://"); scheme > 0 && isSchemeName(lowered[:scheme]) {
		return true
	}
	address := lowered
	if at := strings.Index(address, "@"); at > 0 && isIdentifier(address[:at]) {
		address = address[at+1:]
	}
	colon := strings.Index(address, ":")
	if colon <= 0 {
		return false
	}
	host := address[:colon]
	return strings.Contains(host, ".") && isHostName(host)
}

func isSchemeName(text string) bool {
	if text == "" || text[0] < 'a' || text[0] > 'z' {
		return false
	}
	for index := 0; index < len(text); index++ {
		switch character := text[index]; {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '+' || character == '-' || character == '.':
		default:
			return false
		}
	}
	return true
}

func isHostName(text string) bool {
	for index := 0; index < len(text); index++ {
		switch character := text[index]; {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '-' || character == '.':
		default:
			return false
		}
	}
	return true
}

// tagCommitEvents are the events under which github.sha and, outside a
// reusable workflow, job.workflow_sha name the commit the tag points at. Under
// workflow_run and schedule they name the default branch's current commit,
// and under workflow_dispatch whatever ref was dispatched, all of which move
// between runs. release is listed because GITHUB_SHA under it is the
// release's tagged commit; whether a release-triggered workflow competes for
// the tag is a separate question the trigger classification in triggers.go
// answers, and which SB23-757 tracks.
var tagCommitEvents = map[string]struct{}{
	"create":  {},
	"push":    {},
	"release": {},
}

// auditSourceRef requires a step or a reusable workflow call that selects
// source of its own to select an immutable commit. A ref: input must be
// immutable: a branch or a tag resolves to whatever it points at when the job
// runs, so rerunning the same release tag can build different source while
// every action stays pinned. On an action that clones a repository, and on
// any reusable workflow call, a repository: with no ref: is refused too: the
// former checks out that repository's default branch, and what the latter
// does with the input cannot be established here. With neither input a
// checkout step fetches the commit of the event that started the run, which
// for a tag push is the tagged commit. Only the inputs named ref and
// repository are read: a reusable workflow can take source selection under
// any name, and the Hextap caller's own tag input is one, so refusing every
// ref-shaped name would refuse the toolkit's own contract. What a callee does
// with an input the audit cannot name is bounded by auditing the callee.
//
// An expression naming a commit is accepted only where it names the tagged
// one. github.sha does so under a push, create or release event and nowhere
// else, since under workflow_run it is the default branch's current commit
// and under workflow_dispatch whatever was dispatched. job.workflow_sha does
// so under the same events and inside a reusable workflow, where it is the
// callee's commit that the caller pinned; a file that is both callable and
// dispatchable runs under the dispatch too, so every declared event must be
// one of those. github.event.workflow_run.head_sha is the upstream run's
// commit and is accepted only when workflow_run is the sole declared event,
// since under any other event the field is absent and the checkout falls
// back to that event's ref. An output of a job or step is accepted only under
// Policy.TrustJobOutputs.
//
// Inputs are matched the way the runner exposes them, case-insensitively, so
// Ref: is ref:. Two spellings of one input in the same block are refused: the
// analysis cannot say which the runner would use, and reading the pinned one
// would certify the other.
func auditSourceRef(file string, uses, with *node, events []string, reusable bool, policy Policy) []Finding {
	if with == nil || with.kind != nodeMapping {
		// An unreadable with: block is reported by auditRuntimeVersions.
		return nil
	}
	mutable := func(detail string) []Finding {
		return []Finding{{
			File:   file,
			Rule:   RuleMutableSourceRef,
			Detail: detail,
			Remedy: "set ref: to a full 40-character commit SHA, or to an expression naming the tagged commit under this workflow's events",
		}}
	}
	ref, refSpellings := inputByName(with, "ref")
	_, repositorySpellings := inputByName(with, "repository")
	if refSpellings > 1 || repositorySpellings > 1 {
		return mutable("the same source input is spelled in more than one way, so the value the runner would use cannot be established")
	}
	if ref == nil {
		if repositorySpellings == 1 && (reusable || fetchesSource(uses)) {
			return mutable("a repository: is selected without a ref:, so the source fetched is whatever that repository's default branch holds when the job runs")
		}
		return nil
	}
	if ref.kind != nodeScalar || ref.style == scalarBlock {
		return mutable("a ref: input is not a readable literal")
	}
	text := strings.TrimSpace(ref.value)
	switch {
	case isHexadecimal(text, 40):
		return nil
	case isStepOutputExpression(text):
		if policy.TrustJobOutputs {
			return nil
		}
		return mutable(fmt.Sprintf("the ref %q is the output of an earlier job or step, which this audit cannot see; it is accepted only under a policy that trusts job outputs", text))
	case isExpression(text, "github.sha"):
		if allTagCommitEvents(events) {
			return nil
		}
		return mutable(fmt.Sprintf("the ref %q names the tagged commit only under a push, create or release event, and this workflow declares %s", text, describeEvents(events)))
	case isExpression(text, "job.workflow_sha"):
		if allWorkflowShaEvents(events) {
			return nil
		}
		return mutable(fmt.Sprintf("the ref %q names this workflow file's commit, which under %s is not the tagged one", text, describeEvents(events)))
	case isExpression(text, "github.event.workflow_run.head_sha"):
		if len(events) == 1 && events[0] == "workflow_run" {
			return nil
		}
		return mutable(fmt.Sprintf("the ref %q is the upstream run's commit only when workflow_run is the sole event, and this workflow declares %s", text, describeEvents(events)))
	}
	return mutable(fmt.Sprintf("the ref %q is not an immutable commit, so rerunning the same release tag can build different source", text))
}

// allTagCommitEvents reports whether every declared event is one under which
// github.sha names the tagged commit. No readable events is not proof.
func allTagCommitEvents(events []string) bool {
	if len(events) == 0 {
		return false
	}
	for _, event := range events {
		if _, ok := tagCommitEvents[event]; !ok {
			return false
		}
	}
	return true
}

// allWorkflowShaEvents reports whether every declared event is one under
// which job.workflow_sha names a commit the tag fixes: a tag-commit event, or
// workflow_call, where it is the callee's commit the caller pinned. A file
// that is also dispatchable or scheduled runs under those events with a
// moving commit, so one such event withdraws the proof.
func allWorkflowShaEvents(events []string) bool {
	if len(events) == 0 {
		return false
	}
	for _, event := range events {
		if _, ok := tagCommitEvents[event]; ok || event == "workflow_call" {
			continue
		}
		return false
	}
	return true
}

func declaresEvent(events []string, wanted string) bool {
	for _, event := range events {
		if event == wanted {
			return true
		}
	}
	return false
}

func describeEvents(events []string) string {
	if len(events) == 0 {
		return "no readable event"
	}
	return strings.Join(events, ", ")
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

// isExpression reports whether a value is exactly one expression with the
// given body, compared case-insensitively as GitHub evaluates contexts.
func isExpression(text, body string) bool {
	if !strings.HasPrefix(text, "${{") || !strings.HasSuffix(text, "}}") || strings.Count(text, "${{") != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(text[len("${{"):len(text)-len("}}")]), body)
}

// setupSelectors lists, for the setup actions the audit knows, the input
// names through which each one takes its version. A selector the action does
// not read is a warning at run time and a fallback to the mutable default, so
// for a known action only its own selectors count; actions/setup-node with
// go-version: 1.22.0 states nothing about Node. Names are normalised the way
// isRuntimeSelector normalises them.
var setupSelectors = map[string][]string{
	"abatilo/actions-poetry":                 {"poetry-version"},
	"actions-rs/toolchain":                   {"toolchain"},
	"actions-rust-lang/setup-rust-toolchain": {"toolchain"},
	"actions/setup-dotnet":                   {"dotnet-version", "global-json-file"},
	"actions/setup-go":                       {"go-version", "go-version-file"},
	"actions/setup-java":                     {"java-version", "java-version-file"},
	"actions/setup-node":                     {"node-version", "node-version-file"},
	"actions/setup-python":                   {"python-version", "python-version-file"},
	"astral-sh/setup-uv":                     {"version", "version-file"},
	"azure/setup-helm":                       {"version"},
	"azure/setup-kubectl":                    {"version"},
	"dart-lang/setup-dart":                   {"sdk"},
	"denoland/setup-deno":                    {"deno-version", "deno-version-file"},
	"docker/setup-buildx-action":             {"version"},
	"dtolnay/rust-toolchain":                 {"toolchain"},
	"erlef/setup-beam":                       {"otp-version", "elixir-version", "gleam-version", "version-file"},
	"golangci/golangci-lint-action":          {"version"},
	"goreleaser/goreleaser-action":           {"version"},
	"haskell-actions/setup":                  {"ghc-version", "cabal-version", "stack-version"},
	"hashicorp/setup-terraform":              {"terraform-version"},
	"jdx/mise-action":                        {"version"},
	"julia-actions/setup-julia":              {"version"},
	"mlugg/setup-zig":                        {"version"},
	"opentofu/setup-opentofu":                {"tofu-version"},
	"oven-sh/setup-bun":                      {"bun-version", "bun-version-file"},
	"pnpm/action-setup":                      {"version", "package-json-file"},
	"ruby/setup-ruby":                        {"ruby-version"},
	"shivammathur/setup-php":                 {"php-version", "php-version-file"},
	"sigstore/cosign-installer":              {"cosign-release"},
	"subosito/flutter-action":                {"flutter-version", "flutter-version-file"},
	"swift-actions/setup-swift":              {"swift-version"},
}

// auditRuntimeSetup requires a step that installs a runtime to say which one,
// through an input the action reads. A setup action invoked with no version
// input falls back to whatever the action defaults to or whatever the runner
// image already carries, and both change as the image is updated, so the same
// tag builds against a different toolchain later. For an action on the
// setupSelectors list only its own inputs count, and their values are checked
// like any runtime selector. For an action the list does not know, one taken
// to install a runtime because its name says setup, install or toolchain, any
// runtime selector or version file counts: which inputs it reads is in its own
// action.yml, outside this directory-only audit. A refused step is fixed by
// stating the version through the right input.
func auditRuntimeSetup(file string, uses, with *node, policy Policy) []Finding {
	if !installsRuntime(uses) {
		return nil
	}
	if with != nil && with.kind != nodeNull && with.kind != nodeMapping {
		// An unreadable with: block is reported by auditRuntimeVersions.
		return nil
	}
	reference := strings.TrimSpace(uses.value)
	action := strings.ToLower(reference)
	if at := strings.LastIndex(action, "@"); at >= 0 {
		action = action[:at]
	}
	selectors, known := setupSelectors[action]
	var findings []Finding
	stated := false
	if with != nil && with.kind == nodeMapping {
		for _, key := range with.keys {
			normalised := normaliseInputName(key)
			switch {
			case known && containsString(selectors, normalised):
				stated = true
				if !isRuntimeSelector(key) && !isRuntimeFileSelector(key) {
					// A selector isRuntimeSelector does not recognise by
					// shape, such as cosign-release, is checked here.
					findings = append(findings, auditRuntimeValue(file, key, with.values[key], policy)...)
				}
			case !known && (isRuntimeSelector(key) || isRuntimeFileSelector(key)):
				stated = true
			}
		}
	}
	if stated {
		return findings
	}
	remedy := "give the setup action an exact version input"
	if known {
		remedy = fmt.Sprintf("state the version through %s", strings.Join(quoteAll(selectors), " or "))
	}
	return append(findings, Finding{
		File:   file,
		Rule:   RuleFloatingRuntimeVersion,
		Detail: fmt.Sprintf("the step invokes the runtime setup action %q without stating a version through an input it reads, so it installs whatever the action or the runner image defaults to when the job runs", reference),
		Remedy: remedy,
	})
}

func normaliseInputName(key string) string {
	return strings.ReplaceAll(strings.ToLower(key), "_", "-")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// runtimeInstallers are actions that install a versioned toolchain without
// saying so in their name, so the name heuristic alone would miss them.
var runtimeInstallers = map[string]struct{}{
	"abatilo/actions-poetry":        {},
	"golangci/golangci-lint-action": {},
	"goreleaser/goreleaser-action":  {},
	"jdx/mise-action":               {},
	"subosito/flutter-action":       {},
}

// runtimeAgnosticSetups are actions whose name says setup but which install
// no versioned toolchain the workflow could state: Homebrew has no exact
// version input at all, and the QEMU action registers an emulator image
// rather than a runtime. Refusing them would be a refusal an adopter cannot
// satisfy.
var runtimeAgnosticSetups = map[string]struct{}{
	"docker/setup-qemu-action":        {},
	"homebrew/actions/setup-homebrew": {},
}

// installsRuntime reports whether a readable uses: value names an action that
// installs a toolchain: one on the installer list, or one whose name says
// setup, install or toolchain and is not on the runtime-agnostic list. Action
// paths are compared case-insensitively, as GitHub resolves them.
func installsRuntime(uses *node) bool {
	if uses == nil || uses.kind != nodeScalar || uses.style == scalarBlock {
		return false
	}
	reference := strings.ToLower(strings.TrimSpace(uses.value))
	if at := strings.LastIndex(reference, "@"); at >= 0 {
		reference = reference[:at]
	}
	if _, installer := runtimeInstallers[reference]; installer {
		return true
	}
	if _, agnostic := runtimeAgnosticSetups[reference]; agnostic {
		return false
	}
	name := reference[strings.LastIndex(reference, "/")+1:]
	return strings.Contains(name, "setup") || strings.Contains(name, "install") || strings.Contains(name, "toolchain")
}

// isRuntimeFileSelector reports whether a with: input names a file in the
// tagged source that carries the version: node-version-file, go-version-file
// and the like, the .NET global-json-file, or pnpm's package-json-file. Such
// a file is fixed by the tag; its content is outside this directory-only
// audit and is not read here.
func isRuntimeFileSelector(key string) bool {
	normalised := strings.ReplaceAll(strings.ToLower(key), "_", "-")
	return normalised == "version-file" || normalised == "global-json-file" || normalised == "package-json-file" ||
		strings.HasSuffix(normalised, "-version-file")
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
func auditRuntimeVersions(file string, with *node, policy Policy) []Finding {
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
		findings = append(findings, auditRuntimeValue(file, key, with.values[key], policy)...)
	}
	return findings
}

// auditRuntimeValue checks one runtime selector's value: a readable literal
// that is an exact version, or, under Policy.TrustJobOutputs, the output of a
// job or step.
func auditRuntimeValue(file, key string, value *node, policy Policy) []Finding {
	if value == nil || value.kind != nodeScalar || value.style == scalarBlock {
		return []Finding{{
			File:   file,
			Rule:   RuleFloatingRuntimeVersion,
			Detail: fmt.Sprintf("the runtime input %q is not a readable literal", key),
			Remedy: "give the runtime an exact version as a plain or quoted scalar",
		}}
	}
	text := strings.TrimSpace(value.value)
	if strings.Contains(text, "${{") {
		if isStepOutputExpression(text) && policy.TrustJobOutputs {
			return nil
		}
		return []Finding{{
			File:   file,
			Rule:   RuleFloatingRuntimeVersion,
			Detail: fmt.Sprintf("the runtime input %q resolves through the expression %q, whose value is not fixed by the tagged workflow source", key, text),
			Remedy: fmt.Sprintf("set %s to an exact version", key),
		}}
	}
	if isFloatingVersion(text) {
		return []Finding{{
			File:   file,
			Rule:   RuleFloatingRuntimeVersion,
			Detail: fmt.Sprintf("the runtime input %q is set to the floating value %q, so the same tag can build against different toolchains", key, text),
			Remedy: fmt.Sprintf("set %s to an exact version", key),
		}}
	}
	return nil
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
// steps.<id>.outputs.<name>. What the producing step computes is not visible
// here, so the shape is accepted only under Policy.TrustJobOutputs, which
// says why. Every other expression root, including vars, inputs, env, github
// and matrix, would need the audit to evaluate expressions to bound, and it
// evaluates none, so it is refused under every policy.
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

// isExactVersion requires a complete major.minor.patch, or a four-component
// version for the tools that number that way, optionally prefixed
// with v and optionally carrying a prerelease and a build suffix in semver
// form: dot-separated identifiers of letters, digits and hyphens, and nothing
// after them. Everything past the core is validated rather than discarded,
// because a range such as "20.0.0+meta || 22" would otherwise pass on its
// first three numbers while its alternative floats.
func isExactVersion(version string) bool {
	version = strings.TrimPrefix(version, "v")
	core := version
	if index := strings.IndexAny(version, "-+"); index >= 0 {
		core = version[:index]
		suffix := version[index:]
		if build := strings.Index(suffix, "+"); build >= 0 {
			if !isSemverIdentifiers(suffix[build+1:]) {
				return false
			}
			suffix = suffix[:build]
		}
		if suffix != "" && (suffix[0] != '-' || !isSemverIdentifiers(suffix[1:])) {
			return false
		}
	}
	// Three components is semver; a fourth is accepted for the tools that
	// version that way, Cabal among them.
	parts := strings.Split(core, ".")
	if len(parts) != 3 && len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if !isDigits(part) {
			return false
		}
	}
	return true
}

// isSemverIdentifiers reports whether text is one or more dot-separated
// identifiers made of letters, digits and hyphens, as a semver prerelease or
// build suffix is.
func isSemverIdentifiers(text string) bool {
	if text == "" {
		return false
	}
	for _, identifier := range strings.Split(text, ".") {
		if identifier == "" {
			return false
		}
		for index := 0; index < len(identifier); index++ {
			character := identifier[index]
			switch {
			case character >= '0' && character <= '9':
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character == '-':
			default:
				return false
			}
		}
	}
	return true
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for index := 0; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			return false
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
