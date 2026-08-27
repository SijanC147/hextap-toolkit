package release

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writePrefetchingFakeBun(t *testing.T, bin, version string) string {
	t.Helper()
	path := filepath.Join(bin, "bun")
	script := `#!/bin/sh
set -eu
test -z "${SECRET_SHOULD_NOT_LEAK:-}"
printf '%s\n' "$*" >> "$(dirname "$0")/commands.log"
if [ "${1:-}" = --version ]; then
  printf '%s\n' "` + version + `"
  exit 0
fi
if [ "${1:-}" = build ]; then
  target=""
  output=""
  for argument in "$@"; do
    case "$argument" in
      --target=*) target="${argument#--target=}" ;;
      --outfile=*) output="${argument#--outfile=}" ;;
    esac
  done
  [ -n "$target" ] && [ -n "$output" ]
  : "${BUN_INSTALL_CACHE_DIR:?}"
  cache="$BUN_INSTALL_CACHE_DIR/$target-v` + version + `"
  if [ -f "$BUN_INSTALL_CACHE_DIR/network-disabled" ] && [ ! -f "$cache" ]; then
    echo "network disabled and runtime cache is missing: $target" >&2
    exit 71
  fi
  mkdir -p "$(dirname "$cache")" "$(dirname "$output")"
  : > "$cache"
  printf '%s\n' "$target" > "$output"
  chmod 755 "$output"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunProfileExecutesDirectInstallAndPhaseCommands(t *testing.T) {
	source, manifestPath := writeProfileBuildFixture(t)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestData = []byte(strings.Replace(string(manifestData),
		`"prepare": []`,
		`"prepare": [{"name": "dashboard", "argv": ["bun", "run", "build:dashboard"]}]`, 1))
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "commands.log")
	writePrefetchingFakeBun(t, bin, "1.3.14")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BUN_INSTALL_CACHE_DIR", t.TempDir())
	t.Setenv("SECRET_SHOULD_NOT_LEAK", "hidden")

	var output bytes.Buffer
	if err := RunProfile(ProfileOptions{
		ManifestPath: manifestPath,
		SourceDir:    source,
		Phase:        ProfileQuality,
		Stdout:       &output,
		Stderr:       &output,
	}); err != nil {
		t.Fatalf("RunProfile(quality) error = %v", err)
	}
	qualityLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(qualityLog), "--version\ninstall --frozen-lockfile\ntest\n"; got != want {
		t.Fatalf("quality command log = %q, want %q", got, want)
	}
	if got, want := output.String(), "CHECK bun-version\nCHECK install\nCHECK test\n"; got != want {
		t.Fatalf("quality output = %q, want %q", got, want)
	}

	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := RunProfile(ProfileOptions{
		ManifestPath: manifestPath,
		SourceDir:    source,
		Phase:        ProfileBuild,
		Stdout:       &output,
		Stderr:       &output,
	}); err != nil {
		t.Fatalf("RunProfile(build) error = %v", err)
	}
	buildLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	buildLines := strings.Split(strings.TrimSpace(string(buildLog)), "\n")
	if len(buildLines) != 8 || buildLines[0] != "--version" || buildLines[1] != "install --frozen-lockfile" || buildLines[7] != "run build:dashboard" {
		t.Fatalf("build command log = %q", buildLog)
	}
	for _, target := range []string{"bun-darwin-arm64", "bun-darwin-x64", "bun-linux-arm64", "bun-linux-x64", "bun-windows-x64"} {
		if !strings.Contains(string(buildLog), "--target="+target) {
			t.Errorf("build command log lacks %s: %q", target, buildLog)
		}
	}
}

func TestRunProfileRejectsLegacyManifestAndUnknownPhaseBeforeExecution(t *testing.T) {
	source, manifestPath := writeBuildFixture(t, true, successfulAdapter)
	for _, phase := range []ProfilePhase{ProfileQuality, "unknown"} {
		if err := RunProfile(ProfileOptions{ManifestPath: manifestPath, SourceDir: source, Phase: phase}); err == nil {
			t.Fatalf("RunProfile(%q) unexpectedly succeeded", phase)
		}
	}
}

func TestRunProfileRejectsRuntimeVersionDriftBeforeInstall(t *testing.T) {
	source, manifestPath := writeProfileBuildFixture(t)
	bin := t.TempDir()
	writePrefetchingFakeBun(t, bin, "1.4.0")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := RunProfile(ProfileOptions{ManifestPath: manifestPath, SourceDir: source, Phase: ProfileQuality}); err == nil || !strings.Contains(err.Error(), "exactly 1.3.14") {
		t.Fatalf("RunProfile(runtime drift) error = %v", err)
	}
	logData, err := os.ReadFile(filepath.Join(bin, "commands.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(logData) != "--version\n" {
		t.Fatalf("commands after runtime drift = %q", logData)
	}
}

func TestRunProfileBuildPrefetchesFreshBunCacheBeforeOfflineFiveTargetAdapter(t *testing.T) {
	source, manifestPath := writeProfileBuildFixture(t)
	bin := t.TempDir()
	home := t.TempDir()
	cacheDirectory := t.TempDir()
	writePrefetchingFakeBun(t, bin, "1.3.14")
	t.Setenv("HOME", home)
	t.Setenv("BUN_INSTALL_CACHE_DIR", cacheDirectory)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	if err := RunProfile(ProfileOptions{
		ManifestPath: manifestPath,
		SourceDir:    source,
		Phase:        ProfileBuild,
		Stdout:       &output,
		Stderr:       &output,
	}); err != nil {
		t.Fatalf("RunProfile(build) error = %v\n%s", err, output.String())
	}
	wantTargets := []string{
		"bun-darwin-arm64",
		"bun-darwin-x64",
		"bun-linux-arm64",
		"bun-linux-x64",
		"bun-windows-x64",
	}
	for _, target := range wantTargets {
		cache := filepath.Join(cacheDirectory, target+"-v1.3.14")
		if _, err := os.Stat(cache); err != nil {
			t.Errorf("prefetched cache %s: %v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".bun", "install", "cache")); !os.IsNotExist(err) {
		t.Fatalf("prefetch used ambient HOME cache: %v", err)
	}

	adapter := `#!/bin/sh
set -eu
case "$HEXTAP_TARGET_OS/$HEXTAP_TARGET_ARCH" in
  darwin/arm64) target=bun-darwin-arm64 ;;
  darwin/amd64) target=bun-darwin-x64 ;;
  linux/arm64) target=bun-linux-arm64 ;;
  linux/amd64) target=bun-linux-x64 ;;
  windows/amd64) target=bun-windows-x64 ;;
  *) exit 64 ;;
esac
bun build --compile --target="$target" --outfile="$HEXTAP_OUTPUT" scripts/hextap-prefetch-entry.ts
`
	if err := os.WriteFile(filepath.Join(source, "scripts", "hextap-build"), []byte(adapter), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "scripts", "hextap-prefetch-entry.ts"), []byte("console.log('prefetch')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDirectory, "network-disabled"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(dist, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Build(buildOptions(source, manifestPath, dist))
	if err != nil {
		t.Fatalf("offline Build() error = %v", err)
	}
	if len(result.Assets) != 7 {
		t.Fatalf("offline assets = %v", result.Assets)
	}
	logData, err := os.ReadFile(filepath.Join(bin, "commands.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range wantTargets {
		if count := strings.Count(string(logData), "--target="+target); count != 2 {
			t.Errorf("target %s compile count = %d, want prefetch + offline adapter", target, count)
		}
	}
	assets := append([]string(nil), result.Assets...)
	sort.Strings(assets)
	if strings.Join(assets, "\n") != strings.Join(result.Assets, "\n") {
		t.Fatalf("Build assets are not sorted: %v", result.Assets)
	}
}
