package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/cli"
)

// Three of the six submodules claude-peers-suite carries, in the exact form
// its .gitmodules writes them. That file is the adopter this check was
// written against; three is enough to prove the comparison and keeps the
// fixture readable.
const suiteGitmodules = `[submodule "components/claude-peers"]
	path = components/claude-peers
	url = https://github.com/SijanC147/claude-peers-mcp.git
[submodule "components/worktree-peers"]
	path = components/worktree-peers
	url = https://github.com/SijanC147/worktree-peers.git
[submodule "components/kitty-skills"]
	path = components/kitty-skills
	url = https://github.com/SijanC147/kitty-skills.git
`

func suiteManifest(t *testing.T, submodules string, allowed []string) string {
	t.Helper()
	quoted := make([]string, 0, len(allowed))
	for _, entry := range allowed {
		quoted = append(quoted, `"`+entry+`"`)
	}
	checkout := `"checkout": {"submodules": "` + submodules + `"`
	if len(quoted) > 0 {
		checkout += `, "submodules_allowed": [` + strings.Join(quoted, ", ") + `]`
	}
	checkout += `},`
	return `{
  "schema": 2,
  "formula": {
    "name": "claude-peers",
    "class": "ClaudePeers",
    "description": "Peer messaging for Claude Code",
    "homepage": "https://github.com/SijanC147/claude-peers-suite",
    "license": "MIT",
    "repository": {"owner": "SijanC147", "name": "claude-peers-suite"},
    "binary": "cpeers",
    "assets": {
      "darwin_arm64": "claude-peers-macos-arm64.tar.gz",
      "darwin_amd64": "claude-peers-macos-x86_64.tar.gz"
    }
  },
  "release": {
    "build_script": "scripts/hextap-build",
    ` + checkout + `
    "profile": {
      "runtime": "bun",
      "runtime_version": "1.4.2",
      "install": {"name": "install", "argv": ["bun", "install", "--frozen-lockfile"]},
      "quality": [{"name": "test", "argv": ["bun", "test"]}],
      "prepare": []
    },
    "targets": {
      "darwin_arm64": {
        "binary": "cpeers-macos-arm64",
        "archive": "claude-peers-macos-arm64.tar.gz",
        "archive_contents": "binary"
      },
      "darwin_amd64": {
        "binary": "cpeers-macos-x86_64",
        "archive": "claude-peers-macos-x86_64.tar.gz",
        "archive_contents": "binary"
      }
    }
  },
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "formula_profile": "claude-peers",
    "service_enabled": true
  }
}`
}

func runSubmodules(t *testing.T, manifestBody, gitmodulesBody string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "project-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(manifestBody), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	gitmodulesPath := filepath.Join(dir, ".gitmodules")
	if gitmodulesBody != "" {
		if err := os.WriteFile(gitmodulesPath, []byte(gitmodulesBody), 0o600); err != nil {
			t.Fatalf("write .gitmodules: %v", err)
		}
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "submodules",
		"--manifest", manifestPath,
		"--gitmodules", gitmodulesPath,
		"--server-url", "https://github.com",
	}, &stdout, &stderr, "test", "test")
	return code, stdout.String(), stderr.String()
}

var suiteURLs = []string{
	"https://github.com/SijanC147/claude-peers-mcp.git",
	"https://github.com/SijanC147/worktree-peers.git",
	"https://github.com/SijanC147/kitty-skills.git",
}

// The adopter this exists for passes once its manifest seals what its
// .gitmodules declares.
func TestTheSuiteManifestIsAcceptedOnceTheFieldIsFilled(t *testing.T) {
	code, stdout, stderr := runSubmodules(t, suiteManifest(t, "recursive", suiteURLs), suiteGitmodules)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "3 declared") {
		t.Errorf("stdout did not report what it checked: %s", stdout)
	}
}

// Omit one URL and the release stops. This is the acceptance line of
// SB23-2504: a tagged commit that adds a submodule outside the sealed list
// fails in the validate job, before any checkout that carries the credential.
func TestAManifestWhoseListOmitsOneURLIsRejected(t *testing.T) {
	code, stdout, stderr := runSubmodules(t, suiteManifest(t, "recursive", suiteURLs[:2]), suiteGitmodules)
	if code == 0 {
		t.Fatalf("exit 0, want failure; the credential would be presented to a repository nothing sealed\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr, "kitty-skills") || !strings.Contains(stderr, "submodules_allowed") {
		t.Errorf("the error does not name the unsealed URL and the field to fix: %s", stderr)
	}
}

// The attack in the issue: the tag edits .gitmodules to add a repository the
// credential can read. The manifest is untouched and still valid.
func TestATaggedCommitThatAddsAnUnsealedSubmoduleIsRejected(t *testing.T) {
	hostile := suiteGitmodules + `[submodule "components/exfiltrated"]
	path = components/exfiltrated
	url = https://github.com/SijanC147/some-other-private-repo.git
`
	code, _, stderr := runSubmodules(t, suiteManifest(t, "recursive", suiteURLs), hostile)
	if code == 0 {
		t.Fatal("exit 0, want failure; a tagged commit steered the credential at a repository the manifest never sealed")
	}
	if !strings.Contains(stderr, "some-other-private-repo") {
		t.Errorf("the error does not name the repository that was steered at: %s", stderr)
	}
}

// SB23-2506: the credential is one http.<origin>/.extraheader, so a submodule
// on another host cannot be authenticated by it. That is refused here rather
// than discovered at checkout time.
func TestASubmoduleOnAnotherHostIsRejectedByName(t *testing.T) {
	elsewhere := `[submodule "components/vendor"]
	path = components/vendor
	url = https://gitlab.example.com/SijanC147/vendor.git
`
	code, _, stderr := runSubmodules(t,
		suiteManifest(t, "recursive", []string{"https://gitlab.example.com/SijanC147/vendor.git"}),
		elsewhere)
	if code == 0 {
		t.Fatal("exit 0, want failure; the credential cannot authenticate another host and the release would fail at checkout")
	}
	if !strings.Contains(stderr, "SB23-2506") || !strings.Contains(stderr, "gitlab.example.com") {
		t.Errorf("the error does not name the restriction and the host: %s", stderr)
	}
}

// An ssh or scp-like URL cannot carry the credential at all.
func TestAnSSHSubmoduleURLIsRejected(t *testing.T) {
	for name, body := range map[string]string{
		"scp-like": "[submodule \"c\"]\n\tpath = c\n\turl = git@github.com:SijanC147/x.git\n",
		"ssh":      "[submodule \"c\"]\n\tpath = c\n\turl = ssh://git@github.com/SijanC147/x.git\n",
		"relative": "[submodule \"c\"]\n\tpath = c\n\turl = ../x.git\n",
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runSubmodules(t, suiteManifest(t, "recursive", suiteURLs), body)
			if code == 0 {
				t.Fatalf("exit 0, want failure; stderr: %s", stderr)
			}
		})
	}
}

// A manifest that fetches no submodules is not asked for a list, and the
// check says so rather than passing silently.
func TestAProjectWithoutSubmodulesPassesAndSaysWhy(t *testing.T) {
	code, stdout, stderr := runSubmodules(t, suiteManifest(t, "false", nil), "")
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "no submodule is fetched") {
		t.Errorf("stdout did not say why it passed: %s", stdout)
	}
}

// A .gitmodules this reader cannot read in full fails the release rather than
// parsing to a shorter list, because a declaration it skipped is a repository
// the credential reaches unsealed.
func TestAnUnreadableGitmodulesFailsTheRelease(t *testing.T) {
	code, _, stderr := runSubmodules(t, suiteManifest(t, "recursive", suiteURLs),
		"[submodule \"c\"\n\turl = https://github.com/SijanC147/x.git\n")
	if code == 0 {
		t.Fatal("exit 0, want failure; a .gitmodules that cannot be read in full must not be summarised")
	}
	if !strings.Contains(stderr, "gitmodules") {
		t.Errorf("the error does not name the file: %s", stderr)
	}
}
