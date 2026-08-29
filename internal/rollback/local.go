package rollback

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (service Service) executeLocal(ctx context.Context, prepared preparedPlan, outcome Outcome) (Outcome, error) {
	if err := service.recheckTap(ctx, prepared, true); err != nil {
		return outcome, err
	}
	if prepared.plan.Kind == FormulaKind {
		if err := service.requireInactiveService(ctx, prepared.brew, prepared.plan.Name); err != nil {
			return outcome, err
		}
	}
	runner := service.runner()
	checkout := service.command("git", "-C", prepared.tapPath, "checkout", prepared.plan.TargetCommit, "--", prepared.definition)
	if _, err := runner.Run(ctx, checkout); err != nil {
		return outcome, fmt.Errorf("could not temporarily check out the historical %s definition", prepared.plan.Kind)
	}

	reinstallTimeout := service.ReinstallTimeout
	if reinstallTimeout <= 0 {
		reinstallTimeout = 10 * time.Minute
	}
	flag := "--formula"
	reinstallArgs := []string{"reinstall", flag, "--no-ask", prepared.plan.FullName}
	if prepared.plan.Kind == CaskKind {
		flag = "--cask"
		reinstallArgs = []string{"reinstall", flag, "--no-ask", "--skip-cask-deps", prepared.plan.FullName}
	} else {
		reinstallArgs[1] = flag
	}
	reinstall := service.command(prepared.brew, reinstallArgs...)
	reinstall.Env = homebrewEnvironment()
	reinstall.Timeout = reinstallTimeout
	_, reinstallErr := runner.Run(ctx, reinstall)

	drift := service.concurrentDrift(prepared)
	restored, clean, restoreErr := service.restoreTap(prepared)
	outcome.Restored = restored
	outcome.TapClean = clean
	if restoreErr != nil {
		return outcome, fmt.Errorf("rollback restoration failed; manual tap recovery is required: %w", restoreErr)
	}
	if drift || !clean {
		return outcome, fmt.Errorf("concurrent tap drift was detected; the selected definition was restored but the tap is not clean")
	}
	if reinstallErr != nil {
		return outcome, fmt.Errorf("Homebrew reinstall failed; the exact original tap definition was restored and the tap is clean")
	}
	outcome.Executed = true
	return outcome, nil
}

func (service Service) recheckTap(ctx context.Context, prepared preparedPlan, requireClean bool) error {
	head, err := runLine(ctx, service.runner(), service.command("git", "-C", prepared.tapPath, "rev-parse", "HEAD"))
	if err != nil || head != prepared.plan.OriginalCommit {
		return fmt.Errorf("tap HEAD changed after planning; refusing stale rollback")
	}
	branch, err := runLine(ctx, service.runner(), service.command("git", "-C", prepared.tapPath, "symbolic-ref", "--quiet", "--short", "HEAD"))
	if err != nil || branch != prepared.branch {
		return fmt.Errorf("tap branch changed after planning; refusing stale rollback")
	}
	if requireClean {
		status, statusErr := service.runner().Run(ctx, service.command("git", "-C", prepared.tapPath, "status", "--porcelain=v1", "--untracked-files=all"))
		if statusErr != nil || status.Stdout != "" {
			return fmt.Errorf("tap changed after planning; refusing stale rollback")
		}
	}
	data, readErr := os.ReadFile(filepath.Join(prepared.tapPath, filepath.FromSlash(prepared.definition)))
	if readErr != nil || !bytes.Equal(data, prepared.currentBytes) {
		return fmt.Errorf("tap definition changed after planning; refusing stale rollback")
	}
	return nil
}

func (service Service) concurrentDrift(prepared preparedPlan) bool {
	path := filepath.Join(prepared.tapPath, filepath.FromSlash(prepared.definition))
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, prepared.historicalBytes) {
		return true
	}
	result, err := service.runner().Run(context.Background(), service.command("git", "-C", prepared.tapPath, "status", "--porcelain=v1", "--untracked-files=all"))
	if err != nil {
		return true
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) != 1 || len(lines[0]) < 4 || strings.TrimSpace(lines[0][3:]) != prepared.definition {
		return true
	}
	return false
}

func (service Service) restoreTap(prepared preparedPlan) (restored, clean bool, err error) {
	restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(prepared.tapPath, filepath.FromSlash(prepared.definition))
	checkout := service.command("git", "-C", prepared.tapPath, "checkout", prepared.plan.OriginalCommit, "--", prepared.definition)
	_, checkoutErr := service.runner().Run(restoreCtx, checkout)
	if checkoutErr != nil {
		if writeErr := atomicWrite(path, prepared.currentBytes, prepared.currentMode); writeErr != nil {
			return false, false, fmt.Errorf("restore exact definition bytes")
		}
		reset := service.command("git", "-C", prepared.tapPath, "reset", prepared.plan.OriginalCommit, "--", prepared.definition)
		if _, resetErr := service.runner().Run(restoreCtx, reset); resetErr != nil {
			return true, false, fmt.Errorf("restore definition index")
		}
	}
	data, readErr := os.ReadFile(path)
	restored = readErr == nil && bytes.Equal(data, prepared.currentBytes)
	head, headErr := runLine(restoreCtx, service.runner(), service.command("git", "-C", prepared.tapPath, "rev-parse", "HEAD"))
	branch, branchErr := runLine(restoreCtx, service.runner(), service.command("git", "-C", prepared.tapPath, "symbolic-ref", "--quiet", "--short", "HEAD"))
	status, statusErr := service.runner().Run(restoreCtx, service.command("git", "-C", prepared.tapPath, "status", "--porcelain=v1", "--untracked-files=all"))
	clean = restored && headErr == nil && branchErr == nil && statusErr == nil && head == prepared.plan.OriginalCommit && branch == prepared.branch && status.Stdout == ""
	if !restored {
		return false, clean, fmt.Errorf("exact original definition bytes were not restored")
	}
	if headErr != nil || head != prepared.plan.OriginalCommit {
		return true, false, fmt.Errorf("tap HEAD changed during rollback")
	}
	if branchErr != nil || branch != prepared.branch {
		return true, false, fmt.Errorf("tap branch changed during rollback")
	}
	if statusErr != nil {
		return true, false, fmt.Errorf("could not prove final tap cleanliness")
	}
	return true, clean, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".hextap-rollback-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
