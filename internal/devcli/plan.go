package devcli

import (
	"context"
	"fmt"

	"github.com/SijanC147/hextap-toolkit/internal/release"
)

// Plan computes one exact release tag without mutating local or remote state.
func (service Service) Plan(ctx context.Context, project string, bump release.Bump) (ReleasePlan, error) {
	status, err := service.Status(ctx, project)
	if err != nil {
		return ReleasePlan{}, err
	}
	next, err := release.BumpStableVersion(status.LatestStableVersion, bump)
	if err != nil {
		return ReleasePlan{}, err
	}
	return ReleasePlan{
		Schema:         1,
		Project:        status.Project,
		Repository:     ToolkitRepository,
		CurrentTag:     status.LatestStableTag,
		CurrentVersion: status.LatestStableVersion,
		Bump:           string(bump),
		Tag:            "v" + next,
		Version:        next,
		Commit:         status.Head,
	}, nil
}

// RequireConfirmation enforces the explicit mutation boundary for release and
// deployment operations.
func RequireConfirmation(plan ReleasePlan, confirmedTag string, execute bool) error {
	if !execute {
		return fmt.Errorf("release mutation requires --execute")
	}
	if confirmedTag != plan.Tag {
		return fmt.Errorf("--confirm-tag must exactly equal %s", plan.Tag)
	}
	return nil
}
