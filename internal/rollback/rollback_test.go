package rollback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const canonicalRemote = "https://github.com/SijanC147/homebrew-hextap.git"

type fixtureRunner struct {
	t              *testing.T
	commands       []Command
	reinstallError error
	onReinstall    func()
	serviceJSON    string
	formulaJSON    string
	caskJSON       string
	ghURL          string
}

type owningRunner struct {
	base    *fixtureRunner
	tapPath string
}

func (runner owningRunner) Run(ctx context.Context, command Command) (Result, error) {
	if slices.Equal(command.Args, []string{"--prefix", toolkitFormula}) {
		if command.Name == "/wrong-brew" {
			return Result{Stdout: "/wrong-prefix\n"}, nil
		}
		return Result{Stdout: "/right-prefix\n"}, nil
	}
	if command.Name == "/brew" && slices.Equal(command.Args, []string{"--repo", defaultTapName}) {
		return Result{Stdout: runner.tapPath + "\n"}, nil
	}
	return runner.base.Run(ctx, command)
}

func (runner *fixtureRunner) Run(ctx context.Context, command Command) (Result, error) {
	runner.t.Helper()
	runner.commands = append(runner.commands, command)
	if command.Name == "git" {
		return osRunner{}.Run(ctx, command)
	}
	if command.Name == "gh" {
		if runner.ghURL == "" {
			return Result{Stderr: "credential=must-not-leak"}, errors.New("command failed")
		}
		return Result{Stdout: runner.ghURL + "\n"}, nil
	}
	if command.Name != "/brew" {
		runner.t.Fatalf("unexpected command: %#v", command)
	}
	if command.Env["HOMEBREW_NO_AUTO_UPDATE"] != "1" || command.Env["HOMEBREW_NO_ANALYTICS"] != "1" {
		runner.t.Fatalf("Homebrew safety environment = %#v", command.Env)
	}
	switch {
	case slices.Equal(command.Args, []string{"services", "list", "--json"}):
		value := runner.serviceJSON
		if value == "" {
			value = "[]\n"
		}
		return Result{Stdout: value}, nil
	case len(command.Args) >= 4 && command.Args[0] == "info" && command.Args[1] == "--json=v2" && command.Args[2] == "--formula":
		return Result{Stdout: runner.formulaJSON}, nil
	case len(command.Args) >= 4 && command.Args[0] == "info" && command.Args[1] == "--json=v2" && command.Args[2] == "--cask":
		return Result{Stdout: runner.caskJSON}, nil
	case len(command.Args) >= 4 && command.Args[0] == "reinstall":
		if runner.onReinstall != nil {
			runner.onReinstall()
		}
		if runner.reinstallError != nil {
			return Result{Stderr: "token=must-not-leak"}, runner.reinstallError
		}
		return Result{Stdout: "reinstalled\n"}, nil
	default:
		runner.t.Fatalf("unexpected brew command: %#v", command)
		return Result{}, nil
	}
}

type tapFixture struct {
	path          string
	historicalSHA string
	currentSHA    string
	definition    string
	currentBytes  []byte
}

func TestLocalFormulaDefaultsToPlanAndRequiresExactConfirmation(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	runner := formulaRunner(t)
	service := fixtureService(fixture, runner, canonicalRemote)

	result, err := service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Executed || result.Plan.TargetVersion != "1.0.0" || result.Plan.CurrentVersion != "2.0.0" {
		t.Fatalf("plan result = %#v", result)
	}
	if !strings.Contains(result.Plan.Confirmation, fixture.historicalSHA) || result.Plan.Confirmation == "" {
		t.Fatalf("confirmation = %q", result.Plan.Confirmation)
	}
	assertFileAndTapClean(t, fixture)
	assertNoCommand(t, runner.commands, "reinstall")

	_, err = service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode,
		Execute: true, Confirm: "wrong",
	})
	if err == nil || !strings.Contains(err.Error(), "exact --confirm") {
		t.Fatalf("mismatched confirmation error = %v", err)
	}
	assertFileAndTapClean(t, fixture)
	assertNoCommand(t, runner.commands, "reinstall")
}

func TestPlanningSelectsHomebrewThatOwnsActiveHextapInsteadOfPATHOrder(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	baseRunner := formulaRunner(t)
	service := Service{
		Runner: owningRunner{base: baseRunner, tapPath: fixture.path}, Invocation: "hextap",
		Executable: "/active/hextap", BrewCandidates: []string{"/wrong-brew", "/brew"},
		ResolvePath: func(path string) (string, error) {
			switch path {
			case "/active/hextap", "/right-prefix/bin/brew-hextap":
				return "/cellar/hextap/2.0.0/bin/brew-hextap", nil
			case "/wrong-prefix/bin/brew-hextap":
				return "/wrong/cellar/hextap", nil
			default:
				return path, nil
			}
		},
		OwnedRemote: canonicalRemote, CommandTimeout: time.Minute,
	}
	result, err := service.Run(context.Background(), Options{Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode})
	if err != nil || result.Plan.TapPath != fixture.path {
		t.Fatalf("owning Homebrew plan = %#v, %v", result, err)
	}
	info := findCommand(t, baseRunner.commands, "info")
	if info.Name != "/brew" {
		t.Fatalf("selected Homebrew = %q", info.Name)
	}
}

func TestLocalFormulaExecutesBoundedReinstallAndRestoresExactTap(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	runner := formulaRunner(t)
	service := fixtureService(fixture, runner, canonicalRemote)
	plan := mustPlan(t, service, FormulaKind, fixture.historicalSHA)

	result, err := service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode,
		Execute: true, Confirm: plan.Confirmation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Executed || !result.Restored || !result.TapClean {
		t.Fatalf("execution result = %#v", result)
	}
	assertFileAndTapClean(t, fixture)
	reinstall := findCommand(t, runner.commands, "reinstall")
	if !slices.Equal(reinstall.Args, []string{"reinstall", "--formula", "--no-ask", "sean/hextap/demo"}) {
		t.Fatalf("reinstall command = %#v", reinstall)
	}
	if reinstall.Timeout <= 0 || reinstall.Timeout > 15*time.Minute {
		t.Fatalf("reinstall timeout = %s", reinstall.Timeout)
	}
	for _, key := range []string{"HOMEBREW_NO_INSTALL_CLEANUP", "HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK"} {
		if reinstall.Env[key] != "1" {
			t.Errorf("%s = %q", key, reinstall.Env[key])
		}
	}
}

func TestLocalRestoresAfterFailureAndCancellationWithoutLeakingDependencyOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "failure", err: errors.New("reinstall failed")},
		{name: "cancellation", err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFormulaTap(t, canonicalRemote)
			runner := formulaRunner(t)
			runner.reinstallError = test.err
			service := fixtureService(fixture, runner, canonicalRemote)
			plan := mustPlan(t, service, FormulaKind, fixture.historicalSHA)

			_, err := service.Run(context.Background(), Options{
				Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode,
				Execute: true, Confirm: plan.Confirmation,
			})
			if err == nil || !strings.Contains(err.Error(), "restored") {
				t.Fatalf("execution error = %v", err)
			}
			if strings.Contains(err.Error(), "must-not-leak") {
				t.Fatalf("dependency output leaked: %v", err)
			}
			assertFileAndTapClean(t, fixture)
		})
	}
}

func TestLocalRefusesActiveServiceBeforeCheckout(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	runner := formulaRunner(t)
	runner.serviceJSON = `[{"name":"demo","status":"started","user":"tester","file":"/tmp/demo.plist"}]`
	service := fixtureService(fixture, runner, canonicalRemote)

	_, err := service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode,
	})
	if err == nil || !strings.Contains(err.Error(), "active Homebrew service") {
		t.Fatalf("active service error = %v", err)
	}
	assertFileAndTapClean(t, fixture)
	assertNoCommand(t, runner.commands, "reinstall")
}

func TestLocalDetectsConcurrentDriftAndStillRestoresTarget(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	runner := formulaRunner(t)
	runner.onReinstall = func() {
		if err := os.WriteFile(filepath.Join(fixture.path, "CONCURRENT"), []byte("owned elsewhere\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := fixtureService(fixture, runner, canonicalRemote)
	plan := mustPlan(t, service, FormulaKind, fixture.historicalSHA)

	result, err := service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode,
		Execute: true, Confirm: plan.Confirmation,
	})
	if err == nil || !strings.Contains(err.Error(), "concurrent tap drift") {
		t.Fatalf("concurrent drift error = %v", err)
	}
	if !result.Restored || result.TapClean {
		t.Fatalf("concurrent result = %#v", result)
	}
	current, readErr := os.ReadFile(filepath.Join(fixture.path, fixture.definition))
	if readErr != nil || !bytes.Equal(current, fixture.currentBytes) {
		t.Fatalf("target was not restored: %v", readErr)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.path, "CONCURRENT")); statErr != nil {
		t.Fatalf("concurrent file was removed: %v", statErr)
	}
}

func TestCaskLocalRollbackUsesCaskReinstallAndVersionSelector(t *testing.T) {
	fixture := newCaskTap(t, canonicalRemote)
	runner := caskRunner(t, false)
	service := fixtureService(fixture, runner, canonicalRemote)
	planResult, err := service.Run(context.Background(), Options{
		Kind: CaskKind, Name: "desktop", ToVersion: "1.0.0", Mode: LocalMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planResult.Plan.TargetCommit != fixture.historicalSHA {
		t.Fatalf("version selected %s, want %s", planResult.Plan.TargetCommit, fixture.historicalSHA)
	}
	result, err := service.Run(context.Background(), Options{
		Kind: CaskKind, Name: "desktop", ToVersion: "1.0.0", Mode: LocalMode,
		Execute: true, Confirm: planResult.Plan.Confirmation,
	})
	if err != nil || !result.TapClean {
		t.Fatalf("cask execution = %#v, %v", result, err)
	}
	command := findCommand(t, runner.commands, "reinstall")
	if !slices.Equal(command.Args, []string{"reinstall", "--cask", "--no-ask", "--skip-cask-deps", "sean/hextap/desktop"}) {
		t.Fatalf("cask reinstall = %#v", command)
	}
}

func TestCaskRollbackRefusesPinnedInstallation(t *testing.T) {
	fixture := newCaskTap(t, canonicalRemote)
	runner := caskRunner(t, false)
	runner.caskJSON = strings.Replace(runner.caskJSON, `"outdated":false`, `"outdated":false,"pinned":true`, 1)
	service := fixtureService(fixture, runner, canonicalRemote)
	_, err := service.Run(context.Background(), Options{Kind: CaskKind, Name: "desktop", ToCommit: fixture.historicalSHA, Mode: LocalMode})
	if err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("pinned Cask error = %v", err)
	}
	assertNoCommand(t, runner.commands, "reinstall")
}

func TestRemoteFormulaReconciliationPreservesCanonicalRuntimeStructureAndBumpsScheme(t *testing.T) {
	current := []byte(formulaDefinition("2.0.0", "new", 2))
	historical := []byte(formulaDefinition("1.0.0", "old", 0))
	updated, scheme, err := reconcileRemoteDefinition(FormulaKind, current, historical, 2)
	if err != nil {
		t.Fatal(err)
	}
	if scheme != 3 || !strings.Contains(string(updated), "version_scheme 3") || !strings.Contains(string(updated), "/v1.0.0/") {
		t.Fatalf("reconciled Formula =\n%s", updated)
	}
	for _, canonical := range []string{
		`service do`, `run opt_bin/"demo"`, `CURRENT_SERVICE_VALUE`,
		`def caveats`, `CURRENT CAVEAT`, `test do`, `--self-test`,
	} {
		if !strings.Contains(string(updated), canonical) {
			t.Errorf("reconciled Formula lost %q", canonical)
		}
	}
	if strings.Contains(string(updated), "HISTORICAL CAVEAT") || strings.Contains(string(updated), "OLD_SERVICE_VALUE") {
		t.Fatalf("historical runtime structure leaked into remote rollback:\n%s", updated)
	}
}

func TestRemoteFormulaReconciliationUpdatesEveryArchitectureMetadataPair(t *testing.T) {
	current := []byte(`class Demo < Formula
  desc "Demo"
  homepage "https://example.invalid"
  license "MIT"

  if Hardware::CPU.arm?
    url "https://example.invalid/releases/download/v2.0.0/demo-arm.tar.gz"
    sha256 "current-arm"
  else
    url "https://example.invalid/releases/download/v2.0.0/demo-intel.tar.gz"
    sha256 "current-intel"
  end

  def install
    bin.install "demo"
  end

  test do
    system bin/"demo", "--current-test"
  end
end
`)
	historical := []byte(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(current), "v2.0.0", "v1.0.0"), "current-arm", "old-arm"), "current-intel", "old-intel"))
	historical = bytes.Replace(historical, []byte("--current-test"), []byte("--historical-test"), 1)
	updated, scheme, err := reconcileRemoteDefinition(FormulaKind, current, historical, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	for _, required := range []string{"v1.0.0/demo-arm", "v1.0.0/demo-intel", "old-arm", "old-intel", "version_scheme 1", "--current-test"} {
		if !strings.Contains(text, required) {
			t.Errorf("reconciled Formula lacks %q:\n%s", required, text)
		}
	}
	if scheme != 1 || strings.Contains(text, "--historical-test") {
		t.Fatalf("reconciled Formula =\n%s", text)
	}
}

func TestSchema2RemoteRollbackUpdatesAuthoritativeTemplateAndFormulaTogether(t *testing.T) {
	template := []byte(`class Demo < Formula
  desc "Demo"
  homepage "https://example.invalid"
  license "MIT"

  if Hardware::CPU.arm?
    url "@ARM64_URL@"
    sha256 "@ARM64_SHA256@"
  else
    url "@AMD64_URL@"
    sha256 "@AMD64_SHA256@"
  end

  def install
    bin.install "demo"
  end

  service do
    run opt_bin/"demo", "--current-service"
  end

  test do
    system bin/"demo", "--current-test"
  end
end
`)
	currentValues := profileValues{
		armURL: "https://example.invalid/releases/download/v2.0.0/demo-arm.tar.gz",
		armSHA: strings.Repeat("a", 64),
		amdURL: "https://example.invalid/releases/download/v2.0.0/demo-intel.tar.gz",
		amdSHA: strings.Repeat("b", 64),
	}
	historicalValues := profileValues{
		armURL: "https://example.invalid/releases/download/v1.0.0/demo-arm.tar.gz",
		armSHA: strings.Repeat("c", 64),
		amdURL: "https://example.invalid/releases/download/v1.0.0/demo-intel.tar.gz",
		amdSHA: strings.Repeat("d", 64),
	}
	current := renderProfile(template, currentValues)
	historical := bytes.Replace(renderProfile(template, historicalValues), []byte("--current-service"), []byte("--historical-service"), 1)
	updated, updatedTemplate, scheme, err := reconcileProfileFormula(current, historical, template, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{updated, updatedTemplate} {
		if !bytes.Contains(data, []byte("version_scheme 1")) {
			t.Fatalf("paired schema-2 output lacks version_scheme:\n%s", data)
		}
	}
	for _, required := range []string{"v1.0.0/demo-arm", "v1.0.0/demo-intel", strings.Repeat("c", 64), strings.Repeat("d", 64), "--current-service", "--current-test"} {
		if !strings.Contains(string(updated), required) {
			t.Errorf("updated Formula lacks %q", required)
		}
	}
	if scheme != 1 || strings.Contains(string(updated), "--historical-service") || !bytes.Equal(renderProfile(updatedTemplate, historicalValues), updated) {
		t.Fatalf("schema-2 paired reconciliation diverged:\nFORMULA\n%s\nTEMPLATE\n%s", updated, updatedTemplate)
	}
}

func TestSchema2RemoteRollbackRejectsFormulaTemplateDrift(t *testing.T) {
	template := []byte(`class Demo < Formula
  license "MIT"
  if Hardware::CPU.arm?
    url "@ARM64_URL@"
    sha256 "@ARM64_SHA256@"
  else
    url "@AMD64_URL@"
    sha256 "@AMD64_SHA256@"
  end
  def install
    bin.install "demo"
  end
end
`)
	values := profileValues{
		armURL: "https://example.invalid/releases/download/v2.0.0/demo-arm.tar.gz", armSHA: strings.Repeat("a", 64),
		amdURL: "https://example.invalid/releases/download/v2.0.0/demo-intel.tar.gz", amdSHA: strings.Repeat("b", 64),
	}
	current := append(renderProfile(template, values), []byte("# drift\n")...)
	if _, _, _, err := reconcileProfileFormula(current, renderProfile(template, values), template, 0); err == nil || !strings.Contains(err.Error(), "byte-identical") {
		t.Fatalf("template drift error = %v", err)
	}
}

func TestRemoteReconciliationFailsClosedOnHistoricalStructureDrift(t *testing.T) {
	current := []byte(formulaDefinition("2.0.0", "new", 2))
	historical := bytes.Replace([]byte(formulaDefinition("1.0.0", "old", 0)), []byte("  sha256 "), []byte("    sha256 "), 1)
	if _, _, err := reconcileRemoteDefinition(FormulaKind, current, historical, 2); err == nil || !strings.Contains(err.Error(), "release metadata") {
		t.Fatalf("structure mismatch error = %v", err)
	}
}

func TestRemoteCaskReconciliationReplacesEveryMultilineChecksum(t *testing.T) {
	current := []byte(`cask "desktop" do
  version "2.0.0"
  sha256 arm64_linux:  "current-arm",
         x86_64_linux: "current-intel"
  url "https://example.invalid/desktop-#{version}.zip"
  binary "desktop"
  caveats "CURRENT CASK CAVEAT"
end
`)
	historical := []byte(`cask "desktop" do
  version "1.0.0"
  sha256 arm64_linux:  "old-arm",
         x86_64_linux: "old-intel"
  url "https://example.invalid/desktop-#{version}.zip"
  binary "desktop"
  caveats "HISTORICAL CASK CAVEAT"
end
`)
	updated, scheme, err := reconcileRemoteDefinition(CaskKind, current, historical, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	for _, required := range []string{`version "1.0.0"`, `"old-arm"`, `"old-intel"`, `CURRENT CASK CAVEAT`} {
		if !strings.Contains(text, required) {
			t.Errorf("reconciled Cask lacks %q:\n%s", required, text)
		}
	}
	if scheme != 0 || strings.Contains(text, "current-arm") || strings.Contains(text, "current-intel") || strings.Contains(text, "HISTORICAL CASK CAVEAT") {
		t.Fatalf("reconciled Cask =\n%s", text)
	}
}

func TestRemoteFormulaExecutionPushesOnlyOwnedFeatureBranchAndCreatesProtectedPR(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "homebrew-hextap.git")
	runGit(t, "init", "--bare", bare)
	fixture := newFormulaTap(t, bare)
	runGitAt(t, fixture.path, "push", "-u", "origin", "main")
	runner := formulaRunner(t)
	runner.ghURL = "https://github.com/SijanC147/homebrew-hextap/pull/99"
	service := fixtureService(fixture, runner, bare)
	plan := mustRemotePlan(t, service, FormulaKind, fixture.historicalSHA)

	result, err := service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: RemoteMode,
		Execute: true, Confirm: plan.Confirmation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Executed || result.PullRequestURL != runner.ghURL || !strings.HasPrefix(result.Plan.Branch, "codex/hextap-rollback-") {
		t.Fatalf("remote result = %#v", result)
	}
	if got := strings.TrimSpace(runGitOutput(t, "--git-dir", bare, "rev-parse", "refs/heads/main")); got != fixture.currentSHA {
		t.Fatalf("remote main moved to %s, want %s", got, fixture.currentSHA)
	}
	branchSHA := strings.TrimSpace(runGitOutput(t, "--git-dir", bare, "rev-parse", "refs/heads/"+result.Plan.Branch))
	if branchSHA == "" || branchSHA == fixture.currentSHA {
		t.Fatalf("feature branch SHA = %q", branchSHA)
	}
	push := findGitPush(t, runner.commands)
	if slices.Contains(push.Args, "--force") || slices.Contains(push.Args, "main") || !slices.Contains(push.Args, "HEAD:refs/heads/"+result.Plan.Branch) {
		t.Fatalf("unsafe push command = %#v", push)
	}
	gh := findNamedCommand(t, runner.commands, "gh")
	if !slices.Contains(gh.Args, "--base") || !slices.Contains(gh.Args, "main") || slices.Contains(gh.Args, "merge") {
		t.Fatalf("unsafe PR command = %#v", gh)
	}
}

func TestRemoteCaskExplainsActualUpgradeConvergence(t *testing.T) {
	fixture := newCaskTap(t, canonicalRemote)
	for _, test := range []struct {
		name        string
		autoUpdates bool
		want        string
	}{
		{name: "ordinary cask", want: "brew update && brew upgrade --cask sean/hextap/desktop"},
		{name: "auto updates", autoUpdates: true, want: "--greedy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := caskRunner(t, test.autoUpdates)
			service := fixtureService(fixture, runner, canonicalRemote)
			result, err := service.Run(context.Background(), Options{
				Kind: CaskKind, Name: "desktop", ToCommit: fixture.historicalSHA, Mode: RemoteMode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Plan.Convergence, test.want) {
				t.Fatalf("convergence = %q, want %q", result.Plan.Convergence, test.want)
			}
		})
	}
}

func TestRemotePRFailureDoesNotLeakDependencyOutputOrMoveMain(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "homebrew-hextap.git")
	runGit(t, "init", "--bare", bare)
	fixture := newFormulaTap(t, bare)
	runGitAt(t, fixture.path, "push", "-u", "origin", "main")
	runner := formulaRunner(t)
	service := fixtureService(fixture, runner, bare)
	plan := mustRemotePlan(t, service, FormulaKind, fixture.historicalSHA)
	_, err := service.Run(context.Background(), Options{
		Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: RemoteMode,
		Execute: true, Confirm: plan.Confirmation,
	})
	if err == nil || !strings.Contains(err.Error(), "pull-request creation failed") || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("PR failure = %v", err)
	}
	if got := strings.TrimSpace(runGitOutput(t, "--git-dir", bare, "rev-parse", "refs/heads/main")); got != fixture.currentSHA {
		t.Fatalf("remote main moved to %s", got)
	}
}

func TestValidationRejectsAmbiguityDirtyTapUnsafeNamesAndNonAncestor(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	runner := formulaRunner(t)
	service := fixtureService(fixture, runner, canonicalRemote)
	for _, test := range []struct {
		name    string
		options Options
		want    string
	}{
		{name: "missing selector", options: Options{Kind: FormulaKind, Name: "demo", Mode: LocalMode}, want: "exactly one"},
		{name: "two selectors", options: Options{Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, ToVersion: "1.0.0", Mode: LocalMode}, want: "exactly one"},
		{name: "unsafe name", options: Options{Kind: FormulaKind, Name: "../demo", ToCommit: fixture.historicalSHA, Mode: LocalMode}, want: "package name"},
		{name: "short SHA", options: Options{Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA[:8], Mode: LocalMode}, want: "full 40-character"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Run(context.Background(), test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(fixture.path, "DIRTY"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := service.Run(context.Background(), Options{Kind: FormulaKind, Name: "demo", ToCommit: fixture.historicalSHA, Mode: LocalMode})
	if err == nil || !strings.Contains(err.Error(), "tap must be clean") {
		t.Fatalf("dirty tap error = %v", err)
	}
}

func TestCLIHelpJSONAndEveryShortAlias(t *testing.T) {
	fixture := newFormulaTap(t, canonicalRemote)
	runner := formulaRunner(t)
	service := fixtureService(fixture, runner, canonicalRemote)
	service.Invocation = "hextap"
	for _, args := range [][]string{{"-h"}, {"--help"}, {"formula", "-h"}, {"formula", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := service.RunCLI(context.Background(), args, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Safety:") || !strings.Contains(stdout.String(), "Examples:") {
			t.Fatalf("help %v exit=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	commandsBefore := len(runner.commands)
	var stdout, stderr bytes.Buffer
	code := service.RunCLI(context.Background(), []string{"formula", "demo", "-t", fixture.historicalSHA, "-m", "local", "-j"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("short aliases exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var outcome Outcome
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil || outcome.Plan.TargetCommit != fixture.historicalSHA || outcome.Executed {
		t.Fatalf("CLI JSON = %#v, %v", outcome, err)
	}
	if len(runner.commands) == commandsBefore {
		t.Fatal("CLI did not build a real plan")
	}
}

func newFormulaTap(t *testing.T, remote string) tapFixture {
	t.Helper()
	return newTap(t, remote, "Formula/demo.rb", formulaDefinition("1.0.0", "old", 0), formulaDefinition("2.0.0", "new", 2))
}

func newCaskTap(t *testing.T, remote string) tapFixture {
	t.Helper()
	return newTap(t, remote, "Casks/desktop.rb", caskDefinition("1.0.0", "old"), caskDefinition("2.0.0", "new"))
}

func newTap(t *testing.T, remote, definition, historical, current string) tapFixture {
	t.Helper()
	root := t.TempDir()
	runGitAt(t, root, "init", "-b", "main")
	runGitAt(t, root, "config", "user.name", "Hextap Test")
	runGitAt(t, root, "config", "user.email", "hextap@example.invalid")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(definition)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, definition), []byte(historical), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, root, "add", definition)
	runGitAt(t, root, "commit", "-m", "old definition")
	historicalSHA := strings.TrimSpace(runGitOutput(t, "-C", root, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(root, definition), []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, root, "add", definition)
	runGitAt(t, root, "commit", "-m", "current definition")
	currentSHA := strings.TrimSpace(runGitOutput(t, "-C", root, "rev-parse", "HEAD"))
	runGitAt(t, root, "remote", "add", "origin", remote)
	return tapFixture{path: root, historicalSHA: historicalSHA, currentSHA: currentSHA, definition: definition, currentBytes: []byte(current)}
}

func formulaDefinition(version, digest string, scheme int) string {
	schemeLine := ""
	if scheme != 0 {
		schemeLine = "  version_scheme " + string(rune('0'+scheme)) + "\n"
	}
	serviceValue := "CURRENT_SERVICE_VALUE"
	caveat := "CURRENT CAVEAT"
	if version == "1.0.0" {
		serviceValue = "OLD_SERVICE_VALUE"
		caveat = "HISTORICAL CAVEAT"
	}
	return "class Demo < Formula\n" +
		"  desc \"Demo\"\n" +
		"  homepage \"https://example.invalid\"\n" +
		"  license \"MIT\"\n" + schemeLine +
		"\n" +
		"  url \"https://example.invalid/releases/download/v" + version + "/demo.tar.gz\"\n" +
		"  sha256 \"" + strings.Repeat(digest, 64)[:64] + "\"\n" +
		"\n" +
		"  def install\n" +
		"    bin.install \"demo\"\n" +
		"  end\n" +
		"\n" +
		"  service do\n" +
		"    run opt_bin/\"demo\"\n" +
		"    environment_variables DEMO_VALUE: \"" + serviceValue + "\"\n" +
		"  end\n" +
		"\n" +
		"  def caveats\n" +
		"    \"" + caveat + "\"\n" +
		"  end\n" +
		"\n" +
		"  test do\n" +
		"    system bin/\"demo\", \"--self-test\"\n" +
		"  end\n" +
		"end\n"
}

func caskDefinition(version, digest string) string {
	return "cask \"desktop\" do\n" +
		"  version \"" + version + "\"\n" +
		"  sha256 \"" + strings.Repeat(digest, 64)[:64] + "\"\n" +
		"  url \"https://example.invalid/desktop-#{version}.zip\"\n" +
		"  name \"Desktop\"\n" +
		"  desc \"Desktop demo\"\n" +
		"  homepage \"https://example.invalid\"\n" +
		"  binary \"desktop\"\n" +
		"  caveats \"CURRENT CASK CAVEAT\"\n" +
		"end\n"
}

func formulaRunner(t *testing.T) *fixtureRunner {
	t.Helper()
	return &fixtureRunner{
		t:           t,
		formulaJSON: `{"formulae":[{"name":"demo","full_name":"sean/hextap/demo","versions":{"stable":"2.0.0"},"installed":[{"version":"2.0.0"}],"outdated":false,"pinned":false,"version_scheme":2,"service":{"run":"/opt/demo","environment_variables":{"TOKEN":"secret-must-not-leak"}}}],"casks":[]}`,
	}
}

func caskRunner(t *testing.T, autoUpdates bool) *fixtureRunner {
	t.Helper()
	value := "false"
	if autoUpdates {
		value = "true"
	}
	return &fixtureRunner{
		t:        t,
		caskJSON: `{"formulae":[],"casks":[{"token":"desktop","full_token":"sean/hextap/desktop","version":"2.0.0","installed":"2.0.0","outdated":false,"auto_updates":` + value + `,"artifacts":[{"binary":["desktop"]}]}]}`,
	}
}

func fixtureService(fixture tapFixture, runner *fixtureRunner, ownedRemote string) Service {
	return Service{
		Runner: runner, Brew: "/brew", TapPath: fixture.path, TapName: "sean/hextap",
		OwnedRemote: ownedRemote, CommandTimeout: time.Minute, TempRoot: filepath.Dir(fixture.path),
	}
}

func mustPlan(t *testing.T, service Service, kind Kind, commit string) Plan {
	t.Helper()
	result, err := service.Run(context.Background(), Options{Kind: kind, Name: "demo", ToCommit: commit, Mode: LocalMode})
	if err != nil {
		t.Fatal(err)
	}
	return result.Plan
}

func mustRemotePlan(t *testing.T, service Service, kind Kind, commit string) Plan {
	t.Helper()
	result, err := service.Run(context.Background(), Options{Kind: kind, Name: "demo", ToCommit: commit, Mode: RemoteMode})
	if err != nil {
		t.Fatal(err)
	}
	return result.Plan
}

func assertFileAndTapClean(t *testing.T, fixture tapFixture) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.path, fixture.definition))
	if err != nil || !bytes.Equal(data, fixture.currentBytes) {
		t.Fatalf("definition not restored exactly: %v", err)
	}
	if got := strings.TrimSpace(runGitOutput(t, "-C", fixture.path, "rev-parse", "HEAD")); got != fixture.currentSHA {
		t.Fatalf("tap HEAD = %s, want %s", got, fixture.currentSHA)
	}
	if got := runGitOutput(t, "-C", fixture.path, "status", "--porcelain=v1"); got != "" {
		t.Fatalf("tap is dirty: %q", got)
	}
}

func assertNoCommand(t *testing.T, commands []Command, argument string) {
	t.Helper()
	for _, command := range commands {
		if slices.Contains(command.Args, argument) {
			t.Fatalf("unexpected command: %#v", command)
		}
	}
}

func findCommand(t *testing.T, commands []Command, argument string) Command {
	t.Helper()
	for _, command := range commands {
		if slices.Contains(command.Args, argument) {
			return command
		}
	}
	t.Fatalf("command containing %q not found in %#v", argument, commands)
	return Command{}
}

func findNamedCommand(t *testing.T, commands []Command, name string) Command {
	t.Helper()
	for _, command := range commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("command %q not found in %#v", name, commands)
	return Command{}
}

func findGitPush(t *testing.T, commands []Command) Command {
	t.Helper()
	for _, command := range commands {
		if command.Name == "git" && slices.Contains(command.Args, "push") {
			return command
		}
	}
	t.Fatalf("git push not found in %#v", commands)
	return Command{}
}

func runGit(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Env = sanitizedEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func runGitAt(t *testing.T, directory string, args ...string) {
	t.Helper()
	runGit(t, append([]string{"-C", directory}, args...)...)
}

func runGitOutput(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Env = sanitizedEnvironment()
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

func sanitizedEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_CONFIG_COUNT=") || strings.HasPrefix(entry, "GIT_CONFIG_KEY_") || strings.HasPrefix(entry, "GIT_CONFIG_VALUE_") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
