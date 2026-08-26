package devcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var safeBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

type repositoryState struct {
	Project    string
	Branch     string
	Head       string
	Clean      bool
	GitHubUser string
}

func (service Service) inspectRepository(ctx context.Context, requested string) (repositoryState, error) {
	runner := service.runner()
	resolved, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", requested, "rev-parse", "--show-toplevel"}})
	if err != nil {
		return repositoryState{}, fmt.Errorf("resolve toolkit Git root: %w", err)
	}
	project, err := filepath.Abs(resolved)
	if err != nil {
		return repositoryState{}, fmt.Errorf("resolve toolkit path: %w", err)
	}
	if err := requireToolkitModule(project); err != nil {
		return repositoryState{}, err
	}
	remotes, err := runLines(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "remote"}})
	if err != nil {
		return repositoryState{}, err
	}
	if !contains(remotes, "origin") {
		return repositoryState{}, fmt.Errorf("toolkit repository has no origin remote")
	}
	fetchURL, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "remote", "get-url", "origin"}})
	if err != nil {
		return repositoryState{}, err
	}
	pushURL, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "remote", "get-url", "--push", "origin"}})
	if err != nil {
		return repositoryState{}, err
	}
	if !isCanonicalToolkitURL(fetchURL) || !isCanonicalToolkitURL(pushURL) {
		return repositoryState{}, fmt.Errorf("origin must fetch and push only %s", ToolkitRepository)
	}
	sort.Strings(remotes)
	for _, remote := range remotes {
		if remote == "origin" {
			continue
		}
		urls, urlErr := runLines(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "remote", "get-url", "--push", "--all", remote}})
		if urlErr != nil {
			return repositoryState{}, fmt.Errorf("inspect remote %q push URLs: %w", remote, urlErr)
		}
		for _, url := range urls {
			if url != "no_push" {
				return repositoryState{}, fmt.Errorf("remote %q has writable remote URL; disable its push URL", remote)
			}
		}
	}
	branch, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return repositoryState{}, fmt.Errorf("resolve toolkit branch: %w", err)
	}
	head, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", project, "rev-parse", "HEAD"}})
	if err != nil || !isHexCommit(head) {
		return repositoryState{}, fmt.Errorf("resolve toolkit HEAD")
	}
	working, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "status", "--porcelain=v1", "--untracked-files=all"}})
	if err != nil {
		return repositoryState{}, err
	}
	user, err := runSingleLine(ctx, runner, Command{Name: "gh", Args: []string{"api", "user", "--hostname", "github.com", "--jq", ".login"}, Env: map[string]string{"GH_HOST": "github.com"}})
	if err != nil {
		return repositoryState{}, fmt.Errorf("resolve GitHub identity: %w", err)
	}
	if user != ToolkitOwner {
		return repositoryState{}, fmt.Errorf("GitHub login must be %s", ToolkitOwner)
	}
	return repositoryState{Project: project, Branch: branch, Head: head, Clean: working.Stdout == "", GitHubUser: user}, nil
}

func requireToolkitModule(project string) error {
	data, err := os.ReadFile(filepath.Join(project, "go.mod"))
	if err != nil {
		return fmt.Errorf("read toolkit go.mod: %w", err)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	if first != "module "+ToolkitModule {
		return fmt.Errorf("project module must be %s", ToolkitModule)
	}
	if info, err := os.Lstat(filepath.Join(project, ".hextap.json")); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("project must contain a regular .hextap.json")
	}
	return nil
}

func isCanonicalToolkitURL(value string) bool {
	return value == ToolkitOriginHTTPS || value == "git@github.com:SijanC147/hextap-toolkit.git" || value == "ssh://git@github.com/SijanC147/hextap-toolkit.git"
}

func isHexCommit(value string) bool {
	if len(value) < 40 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func isSafeBranch(value string) bool {
	return safeBranchPattern.MatchString(value) && !strings.Contains(value, "..") && !strings.Contains(value, "@{") && !strings.HasSuffix(value, "/") && !strings.HasSuffix(value, ".") && !strings.Contains(value, "//")
}

func runSingleLine(ctx context.Context, runner Runner, command Command) (string, error) {
	result, err := runner.Run(ctx, command)
	if err != nil {
		return "", err
	}
	if result.Stdout == "" || !strings.HasSuffix(result.Stdout, "\n") {
		return "", fmt.Errorf("command %q returned no terminated record", command.Name)
	}
	value := strings.TrimSuffix(result.Stdout, "\n")
	if value == "" || strings.ContainsAny(value, "\x00\n\r") {
		return "", fmt.Errorf("command %q returned an invalid record", command.Name)
	}
	return value, nil
}

func runLines(ctx context.Context, runner Runner, command Command) ([]string, error) {
	result, err := runner.Run(ctx, command)
	if err != nil {
		return nil, err
	}
	if result.Stdout == "" {
		return nil, nil
	}
	if !strings.HasSuffix(result.Stdout, "\n") || strings.ContainsAny(strings.TrimSuffix(result.Stdout, "\n"), "\x00\r") {
		return nil, fmt.Errorf("command %q returned invalid records", command.Name)
	}
	return strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n"), nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
