package release

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const xcodeAdapter = `#!/bin/sh
set -eu
mkdir -p "$HEXTAP_OUTPUT/Contents/MacOS" "$HEXTAP_OUTPUT/Contents/Resources"
printf '<plist/>\n' > "$HEXTAP_OUTPUT/Contents/Info.plist"
cp "fixtures/$HEXTAP_TARGET_ARCH" "$HEXTAP_OUTPUT/Contents/MacOS/a-bar"
chmod 755 "$HEXTAP_OUTPUT/Contents/MacOS/a-bar"
printf '%s\n' "$HEXTAP_VERSION" > "$HEXTAP_OUTPUT/Contents/Resources/version.txt"
ln -s MacOS/a-bar "$HEXTAP_OUTPUT/Contents/current"
`

func writeXcodeBuildFixture(t *testing.T, script string) (string, string) {
	t.Helper()
	source := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "a-bar.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(source, ".hextap.json")
	for name, contents := range map[string][]byte{
		".hextap.json":         data,
		"LICENSE":              []byte("license\n"),
		"README.md":            []byte("readme\n"),
		"fixtures/arm64":       machOExecutable(0x0100000c, 0),
		"fixtures/amd64":       machOExecutable(0x01000007, 3),
		"scripts/hextap-build": []byte(script),
	} {
		path := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return source, manifestPath
}

func buildXcodeOnce(t *testing.T, source, manifestPath string) (string, error) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Build(buildOptions(source, manifestPath, output))
	return output, err
}

func TestXcodeBuildWritesDeterministicAppZipsThatVerify(t *testing.T) {
	source, manifestPath := writeXcodeBuildFixture(t, xcodeAdapter)
	first, err := buildXcodeOnce(t, source, manifestPath)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	names := entryNames(mustReadDir(t, first))
	if strings.Join(names, ",") != "SHA256SUMS,a-bar-darwin-amd64.zip,a-bar-darwin-arm64.zip" {
		t.Fatalf("outputs = %v", names)
	}
	second, err := buildXcodeOnce(t, source, manifestPath)
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	for _, name := range names {
		a, _ := os.ReadFile(filepath.Join(first, name))
		b, _ := os.ReadFile(filepath.Join(second, name))
		if sha256.Sum256(a) != sha256.Sum256(b) {
			t.Fatalf("%s is not reproducible", name)
		}
	}
	result, err := Verify(VerifyOptions{ManifestPath: manifestPath, Version: "1.2.3", Commit: testCommit, Directory: first})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(result.Assets) != 2 {
		t.Fatalf("verified assets = %v", result.Assets)
	}
}

func TestXcodeBuildRejectsBadAppOutput(t *testing.T) {
	cases := map[string]string{
		"regular file instead of app": "#!/bin/sh\nset -eu\nprintf x > \"$HEXTAP_OUTPUT\"\n",
		"missing executable":          "#!/bin/sh\nset -eu\nmkdir -p \"$HEXTAP_OUTPUT/Contents\"\nprintf x > \"$HEXTAP_OUTPUT/Contents/Info.plist\"\n",
		"escaping symlink":            xcodeAdapter + "ln -s ../../../etc \"$HEXTAP_OUTPUT/Contents/escape\"\n",
		"extra staging file":          xcodeAdapter + "printf x > \"$(dirname \"$HEXTAP_OUTPUT\")/stray\"\n",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			source, manifestPath := writeXcodeBuildFixture(t, script)
			output, err := buildXcodeOnce(t, source, manifestPath)
			if err == nil {
				t.Fatalf("Build() accepted %s", name)
			}
			if leftovers := entryNames(mustReadDir(t, output)); len(leftovers) != 0 {
				t.Fatalf("failed build left %v", leftovers)
			}
		})
	}
}

func TestVerifyAppZipRejectsWrongArchitecture(t *testing.T) {
	source, manifestPath := writeXcodeBuildFixture(t, xcodeAdapter)
	output, err := buildXcodeOnce(t, source, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(output, "a-bar-darwin-arm64.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAppZip(raw, "a-bar.app", "a-bar", "darwin", "amd64"); err == nil {
		t.Fatal("verifyAppZip accepted an arm64 executable for amd64")
	}
	if _, err := verifyAppZip(raw, "other.app", "a-bar", "darwin", "arm64"); err == nil {
		t.Fatal("verifyAppZip accepted entries outside the declared app")
	}
}

func mustReadDir(t *testing.T, directory string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
