package skillinstall

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Target maps a stable agent identifier to user- and project-scoped skill
// directories. Paths are relative to the selected home or project root.
// Virtual targets expand through resolver policy and have no direct path.
//
// Keep all agent-specific filesystem knowledge in this registry so mappings
// can be reviewed against current official documentation independently of the
// installer transaction.
type Target struct {
	ID               string
	UserSkillsDir    string
	ProjectSkillsDir string
	Virtual          bool
}

var targetRegistry = []Target{
	{ID: "agents", UserSkillsDir: ".agents/skills", ProjectSkillsDir: ".agents/skills"},
	{ID: "all", Virtual: true},
	{ID: "claude-code", UserSkillsDir: ".claude/skills", ProjectSkillsDir: ".claude/skills"},
	{ID: "codex", UserSkillsDir: ".agents/skills", ProjectSkillsDir: ".agents/skills"},
	{ID: "cursor", UserSkillsDir: ".cursor/skills", ProjectSkillsDir: ".cursor/skills"},
}

// Targets returns a sorted copy of every supported agent target.
func Targets() []Target {
	result := make([]Target, len(targetRegistry))
	copy(result, targetRegistry)
	return result
}

type resolvedTarget struct {
	agent     string
	skillsDir string
}

func resolveTargets(options Options) ([]resolvedTarget, error) {
	if options.Scope != UserScope && options.Scope != ProjectScope {
		return nil, fmt.Errorf("scope must be explicitly set to %q or %q", UserScope, ProjectScope)
	}
	ids, err := normalizeAgentIDs(options.Agents)
	if err != nil {
		return nil, err
	}
	if contains(ids, "all") {
		if len(ids) != 1 {
			return nil, errorsForAllWithConcreteTargets()
		}
		if !options.AllowOverlappingDiscovery {
			return nil, overlapError()
		}
		return []resolvedTarget{
			{agent: "agents", skillsDir: pathForScope(mustTarget("agents"), options.Scope)},
			{agent: "claude-code", skillsDir: pathForScope(mustTarget("claude-code"), options.Scope)},
		}, nil
	}

	paths := make(map[string][]string)
	for _, id := range ids {
		target, _ := lookupTarget(id)
		directory := pathForScope(target, options.Scope)
		if err := validateTargetDirectory(id, directory); err != nil {
			return nil, err
		}
		paths[directory] = append(paths[directory], id)
	}
	if len(paths) > 1 && !options.AllowOverlappingDiscovery {
		return nil, overlapError()
	}

	// Cursor discovers native .cursor skills as well as .agents and .claude.
	// When another selected path already covers Cursor, an acknowledged
	// multi-agent install deliberately omits the redundant native copy.
	if contains(ids, "cursor") && len(paths) > 1 {
		cursorDir := pathForScope(mustTarget("cursor"), options.Scope)
		delete(paths, cursorDir)
		directories := sortedMapKeys(paths)
		paths[directories[0]] = append(paths[directories[0]], "cursor")
	}

	directories := sortedMapKeys(paths)
	result := make([]resolvedTarget, 0, len(directories))
	for _, directory := range directories {
		agents := deduplicateSorted(paths[directory])
		result = append(result, resolvedTarget{agent: strings.Join(agents, "+"), skillsDir: directory})
	}
	return result, nil
}

func normalizeAgentIDs(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, fmt.Errorf("at least one --agent is required")
	}
	unique := make(map[string]bool, len(requested))
	for _, id := range requested {
		if _, ok := lookupTarget(id); !ok {
			ids := make([]string, 0, len(targetRegistry))
			for _, registered := range targetRegistry {
				ids = append(ids, registered.ID)
			}
			return nil, fmt.Errorf("unknown agent %q; expected one of %s", id, strings.Join(ids, ", "))
		}
		unique[id] = true
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func lookupTarget(id string) (Target, bool) {
	for _, target := range targetRegistry {
		if target.ID == id {
			return target, true
		}
	}
	return Target{}, false
}

func mustTarget(id string) Target {
	target, ok := lookupTarget(id)
	if !ok {
		panic("missing built-in target " + id)
	}
	return target
}

func pathForScope(target Target, scope Scope) string {
	if scope == ProjectScope {
		return target.ProjectSkillsDir
	}
	return target.UserSkillsDir
}

func validateTargetDirectory(agent, directory string) error {
	clean := filepath.Clean(filepath.FromSlash(directory))
	if directory == "" || filepath.IsAbs(directory) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("agent %q has unsafe target directory %q", agent, directory)
	}
	return nil
}

func errorsForAllWithConcreteTargets() error {
	return fmt.Errorf("agent %q must be used alone", "all")
}

func overlapError() error {
	return fmt.Errorf("selected targets overlap Cursor discovery; rerun with --allow-overlapping-discovery to acknowledge shared .agents/.claude discovery")
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func deduplicateSorted(values []string) []string {
	unique := make(map[string]bool, len(values))
	for _, value := range values {
		unique[value] = true
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedMapKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
