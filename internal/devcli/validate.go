package devcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Validate runs the toolkit's local CI contract and verifies that no gate
// changes the working tree relative to its starting state.
func (service Service) Validate(ctx context.Context, options ValidateOptions) (ValidateResult, error) {
	project, err := resolveValidationProject(ctx, service.runner(), options.Project)
	if err != nil {
		return ValidateResult{}, err
	}
	runner := service.runner()
	before, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "status", "--porcelain=v1", "--untracked-files=all"}})
	if err != nil {
		return ValidateResult{}, err
	}
	beforeFiles, err := snapshotProjectFiles(ctx, runner, project)
	if err != nil {
		return ValidateResult{}, err
	}
	scripts, err := filepath.Glob(filepath.Join(project, "scripts", "*.sh"))
	if err != nil || len(scripts) == 0 {
		return ValidateResult{}, fmt.Errorf("resolve toolkit shell scripts")
	}
	sort.Strings(scripts)
	commands := []Command{
		{Name: "gofmt", Args: []string{"-l", "."}, Dir: project},
		{Name: "go", Args: []string{"test", "-count=1", "./..."}, Dir: project, Timeout: 30 * time.Minute},
	}
	if options.Full {
		commands = append(commands, Command{Name: "go", Args: []string{"test", "-race", "-count=1", "./..."}, Dir: project, Timeout: 30 * time.Minute})
	}
	commands = append(commands,
		Command{Name: "go", Args: []string{"vet", "./..."}, Dir: project},
		Command{Name: "go", Args: []string{"build", "-trimpath", "./..."}, Dir: project},
		Command{Name: "bash", Args: append([]string{"-n"}, scripts...), Dir: project},
		Command{Name: "shellcheck", Args: scripts, Dir: project},
		Command{Name: filepath.Join(project, "scripts", "check-actionlint.sh"), Dir: project},
		Command{Name: "git", Args: []string{"-C", project, "diff", "--check"}},
	)
	for _, command := range commands {
		description := command.Name
		if len(command.Args) != 0 {
			description += " " + strings.Join(command.Args, " ")
		}
		service.progress("CHECK %s", description)
		result, commandErr := runner.Run(ctx, command)
		if commandErr != nil {
			return ValidateResult{}, fmt.Errorf("validation gate %s failed: %w", command.Name, commandErr)
		}
		if command.Name == "gofmt" && result.Stdout != "" {
			return ValidateResult{}, fmt.Errorf("validation gate gofmt found unformatted files")
		}
	}
	after, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "status", "--porcelain=v1", "--untracked-files=all"}})
	if err != nil {
		return ValidateResult{}, err
	}
	afterFiles, snapshotErr := snapshotProjectFiles(ctx, runner, project)
	if snapshotErr != nil {
		return ValidateResult{}, snapshotErr
	}
	if before.Stdout != after.Stdout || beforeFiles != afterFiles {
		return ValidateResult{}, fmt.Errorf("working tree changed during validation")
	}
	return ValidateResult{Project: project, Race: options.Full}, nil
}

// snapshotProjectFiles hashes the identity and contents of every file Git
// considers part of the working tree: tracked files plus untracked files that
// no ignore rule excludes. Ignored volatile state — agent metadata, build
// caches, editor scratch files — is deliberately outside the snapshot, because
// mutating it is not a change to the project. Enumeration is delegated to Git
// itself so the snapshot obeys exactly the ignore rules that `git status` and
// CI's `git diff --exit-code` obey.
func snapshotProjectFiles(ctx context.Context, runner Runner, project string) (string, error) {
	paths, err := snapshotPaths(ctx, runner, project)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, relative := range paths {
		if err := hashProjectFile(hash, project, relative); err != nil {
			return "", fmt.Errorf("snapshot toolkit files: %w", err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// snapshotPaths returns the sorted, deduplicated slash-separated paths Git
// reports for the working tree. Unmerged paths appear once per stage in Git's
// output, so deduplication is required for a deterministic snapshot.
func snapshotPaths(ctx context.Context, runner Runner, project string) ([]string, error) {
	listed, err := runner.Run(ctx, Command{Name: "git", Args: []string{"-C", project, "ls-files", "-z", "--cached", "--others", "--exclude-standard"}})
	if err != nil {
		return nil, fmt.Errorf("list tracked and non-ignored files in %q: %w", project, err)
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, strings.Count(listed.Stdout, "\x00"))
	for _, relative := range strings.Split(listed.Stdout, "\x00") {
		if relative == "" {
			continue
		}
		if _, duplicate := seen[relative]; duplicate {
			continue
		}
		seen[relative] = struct{}{}
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	return paths, nil
}

// hashProjectFile folds one path's identity, mode, size and content into the
// running snapshot. A path Git lists but that no longer exists is recorded as
// missing rather than failing, so a deletion during validation is reported as
// the working-tree mutation it is instead of an unrelated I/O error.
func hashProjectFile(hash io.Writer, project, relative string) error {
	path := filepath.Join(project, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(hash, "%d:%s:missing\n", len(relative), relative)
			return nil
		}
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	fmt.Fprintf(hash, "%d:%s:%s:%d\n", len(relative), relative, info.Mode().String(), info.Size())
	switch {
	case info.Mode().IsRegular():
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %q: %w", path, err)
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("read %q: %w", path, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %q: %w", path, closeErr)
		}
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read symlink %q: %w", path, err)
		}
		fmt.Fprintf(hash, "%d:%s\n", len(target), target)
	}
	return nil
}

func resolveValidationProject(ctx context.Context, runner Runner, requested string) (string, error) {
	project, err := runSingleLine(ctx, runner, Command{Name: "git", Args: []string{"-C", requested, "rev-parse", "--show-toplevel"}})
	if err != nil {
		return "", fmt.Errorf("resolve validation project: %w", err)
	}
	if err := requireToolkitModule(project); err != nil {
		return "", err
	}
	return project, nil
}
