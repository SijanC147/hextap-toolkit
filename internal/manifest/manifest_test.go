package manifest

import (
	"strings"
	"testing"
)

const validManifest = `{
  "schema": 1,
  "formula": {
    "name": "claude-rc-proxy",
    "class": "ClaudeRcProxy",
    "description": "Selective Anthropic proxy preserving Claude Code Remote Control",
    "homepage": "https://github.com/SijanC147/claude-rc-proxy",
    "license": "MIT",
    "repository": {
      "owner": "SijanC147",
      "name": "claude-rc-proxy"
    },
    "binary": "claude-rc-proxy",
    "assets": {
      "darwin_arm64": "claude-rc-proxy-darwin-arm64.tar.gz",
      "darwin_amd64": "claude-rc-proxy-darwin-amd64.tar.gz"
    }
  },
  "release": {
    "build_script": "scripts/hextap-build",
    "linux": true
  },
  "homebrew": {
    "macos_only": true,
    "test_args": ["--version"],
    "service": {
      "enabled": true,
      "run_args": ["--config", "settings.json"],
      "keep_alive": {
        "crashed": true
      },
      "restart_delay": 5,
      "environment": {
        "CLAUDE_RC_PROXY_LISTEN": "127.0.0.1:9801",
        "RUNTIME_MODE": "service"
      },
      "log_path": "log/claude-rc-proxy/launchd.log",
      "error_log_path": "log/claude-rc-proxy/launchd.log"
    },
    "caveats": "Configure the service in {{home}}/.config.\\nLogs are under {{var}}/log."
  }
}`

func TestParseValidManifest(t *testing.T) {
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if m.Schema != 1 || m.Formula.Name != "claude-rc-proxy" {
		t.Fatalf("Parse() = %#v", m)
	}
	if got := m.RepositorySlug(); got != "SijanC147/claude-rc-proxy" {
		t.Fatalf("RepositorySlug() = %q", got)
	}
}

func TestParseRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	tests := map[string]string{
		"root unknown":             strings.Replace(validManifest, `"schema": 1,`, `"schema": 1, "surprise": true,`, 1),
		"nested unknown":           strings.Replace(validManifest, `"name": "claude-rc-proxy",`, `"name": "claude-rc-proxy", "surprise": true,`, 1),
		"missing required boolean": strings.Replace(validManifest, "    \"linux\": true\n", "", 1),
		"disabled service fields":  strings.Replace(validManifest, `"enabled": true`, `"enabled": false`, 1),
		"trailing document":        validManifest + ` {"schema":1}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("Parse() unexpectedly succeeded")
			}
		})
	}
}

func TestValidateRejectsHighRiskStrings(t *testing.T) {
	tests := map[string]func(*Manifest){
		"schema":                    func(m *Manifest) { m.Schema = 2 },
		"formula name slash":        func(m *Manifest) { m.Formula.Name = "bad/name" },
		"formula name ruby":         func(m *Manifest) { m.Formula.Name = `bad\"; system(\"id\")` },
		"class punctuation":         func(m *Manifest) { m.Formula.Class = "Bad::Formula" },
		"description newline":       func(m *Manifest) { m.Formula.Description = "first\nsecond" },
		"description interpolation": func(m *Manifest) { m.Formula.Description = `#{system("id")}` },
		"description quote":         func(m *Manifest) { m.Formula.Description = `bad " quote` },
		"empty description":         func(m *Manifest) { m.Formula.Description = "" },
		"homepage http":             func(m *Manifest) { m.Formula.Homepage = "http://example.com" },
		"homepage credentials":      func(m *Manifest) { m.Formula.Homepage = "https://user@example.com" },
		"homepage quote":            func(m *Manifest) { m.Formula.Homepage = `https://example.com/\"` },
		"empty license":             func(m *Manifest) { m.Formula.License = "" },
		"license quote":             func(m *Manifest) { m.Formula.License = `MIT\"` },
		"owner slash":               func(m *Manifest) { m.Formula.Repository.Owner = "bad/owner" },
		"owner trailing dash":       func(m *Manifest) { m.Formula.Repository.Owner = "bad-" },
		"repo traversal":            func(m *Manifest) { m.Formula.Repository.Name = ".." },
		"binary slash":              func(m *Manifest) { m.Formula.Binary = "bin/tool" },
		"asset slash":               func(m *Manifest) { m.Formula.Assets.DarwinARM64 = "dir/tool.tar.gz" },
		"asset extension":           func(m *Manifest) { m.Formula.Assets.DarwinARM64 = "tool.zip" },
		"duplicate assets":          func(m *Manifest) { m.Formula.Assets.DarwinAMD64 = m.Formula.Assets.DarwinARM64 },
		"absolute build script":     func(m *Manifest) { m.Release.BuildScript = "/tmp/build" },
		"build script traversal":    func(m *Manifest) { m.Release.BuildScript = "../build" },
		"build script backslash":    func(m *Manifest) { m.Release.BuildScript = `scripts\\build` },
		"empty test args":           func(m *Manifest) { m.Homebrew.TestArgs = nil },
		"test arg shell":            func(m *Manifest) { m.Homebrew.TestArgs = []string{"$(id)"} },
		"service arg quote":         func(m *Manifest) { m.Homebrew.Service.RunArgs = []string{`\"; system(\"id\")`} },
		"service args null":         func(m *Manifest) { m.Homebrew.Service.RunArgs = nil },
		"environment lowercase key": func(m *Manifest) { m.Homebrew.Service.Environment = map[string]string{"bad": "value"} },
		"environment null":          func(m *Manifest) { m.Homebrew.Service.Environment = nil },
		"environment newline":       func(m *Manifest) { m.Homebrew.Service.Environment["RUNTIME_MODE"] = "one\ntwo" },
		"environment quote":         func(m *Manifest) { m.Homebrew.Service.Environment["RUNTIME_MODE"] = `\"; system(\"id\")` },
		"environment interpolation": func(m *Manifest) { m.Homebrew.Service.Environment["RUNTIME_MODE"] = "#{system}" },
		"restart delay zero":        func(m *Manifest) { m.Homebrew.Service.RestartDelay = 0 },
		"missing keep alive policy": func(m *Manifest) { m.Homebrew.Service.KeepAlive = KeepAlive{} },
		"multiple keep alive policies": func(m *Manifest) {
			value := false
			m.Homebrew.Service.KeepAlive.SuccessfulExit = &value
		},
		"absolute log":                func(m *Manifest) { m.Homebrew.Service.LogPath = "/tmp/out.log" },
		"log traversal":               func(m *Manifest) { m.Homebrew.Service.ErrorLogPath = "../out.log" },
		"caveats terminator":          func(m *Manifest) { m.Homebrew.Caveats = "before\nEOS\nafter" },
		"caveats interpolation":       func(m *Manifest) { m.Homebrew.Caveats = "#{system(\"id\")}" },
		"caveats unknown placeholder": func(m *Manifest) { m.Homebrew.Caveats = "Use {{prefix}} here" },
		"caveats carriage return":     func(m *Manifest) { m.Homebrew.Caveats = "one\rtwo" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			m, err := Parse([]byte(validManifest))
			if err != nil {
				t.Fatalf("valid fixture: %v", err)
			}
			mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}

func TestValidateAllowsDisabledServiceWithoutServiceFields(t *testing.T) {
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("valid fixture: %v", err)
	}
	m.Homebrew.Service = &Service{Enabled: false}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestStableVersion(t *testing.T) {
	valid := []string{"0.0.0", "0.1.0", "1.2.3", "18446744073709551616.2.3"}
	for _, version := range valid {
		if err := ValidateStableVersion(version); err != nil {
			t.Errorf("ValidateStableVersion(%q) = %v", version, err)
		}
	}

	invalid := []string{"", "v1.2.3", "1.2", "1.2.3-beta.1", "01.2.3", "1.02.3", "1.2.03", "+1.2.3", "1.2.3\\n"}
	for _, version := range invalid {
		if err := ValidateStableVersion(version); err == nil {
			t.Errorf("ValidateStableVersion(%q) unexpectedly succeeded", version)
		}
	}
}

func TestCompareStableVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.4", "1.2.3", 1},
		{"2.0.0", "10.0.0", -1},
		{"18446744073709551616.0.0", "18446744073709551615.9.9", 1},
	}
	for _, tt := range tests {
		got, err := CompareStableVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("CompareStableVersions(%q, %q): %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Errorf("CompareStableVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
