package manifest

import (
	"bytes"
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

func TestLegacyBinaryAliasesAndZshCompletionContract(t *testing.T) {
	withInstallMetadata := func(binaryAliases, completion string) string {
		return strings.Replace(validManifest, `"macos_only": true,`, `"macos_only": true,
    "binary_aliases": `+binaryAliases+`,
    "zsh_completion": "`+completion+`",`, 1)
	}
	project, err := Parse([]byte(withInstallMetadata(`["hextap"]`, "completions/_hextap")))
	if err != nil {
		t.Fatalf("valid install metadata rejected: %v", err)
	}
	encoded, err := project.MarshalJSON()
	if err != nil || !bytes.Contains(encoded, []byte(`"binary_aliases":["hextap"]`)) || !bytes.Contains(encoded, []byte(`"zsh_completion":"completions/_hextap"`)) {
		t.Fatalf("MarshalJSON() lost install metadata: %s, error = %v", encoded, err)
	}
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("Parse(MarshalJSON()) rejected install metadata: %v", err)
	}

	for _, test := range []struct {
		name       string
		aliases    string
		completion string
	}{
		{name: "empty aliases", aliases: `[]`, completion: "completions/_hextap"},
		{name: "binary collision", aliases: `["claude-rc-proxy"]`, completion: "completions/_claude-rc-proxy"},
		{name: "case folded duplicate", aliases: `["hextap", "Hextap"]`, completion: "completions/_hextap"},
		{name: "unsafe alias", aliases: `["bin/hextap"]`, completion: "completions/_hextap"},
		{name: "wrong directory", aliases: `["hextap"]`, completion: "completion/_hextap"},
		{name: "missing underscore", aliases: `["hextap"]`, completion: "completions/hextap"},
		{name: "nested completion", aliases: `["hextap"]`, completion: "completions/nested/_hextap"},
		{name: "completion stem mismatch", aliases: `["hextap"]`, completion: "completions/_other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(withInstallMetadata(test.aliases, test.completion))); err == nil {
				t.Fatal("Parse() unexpectedly accepted invalid install metadata")
			}
		})
	}
}

func TestParseRejectsDuplicateObjectKeysAtEveryNestingLevel(t *testing.T) {
	tests := map[string]string{
		"root":         strings.Replace(validManifest, `"schema": 1,`, `"schema": 1, "schema": 1,`, 1),
		"escaped root": strings.Replace(validManifest, `"schema": 1,`, `"schema": 1, "\u0073chema": 1,`, 1),
		"formula":      strings.Replace(validManifest, `"name": "claude-rc-proxy",`, `"name": "claude-rc-proxy", "name": "other",`, 1),
		"repository":   strings.Replace(validManifest, `"owner": "SijanC147",`, `"owner": "SijanC147", "owner": "other",`, 1),
		"service":      strings.Replace(validManifest, `"crashed": true`, `"crashed": true, "crashed": false`, 1),
		"array item":   strings.Replace(validManifest, `"test_args": ["--version"]`, `"test_args": [{"value": 1, "value": 2}]`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
				t.Fatalf("Parse() error = %v, want duplicate object key rejection", err)
			}
		})
	}
}

func TestParseRejectsCaseFoldAliasesAtEveryTypedObject(t *testing.T) {
	const hiddenCredential = "github_pat_1234567890hiddenalias"
	tests := map[string]string{
		"root alias":        strings.Replace(validManifest, `"formula": {`, `"Formula": {`, 1),
		"root collision":    strings.Replace(validManifest, `"formula": {`, `"Formula": {}, "formula": {`, 1),
		"formula alias":     strings.Replace(validManifest, `"name": "claude-rc-proxy",`, `"Name": "claude-rc-proxy",`, 1),
		"formula collision": strings.Replace(validManifest, `"name": "claude-rc-proxy",`, `"name": "claude-rc-proxy", "Name": "`+hiddenCredential+`",`, 1),
		"repository alias":  strings.Replace(validManifest, `"owner": "SijanC147",`, `"Owner": "SijanC147",`, 1),
		"assets alias":      strings.Replace(validManifest, `"darwin_arm64":`, `"Darwin_ARM64":`, 1),
		"release alias":     strings.Replace(validManifest, `"build_script":`, `"Build_Script":`, 1),
		"homebrew alias":    strings.Replace(validManifest, `"macos_only":`, `"MacOS_Only":`, 1),
		"service alias":     strings.Replace(validManifest, `"enabled": true`, `"Enabled": true`, 1),
		"keep alive alias":  strings.Replace(validManifest, `"crashed": true`, `"Crashed": true`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("Parse() unexpectedly accepted a case-fold alias")
			} else if strings.Contains(err.Error(), hiddenCredential) {
				t.Fatalf("Parse() leaked losing alias value: %v", err)
			}
		})
	}
}

func TestParseRejectsInvalidOrLossyUnicode(t *testing.T) {
	invalidUTF8 := bytes.Replace([]byte(validManifest), []byte("Selective Anthropic"), append([]byte("Selective "), 0xff), 1)
	tests := map[string][]byte{
		"invalid UTF-8":       invalidUTF8,
		"unpaired surrogate":  bytes.Replace([]byte(validManifest), []byte("Selective Anthropic"), []byte(`Selective \ud800`), 1),
		"replacement escape":  bytes.Replace([]byte(validManifest), []byte("Selective Anthropic"), []byte(`Selective \ufffd`), 1),
		"literal replacement": bytes.Replace([]byte(validManifest), []byte("Selective Anthropic"), []byte("Selective �"), 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatal("Parse() unexpectedly accepted invalid or lossy Unicode")
			}
		})
	}
}

func TestValidateRejectsHighRiskStrings(t *testing.T) {
	tests := map[string]func(*Manifest){
		"schema":                    func(m *Manifest) { m.Schema = 2 },
		"macos only false":          func(m *Manifest) { m.Homebrew.MacOSOnly = false },
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

func TestValidateRejectsEveryRubyInterpolationIntroducer(t *testing.T) {
	introducers := []string{"#{danger}", "#@danger", "#$danger"}
	fields := map[string]func(*Manifest, string){
		"description":      func(m *Manifest, value string) { m.Formula.Description = "prefix " + value },
		"homepage":         func(m *Manifest, value string) { m.Formula.Homepage = "https://example.com/" + value },
		"license":          func(m *Manifest, value string) { m.Formula.License = "MIT " + value },
		"repository owner": func(m *Manifest, value string) { m.Formula.Repository.Owner = "owner" + value },
		"repository name":  func(m *Manifest, value string) { m.Formula.Repository.Name = "repo" + value },
		"binary":           func(m *Manifest, value string) { m.Formula.Binary = "binary" + value },
		"arm64 asset":      func(m *Manifest, value string) { m.Formula.Assets.DarwinARM64 = "asset" + value + ".tar.gz" },
		"amd64 asset":      func(m *Manifest, value string) { m.Formula.Assets.DarwinAMD64 = "asset" + value + ".tar.gz" },
		"test argument":    func(m *Manifest, value string) { m.Homebrew.TestArgs = []string{"--version" + value} },
		"service argument": func(m *Manifest, value string) { m.Homebrew.Service.RunArgs = []string{"--config" + value} },
		"environment":      func(m *Manifest, value string) { m.Homebrew.Service.Environment["RUNTIME_MODE"] = "prefix " + value },
		"log path":         func(m *Manifest, value string) { m.Homebrew.Service.LogPath = "log/output" + value },
		"error log path":   func(m *Manifest, value string) { m.Homebrew.Service.ErrorLogPath = "log/error" + value },
		"caveats":          func(m *Manifest, value string) { m.Homebrew.Caveats = "prefix " + value },
	}
	for _, introducer := range introducers {
		for field, mutate := range fields {
			t.Run(field+"/"+introducer[:2], func(t *testing.T) {
				m, err := Parse([]byte(validManifest))
				if err != nil {
					t.Fatalf("valid fixture: %v", err)
				}
				mutate(&m, introducer)
				if err := m.Validate(); err == nil {
					t.Fatalf("Validate() accepted Ruby interpolation introducer %q in %s", introducer[:2], field)
				}
			})
		}
	}
}

func TestFormulaClassMustBeDerivedFromFormulaName(t *testing.T) {
	tests := []struct {
		name      string
		class     string
		wantClass string
		valid     bool
	}{
		{name: "claude-rc-proxy", class: "ClaudeRcProxy", wantClass: "ClaudeRcProxy", valid: true},
		{name: "tool2-api3", class: "Tool2Api3", wantClass: "Tool2Api3", valid: true},
		{name: "claude-rc-proxy", class: "ClaudeRCProxy", wantClass: "ClaudeRcProxy", valid: false},
		{name: "2tool", class: "Tool", valid: false},
		{name: "tool-2api", class: "Tool2Api", valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.class, func(t *testing.T) {
			m, err := Parse([]byte(validManifest))
			if err != nil {
				t.Fatalf("valid fixture: %v", err)
			}
			m.Formula.Name = tt.name
			m.Formula.Class = tt.class
			err = m.Validate()
			if tt.valid && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
			if tt.wantClass != "" {
				if got := formulaClassForName(tt.name); got != tt.wantClass {
					t.Fatalf("formulaClassForName(%q) = %q, want %q", tt.name, got, tt.wantClass)
				}
			}
		})
	}
}

func TestValidateEnforcesGeneratedAndFilesystemPathByteLimits(t *testing.T) {
	base, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Manifest){
		"formula generated suffix reserve": func(project *Manifest) {
			project.Formula.Name = strings.Repeat("a", 236)
			project.Formula.Class = formulaClassForName(project.Formula.Name)
			project.Formula.Assets.DarwinARM64 = project.Formula.Name + "-darwin-arm64.tar.gz"
			project.Formula.Assets.DarwinAMD64 = project.Formula.Name + "-darwin-amd64.tar.gz"
		},
		"class basename": func(project *Manifest) {
			project.Formula.Class = strings.Repeat("A", 256)
		},
		"binary basename": func(project *Manifest) {
			project.Formula.Binary = strings.Repeat("a", 256)
		},
		"explicit asset basename": func(project *Manifest) {
			project.Formula.Assets.DarwinARM64 = strings.Repeat("a", 249) + ".tar.gz"
		},
		"relative component": func(project *Manifest) {
			project.Release.BuildScript = "scripts/" + strings.Repeat("a", 256)
		},
		"relative total": func(project *Manifest) {
			component := strings.Repeat("a", 250)
			project.Release.BuildScript = strings.Join([]string{component, component, component, component, component}, "/")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			project := base
			mutate(&project)
			if err := project.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly accepted an oversized path value")
			}
		})
	}

	boundary := base
	boundary.Formula.Name = strings.Repeat("a", 235)
	boundary.Formula.Class = formulaClassForName(boundary.Formula.Name)
	boundary.Formula.Binary = strings.Repeat("b", 255)
	boundary.Formula.Assets.DarwinARM64 = boundary.Formula.Name + "-darwin-arm64.tar.gz"
	boundary.Formula.Assets.DarwinAMD64 = boundary.Formula.Name + "-darwin-amd64.tar.gz"
	boundary.Release.BuildScript = strings.Repeat("c", 255)
	if err := boundary.Validate(); err != nil {
		t.Fatalf("Validate(boundary values) = %v", err)
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
