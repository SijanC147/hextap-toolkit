package manifest

import (
	"fmt"
	"strings"
)

// WorkflowExport contains the validated scalar manifest values that a
// reusable workflow is allowed to consume. It intentionally excludes service
// configuration, arbitrary JSON, and multiline data.
type WorkflowExport struct {
	Formula        string
	Binary         string
	Owner          string
	RepositoryName string
	Repository     string
	ARM64Asset     string
	AMD64Asset     string
	BuildScript    string
	Linux          bool
	Runtime        string
	RuntimeVersion string
	NativeMatrix   string
}

// WorkflowExport requires the caller repository to exactly match the
// manifest's canonical repository before returning workflow scalars.
func (m Manifest) WorkflowExport(repository string) (WorkflowExport, error) {
	if err := m.Validate(); err != nil {
		return WorkflowExport{}, err
	}
	if repository != m.RepositorySlug() {
		return WorkflowExport{}, fmt.Errorf("manifest repository %q does not match input repository %q", m.RepositorySlug(), repository)
	}
	runtime := "go"
	runtimeVersion := ""
	if m.Release.Profile != nil {
		runtime = m.Release.Profile.Runtime
		runtimeVersion = m.Release.Profile.RuntimeVersion
	}
	return WorkflowExport{
		Formula:        m.Formula.Name,
		Binary:         m.Formula.Binary,
		Owner:          m.Formula.Repository.Owner,
		RepositoryName: m.Formula.Repository.Name,
		Repository:     m.RepositorySlug(),
		ARM64Asset:     m.Formula.Assets.DarwinARM64,
		AMD64Asset:     m.Formula.Assets.DarwinAMD64,
		BuildScript:    m.Release.BuildScript,
		Linux:          m.Release.LinuxEnabled(),
		Runtime:        runtime,
		RuntimeVersion: runtimeVersion,
		NativeMatrix:   nativeMatrix(m),
	}, nil
}

func nativeMatrix(project Manifest) string {
	include := make([]string, 0, 5)
	if project.Schema == LegacySchema {
		if project.Release.LinuxEnabled() {
			include = append(include,
				`{"runner":"ubuntu-24.04","target":"linux-amd64"}`,
				`{"runner":"ubuntu-24.04-arm","target":"linux-arm64"}`,
			)
		}
		include = append(include,
			`{"runner":"macos-15","target":"darwin-arm64"}`,
			`{"runner":"macos-15-intel","target":"darwin-amd64"}`,
		)
	} else {
		for _, target := range []struct {
			name   string
			runner string
		}{
			{"linux_amd64", "ubuntu-24.04"},
			{"linux_arm64", "ubuntu-24.04-arm"},
			{"darwin_arm64", "macos-15"},
			{"darwin_amd64", "macos-15-intel"},
			{"windows_amd64", "windows-2025"},
		} {
			if _, exists := project.Release.Targets[target.name]; !exists {
				continue
			}
			include = append(include, fmt.Sprintf(`{"runner":%q,"target":%q}`, target.runner, strings.ReplaceAll(target.name, "_", "-")))
		}
	}
	return `{"include":[` + strings.Join(include, ",") + `]}`
}
