package release

import "testing"

func TestParseMetadataStableAndPrerelease(t *testing.T) {
	tests := []struct {
		tag, mode, version string
		stable, prerelease bool
	}{
		{tag: "v0.0.0", mode: "full", version: "0.0.0", stable: true},
		{tag: "v1.2.3", mode: "homebrew-only", version: "1.2.3", stable: true},
		{tag: "v1.2.3-rc.1", mode: "full", version: "1.2.3-rc.1", prerelease: true},
		{tag: "v1.2.3-alpha-1", mode: "full", version: "1.2.3-alpha-1", prerelease: true},
	}
	for _, test := range tests {
		t.Run(test.tag+"/"+test.mode, func(t *testing.T) {
			got, err := ParseMetadata(test.tag, test.mode)
			if err != nil {
				t.Fatalf("ParseMetadata() error = %v", err)
			}
			if got.Tag != test.tag || got.Mode != test.mode || got.Version != test.version || got.Stable != test.stable || got.Prerelease != test.prerelease {
				t.Fatalf("ParseMetadata() = %#v", got)
			}
		})
	}
}

func TestParseMetadataRejectsMalformedValues(t *testing.T) {
	tests := []struct{ tag, mode string }{
		{"", "full"},
		{"1.2.3", "full"},
		{"v1.2", "full"},
		{"v01.2.3", "full"},
		{"v1.02.3", "full"},
		{"v1.2.03", "full"},
		{"v1.2.3-", "full"},
		{"v1.2.3-.rc", "full"},
		{"v1.2.3-rc..1", "full"},
		{"v1.2.3-01", "full"},
		{"v1.2.3+build", "full"},
		{"v1.2.3-rc/1", "full"},
		{"v1.2.3\ninjected=true", "full"},
		{"v1.2.3", "other"},
		{"v1.2.3-rc.1", "homebrew-only"},
		{"v1.2.3-" + string(make([]byte, 256)), "full"},
	}
	for _, test := range tests {
		t.Run(test.tag+"/"+test.mode, func(t *testing.T) {
			if _, err := ParseMetadata(test.tag, test.mode); err == nil {
				t.Fatal("ParseMetadata() unexpectedly succeeded")
			}
		})
	}
}

func TestParseVersionAcceptsNormalizedPrerelease(t *testing.T) {
	metadata, err := ParseVersion("1.2.3-rc.1")
	if err != nil {
		t.Fatalf("ParseVersion() error = %v", err)
	}
	if metadata.Version != "1.2.3-rc.1" || !metadata.Prerelease || metadata.Stable {
		t.Fatalf("ParseVersion() = %#v", metadata)
	}
	for _, invalid := range []string{"v1.2.3", "1.2.3+build", "1.2.3-01"} {
		if _, err := ParseVersion(invalid); err == nil {
			t.Errorf("ParseVersion(%q) unexpectedly succeeded", invalid)
		}
	}
}
