package devcli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const TapRepository = "SijanC147/homebrew-hextap"

func (service Service) verifyTapPublication(ctx context.Context, version string) (string, error) {
	commitResult, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"api", "repos/" + TapRepository + "/commits?path=Formula%2Fhextap.rb&sha=main&per_page=1", "--hostname", "github.com"}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return "", err
	}
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.Unmarshal([]byte(commitResult.Stdout), &commits); err != nil || len(commits) != 1 || !isHexCommit(commits[0].SHA) || commits[0].Commit.Message != "Update hextap to "+version {
		return "", fmt.Errorf("latest tap Formula commit does not match Hextap %s", version)
	}
	tapSHA := commits[0].SHA
	listCommand := Command{Name: "gh", Args: []string{"run", "list", "--repo", TapRepository, "--event", "push", "--limit", "20", "--json", "databaseId,headSha,status,conclusion,name,url"}, Env: map[string]string{"GH_HOST": "github.com"}}
	runURL, err := service.waitForWorkflowRun(ctx, TapRepository, listCommand, func(run workflowRun) bool {
		return run.HeadSHA == tapSHA && run.Name == "brew test-bot"
	})
	if err != nil {
		return "", err
	}
	formulaResult, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"api", "repos/" + TapRepository + "/contents/Formula/hextap.rb?ref=main", "--hostname", "github.com"}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return "", err
	}
	var content struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(formulaResult.Stdout), &content); err != nil || content.Encoding != "base64" {
		return "", fmt.Errorf("decode tap Formula response")
	}
	formula, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decode tap Formula bytes: %w", err)
	}
	for _, asset := range []string{"hextap-darwin-arm64.tar.gz", "hextap-darwin-amd64.tar.gz"} {
		wanted := "/releases/download/v" + version + "/" + asset
		if !strings.Contains(string(formula), wanted) {
			return "", fmt.Errorf("tap Formula is missing %s", wanted)
		}
	}
	return runURL, nil
}
