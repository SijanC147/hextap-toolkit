package devcli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeployRunsProtectedPRMainCIAndConfirmedRelease(t *testing.T) {
	project := releaseToolkitFixture(t)
	runner := canonicalDeployRunner(t, project, false)
	service := Service{Runner: runner, Version: "dev", Commit: "unknown", Sleep: func(context.Context, time.Duration) error { return nil }}
	outcome, err := service.Deploy(context.Background(), DeployOptions{
		ReleaseOptions: ReleaseOptions{Project: project, Bump: "minor", ConfirmTag: "v0.3.0", Execute: true},
		PRTitle:        "feat: add developer orchestration",
	})
	if err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	mergeSHA := strings.Repeat("c", 40)
	if outcome.PRURL != "https://github.example/pr/6" || outcome.Commit != mergeSHA || outcome.Tag != "v0.3.0" || outcome.MainRunURL != "https://github.example/main-merge-run" {
		t.Fatalf("Deploy() = %#v", outcome)
	}
	keys := commandKeys(runner.commands)
	for _, required := range []string{
		"git -C " + project + " push --set-upstream origin HEAD:codex/dev-release",
		"gh pr checks 6 --repo " + ToolkitRepository + " --watch --interval 10",
		"gh pr merge 6 --repo " + ToolkitRepository + " --merge --delete-branch",
		"git -C " + project + " tag -a v0.3.0 " + mergeSHA + " -m hextap-toolkit v0.3.0",
	} {
		if !containsString(keys, required) {
			t.Errorf("deploy commands %v are missing %q", keys, required)
		}
	}
	for _, key := range keys {
		if strings.Contains(key, "--admin") || strings.Contains(key, "--auto") || strings.Contains(key, "threads resolve") || strings.Contains(key, "force") {
			t.Errorf("deploy used forbidden bypass command %q", key)
		}
	}
}

func TestDeployStopsOnUnresolvedReviewBeforeMergeOrTag(t *testing.T) {
	project := releaseToolkitFixture(t)
	runner := canonicalDeployRunner(t, project, true)
	service := Service{Runner: runner, Version: "dev", Commit: "unknown", Sleep: func(context.Context, time.Duration) error { return nil }}
	_, err := service.Deploy(context.Background(), DeployOptions{
		ReleaseOptions: ReleaseOptions{Project: project, Bump: "minor", ConfirmTag: "v0.3.0", Execute: true},
	})
	if err == nil || !strings.Contains(err.Error(), "unresolved review") {
		t.Fatalf("Deploy(unresolved review) error = %v", err)
	}
	for _, key := range commandKeys(runner.commands) {
		if strings.Contains(key, " pr merge ") || strings.Contains(key, " tag -a ") || strings.Contains(key, "refs/tags/v0.3.0") {
			t.Fatalf("blocked deploy performed later mutation %q", key)
		}
	}
}

func TestDeployResumesAfterAlreadyMergedPullRequestWithoutRepublishingBranch(t *testing.T) {
	project := releaseToolkitFixture(t)
	runner := canonicalDeployRunner(t, project, false)
	original := runner.handler
	featureSHA := strings.Repeat("b", 40)
	mergeSHA := strings.Repeat("c", 40)
	mergedList, _ := json.Marshal([]map[string]any{{"number": 6, "url": "https://github.example/pr/6", "headRefOid": featureSHA, "state": "MERGED"}})
	runner.handler = func(command Command) (Result, error) {
		switch commandKey(command) {
		case "git -C " + project + " rev-parse origin/main":
			return Result{Stdout: mergeSHA + "\n"}, nil
		case "gh pr list --repo " + ToolkitRepository + " --head codex/dev-release --state merged --json number,url,headRefOid,state":
			return Result{Stdout: string(mergedList) + "\n"}, nil
		default:
			return original(command)
		}
	}
	service := Service{Runner: runner, Version: "dev", Commit: "unknown", Sleep: func(context.Context, time.Duration) error { return nil }}
	outcome, err := service.Deploy(context.Background(), DeployOptions{ReleaseOptions: ReleaseOptions{Project: project, Bump: "minor", ConfirmTag: "v0.3.0", Execute: true}})
	if err != nil || outcome.Commit != mergeSHA || outcome.PRURL != "https://github.example/pr/6" {
		t.Fatalf("Deploy(resume merged) = %#v, %v", outcome, err)
	}
	for _, key := range commandKeys(runner.commands) {
		if strings.Contains(key, " push --set-upstream ") || strings.Contains(key, " pr checks ") || strings.Contains(key, " pr merge ") {
			t.Fatalf("merged resume repeated branch/PR mutation %q", key)
		}
	}
}

func canonicalDeployRunner(t *testing.T, project string, unresolved bool) *scriptedRunner {
	t.Helper()
	featureSHA := strings.Repeat("b", 40)
	mainBefore := strings.Repeat("a", 40)
	mergeSHA := strings.Repeat("c", 40)
	base := canonicalStatusRunner(t, project)
	original := base.handler
	prList, _ := json.Marshal([]map[string]any{{"number": 6, "url": "https://github.example/pr/6", "headRefOid": featureSHA, "state": "OPEN"}})
	prOpen, _ := json.Marshal(map[string]any{
		"number": 6, "url": "https://github.example/pr/6", "state": "OPEN", "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "headRefOid": featureSHA,
		"statusCheckRollup": []map[string]any{{"status": "COMPLETED", "conclusion": "SUCCESS", "name": "Toolkit"}},
	})
	prMerged, _ := json.Marshal(map[string]any{"state": "MERGED", "mergedAt": "2026-08-27T00:00:00Z", "mergeCommit": map[string]any{"oid": mergeSHA}, "url": "https://github.example/pr/6"})
	threads := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":true}]}}}}}` + "\n"
	if unresolved {
		threads = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false}]}}}}}` + "\n"
	}
	mainRuns, _ := json.Marshal([]map[string]any{{"databaseId": 303, "headSha": mergeSHA, "status": "completed", "conclusion": "success", "name": "CI", "url": "https://github.example/main-merge-run"}})
	releaseRuns, _ := json.Marshal([]map[string]any{{"databaseId": 404, "headBranch": "v0.3.0", "headSha": mergeSHA, "status": "completed", "conclusion": "success", "name": "Hextap toolkit release", "url": "https://github.example/release-run"}})
	originMainCalls := 0
	base.handler = func(command Command) (Result, error) {
		key := commandKey(command)
		if result, ok := tapVerificationFake(key); ok {
			return result, nil
		}
		switch key {
		case "git -C " + project + " fetch origin main --tags":
			return Result{}, nil
		case "git -C " + project + " rev-parse origin/main":
			originMainCalls++
			if originMainCalls == 1 {
				return Result{Stdout: mainBefore + "\n"}, nil
			}
			return Result{Stdout: mergeSHA + "\n"}, nil
		case "gh pr list --repo " + ToolkitRepository + " --head codex/dev-release --state merged --json number,url,headRefOid,state":
			return Result{Stdout: "[]\n"}, nil
		case "git -C " + project + " merge-base --is-ancestor origin/main HEAD":
			return Result{}, nil
		case "git -C " + project + " rev-list --count origin/main..HEAD":
			return Result{Stdout: "3\n"}, nil
		case "git -C " + project + " push --set-upstream origin HEAD:codex/dev-release":
			return Result{}, nil
		case "gh pr list --repo " + ToolkitRepository + " --head codex/dev-release --state open --json number,url,headRefOid,state":
			return Result{Stdout: string(prList) + "\n"}, nil
		case "gh pr checks 6 --repo " + ToolkitRepository + " --watch --interval 10":
			return Result{}, nil
		case "gh pr view 6 --repo " + ToolkitRepository + " --json number,url,state,mergeable,mergeStateStatus,headRefOid,statusCheckRollup":
			return Result{Stdout: string(prOpen) + "\n"}, nil
		case "gh api graphql --hostname github.com -f query=" + reviewThreadsQuery + " -F owner=" + ToolkitOwner + " -F name=hextap-toolkit -F number=6":
			return Result{Stdout: threads}, nil
		case "gh pr merge 6 --repo " + ToolkitRepository + " --merge --delete-branch":
			return Result{}, nil
		case "gh pr view 6 --repo " + ToolkitRepository + " --json state,mergedAt,mergeCommit,url":
			return Result{Stdout: string(prMerged) + "\n"}, nil
		case "gh run list --repo " + ToolkitRepository + " --branch main --event push --limit 20 --json databaseId,headSha,status,conclusion,name,url":
			return Result{Stdout: string(mainRuns) + "\n"}, nil
		case "gh run watch 303 --repo " + ToolkitRepository + " --exit-status --interval 10":
			return Result{}, nil
		case "git -C " + project + " tag --list v0.3.0":
			return Result{}, nil
		case "git -C " + project + " ls-remote --tags origin refs/tags/v0.3.0 refs/tags/v0.3.0^{}":
			return Result{}, nil
		case "git -C " + project + " tag -a v0.3.0 " + mergeSHA + " -m hextap-toolkit v0.3.0":
			return Result{}, nil
		case "git -C " + project + " push origin refs/tags/v0.3.0":
			return Result{}, nil
		case "gh run list --repo " + ToolkitRepository + " --event push --limit 30 --json databaseId,headBranch,headSha,status,conclusion,name,url":
			return Result{Stdout: string(releaseRuns) + "\n"}, nil
		case "gh run watch 404 --repo " + ToolkitRepository + " --exit-status --interval 10":
			return Result{}, nil
		case "gh release view v0.3.0 --repo " + ToolkitRepository + " --json tagName,isDraft,isPrerelease,isImmutable,assets,url":
			return Result{Stdout: validReleaseViewJSON("v0.3.0")}, nil
		case "gh release verify v0.3.0 --repo " + ToolkitRepository:
			return Result{}, nil
		case "git -C " + project + " ls-files -z --cached --others --exclude-standard":
			return fixtureListing("go.mod", ".hextap.json", "scripts/check-actionlint.sh"), nil
		case "gofmt -l .", "git -C " + project + " diff --check", filepath.Join(project, "scripts", "check-actionlint.sh"), "bash -n " + filepath.Join(project, "scripts", "check-actionlint.sh"), "shellcheck " + filepath.Join(project, "scripts", "check-actionlint.sh"):
			return Result{}, nil
		}
		if strings.HasPrefix(key, "go ") {
			return Result{Stdout: "ok\n"}, nil
		}
		result, err := original(command)
		if err != nil {
			return Result{}, fmt.Errorf("deploy fake: %w", err)
		}
		return result, nil
	}
	return base
}

func commandKeys(commands []Command) []string {
	result := make([]string, len(commands))
	for index, command := range commands {
		result[index] = commandKey(command)
	}
	return result
}
