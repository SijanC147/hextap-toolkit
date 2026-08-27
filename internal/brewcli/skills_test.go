package brewcli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsInstallAndStatusCLIUseTemporaryUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const credential = "ops_cli_skill_installer_must_not_echo_1234567890"
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", credential)

	code, stdout, stderr := execute("1.2.3", "0123456789abcdef0123456789abcdef01234567",
		"skills", "install",
		"--agent", "claude-code",
		"--scope", "user",
	)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "CREATE claude-code ") {
		t.Fatalf("Run(skills install) = %d, %q, %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, credential) || strings.Contains(stderr, credential) {
		t.Fatalf("skills install leaked credential: stdout=%q stderr=%q", stdout, stderr)
	}
	destination := filepath.Join(home, ".claude", "skills", "hextap", "SKILL.md")
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("installed skill %s: %v", destination, err)
	}

	code, stdout, stderr = execute("dev", "unknown",
		"skills", "status",
		"--agent", "claude-code",
		"--scope", "user",
	)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "CURRENT claude-code discovered_by=claude-code,cursor installed=1.1.1 available=1.1.1 action=NONE ") {
		t.Fatalf("Run(skills status) = %d, %q, %q", code, stdout, stderr)
	}
}

func TestSkillsStatusDefaultsToCompleteUserInventoryAndSupportsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code, stdout, stderr := execute("dev", "unknown", "skills", "install", "--agent", "codex", "--scope", "user"); code != 0 {
		t.Fatalf("install = %d, %q, %q", code, stdout, stderr)
	}

	code, stdout, stderr := execute("dev", "unknown", "skills", "status")
	if code != 0 || stderr != "" {
		t.Fatalf("status = %d, %q, %q", code, stdout, stderr)
	}
	for _, expected := range []string{
		"CURRENT agents+codex discovered_by=agents,codex,cursor installed=1.1.1 available=1.1.1 action=NONE ",
		"NOT_INSTALLED claude-code discovered_by=claude-code,cursor installed=- available=1.1.1 action=INSTALL ",
		"NOT_INSTALLED cursor discovered_by=cursor installed=- available=1.1.1 action=INSTALL ",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("human inventory %q is missing %q", stdout, expected)
		}
	}

	code, stdout, stderr = execute("dev", "unknown", "skills", "status", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("status JSON = %d, %q, %q", code, stdout, stderr)
	}
	var document struct {
		Schema  int    `json:"schema"`
		Scope   string `json:"scope"`
		Entries []struct {
			State            string   `json:"state"`
			Agent            string   `json:"agent"`
			DiscoveredBy     []string `json:"discovered_by"`
			InstalledVersion string   `json:"installed_version"`
			AvailableVersion string   `json:"available_version"`
			Recommendation   string   `json:"recommendation"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("decode status JSON %q: %v", stdout, err)
	}
	if document.Schema != 1 || document.Scope != "user" || len(document.Entries) != 3 || document.Entries[0].Agent != "agents+codex" || strings.Join(document.Entries[0].DiscoveredBy, ",") != "agents,codex,cursor" || document.Entries[0].InstalledVersion != "1.1.1" {
		t.Fatalf("status JSON = %#v", document)
	}
}

func TestSkillsUpgradeCLIPlansAndAppliesManagedForwardUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".claude", "skills", "hextap")
	writeOldManagedSkillFixture(t, skillDir)

	code, stdout, stderr := execute("dev", "unknown", "skills", "upgrade", "--agent", "claude-code", "--scope", "user", "--dry-run")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "UPGRADE claude-code from=0.9.0 to=1.1.1 ") || strings.Contains(stdout, "backup=") {
		t.Fatalf("upgrade dry-run = %d, %q, %q", code, stdout, stderr)
	}
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil || string(data) != "old managed skill\n" {
		t.Fatalf("upgrade dry-run changed skill: data=%q error=%v", data, err)
	}

	code, stdout, stderr = execute("dev", "unknown", "skills", "upgrade", "--agent", "claude-code", "--scope", "user")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "UPGRADE claude-code from=0.9.0 to=1.1.1 ") || !strings.Contains(stdout, " backup=") {
		t.Fatalf("upgrade = %d, %q, %q", code, stdout, stderr)
	}
	code, statusOutput, stderr := execute("dev", "unknown", "skills", "status", "--agent", "claude-code", "--scope", "user")
	if code != 0 || stderr != "" || !strings.Contains(statusOutput, "CURRENT claude-code discovered_by=claude-code,cursor installed=1.1.1 available=1.1.1 action=NONE ") {
		t.Fatalf("post-upgrade status = %d, %q, %q", code, statusOutput, stderr)
	}
}

func TestSkillsProjectDryRunUsesAgentsConvention(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repository ")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "-C", project, "init", "-q", "-b", "main")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	nested := filepath.Join(project, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	code, stdout, stderr := execute("dev", "unknown",
		"skills", "install",
		"--agent", "codex",
		"--scope", "project",
		"--project", nested,
		"--dry-run",
	)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "CREATE codex ") {
		t.Fatalf("Run(project dry-run) = %d, %q, %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, filepath.Join(project, ".agents", "skills", "hextap")) || strings.Contains(stdout, filepath.Join(nested, ".agents")) {
		t.Fatalf("project dry-run did not resolve Git top-level: %q", stdout)
	}
	destination := filepath.Join(project, ".agents", "skills", "hextap", "SKILL.md")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("dry-run destination exists: %v", err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("project dry-run touched HOME: entries=%v error=%v", entries, err)
	}
}

func TestParseGitTopLevelRecordPreservesWhitespaceAndRejectsAmbiguity(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{name: "trailing space", output: "/tmp/repository \n", want: "/tmp/repository "},
		{name: "missing terminator", output: "/tmp/repository ", wantErr: true},
		{name: "extra record", output: "/tmp/one\n/tmp/two\n", wantErr: true},
		{name: "empty record", output: "\n", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseGitTopLevelRecord([]byte(test.output))
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("parseGitTopLevelRecord(%q) = %q, %v", test.output, got, err)
			}
		})
	}
}

func TestSkillsAllRequiresExplicitOverlapAcknowledgement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, stdout, stderr := execute("dev", "unknown",
		"skills", "install", "--agent", "all", "--scope", "user",
	)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "--allow-overlapping-discovery") {
		t.Fatalf("Run(all without acknowledgement) = %d, %q, %q", code, stdout, stderr)
	}
	code, stdout, stderr = execute("dev", "unknown",
		"skills", "install", "--agent", "all", "--scope", "user", "--allow-overlapping-discovery",
	)
	if code != 0 || stderr != "" || !strings.Contains(stdout, filepath.Join(home, ".agents", "skills", "hextap")) || !strings.Contains(stdout, filepath.Join(home, ".claude", "skills", "hextap")) {
		t.Fatalf("Run(all acknowledged) = %d, %q, %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, filepath.Join(home, ".cursor")) {
		t.Fatalf("all install emitted redundant Cursor path: %q", stdout)
	}
}

func TestSkillsTargetsUsageAndErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	code, stdout, stderr := execute("dev", "unknown", "skills", "targets")
	if code != 0 || stderr != "" {
		t.Fatalf("Run(skills targets) = %d, %q, %q", code, stdout, stderr)
	}
	for _, id := range []string{"agents", "all", "claude-code", "codex", "cursor"} {
		if !strings.Contains(stdout, id) {
			t.Errorf("targets output %q is missing %q", stdout, id)
		}
	}

	for _, args := range [][]string{
		{"skills", "--help"},
		{"skills", "install", "--help"},
		{"skills", "status", "--help"},
		{"skills", "upgrade", "--help"},
	} {
		code, stdout, stderr = execute("dev", "unknown", args...)
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "usage: brew-hextap skills") {
			t.Fatalf("Run(%v) = %d, %q, %q", args, code, stdout, stderr)
		}
	}

	for _, args := range [][]string{
		{"skills"},
		{"skills", "unknown"},
		{"skills", "install", "--agent", "codex"},
		{"skills", "install", "--scope", "user"},
		{"skills", "upgrade", "--scope", "user"},
		{"skills", "install", "--agent", "unknown", "--scope", "user"},
		{"skills", "install", "--agent", "codex", "--scope", "system"},
		{"skills", "status", "--scope", "system"},
		{"skills", "install", "--agent", "codex", "--scope", "user", "extra"},
	} {
		code, stdout, stderr = execute("dev", "unknown", args...)
		if code == 0 || stdout != "" || !strings.HasPrefix(stderr, "error: skills") || !strings.HasSuffix(stderr, "\n") {
			t.Errorf("Run(%v) = %d, %q, %q", args, code, stdout, stderr)
		}
	}
}

func writeOldManagedSkillFixture(t *testing.T, skillDir string) {
	t.Helper()
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "SKILL.md"
	data := []byte("old managed skill\n")
	if err := os.WriteFile(filepath.Join(skillDir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.Sum256(data)
	fileHashString := fmt.Sprintf("%x", fileHash)
	bundleHash := sha256.Sum256([]byte(fmt.Sprintf("version:0.9.0\n%d:%s:%s\n", len(name), name, fileHashString)))
	marker := struct {
		Schema       int    `json:"schema"`
		Bundle       string `json:"bundle"`
		Version      string `json:"version"`
		BundleSHA256 string `json:"bundle_sha256"`
		Files        []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}{Schema: 1, Bundle: "hextap", Version: "0.9.0", BundleSHA256: fmt.Sprintf("%x", bundleHash)}
	marker.Files = append(marker.Files, struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}{Path: name, SHA256: fileHashString})
	encoded, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(skillDir, ".hextap-install.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
