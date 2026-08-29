package inventory

import "fmt"

// Filter returns a copy containing only one category and optional exact name.
func Filter(source Report, kind Kind, name string) (Report, error) {
	if kind != AllKind && kind != ProjectKind && kind != FormulaKind && kind != CaskKind && kind != SkillKind {
		return Report{}, fmt.Errorf("kind must be all, project, formula, cask, or skill")
	}
	result := source
	result.Projects = []ProjectInfo{}
	result.Formulae = []FormulaInfo{}
	result.Casks = []CaskInfo{}
	result.Skills = []SkillInfo{}
	result.LocalProject = nil
	if kind == AllKind || kind == ProjectKind {
		for _, entry := range source.Projects {
			if matchesProject(entry, name) {
				result.Projects = append(result.Projects, entry)
			}
		}
		if source.LocalProject != nil && matchesProject(*source.LocalProject, name) {
			value := *source.LocalProject
			result.LocalProject = &value
		}
	}
	if kind == AllKind || kind == FormulaKind {
		for _, entry := range source.Formulae {
			if name == "" || entry.Name == name || entry.FullName == name {
				result.Formulae = append(result.Formulae, entry)
			}
		}
	}
	if kind == AllKind || kind == CaskKind {
		for _, entry := range source.Casks {
			if name == "" || entry.Name == name || entry.FullName == name {
				result.Casks = append(result.Casks, entry)
			}
		}
	}
	if kind == AllKind || kind == SkillKind {
		for _, entry := range source.Skills {
			if name == "" || entry.Agent == name || containsString(entry.DiscoveredBy, name) {
				result.Skills = append(result.Skills, entry)
			}
		}
	}
	return result, nil
}

func matchesProject(entry ProjectInfo, name string) bool {
	return name == "" || entry.Name == name || entry.Repository == name
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
