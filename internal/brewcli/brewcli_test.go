package brewcli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func execute(version, commit string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr, version, commit)
	return code, stdout.String(), stderr.String()
}

func TestStableBuildDefaultsOnboardPinAndValidate(t *testing.T) {
	project := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod":    "module github.com/SijanC147/cli-fixture\n\ngo 1.26\n",
		"main.go":   "package main\nimport \"fmt\"\nvar version = \"dev\"\nvar commit = \"unknown\"\nfunc main() { fmt.Printf(\"cli-fixture %s (commit %s)\\n\", version, commit) }\n",
		"LICENSE":   "MIT\n",
		"README.md": "# CLI fixture\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/SijanC147/cli-fixture.git"}} {
		if output, err := exec.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	const commit = "0123456789abcdef0123456789abcdef01234567"
	const credential = "ops_1234567890clioutputsecret"
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", credential)
	code, stdout, stderr := execute("v1.2.3", commit,
		"onboard", "--project", project,
		"--description", "CLI fixture", "--license", "MIT", "--go-package", ".",
		"--required-check", "test", "--linux=false",
	)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "CREATE .hextap.json\n") {
		t.Fatalf("Run(onboard) = %d, %q, %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, credential) || strings.Contains(stderr, credential) {
		t.Fatalf("Run(onboard) leaked credential: stdout=%q stderr=%q", stdout, stderr)
	}
	workflow, err := os.ReadFile(filepath.Join(project, ".github", "workflows", "hextap-release.yml"))
	if err != nil || !bytes.Contains(workflow, []byte("@"+commit+" # v1.2.3")) {
		t.Fatalf("workflow pin = %q, error = %v", workflow, err)
	}
	code, stdout, stderr = execute("dev", "unknown", "validate", "--project", project)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "VALIDATED ") {
		t.Fatalf("Run(validate) = %d, %q, %q", code, stdout, stderr)
	}
}

func TestVersionAliases(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		code, stdout, stderr := execute("v1.2.3", "0123456789abcdef0123456789abcdef01234567", args...)
		if code != 0 || stdout != "brew-hextap v1.2.3 (commit 0123456789abcdef0123456789abcdef01234567)\n" || stderr != "" {
			t.Fatalf("Run(%v) = %d, %q, %q", args, code, stdout, stderr)
		}
	}
}

func TestUsageAndCommandErrors(t *testing.T) {
	code, stdout, stderr := execute("dev", "unknown", "--help")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "usage: brew-hextap") {
		t.Fatalf("Run(--help) = %d, %q, %q", code, stdout, stderr)
	}
	for _, command := range []string{"onboard", "validate", "doctor"} {
		code, stdout, stderr = execute("dev", "unknown", command, "--help")
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "usage: brew-hextap "+command) {
			t.Fatalf("Run(%s --help) = %d, %q, %q", command, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{nil, {"unknown"}, {"version", "extra"}, {"doctor", "extra"}, {"validate", "--wat"}, {"onboard", "--required-check", "test"}} {
		code, stdout, stderr = execute("dev", "unknown", args...)
		if code == 0 || stdout != "" || !strings.HasPrefix(stderr, "error: ") || !strings.HasSuffix(stderr, "\n") {
			t.Errorf("Run(%v) = %d, %q, %q", args, code, stdout, stderr)
		}
	}
}
