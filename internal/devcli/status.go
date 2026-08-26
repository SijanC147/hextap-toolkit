package devcli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SijanC147/hextap-toolkit/internal/release"
)

type githubRelease struct {
	TagName    string `json:"tagName"`
	Draft      bool   `json:"isDraft"`
	Prerelease bool   `json:"isPrerelease"`
	Immutable  bool   `json:"isImmutable"`
}

// Status returns read-only repository, identity, and release inventory.
func (service Service) Status(ctx context.Context, project string) (StatusResult, error) {
	repository, err := service.inspectRepository(ctx, project)
	if err != nil {
		return StatusResult{}, err
	}
	latestTag, latestVersion, err := service.latestStableRelease(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	nextPatch, err := release.BumpStableVersion(latestVersion, release.PatchBump)
	if err != nil {
		return StatusResult{}, err
	}
	nextMinor, err := release.BumpStableVersion(latestVersion, release.MinorBump)
	if err != nil {
		return StatusResult{}, err
	}
	nextMajor, err := release.BumpStableVersion(latestVersion, release.MajorBump)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{
		Schema:              1,
		Project:             repository.Project,
		Repository:          ToolkitRepository,
		Branch:              repository.Branch,
		Head:                repository.Head,
		Clean:               repository.Clean,
		GitHubUser:          repository.GitHubUser,
		LatestStableTag:     latestTag,
		LatestStableVersion: latestVersion,
		NextPatch:           nextPatch,
		NextMinor:           nextMinor,
		NextMajor:           nextMajor,
		CLIVersion:          service.Version,
		CLICommit:           service.Commit,
	}, nil
}

func (service Service) latestStableRelease(ctx context.Context) (string, string, error) {
	result, err := service.runner().Run(ctx, Command{
		Name: "gh",
		Args: []string{"release", "list", "--repo", ToolkitRepository, "--limit", "100", "--json", "tagName,isDraft,isPrerelease,isImmutable"},
		Env:  map[string]string{"GH_HOST": "github.com"},
	})
	if err != nil {
		return "", "", fmt.Errorf("list toolkit releases: %w", err)
	}
	var releases []githubRelease
	if err := json.Unmarshal([]byte(result.Stdout), &releases); err != nil {
		return "", "", fmt.Errorf("decode toolkit releases: %w", err)
	}
	latestTag := ""
	latestVersion := ""
	for _, candidate := range releases {
		if candidate.Draft || candidate.Prerelease || !candidate.Immutable {
			continue
		}
		metadata, parseErr := release.ParseMetadata(candidate.TagName, "full")
		if parseErr != nil || !metadata.Stable {
			continue
		}
		if latestVersion == "" {
			latestTag, latestVersion = candidate.TagName, metadata.Version
			continue
		}
		comparison, compareErr := release.CompareStableVersions(metadata.Version, latestVersion)
		if compareErr != nil {
			return "", "", compareErr
		}
		if comparison > 0 {
			latestTag, latestVersion = candidate.TagName, metadata.Version
		}
	}
	if latestVersion == "" {
		return "", "", fmt.Errorf("no immutable stable toolkit release found")
	}
	return latestTag, latestVersion, nil
}
