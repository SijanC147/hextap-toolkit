package workflow

import (
	"fmt"
	"strings"
)

// TagTrigger classifies how a workflow responds to a pushed Git tag.
type TagTrigger int

const (
	// TagTriggerNone means the workflow cannot start from a tag push.
	TagTriggerNone TagTrigger = iota
	// TagTriggerFiltered means the workflow starts on tag pushes matching an
	// explicit pattern list.
	TagTriggerFiltered
	// TagTriggerAny means the workflow starts on every tag push.
	TagTriggerAny
	// TagTriggerUnknown means the file could not be read closely enough to
	// prove either answer. It is deliberately ordered above TagTriggerAny so
	// that combining classifications keeps the least certain result.
	TagTriggerUnknown
)

func (trigger TagTrigger) String() string {
	switch trigger {
	case TagTriggerNone:
		return "no tag trigger"
	case TagTriggerFiltered:
		return "filtered tag trigger"
	case TagTriggerAny:
		return "unfiltered tag trigger"
	case TagTriggerUnknown:
		return "unreadable trigger"
	default:
		return "unrecognised trigger"
	}
}

// RespondsToTagPush reports whether a workflow with this classification may
// start automatically when a tag is pushed. An unreadable trigger answers yes,
// because a file the analyser cannot understand has not been shown to be safe.
func (trigger TagTrigger) RespondsToTagPush() bool {
	return trigger != TagTriggerNone
}

// triggerAnalysis is the outcome of reading one workflow's on: block.
type triggerAnalysis struct {
	trigger  TagTrigger
	reason   string
	events   []string
	patterns []string
	// watches holds the workflow names named by a workflow_run trigger. A
	// workflow that chains off a tag-responsive workflow is itself reachable
	// from a tag push, but only the directory-wide pass can resolve that.
	watches []string
}

// knownEvents is the set of GitHub Actions workflow events. An event outside
// this set is treated as unreadable rather than harmless: the analyser refuses
// to assert anything about a trigger it does not recognise.
var knownEvents = map[string]struct{}{
	"branch_protection_rule":      {},
	"check_run":                   {},
	"check_suite":                 {},
	"create":                      {},
	"delete":                      {},
	"deployment":                  {},
	"deployment_status":           {},
	"discussion":                  {},
	"discussion_comment":          {},
	"fork":                        {},
	"gollum":                      {},
	"issue_comment":               {},
	"issues":                      {},
	"label":                       {},
	"merge_group":                 {},
	"milestone":                   {},
	"page_build":                  {},
	"project":                     {},
	"project_card":                {},
	"project_column":              {},
	"public":                      {},
	"pull_request":                {},
	"pull_request_comment":        {},
	"pull_request_review":         {},
	"pull_request_review_comment": {},
	"pull_request_target":         {},
	"push":                        {},
	"registry_package":            {},
	"release":                     {},
	"repository_dispatch":         {},
	"schedule":                    {},
	"status":                      {},
	"watch":                       {},
	"workflow_call":               {},
	"workflow_dispatch":           {},
	"workflow_run":                {},
}

// pushRefFilters is the exact set of filter keys GitHub accepts under a push
// event. An unrecognised key is refused because it may be a misspelling of a
// ref filter, and a misspelled ref filter changes which refs actually start the
// workflow.
var pushRefFilters = map[string]struct{}{
	"branches":        {},
	"branches-ignore": {},
	"tags":            {},
	"tags-ignore":     {},
	"paths":           {},
	"paths-ignore":    {},
}

var workflowRunFilters = map[string]struct{}{
	"workflows":       {},
	"types":           {},
	"branches":        {},
	"branches-ignore": {},
}

// analyzeTriggers classifies a parsed workflow document. It never returns an
// error: every failure to understand the document becomes TagTriggerUnknown
// with a reason, so that a caller cannot accidentally treat a read failure as
// an absence of triggers.
func analyzeTriggers(document *node) triggerAnalysis {
	if document == nil || document.kind != nodeMapping {
		return unreadableTrigger("the workflow is not a YAML mapping")
	}
	if !document.has("on") {
		return unreadableTrigger("the workflow declares no on: key, so its triggers cannot be established")
	}
	triggers := document.child("on")

	events, filters, err := triggerEvents(triggers)
	if err != nil {
		return unreadableTrigger(err.Error())
	}

	result := triggerAnalysis{trigger: TagTriggerNone, events: events}
	var reasons []string
	for _, event := range events {
		if _, known := knownEvents[event]; !known {
			return unreadableTrigger(fmt.Sprintf("line %d declares the unrecognised event %q", triggers.line, event))
		}
		trigger, patterns, watches, reason := classifyEvent(event, filters[event])
		if trigger > result.trigger {
			result.trigger = trigger
		}
		result.patterns = append(result.patterns, patterns...)
		result.watches = append(result.watches, watches...)
		if reason != "" {
			reasons = append(reasons, reason)
		}
	}
	result.reason = strings.Join(reasons, "; ")
	if result.reason == "" {
		result.reason = "no event in the on: block reaches a tag push"
	}
	return result
}

func unreadableTrigger(reason string) triggerAnalysis {
	return triggerAnalysis{trigger: TagTriggerUnknown, reason: reason}
}

// triggerEvents normalises the three shapes GitHub accepts for on: into an
// event list plus, where the mapping form is used, each event's filter node.
func triggerEvents(triggers *node) ([]string, map[string]*node, error) {
	filters := make(map[string]*node)
	switch {
	case triggers == nil || triggers.kind == nodeNull:
		return nil, nil, fmt.Errorf("the on: key has no value")
	case triggers.kind == nodeScalar:
		if triggers.style == scalarBlock {
			return nil, nil, fmt.Errorf("line %d writes on: as a block scalar", triggers.line)
		}
		return []string{triggers.value}, filters, nil
	case triggers.kind == nodeSequence:
		events := make([]string, 0, len(triggers.items))
		for _, item := range triggers.items {
			if item == nil || item.kind != nodeScalar || item.style == scalarBlock {
				return nil, nil, fmt.Errorf("line %d lists an unreadable event under on:", triggers.line)
			}
			events = append(events, item.value)
		}
		if len(events) == 0 {
			return nil, nil, fmt.Errorf("line %d lists no events under on:", triggers.line)
		}
		return events, filters, nil
	case triggers.kind == nodeMapping:
		if len(triggers.keys) == 0 {
			return nil, nil, fmt.Errorf("line %d maps no events under on:", triggers.line)
		}
		for _, event := range triggers.keys {
			filters[event] = triggers.values[event]
		}
		return append([]string(nil), triggers.keys...), filters, nil
	default:
		return nil, nil, fmt.Errorf("line %d has an unreadable on: block", triggers.line)
	}
}

// classifyEvent decides whether one event can start the workflow from a tag
// push, and returns any tag patterns and watched workflow names it carries.
func classifyEvent(event string, filters *node) (TagTrigger, []string, []string, string) {
	switch event {
	case "push":
		return classifyPush(filters)
	case "create":
		// GitHub fires create for a new branch or a new tag, and the event
		// accepts no ref filters at all. Pushing a tag therefore always starts
		// the workflow.
		return TagTriggerAny, nil, nil, "create fires on every new tag and cannot be filtered"
	case "workflow_run":
		return classifyWorkflowRun(filters)
	default:
		return TagTriggerNone, nil, nil, ""
	}
}

func classifyPush(filters *node) (TagTrigger, []string, []string, string) {
	if filters.isEmpty() {
		return TagTriggerAny, nil, nil, "push carries no ref filters, so every tag push starts it"
	}
	if filters.kind != nodeMapping {
		return TagTriggerUnknown, nil, nil, fmt.Sprintf("line %d has an unreadable push filter block", filters.line)
	}
	for _, key := range filters.keys {
		if _, known := pushRefFilters[key]; !known {
			return TagTriggerUnknown, nil, nil, fmt.Sprintf("line %d filters push on the unrecognised key %q", filters.line, key)
		}
	}

	hasTags := filters.has("tags")
	hasTagsIgnore := filters.has("tags-ignore")
	hasBranches := filters.has("branches")
	hasBranchesIgnore := filters.has("branches-ignore")

	if hasTags && hasTagsIgnore {
		return TagTriggerUnknown, nil, nil, fmt.Sprintf("line %d combines tags and tags-ignore, which GitHub rejects", filters.line)
	}
	if hasBranches && hasBranchesIgnore {
		return TagTriggerUnknown, nil, nil, fmt.Sprintf("line %d combines branches and branches-ignore, which GitHub rejects", filters.line)
	}

	if hasTags {
		patterns, err := stringList(filters.child("tags"))
		if err != nil {
			return TagTriggerUnknown, nil, nil, fmt.Sprintf("push tags filter is unreadable: %v", err)
		}
		switch {
		case !hasPositivePattern(patterns):
			return TagTriggerUnknown, nil, nil, fmt.Sprintf("line %d lists only negated tag patterns, which GitHub rejects", filters.line)
		case excludesEveryTag(patterns):
			return TagTriggerNone, nil, nil, ""
		}
		return TagTriggerFiltered, patterns, nil, fmt.Sprintf("push filters tags on %s", strings.Join(quoteAll(patterns), ", "))
	}
	if hasTagsIgnore {
		patterns, err := stringList(filters.child("tags-ignore"))
		if err != nil {
			return TagTriggerUnknown, nil, nil, fmt.Sprintf("push tags-ignore filter is unreadable: %v", err)
		}
		if ignoresEveryTag(patterns) {
			return TagTriggerNone, nil, nil, ""
		}
		return TagTriggerAny, nil, nil, fmt.Sprintf("push ignores only the tags %s and runs on every other tag", strings.Join(quoteAll(patterns), ", "))
	}
	if hasBranches || hasBranchesIgnore {
		return TagTriggerNone, nil, nil, ""
	}
	return TagTriggerAny, nil, nil, "push filters no refs, so every tag push starts it"
}

// hasPositivePattern reports whether a filter list carries at least one
// entry that is not negated. GitHub requires one whenever a negated entry is
// present, so a list without one describes no working trigger.
func hasPositivePattern(patterns []string) bool {
	for _, pattern := range patterns {
		if !strings.HasPrefix(pattern, "!") {
			return true
		}
	}
	return false
}

// excludesEveryTag reports whether a tags list provably matches no tag. GitHub
// evaluates the entries in order and the last matching entry decides, so a
// positive entry is cancelled when a later entry negates it exactly or
// negates everything; a list is exhaustive when every positive entry is
// cancelled, with nothing after the cancellation re-including it. Only exact
// text is compared: whether "!v*" also cancels "v[0-9]*" would need GitHub's
// glob grammar, and getting that wrong under-reports, so such a list stays
// filtered and a caller carrying it is still verified.
func excludesEveryTag(patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for index, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		cancelled := false
		for _, later := range patterns[index+1:] {
			if later == "!"+pattern || later == "!**" {
				cancelled = true
			}
		}
		if !cancelled {
			return false
		}
	}
	return true
}

// ignoresEveryTag reports whether a tags-ignore list provably excludes every
// tag, which is the one shape under which a push trigger carrying tags-ignore
// cannot start from a tag. GitHub's filter grammar gives ** alone that
// property: ** "matches zero or more of any character", whereas * "does not
// match the / character" and so leaves a tag such as release/1.0 reachable. A
// negated entry re-includes whatever it matches, so any entry starting with !
// withdraws the proof. Every other tags-ignore list still lets some tag
// through and stays tag-capable.
func ignoresEveryTag(patterns []string) bool {
	exhaustive := false
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			return false
		}
		if pattern == "**" {
			exhaustive = true
		}
	}
	return exhaustive
}

// classifyWorkflowRun records which workflows a chained trigger watches. The
// classification is completed by the directory-wide pass, which knows whether
// any watched workflow is itself reachable from a tag push.
//
// Branch filters on workflow_run are read for validity but deliberately not
// applied. GitHub reports a tag push to the chained run as a head_branch, and
// resolving whether a filter excludes it would decide safety on a value this
// analysis cannot pin down. Ignoring the filter can only over-report.
func classifyWorkflowRun(filters *node) (TagTrigger, []string, []string, string) {
	if filters.isEmpty() || filters.kind != nodeMapping {
		return TagTriggerUnknown, nil, nil, "workflow_run carries no readable filter block, so the workflows it chains from cannot be resolved"
	}
	for _, key := range filters.keys {
		if _, known := workflowRunFilters[key]; !known {
			return TagTriggerUnknown, nil, nil, fmt.Sprintf("line %d filters workflow_run on the unrecognised key %q", filters.line, key)
		}
	}
	if !filters.has("workflows") {
		return TagTriggerUnknown, nil, nil, "workflow_run names no workflows, so the workflows it chains from cannot be resolved"
	}
	watched, err := stringList(filters.child("workflows"))
	if err != nil {
		return TagTriggerUnknown, nil, nil, fmt.Sprintf("workflow_run workflows list is unreadable: %v", err)
	}
	return TagTriggerNone, nil, watched, ""
}

// stringList reads a scalar or a sequence of scalars. Block scalars are refused
// because their folding rules would make the resulting value a guess.
func stringList(list *node) ([]string, error) {
	switch {
	case list == nil || list.kind == nodeNull:
		return nil, fmt.Errorf("the list has no value")
	case list.kind == nodeScalar:
		if list.style == scalarBlock {
			return nil, fmt.Errorf("line %d writes the list as a block scalar", list.line)
		}
		return []string{list.value}, nil
	case list.kind == nodeSequence:
		if len(list.items) == 0 {
			return nil, fmt.Errorf("line %d has an empty list", list.line)
		}
		values := make([]string, 0, len(list.items))
		for _, item := range list.items {
			if item == nil || item.kind != nodeScalar || item.style == scalarBlock {
				return nil, fmt.Errorf("line %d has an unreadable list entry", list.line)
			}
			values = append(values, item.value)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("line %d is not a list", list.line)
	}
}

func quoteAll(values []string) []string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return quoted
}
