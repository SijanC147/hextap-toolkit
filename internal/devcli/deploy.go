package devcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/release"
)

const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100){nodes{isResolved}pageInfo{hasNextPage}}}}}`

type pullRequest struct {
	Number           int           `json:"number"`
	URL              string        `json:"url"`
	State            string        `json:"state"`
	Mergeable        string        `json:"mergeable"`
	MergeStateStatus string        `json:"mergeStateStatus"`
	HeadRefOID       string        `json:"headRefOid"`
	StatusChecks     []statusCheck `json:"statusCheckRollup"`
}

type statusCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
}

type mergedPullRequest struct {
	State       string `json:"state"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	URL string `json:"url"`
}

// Deploy runs the authorized feature-branch validation, protected PR, exact
// merged-main CI, immutable release, and optional local-install workflow.
func (service Service) Deploy(ctx context.Context, options DeployOptions) (Outcome, error) {
	bump := release.Bump(options.Bump)
	if bump != release.PatchBump && bump != release.MinorBump && bump != release.MajorBump {
		return Outcome{}, fmt.Errorf("deploy bump must be patch, minor, or major")
	}
	initialPlan, err := service.Plan(ctx, options.Project, bump)
	if err != nil {
		return Outcome{}, err
	}
	if err := RequireConfirmation(initialPlan, options.ConfirmTag, options.Execute); err != nil {
		return Outcome{}, err
	}
	service.progress("PHASE deploy-preflight tag=%s", initialPlan.Tag)
	repository, err := service.inspectRepository(ctx, initialPlan.Project)
	if err != nil {
		return Outcome{}, err
	}
	if !repository.Clean || repository.Branch == "main" || repository.Branch == "HEAD" {
		return Outcome{}, fmt.Errorf("deploy requires a clean named feature branch")
	}
	if !isSafeBranch(repository.Branch) || initialPlan.Commit != repository.Head {
		return Outcome{}, fmt.Errorf("deploy feature branch identity changed or is unsafe")
	}
	runner := service.runner()
	if _, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", repository.Project, "fetch", "origin", "main", "--tags"}}); err != nil {
		return Outcome{}, fmt.Errorf("fetch canonical main and tags: %w", err)
	}
	mainBefore, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", repository.Project, "rev-parse", "origin/main"}})
	if err != nil || !isHexCommit(mainBefore) {
		return Outcome{}, fmt.Errorf("resolve origin/main before deployment")
	}
	alreadyMerged, err := service.findMergedPullRequestForHead(ctx, repository)
	if err != nil {
		return Outcome{}, err
	}
	if alreadyMerged != nil {
		service.progress("PHASE resume-merged-pr %s", alreadyMerged.URL)
		return service.completeMergedDeployment(ctx, repository, bump, options, *alreadyMerged)
	}
	if _, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", repository.Project, "merge-base", "--is-ancestor", "origin/main", "HEAD"}}); err != nil {
		return Outcome{}, fmt.Errorf("feature branch must contain current origin/main")
	}
	ahead, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", repository.Project, "rev-list", "--count", "origin/main..HEAD"}})
	if err != nil || ahead == "0" {
		return Outcome{}, fmt.Errorf("feature branch has no commits ahead of origin/main")
	}
	if _, err := strconv.ParseUint(ahead, 10, 64); err != nil {
		return Outcome{}, fmt.Errorf("feature branch ahead count is invalid")
	}
	if _, err := service.Validate(ctx, ValidateOptions{Project: repository.Project, Full: true}); err != nil {
		return Outcome{}, err
	}
	service.progress("PHASE feature-push branch=%s", repository.Branch)
	freshRepository, err := service.inspectRepository(ctx, repository.Project)
	if err != nil || !freshRepository.Clean || freshRepository.Branch != repository.Branch || freshRepository.Head != repository.Head {
		return Outcome{}, fmt.Errorf("feature branch changed after validation")
	}
	if _, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", repository.Project, "push", "--set-upstream", "origin", "HEAD:" + repository.Branch}}); err != nil {
		return Outcome{}, fmt.Errorf("push feature branch to origin: %w", err)
	}
	pr, err := service.findOrCreatePullRequest(ctx, repository, options.PRTitle)
	if err != nil {
		return Outcome{}, err
	}
	service.progress("PHASE pull-request %s", pr.URL)
	if err := service.waitForPullRequestChecks(ctx, pr.Number, repository.Head); err != nil {
		return Outcome{}, err
	}
	if _, err := runner.Run(ctx, Command{Name: "gh", Args: []string{"pr", "checks", strconv.Itoa(pr.Number), "--repo", ToolkitRepository, "--watch", "--interval", "10"}, Env: map[string]string{"GH_HOST": "github.com"}, Timeout: 40 * time.Minute}); err != nil {
		return Outcome{}, fmt.Errorf("wait for pull request checks: %w", err)
	}
	service.progress("PHASE review-settlement pr=%d", pr.Number)
	if err := service.pause(ctx, 20*time.Second); err != nil {
		return Outcome{}, err
	}
	pr, err = service.requireMergeReadyPullRequest(ctx, pr.Number, repository.Head)
	if err != nil {
		return Outcome{}, err
	}
	if err := service.requireResolvedReviewThreads(ctx, pr.Number); err != nil {
		return Outcome{}, err
	}
	if _, err := runner.Run(ctx, Command{Name: "gh", Args: []string{"pr", "merge", strconv.Itoa(pr.Number), "--repo", ToolkitRepository, "--merge", "--delete-branch"}, Env: map[string]string{"GH_HOST": "github.com"}}); err != nil {
		return Outcome{}, fmt.Errorf("merge through protected pull request path: %w", err)
	}
	service.progress("PHASE protected-merge pr=%d", pr.Number)
	merged, err := service.readMergedPullRequest(ctx, pr.Number)
	if err != nil {
		return Outcome{}, err
	}
	return service.completeMergedDeployment(ctx, repository, bump, options, merged)
}

func (service Service) completeMergedDeployment(ctx context.Context, repository repositoryState, bump release.Bump, options DeployOptions, merged mergedPullRequest) (Outcome, error) {
	runner := service.runner()
	if _, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", repository.Project, "fetch", "origin", "main", "--tags"}}); err != nil {
		return Outcome{}, err
	}
	mainAfter, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", repository.Project, "rev-parse", "origin/main"}})
	if err != nil || mainAfter != merged.MergeCommit.OID {
		return Outcome{}, fmt.Errorf("merged pull request commit is not current origin/main")
	}
	mainRunURL, err := service.waitForMainCI(ctx, mainAfter)
	if err != nil {
		return Outcome{}, fmt.Errorf("require merged-main CI: %w", err)
	}
	latestTag, latestVersion, err := service.latestStableRelease(ctx)
	if err != nil {
		return Outcome{}, err
	}
	nextVersion, err := release.BumpStableVersion(latestVersion, bump)
	if err != nil {
		return Outcome{}, err
	}
	if "v"+nextVersion != options.ConfirmTag {
		return Outcome{}, fmt.Errorf("release baseline changed during deployment; rerun plan")
	}
	plan := ReleasePlan{Schema: 1, Project: repository.Project, Repository: ToolkitRepository, CurrentTag: latestTag, CurrentVersion: latestVersion, Bump: string(bump), Tag: options.ConfirmTag, Version: nextVersion, Commit: mainAfter}
	return service.finishRelease(ctx, plan, mainRunURL, merged.URL, options.Install, options.SkillAgents)
}

func (service Service) findMergedPullRequestForHead(ctx context.Context, repository repositoryState) (*mergedPullRequest, error) {
	result, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"pr", "list", "--repo", ToolkitRepository, "--head", repository.Branch, "--state", "merged", "--json", "number,url,headRefOid,state"}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return nil, err
	}
	var prs []pullRequest
	if err := json.Unmarshal([]byte(result.Stdout), &prs); err != nil {
		return nil, fmt.Errorf("decode merged pull request list: %w", err)
	}
	matches := make([]pullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.HeadRefOID == repository.Head && pr.State == "MERGED" {
			matches = append(matches, pr)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("multiple merged pull requests match feature head")
	}
	merged, err := service.readMergedPullRequest(ctx, matches[0].Number)
	if err != nil {
		return nil, err
	}
	return &merged, nil
}

func (service Service) waitForPullRequestChecks(ctx context.Context, number int, head string) error {
	for attempt := 0; attempt < 60; attempt++ {
		pr, err := service.readPullRequestState(ctx, number)
		if err == nil && pr.State == "OPEN" && pr.HeadRefOID == head && len(pr.StatusChecks) != 0 {
			return nil
		}
		if err := service.pause(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	return fmt.Errorf("no hosted checks appeared for pull request %d", number)
}

func (service Service) findOrCreatePullRequest(ctx context.Context, repository repositoryState, title string) (pullRequest, error) {
	runner := service.runner()
	list := Command{Name: "gh", Args: []string{"pr", "list", "--repo", ToolkitRepository, "--head", repository.Branch, "--state", "open", "--json", "number,url,headRefOid,state"}, Env: map[string]string{"GH_HOST": "github.com"}}
	read := func() ([]pullRequest, error) {
		result, err := runner.Run(ctx, list)
		if err != nil {
			return nil, err
		}
		var prs []pullRequest
		if err := json.Unmarshal([]byte(result.Stdout), &prs); err != nil {
			return nil, fmt.Errorf("decode pull request list: %w", err)
		}
		return prs, nil
	}
	prs, err := read()
	if err != nil {
		return pullRequest{}, err
	}
	if len(prs) == 0 {
		if title == "" {
			title, err = runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", repository.Project, "log", "-1", "--pretty=%s"}})
			if err != nil {
				return pullRequest{}, fmt.Errorf("derive pull request title: %w", err)
			}
		}
		body := "## Hextap developer deployment\n\nLocal full validation passed before publication. Hosted CI and review gates remain authoritative.\n"
		if _, err := runner.Run(ctx, Command{Name: "gh", Args: []string{"pr", "create", "--repo", ToolkitRepository, "--base", "main", "--head", repository.Branch, "--title", title, "--body", body}, Env: map[string]string{"GH_HOST": "github.com"}}); err != nil {
			return pullRequest{}, fmt.Errorf("create pull request: %w", err)
		}
		prs, err = read()
		if err != nil {
			return pullRequest{}, err
		}
	}
	if len(prs) != 1 || prs[0].HeadRefOID != repository.Head || prs[0].State != "OPEN" || prs[0].Number <= 0 || prs[0].URL == "" {
		return pullRequest{}, fmt.Errorf("feature branch must have exactly one matching open pull request")
	}
	return prs[0], nil
}

func (service Service) requireMergeReadyPullRequest(ctx context.Context, number int, head string) (pullRequest, error) {
	pr, err := service.readPullRequestState(ctx, number)
	if err != nil {
		return pullRequest{}, err
	}
	if pr.State != "OPEN" || pr.HeadRefOID != head || pr.Mergeable != "MERGEABLE" || pr.MergeStateStatus != "CLEAN" || len(pr.StatusChecks) == 0 {
		return pullRequest{}, fmt.Errorf("pull request %d is not clean and merge-ready", number)
	}
	for _, check := range pr.StatusChecks {
		if check.Status != "COMPLETED" || check.Conclusion != "SUCCESS" {
			return pullRequest{}, fmt.Errorf("pull request check %q is not successful", check.Name)
		}
	}
	return pr, nil
}

func (service Service) readPullRequestState(ctx context.Context, number int) (pullRequest, error) {
	result, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"pr", "view", strconv.Itoa(number), "--repo", ToolkitRepository, "--json", "number,url,state,mergeable,mergeStateStatus,headRefOid,statusCheckRollup"}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return pullRequest{}, err
	}
	var pr pullRequest
	if err := json.Unmarshal([]byte(result.Stdout), &pr); err != nil {
		return pullRequest{}, fmt.Errorf("decode pull request state: %w", err)
	}
	return pr, nil
}

func (service Service) requireResolvedReviewThreads(ctx context.Context, number int) error {
	result, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"api", "graphql", "--hostname", "github.com", "-f", "query=" + reviewThreadsQuery, "-F", "owner=" + ToolkitOwner, "-F", "name=hextap-toolkit", "-F", "number=" + strconv.Itoa(number)}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return err
	}
	var response struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							Resolved bool `json:"isResolved"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return fmt.Errorf("decode review threads: %w", err)
	}
	if response.Data.Repository.PullRequest.ReviewThreads.PageInfo.HasNextPage {
		return fmt.Errorf("pull request %d has more than 100 review threads; inspect manually", number)
	}
	for _, thread := range response.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if !thread.Resolved {
			return fmt.Errorf("pull request %d has unresolved review threads", number)
		}
	}
	return nil
}

func (service Service) readMergedPullRequest(ctx context.Context, number int) (mergedPullRequest, error) {
	result, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"pr", "view", strconv.Itoa(number), "--repo", ToolkitRepository, "--json", "state,mergedAt,mergeCommit,url"}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return mergedPullRequest{}, err
	}
	var pr mergedPullRequest
	if err := json.Unmarshal([]byte(result.Stdout), &pr); err != nil {
		return mergedPullRequest{}, err
	}
	if pr.State != "MERGED" || pr.MergedAt == "" || !isHexCommit(pr.MergeCommit.OID) || pr.URL == "" {
		return mergedPullRequest{}, fmt.Errorf("pull request %d did not merge cleanly", number)
	}
	return pr, nil
}
