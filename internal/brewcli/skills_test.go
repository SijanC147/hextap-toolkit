package brewcli

import (
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
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "CURRENT claude-code ") {
		t.Fatalf("Run(skills status) = %d, %q, %q", code, stdout, stderr)
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
		{"skills", "install", "--agent", "unknown", "--scope", "user"},
		{"skills", "install", "--agent", "codex", "--scope", "system"},
		{"skills", "status", "--agent", "codex"},
		{"skills", "install", "--agent", "codex", "--scope", "user", "extra"},
	} {
		code, stdout, stderr = execute("dev", "unknown", args...)
		if code == 0 || stdout != "" || !strings.HasPrefix(stderr, "error: skills") || !strings.HasSuffix(stderr, "\n") {
			t.Errorf("Run(%v) = %d, %q, %q", args, code, stdout, stderr)
		}
	}
}
