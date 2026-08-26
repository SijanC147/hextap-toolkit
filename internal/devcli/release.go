package devcli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
	"github.com/SijanC147/hextap-toolkit/internal/release"
)

type releaseAsset struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Digest string `json:"digest"`
}

type releaseView struct {
	TagName    string         `json:"tagName"`
	Draft      bool           `json:"isDraft"`
	Prerelease bool           `json:"isPrerelease"`
	Immutable  bool           `json:"isImmutable"`
	Assets     []releaseAsset `json:"assets"`
	URL        string         `json:"url"`
}

// Release validates canonical main, creates or resumes one exact annotated
// tag, waits for the self-release workflow, and verifies the immutable result.
func (service Service) Release(ctx context.Context, options ReleaseOptions) (Outcome, error) {
	bump := release.Bump(options.Bump)
	if bump != release.PatchBump && bump != release.MinorBump && bump != release.MajorBump {
		return Outcome{}, fmt.Errorf("release bump must be patch, minor, or major")
	}
	plan, err := service.Plan(ctx, options.Project, bump)
	if err != nil {
		return Outcome{}, err
	}
	if err := RequireConfirmation(plan, options.ConfirmTag, options.Execute); err != nil {
		return Outcome{}, err
	}
	service.progress("PHASE release-preflight tag=%s", plan.Tag)
	repository, err := service.inspectRepository(ctx, plan.Project)
	if err != nil {
		return Outcome{}, err
	}
	if repository.Branch != "main" || !repository.Clean {
		return Outcome{}, fmt.Errorf("release requires a clean canonical main checkout")
	}
	if _, err := service.runner().Run(ctx, Command{Name: "git", Args: []string{"-C", plan.Project, "fetch", "origin", "main", "--tags"}}); err != nil {
		return Outcome{}, fmt.Errorf("fetch canonical main and tags: %w", err)
	}
	mainSHA, err := runSingleLine(ctx, service.runner(), Command{Name: "git", Args: []string{"-C", plan.Project, "rev-parse", "origin/main"}})
	if err != nil || mainSHA != repository.Head || plan.Commit != repository.Head {
		return Outcome{}, fmt.Errorf("release checkout must equal current origin/main")
	}
	if _, err := service.Validate(ctx, ValidateOptions{Project: plan.Project, Full: true}); err != nil {
		return Outcome{}, err
	}
	service.progress("PHASE merged-main-ci commit=%s", mainSHA)
	mainRunURL, err := service.waitForMainCI(ctx, mainSHA)
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
	if "v"+nextVersion != options.ConfirmTag || latestTag != plan.CurrentTag {
		return Outcome{}, fmt.Errorf("release baseline changed; rerun plan")
	}
	plan.CurrentTag = latestTag
	plan.CurrentVersion = latestVersion
	plan.Version = nextVersion
	plan.Commit = mainSHA
	return service.finishRelease(ctx, plan, mainRunURL, "", options.Install, options.SkillAgents)
}

func (service Service) finishRelease(ctx context.Context, plan ReleasePlan, mainRunURL, prURL string, install bool, skillAgents []string) (Outcome, error) {
	service.progress("PHASE tag tag=%s commit=%s", plan.Tag, plan.Commit)
	if err := service.ensureReleaseTag(ctx, plan.Project, plan.Tag, plan.Commit); err != nil {
		return Outcome{}, err
	}
	releaseRunURL, err := service.waitForReleaseWorkflow(ctx, plan.Tag, plan.Commit)
	if err != nil {
		return Outcome{}, fmt.Errorf("require release workflow: %w", err)
	}
	service.progress("PHASE immutable-release tag=%s", plan.Tag)
	releaseURL, err := service.verifyImmutableRelease(ctx, plan.Project, plan.Tag)
	if err != nil {
		return Outcome{}, err
	}
	tapRunURL, err := service.verifyTapPublication(ctx, plan.Version)
	if err != nil {
		return Outcome{}, fmt.Errorf("release succeeded but tap verification failed: %w", err)
	}
	service.progress("PHASE tap-verified version=%s", plan.Version)
	outcome := Outcome{Schema: 1, Tag: plan.Tag, Version: plan.Version, Commit: plan.Commit, PRURL: prURL, MainRunURL: mainRunURL, ReleaseRunURL: releaseRunURL, TapRunURL: tapRunURL, ReleaseURL: releaseURL}
	if install {
		service.progress("PHASE local-install version=%s", plan.Version)
		if _, err := service.Install(ctx, InstallOptions{Project: plan.Project, Tag: plan.Tag, ExpectedCommit: plan.Commit, Execute: true, SkillAgents: skillAgents}); err != nil {
			return Outcome{}, fmt.Errorf("release succeeded but local install failed: %w", err)
		}
		outcome.Installed = true
	}
	return outcome, nil
}

func (service Service) ensureReleaseTag(ctx context.Context, project, tag, commit string) error {
	runner := service.runner()
	remote, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "ls-remote", "--tags", "origin", "refs/tags/" + tag, "refs/tags/" + tag + "^{}"}})
	if err != nil {
		return err
	}
	remoteExists := remote.Stdout != ""
	if remoteExists {
		peeled := ""
		for _, line := range strings.Split(strings.TrimSuffix(remote.Stdout, "\n"), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == "refs/tags/"+tag+"^{}" {
				peeled = fields[0]
			}
		}
		if peeled != commit {
			return fmt.Errorf("existing remote tag %s does not identify confirmed commit", tag)
		}
	}
	local, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "tag", "--list", tag}})
	if err != nil {
		return err
	}
	if local.Stdout == "" {
		if _, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "tag", "-a", tag, commit, "-m", "hextap-toolkit " + tag}}); err != nil {
			return fmt.Errorf("create annotated release tag: %w", err)
		}
	} else {
		if local.Stdout != tag+"\n" {
			return fmt.Errorf("local release tag lookup is ambiguous")
		}
		resolved, resolveErr := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "rev-parse", "refs/tags/" + tag + "^{commit}"}})
		if resolveErr != nil || resolved != commit {
			return fmt.Errorf("existing local tag %s does not identify confirmed commit", tag)
		}
	}
	if !remoteExists {
		if _, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "push", "origin", "refs/tags/" + tag}}); err != nil {
			return fmt.Errorf("push release tag: %w", err)
		}
		return nil
	}
	return nil
}

func (service Service) verifyImmutableRelease(ctx context.Context, project, tag string) (string, error) {
	result, err := service.runner().Run(ctx, Command{
		Name: "gh",
		Args: []string{"release", "view", tag, "--repo", ToolkitRepository, "--json", "tagName,isDraft,isPrerelease,isImmutable,assets,url"},
		Env:  map[string]string{"GH_HOST": "github.com"},
	})
	if err != nil {
		return "", err
	}
	var view releaseView
	if err := json.Unmarshal([]byte(result.Stdout), &view); err != nil {
		return "", fmt.Errorf("decode release verification: %w", err)
	}
	if view.TagName != tag || view.Draft || view.Prerelease || !view.Immutable || view.URL == "" {
		return "", fmt.Errorf("release %s is not a published immutable stable release", tag)
	}
	projectManifest, err := manifest.Load(filepath.Join(project, ".hextap.json"))
	if err != nil {
		return "", err
	}
	expected := []string{"SHA256SUMS", projectManifest.Formula.Assets.DarwinAMD64, projectManifest.Formula.Assets.DarwinARM64}
	if projectManifest.Release.Linux {
		expected = append(expected, projectManifest.Formula.Name+"-linux-amd64.tar.gz", projectManifest.Formula.Name+"-linux-arm64.tar.gz")
	}
	sort.Strings(expected)
	actual := make([]string, 0, len(view.Assets))
	for _, asset := range view.Assets {
		if asset.State != "uploaded" || !strings.HasPrefix(asset.Digest, "sha256:") || len(strings.TrimPrefix(asset.Digest, "sha256:")) != 64 {
			return "", fmt.Errorf("release asset %q has invalid state or digest", asset.Name)
		}
		actual = append(actual, asset.Name)
	}
	sort.Strings(actual)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return "", fmt.Errorf("release asset set mismatch")
	}
	if _, err := service.runner().Run(ctx, Command{Name: "gh", Args: []string{"release", "verify", tag, "--repo", ToolkitRepository}, Env: map[string]string{"GH_HOST": "github.com"}}); err != nil {
		return "", fmt.Errorf("verify release attestations: %w", err)
	}
	return view.URL, nil
}
