package formula

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	armSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	amdSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func loadManifest(t *testing.T) manifest.Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatalf("ReadFile(manifest): %v", err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatalf("manifest.Parse(): %v", err)
	}
	return m
}

func loadProfileManifest(t *testing.T) manifest.Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "better-ccflare.json"))
	if err != nil {
		t.Fatalf("ReadFile(profile manifest): %v", err)
	}
	project, err := manifest.Parse(data)
	if err != nil {
		t.Fatalf("manifest.Parse(profile): %v", err)
	}
	return project
}

func TestRenderRejectsTapOwnedFormulaProfile(t *testing.T) {
	if _, err := Render(loadProfileManifest(t), "3.8.2", armSHA, amdSHA); err == nil {
		t.Fatal("Render() unexpectedly regenerated a tap-owned Formula profile")
	}
}

func TestRenderMatchesGoldenFile(t *testing.T) {
	got, err := Render(loadManifest(t), "0.1.0", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "claude-rc-proxy.rb.golden"))
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Render() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(got) == 0 || got[len(got)-1] != '\n' {
		t.Fatal("Render() must end with exactly a final newline")
	}
}

func TestRenderSortsEnvironmentDeterministically(t *testing.T) {
	m := loadManifest(t)
	m.Homebrew.Service.Environment = map[string]string{
		"ZZZ_LAST":   "last",
		"AAA_FIRST":  "first",
		"MMM_MIDDLE": "middle",
	}
	first, err := Render(m, "0.1.0", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	second, err := Render(m, "0.1.0", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render() second error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("Render() is nondeterministic")
	}
	text := string(first)
	firstAt := strings.Index(text, "AAA_FIRST:")
	middleAt := strings.Index(text, "MMM_MIDDLE:")
	lastAt := strings.Index(text, "ZZZ_LAST:")
	if !(firstAt >= 0 && firstAt < middleAt && middleAt < lastAt) {
		t.Fatalf("environment order is not sorted:\n%s", text)
	}
}

func TestRenderOmitsDisabledService(t *testing.T) {
	m := loadManifest(t)
	m.Homebrew.Service = &manifest.Service{Enabled: false}
	got, err := Render(m, "0.1.0", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	text := string(got)
	for _, absent := range []string{"service do", "environment_variables"} {
		if strings.Contains(text, absent) {
			t.Fatalf("Render() unexpectedly contains %q", absent)
		}
	}
}

func TestRenderRejectsInvalidReleaseMetadata(t *testing.T) {
	tests := []struct {
		name, version, arm, amd string
	}{
		{"prerelease", "1.0.0-rc.1", armSHA, amdSHA},
		{"leading v", "v1.0.0", armSHA, amdSHA},
		{"short arm SHA", "1.0.0", "abc", amdSHA},
		{"uppercase arm SHA", "1.0.0", strings.Repeat("A", 64), amdSHA},
		{"short amd SHA", "1.0.0", armSHA, "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Render(loadManifest(t), tt.version, tt.arm, tt.amd); err == nil {
				t.Fatal("Render() unexpectedly succeeded")
			}
		})
	}
}

func TestRenderFileFailureLeavesDestinationUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Formula.rb")
	const original = "keep this exact content\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if err := RenderFile(path, loadManifest(t), "not-semver", armSHA, amdSHA); err == nil {
		t.Fatal("RenderFile() unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if string(got) != original {
		t.Fatalf("destination mutated after failure: %q", got)
	}
}
