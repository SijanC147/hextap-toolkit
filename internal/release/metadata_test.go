package release

import (
	"fmt"
	"math"
	"testing"
)

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

func TestStableVersionComparisonAndBumps(t *testing.T) {
	comparisons := []struct {
		left, right string
		want        int
	}{
		{left: "0.0.0", right: "0.0.0", want: 0},
		{left: "0.2.9", right: "0.3.0", want: -1},
		{left: "2.0.0", right: "1.999.999", want: 1},
		{left: "1.2.4", right: "1.2.3", want: 1},
	}
	for _, test := range comparisons {
		got, err := CompareStableVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Errorf("CompareStableVersions(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}

	bumps := []struct {
		current string
		bump    Bump
		want    string
	}{
		{current: "0.0.0", bump: PatchBump, want: "0.0.1"},
		{current: "0.2.9", bump: MinorBump, want: "0.3.0"},
		{current: "0.9.9", bump: MajorBump, want: "1.0.0"},
		{current: "1.2.3", bump: PatchBump, want: "1.2.4"},
		{current: "1.2.3", bump: MinorBump, want: "1.3.0"},
		{current: "1.2.3", bump: MajorBump, want: "2.0.0"},
	}
	for _, test := range bumps {
		got, err := BumpStableVersion(test.current, test.bump)
		if err != nil || got != test.want {
			t.Errorf("BumpStableVersion(%q, %q) = %q, %v; want %q", test.current, test.bump, got, err, test.want)
		}
	}
}

func TestStableVersionOperationsRejectUnsafeOrOverflowingInputs(t *testing.T) {
	invalidVersions := []string{
		"v1.2.3",
		"1.2.3-rc.1",
		"01.2.3",
		"1.2",
		"18446744073709551616.0.0",
	}
	for _, version := range invalidVersions {
		if _, err := ParseStableVersion(version); err == nil {
			t.Errorf("ParseStableVersion(%q) unexpectedly succeeded", version)
		}
	}
	for _, test := range []struct {
		version string
		bump    Bump
	}{
		{version: "1.2.3", bump: Bump("other")},
		{version: "1.2.3-rc.1", bump: PatchBump},
		{version: "1.2." + uintString(math.MaxUint64), bump: PatchBump},
		{version: "1." + uintString(math.MaxUint64) + ".0", bump: MinorBump},
		{version: uintString(math.MaxUint64) + ".0.0", bump: MajorBump},
	} {
		if _, err := BumpStableVersion(test.version, test.bump); err == nil {
			t.Errorf("BumpStableVersion(%q, %q) unexpectedly succeeded", test.version, test.bump)
		}
	}
}

func uintString(value uint64) string {
	return fmt.Sprintf("%d", value)
}
