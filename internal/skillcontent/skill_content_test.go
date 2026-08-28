package skillcontent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var skillRelativePath = func() string {
	if staged := os.Getenv("HEXTAP_SKILL_VALIDATE_DIR"); staged != "" {
		return staged
	}
	return filepath.Join("..", "..", "skills", "hextap")
}()

func readSkillFile(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(skillRelativePath, relative))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

func TestHextapSkillAgentSkillsFrontmatterAndContent(t *testing.T) {
	content := readSkillFile(t, "SKILL.md")
	frontmatter := regexp.MustCompile(`(?s)\A---\nname: hextap\ndescription: "[^"]+"\ncompatibility: "Requires Homebrew and Hextap 0\.5\.0 or newer through hextap or brew hextap\."\nlicense: MIT\nmetadata:\n  hextap-skill-version: "1\.3\.0"\n---\n`).FindString(content)
	if frontmatter == "" {
		t.Fatal("SKILL.md frontmatter is not the lean canonical shape")
	}
	for _, required := range []string{
		"hextap --help",
		"hextap onboard --dry-run",
		"hextap status --json",
		"Command discovery and inventory",
		"validate --build",
		"doctor --online",
		"homebrew-only",
		"third-party upstreams fetch-only",
		"fresh, explicit approval",
		"stable from prerelease",
		"Pair initial tap",
		"release-backed Formula",
		"dev deploy",
		"Toolkit development",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SKILL.md is missing required contract %q", required)
		}
	}
	for _, forbidden := range []string{"TODO", "v0.1.2", "~/.agents", "~/.claude", "~/.codex"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SKILL.md contains transient or agent-specific text %q", forbidden)
		}
	}
}

func TestHextapSkillReferencesResolveAndCoverContracts(t *testing.T) {
	content := readSkillFile(t, "SKILL.md")
	links := regexp.MustCompile(`\]\((references/[a-z0-9-]+\.md)\)`).FindAllStringSubmatch(content, -1)
	if len(links) < 5 {
		t.Fatalf("reference link count = %d, want at least 5", len(links))
	}
	seen := make(map[string]bool)
	for _, link := range links {
		path := link[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		if _, err := os.Stat(filepath.Join(skillRelativePath, filepath.FromSlash(path))); err != nil {
			t.Errorf("linked reference %s: %v", path, err)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("unique linked references = %d, want 5", len(seen))
	}

	allReferences := readSkillFile(t, filepath.Join("references", "onboarding-and-validation.md")) +
		readSkillFile(t, filepath.Join("references", "release-and-recovery.md")) +
		readSkillFile(t, filepath.Join("references", "safety-and-failure-routing.md")) +
		readSkillFile(t, filepath.Join("references", "toolkit-development.md")) +
		readSkillFile(t, filepath.Join("references", "command-discovery-and-inventory.md"))
	for _, required := range []string{
		"OP_SERVICE_ACCOUNT_TOKEN",
		"secrets: inherit",
		"disabled push URL",
		"tap/source manifest mismatch",
		"Hosted checks are absent or GitHub Actions is unavailable",
		"Prerelease has no Formula update",
		"paired protected tap PR",
		"--confirm-tag",
		"skills upgrade --agent",
		"never resolves feedback",
		"hextap completion zsh",
		"hextap info --kind formula --name hextap",
		"hextap -> brew-hextap",
	} {
		if !strings.Contains(allReferences, required) {
			t.Errorf("skill references are missing required contract %q", required)
		}
	}
}
