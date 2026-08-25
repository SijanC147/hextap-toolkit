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
	assertNotContains(t, workflow, "queue:")
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
	assertContains(t, workflow, "needs.validate.outputs.stable == 'true'")
	assertContains(t, workflow, "needs.validate.outputs.mode == 'homebrew-only' && needs.release.result == 'skipped'")
	assertContains(t, workflow, `state="$(gh release view "$RELEASE_TAG"`)
	assertContains(t, workflow, `gh release verify "$RELEASE_TAG"`)
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
	assertContains(t, script, `-f head_sha="$tap_commit"`)
	assertContains(t, script, `run.fetch("path") == ".github/workflows/tests.yml"`)
	assertContains(t, script, `run.fetch("head_sha") == ARGV[0]`)
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
	err       error
}

func runPublisher(t *testing.T, sourceManifest, tapManifest string) publisherResult {
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
	writeFile(t, sourcePath, sourceManifest, 0o600)
	writeFile(t, tapPath, tapManifest, 0o600)
	writeFile(t, filepath.Join(assetDirectory, "SHA256SUMS"),
		strings.Repeat("a", 64)+"  renamed-aarch64-package.tar.gz\n"+
			strings.Repeat("b", 64)+"  renamed-x86_64-package.tar.gz\n", 0o600)

	ghPath := filepath.Join(stubDirectory, "gh")
	writeFile(t, ghPath, `#!/usr/bin/env bash
set -euo pipefail
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
    printf '{"workflow_runs":[{"id":42,"html_url":"https://example.invalid/run/42"}]}\n'
  elif [[ "$endpoint" == */actions/runs/42 ]]; then
    printf '{"repository":{"full_name":"SijanC147/homebrew-hextap"},"path":".github/workflows/tests.yml","head_sha":"%s","status":"completed","conclusion":"success"}\n' "$TEST_TAP_SHA"
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
  exit 0
fi
if [[ " $* " == *" rev-parse HEAD "* ]]; then
  printf '%s\n' "$TEST_TAP_SHA"
  exit 0
fi
exit 0
`, 0o700)

	hextapPath := filepath.Join(temporary, "hextapctl")
	writeFile(t, hextapPath, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_HEXTAP_LOG"
`, 0o700)

	command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts/publish-homebrew.sh"),
		"SijanC147/custom-project", "v1.2.3", "1.2.3", "custom-formula", sourcePath, assetDirectory, hextapPath)
	command.Env = append(os.Environ(),
		"PATH="+stubDirectory+":/usr/bin:/bin",
		"GH_TOKEN=test-token",
		"TEST_TAP_MANIFEST="+tapPath,
		"TEST_FORMULA=custom-formula",
		"TEST_TAP_SHA="+strings.Repeat("c", 40),
		"TEST_HEXTAP_LOG="+hextapLogPath,
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
	return publisherResult{stdout: stdout.String(), stderr: stderr.String(), hextapLog: string(hextapLog), err: err}
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
