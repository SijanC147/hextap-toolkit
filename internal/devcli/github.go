package devcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type workflowRun struct {
	DatabaseID int64  `json:"databaseId"`
	HeadBranch string `json:"headBranch"`
	HeadSHA    string `json:"headSha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
	URL        string `json:"url"`
}

func (service Service) waitForMainCI(ctx context.Context, sha string) (string, error) {
	command := Command{
		Name: "gh",
		Args: []string{"run", "list", "--repo", ToolkitRepository, "--branch", "main", "--event", "push", "--limit", "20", "--json", "databaseId,headSha,status,conclusion,name,url"},
		Env:  map[string]string{"GH_HOST": "github.com"},
	}
	return service.waitForWorkflowRun(ctx, ToolkitRepository, command, func(run workflowRun) bool {
		return run.HeadSHA == sha && run.Name == "CI"
	})
}

func (service Service) waitForReleaseWorkflow(ctx context.Context, tag, sha string) (string, error) {
	command := Command{
		Name: "gh",
		Args: []string{"run", "list", "--repo", ToolkitRepository, "--event", "push", "--limit", "30", "--json", "databaseId,headBranch,headSha,status,conclusion,name,url"},
		Env:  map[string]string{"GH_HOST": "github.com"},
	}
	return service.waitForWorkflowRun(ctx, ToolkitRepository, command, func(run workflowRun) bool {
		return run.HeadSHA == sha && run.HeadBranch == tag && run.Name == "Hextap toolkit release"
	})
}

func (service Service) waitForWorkflowRun(ctx context.Context, repository string, listCommand Command, matches func(workflowRun) bool) (string, error) {
	var selected workflowRun
	for attempt := 0; attempt < 90; attempt++ {
		runs, err := service.listWorkflowRuns(ctx, listCommand)
		if err != nil {
			return "", err
		}
		selected = workflowRun{}
		for _, run := range runs {
			if !matches(run) {
				continue
			}
			if run.DatabaseID > selected.DatabaseID {
				selected = run
			}
		}
		if selected.DatabaseID != 0 {
			break
		}
		if err := service.pause(ctx, 2*time.Second); err != nil {
			return "", err
		}
	}
	if selected.DatabaseID == 0 {
		return "", fmt.Errorf("no exact GitHub Actions run appeared")
	}
	if _, err := service.runner().Run(ctx, Command{
		Name:    "gh",
		Args:    []string{"run", "watch", strconv.FormatInt(selected.DatabaseID, 10), "--repo", repository, "--exit-status", "--interval", "10"},
		Env:     map[string]string{"GH_HOST": "github.com"},
		Timeout: 65 * time.Minute,
	}); err != nil {
		return "", fmt.Errorf("watch exact workflow run %d: %w", selected.DatabaseID, err)
	}
	runs, err := service.listWorkflowRuns(ctx, listCommand)
	if err != nil {
		return "", err
	}
	for _, run := range runs {
		if matches(run) && run.DatabaseID == selected.DatabaseID {
			if run.Status != "completed" || run.Conclusion != "success" {
				return "", fmt.Errorf("workflow run %d completed with %s/%s", run.DatabaseID, run.Status, run.Conclusion)
			}
			return run.URL, nil
		}
	}
	return "", fmt.Errorf("exact workflow run %d disappeared", selected.DatabaseID)
}

func (service Service) listWorkflowRuns(ctx context.Context, command Command) ([]workflowRun, error) {
	result, err := service.runner().Run(ctx, command)
	if err != nil {
		return nil, err
	}
	var runs []workflowRun
	if err := json.Unmarshal([]byte(result.Stdout), &runs); err != nil {
		return nil, fmt.Errorf("decode workflow runs: %w", err)
	}
	return runs, nil
}

func (service Service) pause(ctx context.Context, duration time.Duration) error {
	if service.Sleep != nil {
		return service.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
