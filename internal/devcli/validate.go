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
	linkedDirectories := make(map[string]bool)
	for _, relative := range paths {
		if err := hashProjectFile(hash, project, relative, linkedDirectories); err != nil {
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
//
// A path beneath a symbolic link is recorded as missing too. When a tracked
// directory has been replaced by a link, Git lists both the link and the index
// entries beneath it, but it never reads through the link: `git status` reports
// those entries as deleted. Reading through it here would hash content outside
// the working tree that neither `git status --porcelain` nor CI's
// `git diff --exit-code` can see, so a change to that content would fail
// validation that Git itself would pass. The link is still listed on its own
// and hashed by its target, so retargeting it remains a detected mutation.
//
// Directories Git reports as a single opaque entry are refused rather than
// hashed. See opaqueDirectoryError.
func hashProjectFile(hash io.Writer, project, relative string, linkedDirectories map[string]bool) error {
	if strings.HasSuffix(relative, "/") {
		return opaqueDirectoryError(relative)
	}
	beneathLink, err := hasSymlinkLeadingPath(project, relative, linkedDirectories)
	if err != nil {
		return err
	}
	if beneathLink {
		recordMissing(hash, relative)
		return nil
	}
	path := filepath.Join(project, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			recordMissing(hash, relative)
			return nil
		}
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if info.IsDir() {
		return opaqueDirectoryError(relative)
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

// recordMissing folds a listed path that has no file behind it into the
// snapshot, so that the path's presence in Git's listing still counts while its
// absence on disk is what gets hashed.
func recordMissing(hash io.Writer, relative string) {
	fmt.Fprintf(hash, "%d:%s:missing\n", len(relative), relative)
}

// hasSymlinkLeadingPath reports whether any directory component of relative is
// a symbolic link, mirroring the check Git applies to index entries. The answer
// is cached per directory in linkedDirectories, so a directory is inspected
// once rather than once for every path beneath it. Inspection stops at the
// first link found, because inspecting anything beneath it would itself read
// through the link.
func hasSymlinkLeadingPath(project, relative string, linkedDirectories map[string]bool) (bool, error) {
	components := strings.Split(relative, "/")
	for depth := 1; depth < len(components); depth++ {
		directory := strings.Join(components[:depth], "/")
		linked, known := linkedDirectories[directory]
		if !known {
			path := filepath.Join(project, filepath.FromSlash(directory))
			info, err := os.Lstat(path)
			switch {
			case err == nil:
				linked = info.Mode()&os.ModeSymlink != 0
			case os.IsNotExist(err):
				linked = false
			default:
				return false, fmt.Errorf("inspect %q: %w", path, err)
			}
			linkedDirectories[directory] = linked
		}
		if linked {
			return true, nil
		}
	}
	return false, nil
}

// opaqueDirectoryError refuses a checkout containing a directory Git reports as
// one entry rather than as its contents: an initialized submodule, which Git
// lists as a single gitlink, or an embedded repository, which Git lists as a
// single trailing-slash path.
//
// Such a directory cannot be snapshotted. Its mode and size do not change when
// a file inside it changes, and `git status --porcelain` reports the same entry
// either way, so a gate could rewrite a file inside it and validation would
// still report the working tree as unchanged. That is the false pass this whole
// check exists to prevent, so the gate fails closed and names the path instead
// of reporting coverage it does not have. Recursing would mean reimplementing
// nested ignore, nested submodule and uninitialized-submodule semantics, which
// is exactly what delegating enumeration to Git avoids.
func opaqueDirectoryError(relative string) error {
	return fmt.Errorf("cannot validate %q: Git reports it as one opaque entry (an initialized submodule or an embedded repository), so a gate could change a file inside it without changing this snapshot", relative)
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
