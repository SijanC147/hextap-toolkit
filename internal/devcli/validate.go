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
	beforeFiles, err := snapshotProjectFiles(project)
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
	afterFiles, snapshotErr := snapshotProjectFiles(project)
	if snapshotErr != nil {
		return ValidateResult{}, snapshotErr
	}
	if before.Stdout != after.Stdout || beforeFiles != afterFiles {
		return ValidateResult{}, fmt.Errorf("working tree changed during validation")
	}
	return ValidateResult{Project: project, Race: options.Full}, nil
}

func snapshotProjectFiles(project string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(project, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(project, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%d:%s:%s:%d\n", len(relative), relative, info.Mode().String(), info.Size())
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(hash, "%d:%s\n", len(target), target)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("snapshot toolkit files: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
