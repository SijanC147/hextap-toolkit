package formula

import (
	"bytes"
	"testing"
)

func TestValidateCanonicalAcceptsRenderedFormulaAndReturnsMetadata(t *testing.T) {
	project := loadManifest(t)
	rendered, err := Render(project, "1.2.3", armSHA, amdSHA)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := ValidateCanonical(rendered, project)
	if err != nil {
		t.Fatalf("ValidateCanonical() = %v", err)
	}
	if metadata.Version != "1.2.3" || metadata.ARM64SHA256 != armSHA || metadata.AMD64SHA256 != amdSHA {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestValidateCanonicalRejectsClassCorrectHostileStaleAndNoncanonicalFormulae(t *testing.T) {
	project := loadManifest(t)
	rendered, err := Render(project, "1.2.3", armSHA, amdSHA)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"hostile top-level":  bytes.Replace(rendered, []byte("class ClaudeRcProxy < Formula\n"), []byte("class ClaudeRcProxy < Formula\n  system \"id\"\n"), 1),
		"stale install":      bytes.Replace(rendered, []byte(`bin.install "claude-rc-proxy"`), []byte(`bin.install "other"`), 1),
		"stale service":      bytes.Replace(rendered, []byte("keep_alive crashed: true"), []byte("keep_alive crashed: false"), 1),
		"stale test":         bytes.Replace(rendered, []byte("--version"), []byte("--help"), 1),
		"noncanonical space": bytes.Replace(rendered, []byte("  desc"), []byte("   desc"), 1),
		"wrong asset":        bytes.Replace(rendered, []byte("claude-rc-proxy-darwin-arm64.tar.gz"), []byte("other-darwin-arm64.tar.gz"), 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateCanonical(input, project); err == nil {
				t.Fatal("ValidateCanonical() unexpectedly succeeded")
			}
		})
	}
}
