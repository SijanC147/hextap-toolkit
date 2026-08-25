package manifest

import "testing"

func TestWorkflowExportRequiresExactRepository(t *testing.T) {
	project, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	got, err := project.WorkflowExport("SijanC147/claude-rc-proxy")
	if err != nil {
		t.Fatalf("WorkflowExport() error = %v", err)
	}
	if got.Formula != "claude-rc-proxy" || got.Binary != "claude-rc-proxy" || got.Owner != "SijanC147" || got.RepositoryName != "claude-rc-proxy" || got.Repository != "SijanC147/claude-rc-proxy" || got.ARM64Asset != "claude-rc-proxy-darwin-arm64.tar.gz" || got.AMD64Asset != "claude-rc-proxy-darwin-amd64.tar.gz" || got.BuildScript != "scripts/hextap-build" || !got.Linux {
		t.Fatalf("WorkflowExport() = %#v", got)
	}

	for _, repository := range []string{
		"sijanc147/claude-rc-proxy",
		"SijanC147/other",
		"SijanC147/claude-rc-proxy\ninjected=true",
		"SijanC147/claude-rc-proxy/extra",
		"",
	} {
		if _, err := project.WorkflowExport(repository); err == nil {
			t.Errorf("WorkflowExport(%q) unexpectedly succeeded", repository)
		}
	}
}
