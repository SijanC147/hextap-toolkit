package formula

import (
	"bytes"
	"strings"
	"testing"
)

// The suffix claude-peers needs. Its release asset is in a private repository,
// and the URL the toolkit renders cannot be authenticated, so this strategy is
// the install route rather than a preference (SB23-2503).
const privateStrategySuffix = ", using: GitHubPrivateReleaseDownloadStrategy"

// strategyBaseFormula is the tap #30 shape: a reviewed tap-owned Formula that
// declares its own download strategy class above the Formula class, because the
// release asset it installs is in a private repository.
const strategyBaseFormula = `class GitHubPrivateReleaseDownloadStrategy < CurlDownloadStrategy
  def initialize(url, name, version, **meta)
    meta[:headers] ||= []
    meta[:headers] << "Authorization: Bearer #{ENV.fetch("HOMEBREW_GITHUB_API_TOKEN")}"
    super
  end
end

class BetterCcflare < Formula
  desc "Claude API proxy with intelligent load balancing across multiple accounts"
  homepage "https://github.com/SijanC147/better-ccflare"
  license "MIT"

  if Hardware::CPU.arm?
    url "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz"
    sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  else
    url "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-x86_64.tar.gz"
    sha256 "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  end

  def install
    bin.install "better-ccflare"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/better-ccflare --version")
  end
end
`

// strategyTemplate tokenizes that Formula and puts the requested suffix on each
// url, so every case below differs from the accepted one in exactly that.
func strategyTemplate(t *testing.T, suffixARM64, suffixAMD64 string) []byte {
	t.Helper()
	template := profileTemplate(t, []byte(strategyBaseFormula))
	template = bytesReplace(t, template, `url "@ARM64_URL@"`, `url "@ARM64_URL@"`+suffixARM64)
	template = bytesReplace(t, template, `url "@AMD64_URL@"`, `url "@AMD64_URL@"`+suffixAMD64)
	return template
}

// renderProfileFromTemplate substitutes the four tokens the way
// renderProfileTemplate does, which is how a reviewed seed Formula is written
// by hand before any release exists.
func renderProfileFromTemplate(t *testing.T, template []byte, version, armSHA256, amdSHA256 string) []byte {
	t.Helper()
	project := loadProfileManifest(t)
	result := append([]byte(nil), template...)
	for token, value := range map[string]string{
		arm64URLToken: releaseURL(project, version, project.Formula.Assets.DarwinARM64),
		arm64SHAToken: armSHA256,
		amd64URLToken: releaseURL(project, version, project.Formula.Assets.DarwinAMD64),
		amd64SHAToken: amdSHA256,
	} {
		result = bytesReplace(t, result, token, value)
	}
	return result
}

// A template carrying a download strategy is accepted, updates correctly, and
// keeps the suffix verbatim. Before SB23-2540 this template was rejected
// outright with "found 0", which made a private-asset formula impossible to
// package with a tap-owned profile.
func TestUpdateWithTemplateAcceptsAndPreservesADownloadStrategy(t *testing.T) {
	project := loadProfileManifest(t)
	template := strategyTemplate(t, privateStrategySuffix, privateStrategySuffix)
	original := renderProfileFromTemplate(t, template, "3.8.1", armSHA, amdSHA)

	updated, result, err := UpdateWithTemplate(original, template, project, "3.8.2", newArmSHA, newAmdSHA)
	if err != nil {
		t.Fatalf("UpdateWithTemplate() error = %v", err)
	}
	if result.PreviousVersion != "3.8.1" || result.Version != "3.8.2" || !result.Changed {
		t.Fatalf("UpdateWithTemplate() result = %+v", result)
	}

	// The suffix survives on both urls. Counting rather than asserting presence:
	// a render that dropped one of the two would still contain the string.
	if count := bytes.Count(updated, []byte(privateStrategySuffix)); count != 2 {
		t.Fatalf("download strategy occurrences after update = %d, want 2", count)
	}
	if !bytes.Contains(updated, []byte(`releases/download/v3.8.2/better-ccflare-macos-arm64.tar.gz"`+privateStrategySuffix)) {
		t.Fatal("arm64 url lost its download strategy or its version")
	}
	if !bytes.Contains(updated, []byte(`releases/download/v3.8.2/better-ccflare-macos-x86_64.tar.gz"`+privateStrategySuffix)) {
		t.Fatal("amd64 url lost its download strategy or its version")
	}
	if !bytes.Contains(updated, []byte(newArmSHA)) || !bytes.Contains(updated, []byte(newAmdSHA)) {
		t.Fatal("update did not write both checksums")
	}

	// The publisher and doctor --online must agree about the same bytes.
	metadata, err := ValidateCanonicalWithTemplate(updated, template, project)
	if err != nil {
		t.Fatalf("ValidateCanonicalWithTemplate() error = %v", err)
	}
	if metadata.Version != "3.8.2" || metadata.ARM64SHA256 != newArmSHA || metadata.AMD64SHA256 != newAmdSHA {
		t.Fatalf("ValidateCanonicalWithTemplate() metadata = %+v", metadata)
	}
}

// The other arm. Every one of these was accepted by nothing before and must be
// accepted by nothing now: widening the block to allow a suffix must not widen
// it to allow a different suffix per architecture, a lowercase or malformed
// constant, or arbitrary text after the URL.
func TestUpdateWithTemplateRejectsMalformedOrMismatchedDownloadStrategies(t *testing.T) {
	project := loadProfileManifest(t)
	for name, test := range map[string]struct {
		arm64, amd64 string
		wantError    string
	}{
		"strategy on arm64 only": {
			arm64: privateStrategySuffix, amd64: "",
			wantError: "same download strategy",
		},
		"strategy on amd64 only": {
			arm64: "", amd64: privateStrategySuffix,
			wantError: "same download strategy",
		},
		"different strategy per architecture": {
			arm64: privateStrategySuffix, amd64: ", using: SomeOtherStrategy",
			wantError: "same download strategy",
		},
		"lowercase constant is not a class": {
			arm64:     ", using: gitHubPrivateReleaseDownloadStrategy",
			amd64:     ", using: gitHubPrivateReleaseDownloadStrategy",
			wantError: "canonical architecture metadata block",
		},
		"a method call is not a constant": {
			arm64: ", using: strategy_for(:private)", amd64: ", using: strategy_for(:private)",
			wantError: "canonical architecture metadata block",
		},
		"another keyword argument is not a strategy": {
			arm64: `, verified: "github.com/SijanC147/"`, amd64: `, verified: "github.com/SijanC147/"`,
			wantError: "canonical architecture metadata block",
		},
		"trailing comma without a value": {
			arm64: ", using: ", amd64: ", using: ",
			wantError: "canonical architecture metadata block",
		},
	} {
		t.Run(name, func(t *testing.T) {
			template := strategyTemplate(t, test.arm64, test.amd64)
			// The Formula is rendered from this same template, so the only
			// thing under test is the template contract itself: nothing here
			// can fail merely because the two disagree.
			original := renderProfileFromTemplate(t, template, "3.8.1", armSHA, amdSHA)
			_, _, err := UpdateWithTemplate(original, template, project, "3.8.2", newArmSHA, newAmdSHA)
			if err == nil {
				t.Fatal("UpdateWithTemplate() accepted a malformed download strategy")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("UpdateWithTemplate() error = %v, want it to mention %q", err, test.wantError)
			}
		})
	}
}

// A template with no suffix at all is what every existing adopter ships, and it
// must keep working unchanged. Without this the fix could pass its own tests
// while breaking better-ccflare and claude-rc-proxy.
// A namespaced constant. The pattern documents `Hextap::PrivateAsset` as
// supported and nothing proved it: a reviewer of #26 showed that deleting the
// `(?:::[A-Z][A-Za-z0-9_]*)*` branch left every test passing, so the supported
// shape rested on the comment alone.
func TestUpdateWithTemplateAcceptsANamespacedStrategyConstant(t *testing.T) {
	project := loadProfileManifest(t)
	suffix := ", using: Hextap::PrivateAsset"
	template := strategyTemplate(t, suffix, suffix)
	original := renderProfileFromTemplate(t, template, "3.8.1", armSHA, amdSHA)

	updated, _, err := UpdateWithTemplate(original, template, project, "3.8.2", newArmSHA, newAmdSHA)
	if err != nil {
		t.Fatalf("UpdateWithTemplate() error = %v", err)
	}
	if count := bytes.Count(updated, []byte(suffix)); count != 2 {
		t.Fatalf("namespaced strategy occurrences after update = %d, want 2", count)
	}
}

func TestUpdateWithTemplateStillAcceptsTheSuffixlessBlock(t *testing.T) {
	project := loadProfileManifest(t)
	template := strategyTemplate(t, "", "")
	original := renderProfileFromTemplate(t, template, "3.8.1", armSHA, amdSHA)

	updated, _, err := UpdateWithTemplate(original, template, project, "3.8.2", newArmSHA, newAmdSHA)
	if err != nil {
		t.Fatalf("UpdateWithTemplate() error = %v", err)
	}
	if bytes.Contains(updated, []byte(", using:")) {
		t.Fatal("a suffixless template gained a download strategy")
	}
}
