package devcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/release"
	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

// Install upgrades only the Hextap Formula through the Homebrew installation
// that owns brew-hextap, verifies exact release identity, and optionally
// installs or upgrades explicitly selected user-scoped Hextap skills.
func (service Service) Install(ctx context.Context, options InstallOptions) (InstallResult, error) {
	if !options.Execute {
		return InstallResult{}, fmt.Errorf("local installation requires --execute")
	}
	metadata, err := release.ParseMetadata(options.Tag, "full")
	if err != nil || !metadata.Stable {
		return InstallResult{}, fmt.Errorf("install tag must be stable SemVer")
	}
	if !isHexCommit(options.ExpectedCommit) {
		return InstallResult{}, fmt.Errorf("install requires the exact released commit")
	}
	if options.Project == "" {
		return InstallResult{}, fmt.Errorf("install requires the toolkit project")
	}
	if err := requireToolkitModule(options.Project); err != nil {
		return InstallResult{}, err
	}
	origin, err := runSingleLine(ctx, service.runner(), Command{Name: "git", Args: []string{"-C", options.Project, "remote", "get-url", "origin"}})
	if err != nil || !isCanonicalToolkitURL(origin) {
		return InstallResult{}, fmt.Errorf("install requires canonical toolkit origin")
	}
	remoteTag, err := service.runner().Run(ctx, Command{Name: "git", Args: []string{"-C", options.Project, "ls-remote", "--tags", "origin", "refs/tags/" + options.Tag, "refs/tags/" + options.Tag + "^{}"}})
	if err != nil || peeledTagCommit(remoteTag.Stdout, options.Tag) != options.ExpectedCommit {
		return InstallResult{}, fmt.Errorf("install tag does not identify the expected released commit")
	}
	if _, err := service.verifyImmutableRelease(ctx, options.Project, options.Tag); err != nil {
		return InstallResult{}, err
	}
	brew, binary, err := service.findOwningHomebrew(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	service.progress("PHASE homebrew-update brew=%s", brew)
	runner := service.runner()
	if _, err := runner.Run(ctx, Command{Name: brew, Args: []string{"update"}}); err != nil {
		return InstallResult{}, fmt.Errorf("update Homebrew tap metadata: %w", err)
	}
	infoResult, err := runner.Run(ctx, Command{Name: brew, Args: []string{"info", "sean/hextap/hextap", "--json=v2"}})
	if err != nil {
		return InstallResult{}, fmt.Errorf("inspect available Hextap Formula: %w", err)
	}
	var info struct {
		Formulae []struct {
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal([]byte(infoResult.Stdout), &info); err != nil || len(info.Formulae) != 1 || info.Formulae[0].Versions.Stable != metadata.Version {
		return InstallResult{}, fmt.Errorf("available Hextap Formula does not exactly match %s", metadata.Version)
	}
	if _, err := runner.Run(ctx, Command{Name: brew, Args: []string{"upgrade", "sean/hextap/hextap"}, Env: map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1"}}); err != nil {
		return InstallResult{}, fmt.Errorf("upgrade only Hextap: %w", err)
	}
	service.progress("PHASE hextap-upgraded version=%s", metadata.Version)
	versionOutput, err := runSingleLine(ctx, runner, Command{Name: binary, Args: []string{"--version"}})
	if err != nil {
		return InstallResult{}, fmt.Errorf("verify installed Hextap: %w", err)
	}
	wantVersionOutput := fmt.Sprintf("brew-hextap %s (commit %s)", metadata.Version, options.ExpectedCommit)
	if versionOutput != wantVersionOutput {
		return InstallResult{}, fmt.Errorf("installed Hextap identity mismatch")
	}
	if _, err := runner.Run(ctx, Command{Name: brew, Args: []string{"test", "sean/hextap/hextap"}, Env: map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1"}}); err != nil {
		return InstallResult{}, fmt.Errorf("test installed Hextap Formula: %w", err)
	}
	for _, agent := range options.SkillAgents {
		if agent == "all" {
			return InstallResult{}, fmt.Errorf("dev install requires concrete skill agents rather than all")
		}
		if err := service.reconcileUserSkill(ctx, binary, agent); err != nil {
			return InstallResult{}, err
		}
	}
	return InstallResult{Brew: brew, Binary: binary, Version: metadata.Version, Commit: options.ExpectedCommit}, nil
}

func peeledTagCommit(output, tag string) string {
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "refs/tags/"+tag+"^{}" {
			return fields[0]
		}
	}
	return ""
}

func (service Service) findOwningHomebrew(ctx context.Context) (string, string, error) {
	binary := service.InstalledBinary
	if binary == "" {
		resolved, err := exec.LookPath("brew-hextap")
		if err != nil {
			return "", "", fmt.Errorf("find installed brew-hextap: %w", err)
		}
		binary = resolved
	}
	binaryReal, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", "", fmt.Errorf("resolve installed brew-hextap: %w", err)
	}
	candidates := append([]string(nil), service.BrewCandidates...)
	if len(candidates) == 0 {
		candidates = []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"}
		if path, lookErr := exec.LookPath("brew"); lookErr == nil {
			candidates = append(candidates, path)
		}
	}
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		prefix, commandErr := runSingleLine(ctx, service.runner(), Command{Name: candidate, Args: []string{"--prefix", "sean/hextap/hextap"}})
		if commandErr != nil {
			continue
		}
		owned := filepath.Join(prefix, "bin", "brew-hextap")
		ownedReal, err := filepath.EvalSymlinks(owned)
		if err == nil && ownedReal == binaryReal {
			return candidate, owned, nil
		}
	}
	return "", "", fmt.Errorf("no Homebrew installation owns the active brew-hextap")
}

func (service Service) reconcileUserSkill(ctx context.Context, binary, agent string) error {
	statusResult, err := service.runner().Run(ctx, Command{Name: binary, Args: []string{"skills", "status", "--agent", agent, "--scope", "user", "--json"}})
	if err != nil {
		return fmt.Errorf("inspect %s Hextap skill: %w", agent, err)
	}
	var document struct {
		Entries []skillinstall.StatusEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(statusResult.Stdout), &document); err != nil || len(document.Entries) != 1 {
		return fmt.Errorf("decode %s Hextap skill status", agent)
	}
	entry := document.Entries[0]
	var args []string
	switch entry.State {
	case skillinstall.CurrentState:
		return nil
	case skillinstall.NotInstalledState:
		args = []string{"skills", "install", "--agent", agent, "--scope", "user"}
	case skillinstall.UpdateAvailableState:
		args = []string{"skills", "upgrade", "--agent", agent, "--scope", "user"}
	default:
		return fmt.Errorf("%s Hextap skill state %s requires manual reconciliation", agent, entry.State)
	}
	if _, err := service.runner().Run(ctx, Command{Name: binary, Args: args}); err != nil {
		return fmt.Errorf("reconcile %s Hextap skill: %w", agent, err)
	}
	verified, err := service.runner().Run(ctx, Command{Name: binary, Args: []string{"skills", "status", "--agent", agent, "--scope", "user", "--json"}})
	if err != nil {
		return fmt.Errorf("verify %s Hextap skill after reconciliation", agent)
	}
	var verifiedDocument struct {
		Entries []skillinstall.StatusEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(verified.Stdout), &verifiedDocument); err != nil || len(verifiedDocument.Entries) != 1 || verifiedDocument.Entries[0].State != skillinstall.CurrentState {
		return fmt.Errorf("verify %s Hextap skill after reconciliation", agent)
	}
	return nil
}
