package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

const bunProfileManifest = `{
  "schema": 2,
  "formula": {
    "name": "better-ccflare",
    "class": "BetterCcflare",
    "description": "Claude API proxy with intelligent load balancing across multiple accounts",
    "homepage": "https://github.com/SijanC147/better-ccflare",
    "license": "MIT",
    "repository": {
      "owner": "SijanC147",
      "name": "better-ccflare"
    },
    "binary": "better-ccflare",
    "assets": {
      "darwin_arm64": "better-ccflare-macos-arm64.tar.gz",
      "darwin_amd64": "better-ccflare-macos-x86_64.tar.gz"
    }
  },
  "release": {
    "build_script": "scripts/hextap-build",
    "profile": {
      "runtime": "bun",
      "runtime_version": "1.3.14",
      "install": {
        "name": "install",
        "argv": ["bun", "install", "--frozen-lockfile"]
      },
      "quality": [
        {
          "name": "typecheck",
          "argv": ["bun", "run", "typecheck"]
        },
        {
          "name": "test",
          "argv": ["bun", "test"]
        },
        {
          "name": "dashboard",
          "argv": ["bun", "run", "build:dashboard"]
        }
      ],
      "prepare": [
        {
          "name": "dashboard",
          "argv": ["bun", "run", "build:dashboard"]
        }
      ]
    },
    "targets": {
      "darwin_arm64": {
        "binary": "better-ccflare-macos-arm64",
        "archive": "better-ccflare-macos-arm64.tar.gz",
        "archive_contents": "binary"
      },
      "darwin_amd64": {
        "binary": "better-ccflare-macos-x86_64",
        "archive": "better-ccflare-macos-x86_64.tar.gz",
        "archive_contents": "binary"
      },
      "linux_arm64": {
        "binary": "better-ccflare-linux-arm64"
      },
      "linux_amd64": {
        "binary": "better-ccflare-linux-amd64"
      },
      "windows_amd64": {
        "binary": "better-ccflare-windows-x64.exe"
      }
    }
  },
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "formula_profile": "better-ccflare",
    "service_enabled": true
  }
}`

func TestParseBunProfileAndExportWorkflowScalars(t *testing.T) {
	project, err := Parse([]byte(bunProfileManifest))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if project.Schema != ProfileSchema || project.Release.Profile == nil {
		t.Fatalf("profile manifest = %#v", project)
	}
	if project.Release.Profile.Runtime != "bun" || project.Release.Profile.RuntimeVersion != "1.3.14" {
		t.Fatalf("profile = %#v", project.Release.Profile)
	}
	if got := project.Release.Targets["windows_amd64"].Binary; got != "better-ccflare-windows-x64.exe" {
		t.Fatalf("windows asset = %q", got)
	}
	if project.Homebrew.FormulaProfile != "better-ccflare" || project.Homebrew.ServiceEnabled == nil || !*project.Homebrew.ServiceEnabled {
		t.Fatalf("Homebrew profile = %#v", project.Homebrew)
	}

	exported, err := project.WorkflowExport("SijanC147/better-ccflare")
	if err != nil {
		t.Fatalf("WorkflowExport() error = %v", err)
	}
	if exported.Runtime != "bun" || exported.RuntimeVersion != "1.3.14" {
		t.Fatalf("runtime export = %#v", exported)
	}
	wantMatrix := `{"include":[{"runner":"ubuntu-24.04","target":"linux-amd64"},{"runner":"ubuntu-24.04-arm","target":"linux-arm64"},{"runner":"macos-15","target":"darwin-arm64"},{"runner":"macos-15-intel","target":"darwin-amd64"},{"runner":"windows-2025","target":"windows-amd64"}]}`
	if exported.NativeMatrix != wantMatrix {
		t.Fatalf("native matrix = %q, want %q", exported.NativeMatrix, wantMatrix)
	}
}

func TestBunProfileMarshalRetainsItsExactSchemaFieldSet(t *testing.T) {
	project, err := Parse([]byte(bunProfileManifest))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"linux"`) || strings.Contains(string(encoded), `"caveats"`) || strings.Contains(string(encoded), `"service":`) {
		t.Fatalf("schema-2 marshal leaked legacy fields:\n%s", encoded)
	}
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("Parse(Marshal(profile)) error = %v\n%s", err, encoded)
	}
}

func TestLegacyGoProfileRemainsExactAndExportsFourTargets(t *testing.T) {
	project, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if project.Schema != LegacySchema || project.Release.Profile != nil || project.Release.Targets != nil {
		t.Fatalf("legacy profile changed = %#v", project.Release)
	}
	exported, err := project.WorkflowExport("SijanC147/claude-rc-proxy")
	if err != nil {
		t.Fatal(err)
	}
	wantMatrix := `{"include":[{"runner":"ubuntu-24.04","target":"linux-amd64"},{"runner":"ubuntu-24.04-arm","target":"linux-arm64"},{"runner":"macos-15","target":"darwin-arm64"},{"runner":"macos-15-intel","target":"darwin-amd64"}]}`
	if exported.Runtime != "go" || exported.RuntimeVersion != "" || exported.NativeMatrix != wantMatrix {
		t.Fatalf("legacy workflow export = %#v", exported)
	}
}

func TestBunProfileRejectsUnsafeOrIncompleteContracts(t *testing.T) {
	tests := map[string]string{
		"legacy fields in schema two": strings.Replace(bunProfileManifest, `"profile": {`, `"linux": true, "profile": {`, 1),
		"missing frozen lockfile":     strings.Replace(bunProfileManifest, `, "--frozen-lockfile"`, ``, 1),
		"extra install argument":      strings.Replace(bunProfileManifest, `"--frozen-lockfile"]`, `"--frozen-lockfile", "package"]`, 1),
		"unversioned runtime":         strings.Replace(bunProfileManifest, `"runtime_version": "1.3.14"`, `"runtime_version": "latest"`, 1),
		"shell command string":        strings.Replace(bunProfileManifest, `"argv": ["bun", "test"]`, `"argv": "bun test"`, 1),
		"null preparation list": strings.Replace(bunProfileManifest, `"prepare": [
        {
          "name": "dashboard",
          "argv": ["bun", "run", "build:dashboard"]
        }
      ]`, `"prepare": null`, 1),
		"empty command argument": strings.Replace(bunProfileManifest, `"argv": ["bun", "test"]`, `"argv": ["bun", ""]`, 1),
		"unknown target":         strings.Replace(bunProfileManifest, `"windows_amd64": {`, `"windows_arm64": {`, 1),
		"unpaired linux": strings.Replace(bunProfileManifest, `      "linux_amd64": {
        "binary": "better-ccflare-linux-amd64"
      },
`, ``, 1),
		"windows without exe":       strings.Replace(bunProfileManifest, `.exe"`, `"`, 1),
		"formula target drift":      strings.Replace(bunProfileManifest, `"archive": "better-ccflare-macos-arm64.tar.gz"`, `"archive": "other-macos-arm64.tar.gz"`, 1),
		"duplicate asset":           strings.Replace(bunProfileManifest, `"binary": "better-ccflare-linux-amd64"`, `"binary": "better-ccflare-linux-arm64"`, 1),
		"case-fold duplicate asset": strings.Replace(bunProfileManifest, `"binary": "better-ccflare-linux-amd64"`, `"binary": "BETTER-CCFLARE-LINUX-ARM64"`, 1),
		"reserved checksum asset":   strings.Replace(bunProfileManifest, `"binary": "better-ccflare-linux-amd64"`, `"binary": "SHA256SUMS"`, 1),
		"wrong Formula profile":     strings.Replace(bunProfileManifest, `"formula_profile": "better-ccflare"`, `"formula_profile": "other"`, 1),
		"implicit archive contents": strings.Replace(bunProfileManifest, `,
        "archive_contents": "binary"`, ``, 1),
		"null optional binary": strings.Replace(bunProfileManifest, `"binary": "better-ccflare-macos-arm64"`, `"binary": null`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("Parse() unexpectedly succeeded")
			}
		})
	}
}

func TestSchemaVersionsRejectCrossProfileFieldSets(t *testing.T) {
	legacyWithProfile := strings.Replace(validManifest,
		`"build_script": "scripts/hextap-build",`,
		`"build_script": "scripts/hextap-build", "profile": {},`, 1)
	if _, err := Parse([]byte(legacyWithProfile)); err == nil {
		t.Fatal("schema 1 unexpectedly accepted schema 2 profile fields")
	}

	profileWithLinux := strings.Replace(bunProfileManifest,
		`"build_script": "scripts/hextap-build",`,
		`"build_script": "scripts/hextap-build", "linux": true,`, 1)
	if _, err := Parse([]byte(profileWithLinux)); err == nil {
		t.Fatal("schema 2 unexpectedly accepted schema 1 release fields")
	}
}
