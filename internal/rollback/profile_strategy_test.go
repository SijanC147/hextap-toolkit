package rollback

import "testing"

// The two url lines of a Formula rendered from a tap-owned template whose
// release asset is private. Before SB23-2542 neither matched, profileMetadata
// found zero URLs, and every rollback of such a Formula failed.
const (
	strategyARM64URL = "https://github.com/SijanC147/claude-peers-suite/releases/download/v0.1.0/claude-peers-macos-arm64.tar.gz"
	strategyAMD64URL = "https://github.com/SijanC147/claude-peers-suite/releases/download/v0.1.0/claude-peers-macos-x86_64.tar.gz"
	strategyARM64SHA = "1111111111111111111111111111111111111111111111111111111111111111"
	strategyAMD64SHA = "2222222222222222222222222222222222222222222222222222222222222222"
)

func strategyFormula(armSuffix, amdSuffix string) []byte {
	return []byte(`class ClaudePeers < Formula
  desc "Peer messaging for Claude Code"
  homepage "https://github.com/SijanC147/claude-peers-suite"
  license "MIT"

  if Hardware::CPU.arm?
    url "` + strategyARM64URL + `"` + armSuffix + `
    sha256 "` + strategyARM64SHA + `"
  else
    url "` + strategyAMD64URL + `"` + amdSuffix + `
    sha256 "` + strategyAMD64SHA + `"
  end

  def install
    bin.install "cpeers"
  end
end
`)
}

func TestProfileMetadataReadsAFormulaCarryingADownloadStrategy(t *testing.T) {
	for name, suffix := range map[string]string{
		"no strategy":         "",
		"plain constant":      ", using: GitHubPrivateReleaseDownloadStrategy",
		"namespaced constant": ", using: Hextap::PrivateAsset",
	} {
		t.Run(name, func(t *testing.T) {
			values, err := profileMetadata(strategyFormula(suffix, suffix))
			if err != nil {
				t.Fatalf("profileMetadata() error = %v", err)
			}
			// The captured URL must be the URL alone. A pattern that swallowed
			// the suffix into the group would still return two URLs and would
			// still pass a test that only counted them.
			if values.armURL != strategyARM64URL || values.amdURL != strategyAMD64URL {
				t.Fatalf("profileMetadata() urls = %q, %q", values.armURL, values.amdURL)
			}
			if values.armSHA != strategyARM64SHA || values.amdSHA != strategyAMD64SHA {
				t.Fatalf("profileMetadata() checksums = %q, %q", values.armSHA, values.amdSHA)
			}
		})
	}
}

// The end anchor is the property being preserved, not merely a detail of the
// old pattern: a url line may carry a download strategy and nothing else.
func TestProfileMetadataStillRejectsOtherTrailingText(t *testing.T) {
	for name, suffix := range map[string]string{
		"a different keyword argument": `, verified: "github.com/SijanC147/"`,
		"a lowercase constant":         ", using: privateStrategy",
		"a method call":                ", using: strategy_for(:private)",
		"a trailing comment":           " # private",
		"a doubled strategy":           ", using: A, using: B",
		"a bare trailing comma":        ",",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := profileMetadata(strategyFormula(suffix, suffix)); err == nil {
				t.Fatal("profileMetadata() accepted a url line with unexpected trailing text")
			}
		})
	}
}

// A round trip through renderProfile, which is what reconcileProfileFormula
// requires to be byte-identical before it will roll anything back. Reading the
// metadata is only half the path; if the suffix did not survive the render, the
// byte-equality check at remote.go:184 would fail instead.
func TestRenderProfileKeepsTheDownloadStrategy(t *testing.T) {
	suffix := ", using: GitHubPrivateReleaseDownloadStrategy"
	formula := strategyFormula(suffix, suffix)
	template := strategyFormula(suffix, suffix)
	for from, to := range map[string]string{
		strategyARM64URL: "@ARM64_URL@",
		strategyARM64SHA: "@ARM64_SHA256@",
		strategyAMD64URL: "@AMD64_URL@",
		strategyAMD64SHA: "@AMD64_SHA256@",
	} {
		template = []byte(replaceOnce(t, string(template), from, to))
	}

	values, err := profileMetadata(formula)
	if err != nil {
		t.Fatalf("profileMetadata() error = %v", err)
	}
	if rendered := renderProfile(template, values); string(rendered) != string(formula) {
		t.Fatalf("renderProfile() did not reproduce the Formula:\n%s", rendered)
	}
}

func replaceOnce(t *testing.T, value, from, to string) string {
	t.Helper()
	index := indexOf(value, from)
	if index < 0 || indexOf(value[index+len(from):], from) >= 0 {
		t.Fatalf("fixture occurrence count for %q is not one", from)
	}
	return value[:index] + to + value[index+len(from):]
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
