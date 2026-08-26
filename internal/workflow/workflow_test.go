package workflow_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const customManifest = `{
  "schema": 1,
  "formula": {
    "name": "custom-formula",
    "class": "CustomFormula",
    "description": "Custom release asset contract",
    "homepage": "https://github.com/SijanC147/custom-project",
    "license": "MIT",
    "repository": {"owner": "SijanC147", "name": "custom-project"},
    "binary": "custom-binary",
    "assets": {
      "darwin_arm64": "renamed-aarch64-package.tar.gz",
      "darwin_amd64": "renamed-x86_64-package.tar.gz"
    }
  },
  "release": {"build_script": "scripts/hextap-build", "linux": false},
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "service": {"enabled": false},
    "caveats": ""
  }
}`

func TestReleaseWorkflowTrustedHandoffContract(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")

	assertContains(t, workflow, "group: hextap-release-${{ github.repository }}")
	assertContains(t, workflow, "cancel-in-progress: false")
	assertContains(t, workflow, "queue: max")
	assertCount(t, workflow, "repository: SijanC147/hextap-toolkit", 5)
	assertCount(t, workflow, "ref: ${{ job.workflow_sha }}", 5)
	assertNotContains(t, workflow, "ref: main")
	assertNotContains(t, workflow, "github.workflow_sha")
	assertNotContains(t, workflow, "examples/")

	assertCount(t, workflow, "${{ inputs.manifest_path }}", 1)
	assertContains(t, workflow, "RAW_RELEASE_TAG: ${{ inputs.tag }}")
	assertContains(t, workflow, "RAW_RELEASE_MODE: ${{ inputs.mode }}")
	assertContains(t, workflow, `--tag "$RAW_RELEASE_TAG"`)
	assertContains(t, workflow, `--mode "$RAW_RELEASE_MODE"`)
	assertNotContains(t, workflow, `--tag "${{ inputs.tag }}"`)
	assertNotContains(t, workflow, `--mode "${{ inputs.mode }}"`)

	detachAt := strings.Index(workflow, `git -C source checkout --detach "$sha"`)
	manifestInputAt := strings.Index(workflow, "${{ inputs.manifest_path }}")
	if detachAt == -1 || manifestInputAt == -1 || detachAt >= manifestInputAt {
		t.Fatalf("caller manifest input must only be handled after detaching the resolved tag SHA")
	}
	assertContains(t, workflow, `git -C source merge-base --is-ancestor "$sha" origin/main`)
	assertContains(t, workflow, `[[ "$GITHUB_REF" == "refs/tags/$RELEASE_TAG" ]]`)
	assertContains(t, workflow, `[[ "$GITHUB_SHA" == "$sha" ]]`)
	assertContains(t, workflow, `validated_manifest="$RUNNER_TEMP/project-manifest.json"`)
	assertContains(t, workflow, `pathspec=":(top,literal)$CALLER_MANIFEST_PATH"`)
	assertContains(t, workflow, `ls-files --error-unmatch --stage -- "$pathspec"`)
	assertNotContains(t, workflow, `--format='%(objectmode)`)
	assertContains(t, workflow, `git -C "$source_root" hash-object --no-filters "$validated_manifest"`)
	assertContains(t, workflow, `--file "$validated_manifest"`)

	assertContains(t, workflow, "manifest_artifact_id: ${{ steps.manifest-artifact.outputs.artifact-id }}")
	assertContains(t, workflow, "manifest_sha256: ${{ steps.manifest.outputs.sha256 }}")
	assertContains(t, workflow, "release_assets_artifact_id: ${{ steps.release-assets.outputs.artifact-id }}")
	assertCount(t, workflow, "artifact-ids: ${{ needs.validate.outputs.manifest_artifact_id }}", 4)
	assertCount(t, workflow, "artifact-ids: ${{ needs.build.outputs.release_assets_artifact_id }}", 2)
	assertCount(t, workflow, "digest-mismatch: error", 6)
	assertCount(t, workflow, "merge-multiple: true", 6)
	assertCount(t, workflow, "overwrite: false", 2)
	assertContains(t, workflow, "manifest-${{ github.run_id }}-${{ github.run_attempt }}-${{ job.check_run_id }}")
	assertContains(t, workflow, "release-assets-${{ github.run_id }}-${{ github.run_attempt }}-${{ job.check_run_id }}")
	assertCount(t, workflow, "EXPECTED_MANIFEST_SHA256: ${{ needs.validate.outputs.manifest_sha256 }}", 4)
	assertContains(t, workflow, `mktemp -d "$GITHUB_WORKSPACE/source/.hextap-manifest.XXXXXX"`)

	homebrewCheckAt := strings.Index(workflow, "Validate Homebrew-only release")
	manifestUploadAt := strings.Index(workflow, "id: manifest-artifact")
	if homebrewCheckAt == -1 || manifestUploadAt == -1 || manifestUploadAt <= homebrewCheckAt {
		t.Fatalf("validated manifest must be uploaded after mode-specific validation")
	}

	assertCount(t, workflow, "OP_SERVICE_ACCOUNT_TOKEN:", 1)
	assertCount(t, workflow, "op_service_account_token", 2)
	assertNotContains(t, workflow, "secrets: inherit")
	assertNotContains(t, workflow, "administration:")
	assertContains(t, workflow, "needs.validate.outputs.stable == 'true'")
	assertContains(t, workflow, "needs.validate.outputs.mode == 'homebrew-only' && needs.release.result == 'skipped'")
	assertContains(t, workflow, "needs: [validate, quality, build, native-verify]")
	assertContains(t, workflow, `state="$(gh release view "$RELEASE_TAG"`)
	assertContains(t, workflow, `gh release verify "$RELEASE_TAG"`)
	assertContains(t, workflow, `if state="$(gh release view "$RELEASE_TAG"`)
}

func TestReleaseWorkflowNativeMatrixHonorsManifestLinux(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release-go.yml")

	assertContains(t, workflow, "linux: ${{ steps.manifest.outputs.linux }}")
	assertContains(t, workflow, "native_matrix: ${{ steps.native-matrix.outputs.matrix }}")
	assertContains(t, workflow, "RELEASE_LINUX: ${{ steps.manifest.outputs.linux }}")
	assertContains(t, workflow, "matrix: ${{ fromJSON(needs.validate.outputs.native_matrix) }}")
	assertContains(t, workflow, `if [[ "$RELEASE_LINUX" == true ]]; then`)
	assertContains(t, workflow, `elif [[ "$RELEASE_LINUX" != false ]]; then`)

	matrixStep := textBetween(t, workflow, "- name: Select native verification matrix", "- name: Validate Homebrew-only release")
	assertCount(t, matrixStep, `"target":"linux-amd64"`, 1)
	assertCount(t, matrixStep, `"target":"linux-arm64"`, 1)
	assertCount(t, matrixStep, `"target":"darwin-arm64"`, 2)
	assertCount(t, matrixStep, `"target":"darwin-amd64"`, 2)
}

func TestPublishHomebrewContract(t *testing.T) {
	script := readRepositoryFile(t, "scripts/publish-homebrew.sh")

	assertContains(t, script, "[[ $# -ne 7 ]]")
	assertContains(t, script, "source_manifest=\"$5\"")
	assertContains(t, script, `[[ -f "$source_manifest" && ! -L "$source_manifest" ]]`)
	assertContains(t, script, `source == registered`)
	assertContains(t, script, `fetch("darwin_arm64")`)
	assertContains(t, script, `fetch("darwin_amd64")`)
	assertContains(t, script, `[[ "$arm64_asset" != "$amd64_asset" ]]`)
	assertContains(t, script, `basename -- "$arm64_asset"`)
	assertContains(t, script, `basename -- "$amd64_asset"`)
	assertNotContains(t, script, `$formula-darwin-`)

	assertContains(t, script, `git -C "$attempt_dir" add "Formula/$formula.rb"`)
	assertContains(t, script, `[[ "$changed" == "Formula/$formula.rb" ]]`)
	assertNotContains(t, script, `git -C "$attempt_dir" add .`)
	assertContains(t, script, `for attempt in 1 2 3; do`)
	assertContains(t, script, `push origin HEAD:main`)
	assertContains(t, script, `tap_commit="$(git -C "$attempt_dir" rev-parse HEAD)"`)
	assertNotContains(t, script, `log -1 --format=%H -- "Formula/$formula.rb"`)
	assertContains(t, script, `if (( attempt < 3 )); then`)
	assertContains(t, script, `sleep "$attempt"`)
	assertContains(t, script, `push_output="$(git -C "$attempt_dir" push origin HEAD:main 2>&1)"`)
	assertContains(t, script, `push_diagnostic="${push_output,,}"`)
	assertContains(t, script, `-f head_sha="$tap_commit"`)
	assertContains(t, script, `run_events+=(workflow_dispatch)`)
	assertContains(t, script, `-f event="$candidate_event"`)
	assertContains(t, script, `run.fetch("path") == ".github/workflows/tests.yml"`)
	assertContains(t, script, `run.fetch("head_sha") == ARGV[0]`)
	assertContains(t, script, `run.fetch("event") == ARGV[1]`)
}

func TestPublishReleaseDraftContract(t *testing.T) {
	script := readRepositoryFile(t, "scripts/publish-release.sh")

	assertContains(t, script, `gh release create "${args[@]}"`)
	assertContains(t, script, `echo "existing draft asset differs: $name"`)
	assertContains(t, script, `gh release upload "$tag" "$asset_dir/$name"`)
	assertContains(t, script, `echo "release asset set mismatch"`)
	assertContains(t, script, `gh release edit "$tag" --repo "$repository" --draft=false`)
}

func TestPublishHomebrewUsesCustomManifestAssets(t *testing.T) {
	tapManifest := compactJSON(t, customManifest)
	result := runPublisher(t, customManifest, tapManifest)
	if result.err != nil {
		t.Fatalf("publish-homebrew.sh failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "already-current custom-formula 1.2.3") {
		t.Fatalf("stdout = %q, want already-current result", result.stdout)
	}
	if !strings.Contains(result.hextapLog, "--arm64-sha "+strings.Repeat("a", 64)) {
		t.Fatalf("hextapctl log missing custom arm64 checksum: %q", result.hextapLog)
	}
	if !strings.Contains(result.hextapLog, "--amd64-sha "+strings.Repeat("b", 64)) {
		t.Fatalf("hextapctl log missing custom amd64 checksum: %q", result.hextapLog)
	}
}

func TestPublishHomebrewAlreadyCurrentAcceptsExactManualTapGate(t *testing.T) {
	result := runPublisherWithOptions(t, customManifest, compactJSON(t, customManifest), publisherOptions{
		tapRunMode: "workflow_dispatch",
	})
	if result.err != nil {
		t.Fatalf("publish-homebrew.sh failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "already-current custom-formula 1.2.3") {
		t.Fatalf("stdout = %q, want already-current result", result.stdout)
	}
	if !strings.Contains(result.ghLog, "event=push") {
		t.Fatalf("publisher did not prefer the automatic push gate: %q", result.ghLog)
	}
	if !strings.Contains(result.ghLog, "event=workflow_dispatch") {
		t.Fatalf("publisher did not fall back to the explicit main gate: %q", result.ghLog)
	}
}

func TestPublishHomebrewNewPublicationRequiresAutomaticPushGate(t *testing.T) {
	result := runPublisherWithOptions(t, customManifest, compactJSON(t, customManifest), publisherOptions{
		gitChanged: true,
		tapRunMode: "workflow_dispatch",
	})
	if result.err == nil {
		t.Fatalf("publish-homebrew.sh accepted a manual gate for a new tap commit\nstdout:\n%s\nstderr:\n%s", result.stdout, result.stderr)
	}
	if strings.Contains(result.ghLog, "event=workflow_dispatch") {
		t.Fatalf("new publication fell back to a manual gate: %q", result.ghLog)
	}
}

func TestPublishHomebrewFailsFastWithPushDiagnostic(t *testing.T) {
	for _, test := range []struct {
		name       string
		pushMode   string
		diagnostic string
	}{
		{name: "authorization", pushMode: "auth", diagnostic: "remote: Permission to SijanC147/homebrew-hextap denied"},
		{name: "branch protection", pushMode: "protected", diagnostic: "GH006: Protected branch update failed for refs/heads/main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runPublisherWithOptions(t, customManifest, compactJSON(t, customManifest), publisherOptions{
				gitChanged: true,
				pushMode:   test.pushMode,
			})
			if result.err == nil {
				t.Fatalf("publish-homebrew.sh succeeded after %s rejection", test.name)
			}
			if !strings.Contains(result.stderr, test.diagnostic) {
				t.Fatalf("stderr = %q, want original push diagnostic %q", result.stderr, test.diagnostic)
			}
			if result.pushCount != 1 {
				t.Fatalf("push attempts = %d, want 1 for a non-race failure", result.pushCount)
			}
			if result.sleepLog != "" {
				t.Fatalf("non-race rejection unexpectedly backed off: %q", result.sleepLog)
			}
		})
	}
}

func TestPublishHomebrewRetriesNonFastForwardRace(t *testing.T) {
	result := runPublisherWithOptions(t, customManifest, compactJSON(t, customManifest), publisherOptions{
		gitChanged: true,
		pushMode:   "race",
	})
	if result.err != nil {
		t.Fatalf("publish-homebrew.sh failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if result.pushCount != 3 {
		t.Fatalf("push attempts = %d, want 3", result.pushCount)
	}
	if result.sleepLog != "1\n2\n" {
		t.Fatalf("bounded backoff = %q, want 1 and 2 seconds", result.sleepLog)
	}
}

func TestPublishHomebrewRejectsTapManifestMismatch(t *testing.T) {
	var tap map[string]any
	if err := json.Unmarshal([]byte(customManifest), &tap); err != nil {
		t.Fatal(err)
	}
	tap["formula"].(map[string]any)["description"] = "Different registered project"
	mismatched, err := json.Marshal(tap)
	if err != nil {
		t.Fatal(err)
	}

	result := runPublisher(t, customManifest, string(mismatched))
	if result.err == nil {
		t.Fatalf("publish-homebrew.sh succeeded with unequal manifests\nstdout:\n%s\nstderr:\n%s", result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, "tap/source manifest mismatch") {
		t.Fatalf("stderr = %q, want manifest mismatch", result.stderr)
	}
	if result.hextapLog != "" {
		t.Fatalf("hextapctl must not run for unequal manifests, log = %q", result.hextapLog)
	}
}

type publisherResult struct {
	stdout    string
	stderr    string
	hextapLog string
	ghLog     string
	pushCount int
	sleepLog  string
	err       error
}

type publisherOptions struct {
	gitChanged bool
	pushMode   string
	tapRunMode string
}

func runPublisher(t *testing.T, sourceManifest, tapManifest string) publisherResult {
	t.Helper()
	return runPublisherWithOptions(t, sourceManifest, tapManifest, publisherOptions{})
}

func runPublisherWithOptions(t *testing.T, sourceManifest, tapManifest string, options publisherOptions) publisherResult {
	t.Helper()
	temporary := t.TempDir()
	stubDirectory := filepath.Join(temporary, "bin")
	assetDirectory := filepath.Join(temporary, "assets")
	if err := os.Mkdir(stubDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(assetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(temporary, "source-manifest.json")
	tapPath := filepath.Join(temporary, "tap-manifest.json")
	hextapLogPath := filepath.Join(temporary, "hextap.log")
	ghLogPath := filepath.Join(temporary, "gh.log")
	pushCountPath := filepath.Join(temporary, "push-count")
	sleepLogPath := filepath.Join(temporary, "sleep.log")
	writeFile(t, sourcePath, sourceManifest, 0o600)
	writeFile(t, tapPath, tapManifest, 0o600)
	writeFile(t, filepath.Join(assetDirectory, "SHA256SUMS"),
		strings.Repeat("a", 64)+"  renamed-aarch64-package.tar.gz\n"+
			strings.Repeat("b", 64)+"  renamed-x86_64-package.tar.gz\n", 0o600)

	ghPath := filepath.Join(stubDirectory, "gh")
	writeFile(t, ghPath, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_GH_LOG"
if [[ "$1" == auth && "$2" == setup-git ]]; then
  exit 0
fi
if [[ "$1" == repo && "$2" == clone ]]; then
  destination="$4"
  mkdir -p "$destination/Projects" "$destination/Formula"
  cp "$TEST_TAP_MANIFEST" "$destination/Projects/$TEST_FORMULA.json"
  : > "$destination/Formula/$TEST_FORMULA.rb"
  exit 0
fi
if [[ "$1" == run && "$2" == watch ]]; then
  exit 0
fi
if [[ "$1" == api ]]; then
  endpoint=""
  for argument in "$@"; do
    if [[ "$argument" == repos/* ]]; then
      endpoint="$argument"
    fi
  done
  if [[ "$endpoint" == */actions/workflows/tests.yml/runs ]]; then
	if [[ " $* " == *" event=$TEST_TAP_RUN_MODE "* ]]; then
	  printf '{"workflow_runs":[{"id":42,"html_url":"https://example.invalid/run/42"}]}\n'
	else
	  printf '{"workflow_runs":[]}\n'
	fi
  elif [[ "$endpoint" == */actions/runs/42 ]]; then
	printf '{"repository":{"full_name":"SijanC147/homebrew-hextap"},"path":".github/workflows/tests.yml","head_sha":"%s","event":"%s","status":"completed","conclusion":"success"}\n' "$TEST_TAP_SHA" "$TEST_TAP_RUN_MODE"
  else
    printf 'unexpected gh api endpoint: %s\n' "$endpoint" >&2
    exit 1
  fi
  exit 0
fi
printf 'unexpected gh command: %s\n' "$*" >&2
exit 1
`, 0o700)

	gitPath := filepath.Join(stubDirectory, "git")
	writeFile(t, gitPath, `#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *" diff --cached --quiet "* ]]; then
  [[ "$TEST_GIT_CHANGED" != true ]]
  exit
fi
if [[ " $* " == *" diff --cached --name-only "* ]]; then
  printf 'Formula/%s.rb\n' "$TEST_FORMULA"
  exit 0
fi
if [[ " $* " == *" rev-parse HEAD "* ]]; then
  printf '%s\n' "$TEST_TAP_SHA"
  exit 0
fi
if [[ " $* " == *" push origin HEAD:main "* ]]; then
  count=0
  if [[ -f "$TEST_PUSH_COUNT" ]]; then
    read -r count < "$TEST_PUSH_COUNT"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" > "$TEST_PUSH_COUNT"
  if [[ "$TEST_PUSH_MODE" == auth ]]; then
    echo 'remote: Permission to SijanC147/homebrew-hextap denied' >&2
    exit 1
  fi
  if [[ "$TEST_PUSH_MODE" == protected ]]; then
    echo 'remote: error: GH006: Protected branch update failed for refs/heads/main' >&2
    exit 1
  fi
  if [[ "$TEST_PUSH_MODE" == race && "$count" -lt 3 ]]; then
    echo '! [rejected] HEAD -> main (fetch first)' >&2
    exit 1
  fi
  exit 0
fi
exit 0
`, 0o700)

	sleepPath := filepath.Join(stubDirectory, "sleep")
	writeFile(t, sleepPath, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >> "$TEST_SLEEP_LOG"
`, 0o700)

	hextapPath := filepath.Join(temporary, "hextapctl")
	writeFile(t, hextapPath, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_HEXTAP_LOG"
`, 0o700)

	command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts/publish-homebrew.sh"),
		"SijanC147/custom-project", "v1.2.3", "1.2.3", "custom-formula", sourcePath, assetDirectory, hextapPath)
	gitChanged := "false"
	if options.gitChanged {
		gitChanged = "true"
	}
	tapRunMode := options.tapRunMode
	if tapRunMode == "" {
		tapRunMode = "push"
	}
	command.Env = append(os.Environ(),
		"PATH="+stubDirectory+":/usr/bin:/bin",
		"GH_TOKEN=test-token",
		"TEST_TAP_MANIFEST="+tapPath,
		"TEST_FORMULA=custom-formula",
		"TEST_TAP_SHA="+strings.Repeat("c", 40),
		"TEST_HEXTAP_LOG="+hextapLogPath,
		"TEST_GH_LOG="+ghLogPath,
		"TEST_GIT_CHANGED="+gitChanged,
		"TEST_PUSH_MODE="+options.pushMode,
		"TEST_TAP_RUN_MODE="+tapRunMode,
		"TEST_PUSH_COUNT="+pushCountPath,
		"TEST_SLEEP_LOG="+sleepLogPath,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	hextapLog, readErr := os.ReadFile(hextapLogPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	ghLog, readErr := os.ReadFile(ghLogPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	pushCount := 0
	if data, readErr := os.ReadFile(pushCountPath); readErr == nil {
		if strings.TrimSpace(string(data)) == "1" {
			pushCount = 1
		} else if strings.TrimSpace(string(data)) == "2" {
			pushCount = 2
		} else if strings.TrimSpace(string(data)) == "3" {
			pushCount = 3
		} else {
			t.Fatalf("unexpected push count %q", data)
		}
	} else if !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	sleepLog, readErr := os.ReadFile(sleepLogPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return publisherResult{
		stdout:    stdout.String(),
		stderr:    stderr.String(),
		hextapLog: string(hextapLog),
		ghLog:     string(ghLog),
		pushCount: pushCount,
		sleepLog:  string(sleepLog),
		err:       err,
	}
}

func TestPublishReleaseAcceptsExactImmutableRelease(t *testing.T) {
	result := runReleasePublisher(t, "false\ttrue\tfalse", false)
	if result.err != nil {
		t.Fatalf("publish-release.sh failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "verified immutable SijanC147/custom-project release v1.2.3") {
		t.Fatalf("stdout = %q, want immutable verification result", result.stdout)
	}
	assertNoReleaseMutation(t, result.ghLog)
	if !strings.Contains(result.ghLog, "release verify v1.2.3") {
		t.Fatalf("gh log missing release verification: %q", result.ghLog)
	}
	if got := strings.Count(result.ghLog, "release download v1.2.3"); got != 3 {
		t.Fatalf("downloaded assets = %d, want all 3; gh log: %s", got, result.ghLog)
	}
}

func TestPublishReleaseResumesExactDraft(t *testing.T) {
	result := runReleasePublisher(t, "true\tfalse\tfalse", false)
	if result.err != nil {
		t.Fatalf("publish-release.sh failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "published SijanC147/custom-project release v1.2.3") {
		t.Fatalf("stdout = %q, want draft publication result", result.stdout)
	}
	if !strings.Contains(result.ghLog, "release edit v1.2.3 --repo SijanC147/custom-project --draft=false") {
		t.Fatalf("draft was not published: %q", result.ghLog)
	}
	if strings.Contains(result.ghLog, "release upload ") {
		t.Fatalf("exact resumed draft unexpectedly uploaded assets: %q", result.ghLog)
	}
}

func TestPublishReleaseRejectsImmutableAssetMismatch(t *testing.T) {
	result := runReleasePublisher(t, "false\ttrue\tfalse", true)
	if result.err == nil {
		t.Fatal("publish-release.sh accepted mismatched published assets")
	}
	if !strings.Contains(result.stderr, "published release asset differs: custom-darwin-arm64.tar.gz") {
		t.Fatalf("stderr = %q, want published asset mismatch", result.stderr)
	}
	assertNoReleaseMutation(t, result.ghLog)
}

func TestPublishReleaseRejectsMutablePublishedRelease(t *testing.T) {
	result := runReleasePublisher(t, "false\tfalse\tfalse", false)
	if result.err == nil {
		t.Fatal("publish-release.sh accepted a mutable published release")
	}
	if !strings.Contains(result.stderr, "published release is not immutable") {
		t.Fatalf("stderr = %q, want immutable-state failure", result.stderr)
	}
	assertNoReleaseMutation(t, result.ghLog)
}

func TestPublishReleaseRejectsPublishedPrereleaseMismatch(t *testing.T) {
	result := runReleasePublisher(t, "false\ttrue\ttrue", false)
	if result.err == nil {
		t.Fatal("publish-release.sh accepted a mismatched published prerelease state")
	}
	if !strings.Contains(result.stderr, "published release prerelease state does not match") {
		t.Fatalf("stderr = %q, want prerelease-state failure", result.stderr)
	}
	assertNoReleaseMutation(t, result.ghLog)
}

type releasePublisherResult struct {
	stdout string
	stderr string
	ghLog  string
	err    error
}

func runReleasePublisher(t *testing.T, releaseState string, mismatch bool) releasePublisherResult {
	t.Helper()
	temporary := t.TempDir()
	stubDirectory := filepath.Join(temporary, "bin")
	localDirectory := filepath.Join(temporary, "local")
	remoteDirectory := filepath.Join(temporary, "remote")
	for _, directory := range []string{stubDirectory, localDirectory, remoteDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	assets := map[string]string{
		"SHA256SUMS":                 "checksums\n",
		"custom-darwin-amd64.tar.gz": "amd64\n",
		"custom-darwin-arm64.tar.gz": "arm64\n",
	}
	for name, content := range assets {
		writeFile(t, filepath.Join(localDirectory, name), content, 0o600)
		if mismatch && name == "custom-darwin-arm64.tar.gz" {
			content = "different\n"
		}
		writeFile(t, filepath.Join(remoteDirectory, name), content, 0o600)
	}

	ghLogPath := filepath.Join(temporary, "gh.log")
	ghPath := filepath.Join(stubDirectory, "gh")
	writeFile(t, ghPath, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_GH_LOG"
if [[ "$1" != release ]]; then
	printf 'unexpected gh command: %s\n' "$*" >&2
	exit 1
fi
subcommand="$2"
shift 2
if [[ "$subcommand" == view ]]; then
	if [[ " $* " != *" --json "* ]]; then
		exit 0
	fi
	if [[ " $* " == *" --json isDraft,isImmutable,isPrerelease "* ]]; then
		printf '%b\n' "$TEST_RELEASE_STATE"
		exit 0
	fi
	if [[ " $* " == *" --json isDraft,isPrerelease "* ]]; then
		IFS=$'\t' read -r draft immutable prerelease <<< "$TEST_RELEASE_STATE"
		printf '%s\t%s\n' "$draft" "$prerelease"
		exit 0
	fi
	if [[ " $* " == *" --json assets "* ]]; then
		find "$TEST_REMOTE_ASSETS" -mindepth 1 -maxdepth 1 -type f -print | sed 's#.*/##'
		exit 0
	fi
fi
if [[ "$subcommand" == download ]]; then
	pattern=""
	destination=""
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--pattern) pattern="$2"; shift 2 ;;
			--dir) destination="$2"; shift 2 ;;
			*) shift ;;
		esac
	done
	mkdir -p "$destination"
	cp "$TEST_REMOTE_ASSETS/$pattern" "$destination/$pattern"
	exit 0
fi
if [[ "$subcommand" == verify ]]; then
	exit 0
fi
if [[ "$subcommand" == create || "$subcommand" == upload || "$subcommand" == edit ]]; then
	if [[ "$TEST_ALLOW_MUTATION" == true ]]; then
		exit 0
	fi
	echo "published release mutation attempted: $subcommand" >&2
	exit 99
fi
printf 'unexpected gh release command: %s %s\n' "$subcommand" "$*" >&2
exit 1
`, 0o700)

	command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts/publish-release.sh"),
		"SijanC147/custom-project", "v1.2.3", localDirectory, "false")
	allowMutation := "false"
	if strings.HasPrefix(releaseState, "true\t") {
		allowMutation = "true"
	}
	command.Env = append(os.Environ(),
		"PATH="+stubDirectory+":/usr/bin:/bin",
		"TEST_GH_LOG="+ghLogPath,
		"TEST_RELEASE_STATE="+releaseState,
		"TEST_REMOTE_ASSETS="+remoteDirectory,
		"TEST_ALLOW_MUTATION="+allowMutation,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	ghLog, readErr := os.ReadFile(ghLogPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return releasePublisherResult{stdout: stdout.String(), stderr: stderr.String(), ghLog: string(ghLog), err: err}
}

func assertNoReleaseMutation(t *testing.T, ghLog string) {
	t.Helper()
	for _, command := range []string{"release create ", "release upload ", "release edit "} {
		if strings.Contains(ghLog, command) {
			t.Fatalf("published release mutated via %q; gh log: %s", command, ghLog)
		}
	}
}

func compactJSON(t *testing.T, value string) string {
	t.Helper()
	var output bytes.Buffer
	if err := json.Compact(&output, []byte(value)); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate workflow contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readRepositoryFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func textBetween(t *testing.T, value, start, end string) string {
	t.Helper()
	startIndex := strings.Index(value, start)
	if startIndex == -1 {
		t.Fatalf("missing start marker %q", start)
	}
	endIndex := strings.Index(value[startIndex:], end)
	if endIndex == -1 {
		t.Fatalf("missing end marker %q", end)
	}
	return value[startIndex : startIndex+endIndex]
}

func assertContains(t *testing.T, value, substring string) {
	t.Helper()
	if !strings.Contains(value, substring) {
		t.Fatalf("missing required contract text %q", substring)
	}
}

func assertNotContains(t *testing.T, value, substring string) {
	t.Helper()
	if strings.Contains(value, substring) {
		t.Fatalf("forbidden contract text %q is present", substring)
	}
}

func assertCount(t *testing.T, value, substring string, want int) {
	t.Helper()
	if got := strings.Count(value, substring); got != want {
		t.Fatalf("count of %q = %d, want %d", substring, got, want)
	}
}
