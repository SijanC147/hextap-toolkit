package formula

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	newArmSHA = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	newAmdSHA = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func renderedFormula(t *testing.T) []byte {
	t.Helper()
	data, err := Render(loadManifest(t), "1.2.3", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render(): %v", err)
	}
	return data
}

func TestUpdateChangesOnlyReleaseMetadata(t *testing.T) {
	original := renderedFormula(t)
	got, result, err := Update(original, loadManifest(t), "1.2.4", newArmSHA, newAmdSHA)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.PreviousVersion != "1.2.3" || result.Version != "1.2.4" || !result.Changed {
		t.Fatalf("Update() result = %#v", result)
	}
	text := string(got)
	for _, expected := range []string{
		"/releases/download/v1.2.4/claude-rc-proxy-darwin-arm64.tar.gz",
		"/releases/download/v1.2.4/claude-rc-proxy-darwin-amd64.tar.gz",
		`sha256 "` + newArmSHA + `"`,
		`sha256 "` + newAmdSHA + `"`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("updated Formula missing %q", expected)
		}
	}
	if got, want := stripReleaseMetadata(text), stripReleaseMetadata(string(original)); got != want {
		t.Fatalf("non-metadata bytes changed\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func stripReleaseMetadata(value string) string {
	metadata := regexp.MustCompile(`(?m)^(\s*)(url|sha256) "[^"]*"(\r?\n|$)`)
	return metadata.ReplaceAllString(value, "${1}${2} <metadata>${3}")
}

func TestUpdateEqualVersionIsIdempotentOrCorrectsChecksums(t *testing.T) {
	original := renderedFormula(t)
	got, result, err := Update(original, loadManifest(t), "1.2.3", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Update(idempotent): %v", err)
	}
	if result.Changed || string(got) != string(original) {
		t.Fatal("equal matching metadata should be byte-identical and unchanged")
	}

	corrected, result, err := Update(original, loadManifest(t), "1.2.3", newArmSHA, newAmdSHA)
	if err != nil {
		t.Fatalf("Update(checksum correction): %v", err)
	}
	if !result.Changed || !strings.Contains(string(corrected), newArmSHA) || !strings.Contains(string(corrected), newAmdSHA) {
		t.Fatalf("equal-version checksum correction failed: %#v\n%s", result, corrected)
	}
}

func TestUpdateIgnoresMetadataWordsInsideCaveats(t *testing.T) {
	m := loadManifest(t)
	m.Homebrew.Caveats = "version notes\nurl documentation\nsha256 reference"
	original, err := Render(m, "1.2.3", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render(): %v", err)
	}
	if _, _, err := Update(original, m, "1.2.4", newArmSHA, newAmdSHA); err != nil {
		t.Fatalf("Update() rejected safe caveats text: %v", err)
	}
}

func TestUpdateRejectsDowngrade(t *testing.T) {
	if _, _, err := Update(renderedFormula(t), loadManifest(t), "1.2.2", newArmSHA, newAmdSHA); err == nil {
		t.Fatal("Update() unexpectedly allowed a downgrade")
	}
}

func TestUpdateRejectsMalformedOrNoncanonicalFormula(t *testing.T) {
	base := string(renderedFormula(t))
	tests := map[string]string{
		"explicit version":                   strings.Replace(base, `  license "MIT"`, "  version \"1.2.3\"\n  license \"MIT\"", 1),
		"missing architecture conditional":   strings.Replace(base, "  if Hardware::CPU.arm?\n", "", 1),
		"duplicate architecture conditional": strings.Replace(base, "  if Hardware::CPU.arm?\n", "  if Hardware::CPU.arm?\n  if Hardware::CPU.arm?\n", 1),
		"missing else":                       strings.Replace(base, "  else\n", "", 1),
		"SHA not immediate":                  strings.Replace(base, `    sha256 "`+armSHA+`"`, "    # intervening comment\n    sha256 \""+armSHA+`"`, 1),
		"uppercase SHA":                      strings.Replace(base, armSHA, strings.Repeat("A", 64), 1),
		"duplicate URL":                      strings.Replace(base, `    sha256 "`+armSHA+`"`, "    url \"https://github.com/SijanC147/claude-rc-proxy/releases/download/v1.2.3/claude-rc-proxy-darwin-arm64.tar.gz\"\n    sha256 \""+armSHA+`"`, 1),
		"noncanonical repository":            strings.Replace(base, "https://github.com/SijanC147/claude-rc-proxy/releases/download/v1.2.3/claude-rc-proxy-darwin-arm64.tar.gz", "https://github.com/other/claude-rc-proxy/releases/download/v1.2.3/claude-rc-proxy-darwin-arm64.tar.gz", 1),
		"noncanonical asset":                 strings.Replace(base, "claude-rc-proxy-darwin-arm64.tar.gz", "other-darwin-arm64.tar.gz", 1),
		"mixed versions":                     strings.Replace(base, "/v1.2.3/claude-rc-proxy-darwin-amd64", "/v1.2.2/claude-rc-proxy-darwin-amd64", 1),
		"missing SHA":                        strings.Replace(base, "    sha256 \""+amdSHA+"\"\n", "", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Update([]byte(input), loadManifest(t), "1.2.4", newArmSHA, newAmdSHA); err == nil {
				t.Fatal("Update() unexpectedly succeeded")
			}
		})
	}
}

func TestUpdateFileIsAtomicAndPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ClaudeRcProxy.rb")
	original := renderedFormula(t)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if _, err := UpdateFile(path, loadManifest(t), "1.2.4", newArmSHA, newAmdSHA); err != nil {
		t.Fatalf("UpdateFile(): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %#o, want 0640", got)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if !strings.Contains(string(updated), "/v1.2.4/") {
		t.Fatal("UpdateFile() did not update Formula")
	}

	beforeFailure := append([]byte(nil), updated...)
	if _, err := UpdateFile(path, loadManifest(t), "1.2.3", armSHA, amdSHA); err == nil {
		t.Fatal("UpdateFile() unexpectedly allowed a downgrade")
	}
	afterFailure, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(after failure): %v", err)
	}
	if string(afterFailure) != string(beforeFailure) {
		t.Fatal("UpdateFile() mutated destination after failure")
	}
}
