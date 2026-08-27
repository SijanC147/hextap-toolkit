package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const defaultProfileCommandTimeout = 15 * time.Minute

// ProfilePhase selects the project-owned command group run after dependency
// installation.
type ProfilePhase string

const (
	// ProfileQuality runs the install command followed by every quality command.
	ProfileQuality ProfilePhase = "quality"
	// ProfileBuild validates Bun, runs install, prefetches every declared runtime,
	// then runs each project build-preparation command.
	ProfileBuild ProfilePhase = "build"
)

// ProfileOptions identifies one direct project-command execution.
type ProfileOptions struct {
	ManifestPath string
	SourceDir    string
	Phase        ProfilePhase
	BunCacheDir  string
	Stdout       io.Writer
	Stderr       io.Writer
}

// RunProfile executes the selected schema-2 command phase directly from argv
// arrays. It never evaluates a shell command string.
func RunProfile(options ProfileOptions) error {
	sourceDir, err := validateDirectory(options.SourceDir, "source", false)
	if err != nil {
		return err
	}
	project, err := readManifestWithin(options.ManifestPath, sourceDir)
	if err != nil {
		return err
	}
	if project.Schema != manifest.ProfileSchema || project.Release.Profile == nil {
		return errors.New("release profile commands require a schema 2 manifest")
	}
	var commands []manifest.Command
	switch options.Phase {
	case ProfileQuality:
		commands = project.Release.Profile.Quality
	case ProfileBuild:
		commands = project.Release.Profile.Prepare
	default:
		return fmt.Errorf("unsupported release profile phase %q", options.Phase)
	}
	stdout := options.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	cacheDirectory := options.BunCacheDir
	if options.Phase == ProfileBuild {
		if cacheDirectory == "" {
			cacheDirectory, _ = os.LookupEnv("BUN_INSTALL_CACHE_DIR")
		}
		if cacheDirectory == "" {
			return errors.New("BUN_INSTALL_CACHE_DIR is required for Bun build preparation")
		}
		resolvedCache, err := validateDirectory(cacheDirectory, "Bun runtime cache", false)
		if err != nil {
			return err
		}
		cacheDirectory = resolvedCache
	}
	environment := profileEnvironment(cacheDirectory)
	if err := requireProfileRuntimeVersion(sourceDir, project.Release.Profile.RuntimeVersion, environment, stdout); err != nil {
		return err
	}
	if err := runProfileCommand(sourceDir, project.Release.Profile.Install, environment, stdout, stderr); err != nil {
		return err
	}
	if options.Phase == ProfileBuild {
		if err := prefetchBunRuntimes(sourceDir, project, environment, stdout, stderr); err != nil {
			return err
		}
	}
	for _, command := range commands {
		if err := runProfileCommand(sourceDir, command, environment, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

func requireProfileRuntimeVersion(sourceDir, expected string, environment []string, stdout io.Writer) error {
	if _, err := fmt.Fprintln(stdout, "CHECK bun-version"); err != nil {
		return fmt.Errorf("write profile progress: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	process := exec.CommandContext(ctx, "bun", "--version")
	process.Dir = sourceDir
	process.Env = environment
	process.Stdin = nil
	var versionOutput bytes.Buffer
	var errorOutput bytes.Buffer
	process.Stdout = &versionOutput
	process.Stderr = &errorOutput
	process.WaitDelay = 2 * time.Second
	if err := process.Run(); err != nil {
		return fmt.Errorf("inspect Bun runtime version: %w", err)
	}
	if errorOutput.Len() != 0 || strings.TrimSpace(versionOutput.String()) != expected {
		return fmt.Errorf("Bun runtime version must be exactly %s", expected)
	}
	return nil
}

func runProfileCommand(sourceDir string, command manifest.Command, environment []string, stdout, stderr io.Writer) error {
	if _, err := fmt.Fprintf(stdout, "CHECK %s\n", command.Name); err != nil {
		return fmt.Errorf("write profile progress: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultProfileCommandTimeout)
	defer cancel()
	process := exec.CommandContext(ctx, command.Argv[0], command.Argv[1:]...)
	process.Dir = sourceDir
	process.Env = environment
	process.Stdin = nil
	process.Stdout = stdout
	process.Stderr = stderr
	process.WaitDelay = 2 * time.Second
	err := process.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("profile command %q timed out", command.Name)
	}
	if err != nil {
		return fmt.Errorf("profile command %q failed: %w", command.Name, err)
	}
	return nil
}

func prefetchBunRuntimes(sourceDir string, project manifest.Manifest, environment []string, stdout, stderr io.Writer) error {
	directory, err := os.MkdirTemp("", ".hextap-bun-prefetch-*")
	if err != nil {
		return fmt.Errorf("create Bun runtime prefetch directory: %w", err)
	}
	defer os.RemoveAll(directory)
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure Bun runtime prefetch directory: %w", err)
	}
	entrypoint := filepath.Join(directory, "entry.ts")
	if err := os.WriteFile(entrypoint, []byte("console.log('hextap-runtime-prefetch')\n"), 0o600); err != nil {
		return fmt.Errorf("write Bun runtime prefetch entrypoint: %w", err)
	}
	targets, err := buildTargets(project)
	if err != nil {
		return err
	}
	for _, buildTarget := range targets {
		bunTarget, err := bunTargetFor(buildTarget.OS, buildTarget.Arch)
		if err != nil {
			return err
		}
		suffix := ""
		if buildTarget.OS == "windows" {
			suffix = ".exe"
		}
		output := filepath.Join(directory, strings.ReplaceAll(bunTarget, "-", "_")+suffix)
		command := manifest.Command{
			Name: "prefetch-" + strings.ReplaceAll(bunTarget, "bun-", ""),
			Argv: []string{"bun", "build", entrypoint, "--compile", "--target=" + bunTarget, "--outfile=" + output},
		}
		if err := runProfileCommand(sourceDir, command, environment, stdout, stderr); err != nil {
			return fmt.Errorf("prefetch Bun runtime %s: %w", bunTarget, err)
		}
		info, err := os.Lstat(output)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
			return fmt.Errorf("prefetch Bun runtime %s did not create one executable", bunTarget)
		}
	}
	return nil
}

func bunTargetFor(targetOS, targetArch string) (string, error) {
	switch targetOS + "/" + targetArch {
	case "darwin/arm64":
		return "bun-darwin-arm64", nil
	case "darwin/amd64":
		return "bun-darwin-x64", nil
	case "linux/arm64":
		return "bun-linux-arm64", nil
	case "linux/amd64":
		return "bun-linux-x64", nil
	case "windows/amd64":
		return "bun-windows-x64", nil
	default:
		return "", fmt.Errorf("unsupported Bun release target %s/%s", targetOS, targetArch)
	}
}

func profileEnvironment(cacheDirectory string) []string {
	allow := []string{
		"BUN_INSTALL", "BUN_INSTALL_CACHE_DIR", "CI", "HOME", "LANG", "LC_ALL",
		"LOGNAME", "NO_COLOR", "PATH", "SYSTEMROOT", "TEMP", "TERM", "TMP",
		"TMPDIR", "TZ", "USER", "XDG_CACHE_HOME",
	}
	result := make([]string, 0, len(allow))
	for _, name := range allow {
		if name == "BUN_INSTALL_CACHE_DIR" && cacheDirectory != "" {
			result = append(result, name+"="+cacheDirectory)
			continue
		}
		if value, exists := os.LookupEnv(name); exists {
			result = append(result, name+"="+value)
		}
	}
	return result
}
