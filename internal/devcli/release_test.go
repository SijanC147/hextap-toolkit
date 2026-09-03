package devcli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRequiresConfirmationBeforeRemoteMutation(t *testing.T) {
	project := releaseToolkitFixture(t)
	runner := canonicalMainRunner(t, project)
	service := Service{Runner: runner, Version: "dev", Commit: "unknown"}
	_, err := service.Release(context.Background(), ReleaseOptions{Project: project, Bump: "minor", ConfirmTag: "v0.3.0"})
	if err == nil || !strings.Contains(err.Error(), "--execute") {
		t.Fatalf("Release(no execute) error = %v", err)
	}
	for _, command := range runner.commands {
		key := commandKey(command)
		if strings.Contains(key, " fetch ") || strings.Contains(key, " tag -a ") || strings.Contains(key, " push ") || strings.Contains(key, " pr merge ") {
			t.Fatalf("Release(no execute) performed mutation command %q", key)
		}
	}
}

func TestReleaseFromCanonicalMainValidatesTagsWatchesAndVerifies(t *testing.T) {
	project := releaseToolkitFixture(t)
	runner := canonicalMainRunner(t, project)
	service := Service{Runner: runner, Version: "dev", Commit: "unknown"}
	outcome, err := service.Release(context.Background(), ReleaseOptions{
		Project: project, Bump: "minor", ConfirmTag: "v0.3.0", Execute: true,
	})
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if outcome.Tag != "v0.3.0" || outcome.Version != "0.3.0" || outcome.Commit != strings.Repeat("b", 40) || outcome.MainRunURL != "https://github.example/main-run" || outcome.ReleaseRunURL != "https://github.example/release-run" || outcome.TapRunURL != "https://github.example/tap-run" || outcome.ReleaseURL != "https://github.example/release/v0.3.0" || outcome.Installed {
		t.Fatalf("Release() = %#v", outcome)
	}
	keys := make([]string, len(runner.commands))
	for index, command := range runner.commands {
		keys[index] = commandKey(command)
	}
	for _, required := range []string{
		"git -C " + project + " fetch origin main --tags",
		"git -C " + project + " tag -a v0.3.0 " + strings.Repeat("b", 40) + " -m hextap-toolkit v0.3.0",
		"git -C " + project + " push origin refs/tags/v0.3.0",
		"gh run watch 202 --repo " + ToolkitRepository + " --exit-status --interval 10",
		"gh release view v0.3.0 --repo " + ToolkitRepository + " --json tagName,isDraft,isPrerelease,isImmutable,assets,url",
	} {
		if !containsString(keys, required) {
			t.Errorf("release commands %v are missing %q", keys, required)
		}
	}
}

func TestEnsureReleaseTagRejectsRemoteConflictBeforeCreatingLocalTag(t *testing.T) {
	project := createToolkitFixture(t)
	commit := strings.Repeat("b", 40)
	runner := &scriptedRunner{}
	runner.handler = func(command Command) (Result, error) {
		if commandKey(command) == "git -C "+project+" ls-remote --tags origin refs/tags/v0.3.0 refs/tags/v0.3.0^{}" {
			return Result{Stdout: strings.Repeat("e", 40) + "\trefs/tags/v0.3.0\n" + strings.Repeat("f", 40) + "\trefs/tags/v0.3.0^{}\n"}, nil
		}
		return Result{}, fmt.Errorf("unexpected command %s", commandKey(command))
	}
	err := (Service{Runner: runner}).ensureReleaseTag(context.Background(), project, "v0.3.0", commit)
	if err == nil || !strings.Contains(err.Error(), "remote tag") {
		t.Fatalf("ensureReleaseTag(conflict) error = %v", err)
	}
	for _, key := range commandKeys(runner.commands) {
		if strings.Contains(key, " tag -a ") || strings.Contains(key, " push ") {
			t.Fatalf("remote conflict caused local mutation %q", key)
		}
	}
}

func releaseToolkitFixture(t *testing.T) string {
	t.Helper()
	project := createToolkitFixture(t)
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "scripts", "check-actionlint.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return project
}

func canonicalMainRunner(t *testing.T, project string) *scriptedRunner {
	t.Helper()
	head := strings.Repeat("b", 40)
	base := canonicalStatusRunner(t, project)
	original := base.handler
	mainRuns, _ := json.Marshal([]map[string]any{{
		"databaseId": 101, "headSha": head, "status": "completed", "conclusion": "success", "name": "CI", "url": "https://github.example/main-run",
	}})
	releaseRuns, _ := json.Marshal([]map[string]any{{
		"databaseId": 202, "headBranch": "v0.3.0", "headSha": head, "status": "completed", "conclusion": "success", "name": "Hextap toolkit release", "url": "https://github.example/release-run",
	}})
	releaseView, _ := json.Marshal(map[string]any{
		"tagName": "v0.3.0", "isDraft": false, "isPrerelease": false, "isImmutable": true, "url": "https://github.example/release/v0.3.0",
		"assets": []map[string]any{
			{"name": "SHA256SUMS", "state": "uploaded", "digest": "sha256:" + strings.Repeat("1", 64)},
			{"name": "hextap-darwin-amd64.tar.gz", "state": "uploaded", "digest": "sha256:" + strings.Repeat("2", 64)},
			{"name": "hextap-darwin-arm64.tar.gz", "state": "uploaded", "digest": "sha256:" + strings.Repeat("3", 64)},
			{"name": "hextap-linux-amd64.tar.gz", "state": "uploaded", "digest": "sha256:" + strings.Repeat("4", 64)},
			{"name": "hextap-linux-arm64.tar.gz", "state": "uploaded", "digest": "sha256:" + strings.Repeat("5", 64)},
		},
	})
	base.handler = func(command Command) (Result, error) {
		key := commandKey(command)
		if result, ok := tapVerificationFake(key); ok {
			return result, nil
		}
		switch key {
		case "git -C " + project + " symbolic-ref --quiet --short HEAD":
			return Result{Stdout: "main\n"}, nil
		case "git -C " + project + " fetch origin main --tags":
			return Result{}, nil
		case "git -C " + project + " rev-parse origin/main":
			return Result{Stdout: head + "\n"}, nil
		case "gh run list --repo " + ToolkitRepository + " --branch main --event push --limit 20 --json databaseId,headSha,status,conclusion,name,url":
			return Result{Stdout: string(mainRuns) + "\n"}, nil
		case "gh run watch 101 --repo " + ToolkitRepository + " --exit-status --interval 10":
			return Result{}, nil
		case "git -C " + project + " tag --list v0.3.0":
			return Result{}, nil
		case "git -C " + project + " ls-remote --tags origin refs/tags/v0.3.0 refs/tags/v0.3.0^{}":
			return Result{}, nil
		case "git -C " + project + " tag -a v0.3.0 " + head + " -m hextap-toolkit v0.3.0":
			return Result{}, nil
		case "git -C " + project + " push origin refs/tags/v0.3.0":
			return Result{}, nil
		case "gh run list --repo " + ToolkitRepository + " --event push --limit 30 --json databaseId,headBranch,headSha,status,conclusion,name,url":
			return Result{Stdout: string(releaseRuns) + "\n"}, nil
		case "gh run watch 202 --repo " + ToolkitRepository + " --exit-status --interval 10":
			return Result{}, nil
		case "gh release view v0.3.0 --repo " + ToolkitRepository + " --json tagName,isDraft,isPrerelease,isImmutable,assets,url":
			return Result{Stdout: string(releaseView) + "\n"}, nil
		case "gh release verify v0.3.0 --repo " + ToolkitRepository:
			return Result{}, nil
		case "gofmt -l .":
			return Result{}, nil
		case "git -C " + project + " ls-files -z --cached --others --exclude-standard":
			return fixtureListing("go.mod", ".hextap.json", "scripts/check-actionlint.sh"), nil
		case "git -C " + project + " diff --check":
			return Result{}, nil
		case filepath.Join(project, "scripts", "check-actionlint.sh"):
			return Result{}, nil
		case "bash -n " + filepath.Join(project, "scripts", "check-actionlint.sh"):
			return Result{}, nil
		case "shellcheck " + filepath.Join(project, "scripts", "check-actionlint.sh"):
			return Result{}, nil
		}
		if strings.HasPrefix(key, "go ") {
			return Result{Stdout: "ok\n"}, nil
		}
		result, err := original(command)
		if err != nil {
			return Result{}, fmt.Errorf("release fake: %w", err)
		}
		return result, nil
	}
	return base
}

func tapVerificationFake(key string) (Result, bool) {
	tapSHA := strings.Repeat("d", 40)
	formula := `url "https://github.com/SijanC147/hextap-toolkit/releases/download/v0.3.0/hextap-darwin-arm64.tar.gz"` + "\n" +
		`url "https://github.com/SijanC147/hextap-toolkit/releases/download/v0.3.0/hextap-darwin-amd64.tar.gz"` + "\n"
	commitJSON := `[{"sha":"` + tapSHA + `","commit":{"message":"Update hextap to 0.3.0"}}]` + "\n"
	runsJSON := `[{"databaseId":505,"headSha":"` + tapSHA + `","status":"completed","conclusion":"success","name":"brew test-bot","url":"https://github.example/tap-run"}]` + "\n"
	formulaJSON := `{"encoding":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte(formula)) + `"}` + "\n"
	switch key {
	case "gh api repos/" + TapRepository + "/commits?path=Formula%2Fhextap.rb&sha=main&per_page=1 --hostname github.com":
		return Result{Stdout: commitJSON}, true
	case "gh run list --repo " + TapRepository + " --event push --limit 20 --json databaseId,headSha,status,conclusion,name,url":
		return Result{Stdout: runsJSON}, true
	case "gh run watch 505 --repo " + TapRepository + " --exit-status --interval 10":
		return Result{}, true
	case "gh api repos/" + TapRepository + "/contents/Formula/hextap.rb?ref=main --hostname github.com":
		return Result{Stdout: formulaJSON}, true
	default:
		return Result{}, false
	}
}
