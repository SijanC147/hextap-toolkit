package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadXcodeExample(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "a-bar.json"))
	if err != nil {
		t.Fatalf("ReadFile(a-bar example): %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("Unmarshal(a-bar example): %v", err)
	}
	return value
}

func TestXcodeExampleConformsToGoAndMachineSchema(t *testing.T) {
	value := loadXcodeExample(t)
	data, _ := json.Marshal(value)
	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(a-bar) error = %v", err)
	}
	profile := parsed.Release.Profile
	if parsed.Schema != XcodeSchema || profile.Runtime != RuntimeXcode || profile.Project != "a-bar.xcodeproj" || profile.Scheme != "a-bar" || profile.Configuration != "Release" || profile.App != "a-bar.app" {
		t.Fatalf("parsed profile = %+v", profile)
	}
	if parsed.Homebrew.CaskProfile != "a-bar" {
		t.Fatalf("cask profile = %q", parsed.Homebrew.CaskProfile)
	}
	schema := loadProjectSchema(t)
	if err := validateFixtureShape(schema, schema, value, "$"); err != nil {
		t.Fatalf("machine schema rejected a-bar example: %v", err)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("round trip Parse error = %v\n%s", err, encoded)
	}
	export, err := parsed.WorkflowExport("SijanC147/a-bar")
	if err != nil || export.Runtime != RuntimeXcode || export.NativeMatrix != `{"include":[{"runner":"macos-15","target":"darwin-arm64"},{"runner":"macos-15-intel","target":"darwin-amd64"}]}` {
		t.Fatalf("export = %+v, %v", export, err)
	}
}

func TestXcodeSchemaRejectsForeignShapes(t *testing.T) {
	cases := map[string]func(map[string]any){
		"bun runtime": func(v map[string]any) {
			v["release"].(map[string]any)["profile"].(map[string]any)["runtime"] = "bun"
		},
		"go linux block": func(v map[string]any) { v["release"].(map[string]any)["linux"] = true },
		"linux target": func(v map[string]any) {
			v["release"].(map[string]any)["targets"].(map[string]any)["linux_amd64"] = map[string]any{"archive": "a-bar-linux.zip", "archive_contents": "app"}
		},
		"tar.gz asset": func(v map[string]any) {
			v["formula"].(map[string]any)["assets"].(map[string]any)["darwin_arm64"] = "a-bar-darwin-arm64.tar.gz"
		},
		"bundle contents": func(v map[string]any) {
			v["release"].(map[string]any)["targets"].(map[string]any)["darwin_arm64"].(map[string]any)["archive_contents"] = "bundle"
		},
		"notarization field": func(v map[string]any) {
			v["release"].(map[string]any)["profile"].(map[string]any)["notarize"] = true
		},
		"test args": func(v map[string]any) { v["homebrew"].(map[string]any)["test_args"] = []any{"--version"} },
		"app not binary": func(v map[string]any) {
			v["release"].(map[string]any)["profile"].(map[string]any)["app"] = "other.app"
		},
		"project not xcodeproj": func(v map[string]any) {
			v["release"].(map[string]any)["profile"].(map[string]any)["project"] = "Package.swift"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := loadXcodeExample(t)
			mutate(value)
			data, _ := json.Marshal(value)
			if _, err := Parse(data); err == nil {
				t.Fatalf("Parse accepted %s", name)
			}
		})
	}
	value := loadXcodeExample(t)
	value["schema"] = float64(2)
	data, _ := json.Marshal(value)
	if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "validate manifest") {
		t.Fatalf("schema 2 with Xcode profile error = %v", err)
	}
}
