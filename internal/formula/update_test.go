package formula

import (
	"bytes"
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

func TestUpdateTapOwnedProfilePreservesServiceCaveatsTestsAndFormatting(t *testing.T) {
	project := loadProfileManifest(t)
	original := []byte(`class BetterCcflare < Formula
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

  # Tap-owned install and service contract.
  def install
    bin.install "better-ccflare"
  end

  service do
    run [opt_bin/"better-ccflare", "serve"]
    keep_alive successful_exit: false, crashed: true
    environment_variables BETTER_CCFLARE_LOG_DIR: var/"log/better-ccflare"
  end

  def caveats
    <<~EOS
      Configuration remains tap-owned.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/better-ccflare --version")
  end
end
`)
	template := profileTemplate(t, original)
	if _, _, err := Update(original, project, "3.8.2", newArmSHA, newAmdSHA); err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("Update(profile without template) error = %v", err)
	}
	updated, result, err := UpdateWithTemplate(original, template, project, "3.8.2", newArmSHA, newAmdSHA)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	want := strings.ReplaceAll(string(original), "/v3.8.1/", "/v3.8.2/")
	want = strings.Replace(want, strings.Repeat("a", 64), newArmSHA, 1)
	want = strings.Replace(want, strings.Repeat("b", 64), newAmdSHA, 1)
	if string(updated) != want {
		t.Fatalf("updated Formula changed nonmetadata bytes:\n%s", updated)
	}
	if !result.Changed || result.PreviousVersion != "3.8.1" || result.Version != "3.8.2" {
		t.Fatalf("Update() result = %#v", result)
	}
	if _, _, err := UpdateWithTemplate(updated, template, project, "3.8.1", armSHA, amdSHA); err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("UpdateWithTemplate(downgrade) error = %v", err)
	}
}

func TestUpdateTapOwnedProfileTemplateRejectsFormulaAndTokenDrift(t *testing.T) {
	project := loadProfileManifest(t)
	original := []byte(`class BetterCcflare < Formula
  if Hardware::CPU.arm?
    url "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz"
    sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  else
    url "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-x86_64.tar.gz"
    sha256 "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  end
  marker = "#{send(:url, 'https://example.invalid/tap-owned-code')}"
end
`)
	template := profileTemplate(t, original)
	tests := map[string]struct {
		formula  []byte
		template []byte
	}{
		"nonmetadata Formula drift": {
			formula:  bytesReplace(t, original, `marker =`, `changed =`),
			template: template,
		},
		"noncanonical current URL": {
			formula: bytesReplace(
				t,
				original,
				"https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz",
				"https://github.com/other/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz",
			),
			template: template,
		},
		"uppercase current SHA": {
			formula:  bytesReplace(t, original, strings.Repeat("a", 64), strings.Repeat("A", 64)),
			template: template,
		},
		"missing token": {
			formula:  original,
			template: bytesReplace(t, template, "@ARM64_URL@", "@AMD64_URL@"),
		},
		"unknown token": {
			formula:  original,
			template: append(append([]byte(nil), template...), []byte("@UNREVIEWED@\n")...),
		},
		"lowercase token": {
			formula:  original,
			template: append(append([]byte(nil), template...), []byte("@unreviewed@\n")...),
		},
		"mixed-case token": {
			formula:  original,
			template: append(append([]byte(nil), template...), []byte("@Unreviewed@\n")...),
		},
		"tokens outside canonical architecture block": {
			formula: original,
			template: bytesReplace(
				t,
				template,
				"  if Hardware::CPU.arm?\n",
				"  if Hardware::CPU.intel?\n",
			),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := UpdateWithTemplate(test.formula, test.template, project, "3.8.2", newArmSHA, newAmdSHA); err == nil {
				t.Fatal("UpdateWithTemplate() accepted Formula/template drift")
			}
		})
	}
}

func profileTemplate(t *testing.T, formula []byte) []byte {
	t.Helper()
	result := append([]byte(nil), formula...)
	for current, token := range map[string]string{
		"https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz": "@ARM64_URL@",
		strings.Repeat("a", 64): "@ARM64_SHA256@",
		"https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-x86_64.tar.gz": "@AMD64_URL@",
		strings.Repeat("b", 64): "@AMD64_SHA256@",
	} {
		result = bytesReplace(t, result, current, token)
	}
	return result
}

func bytesReplace(t *testing.T, value []byte, current, replacement string) []byte {
	t.Helper()
	if bytes.Count(value, []byte(current)) != 1 {
		t.Fatalf("fixture occurrence count for %q is not one", current)
	}
	return bytes.Replace(value, []byte(current), []byte(replacement), 1)
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

func TestUpdateRejectsHeredocMarkersThatCouldHideFormulaDeclarations(t *testing.T) {
	base := string(renderedFormula(t))
	insertBefore := "\n  def install\n"
	markers := map[string]string{
		"comment text": `  # <<~EOS`,
		"string text":  `  marker = "<<~EOS"`,
	}
	declarations := map[string]string{
		"version": `  version "9.9.9"`,
		"URL":     `  url "https://example.invalid/hidden.tar.gz"`,
		"SHA":     `  sha256 "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"`,
	}
	for markerName, marker := range markers {
		for declarationName, declaration := range declarations {
			t.Run(markerName+"/"+declarationName, func(t *testing.T) {
				hidden := marker + "\n" + declaration + "\n  EOS"
				input := strings.Replace(base, insertBefore, "\n"+hidden+insertBefore, 1)
				if _, _, err := Update([]byte(input), loadManifest(t), "1.2.4", newArmSHA, newAmdSHA); err == nil {
					t.Fatal("Update() accepted a declaration hidden behind non-code heredoc text")
				}
			})
		}
	}
}

func TestUpdateAcceptsOnlyTheGeneratedCaveatsBlock(t *testing.T) {
	base := string(renderedFormula(t))
	insertBefore := "\n  def install\n"
	generatedBlock := "  def caveats\n    <<~EOS\n      generated text\n    EOS\n  end"
	tests := map[string]string{
		"unsupported heredoc":    strings.Replace(base, insertBefore, "\n  def other\n    <<~TEXT\n      data\n    TEXT\n  end"+insertBefore, 1),
		"marker indentation":     strings.Replace(base, "    <<~EOS\n", "      <<~EOS\n", 1),
		"terminator indentation": strings.Replace(base, "    EOS\n", "      EOS\n", 1),
		"end indentation": strings.Replace(
			base,
			"      Logs are written below #{var}/log/claude-rc-proxy.\n    EOS\n  end\n",
			"      Logs are written below #{var}/log/claude-rc-proxy.\n    EOS\n    end\n",
			1,
		),
		"additional caveats block": strings.Replace(base, insertBefore, "\n"+generatedBlock+insertBefore, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Update([]byte(input), loadManifest(t), "1.2.4", newArmSHA, newAmdSHA); err == nil {
				t.Fatal("Update() accepted a non-generated caveats/heredoc structure")
			}
		})
	}

	withoutCaveats := loadManifest(t)
	withoutCaveats.Homebrew.Caveats = ""
	formulaWithoutCaveats, err := Render(withoutCaveats, "1.2.3", armSHA, amdSHA)
	if err != nil {
		t.Fatalf("Render(without caveats): %v", err)
	}
	withUnexpectedBlock := strings.Replace(string(formulaWithoutCaveats), insertBefore, "\n"+generatedBlock+insertBefore, 1)
	if _, _, err := Update([]byte(withUnexpectedBlock), withoutCaveats, "1.2.4", newArmSHA, newAmdSHA); err == nil {
		t.Fatal("Update() accepted caveats when the manifest disables them")
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
		"explicit version quoted":            strings.Replace(base, `  license "MIT"`, "  version \"1.2.3\"\n  license \"MIT\"", 1),
		"explicit version parenthesized":     strings.Replace(base, `  license "MIT"`, "  version(\"1.2.3\")\n  license \"MIT\"", 1),
		"explicit version trailing comment":  strings.Replace(base, `  license "MIT"`, "  version \"1.2.3\" # forbidden\n  license \"MIT\"", 1),
		"explicit version call comment":      strings.Replace(base, `  license "MIT"`, "  version(\"1.2.3\") # forbidden\n  license \"MIT\"", 1),
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

func TestUpdateRejectsExtraURLOrSHADeclarationsAnywhereInFormulaCode(t *testing.T) {
	base := string(renderedFormula(t))
	insertBefore := "\n  def install\n"
	tests := map[string]string{
		"top-level URL":            `  url "https://example.invalid/extra.tar.gz"`,
		"top-level SHA":            `  sha256 "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"`,
		"differently-indented URL": `      url "https://example.invalid/extra.tar.gz"`,
		"differently-indented SHA": `      sha256 "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"`,
		"resource-block URL":       "  resource \"extra\" do\n    url \"https://example.invalid/extra.tar.gz\"\n  end",
		"resource-block SHA":       "  resource \"extra\" do\n    sha256 \"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\"\n  end",
	}
	for name, declaration := range tests {
		t.Run(name, func(t *testing.T) {
			input := strings.Replace(base, insertBefore, "\n"+declaration+insertBefore, 1)
			if _, _, err := Update([]byte(input), loadManifest(t), "1.2.4", newArmSHA, newAmdSHA); err == nil {
				t.Fatal("Update() accepted an extra Formula-level release metadata declaration")
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

func TestUpdateFileWithTemplateRejectsSymlinkTemplateWithoutMutation(t *testing.T) {
	project := loadProfileManifest(t)
	original := []byte(`class BetterCcflare < Formula
  if Hardware::CPU.arm?
    url "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-arm64.tar.gz"
    sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  else
    url "https://github.com/SijanC147/better-ccflare/releases/download/v3.8.1/better-ccflare-macos-x86_64.tar.gz"
    sha256 "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  end
end
`)
	template := profileTemplate(t, original)
	directory := t.TempDir()
	formulaPath := filepath.Join(directory, "better-ccflare.rb")
	templateTarget := filepath.Join(directory, "better-ccflare.real.rb.tmpl")
	templatePath := filepath.Join(directory, "better-ccflare.rb.tmpl")
	if err := os.WriteFile(formulaPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templateTarget, template, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(templateTarget, templatePath); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateFileWithTemplate(formulaPath, templatePath, project, "3.8.2", newArmSHA, newAmdSHA); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("UpdateFileWithTemplate(symlink) error = %v", err)
	}
	after, err := os.ReadFile(formulaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("UpdateFileWithTemplate() mutated Formula after template rejection")
	}
}
