package manifest

import "fmt"

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
	return WorkflowExport{
		Formula:        m.Formula.Name,
		Binary:         m.Formula.Binary,
		Owner:          m.Formula.Repository.Owner,
		RepositoryName: m.Formula.Repository.Name,
		Repository:     m.RepositorySlug(),
		ARM64Asset:     m.Formula.Assets.DarwinARM64,
		AMD64Asset:     m.Formula.Assets.DarwinAMD64,
		BuildScript:    m.Release.BuildScript,
		Linux:          m.Release.Linux,
	}, nil
}
