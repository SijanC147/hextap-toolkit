package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRemoteRulesetFixture(t *testing.T, localPath, destination string, id int64, mutate func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	body["id"] = id
	body["source_type"] = "Repository"
	body["source"] = "SijanC147/example-tool"
	body["bypass_actors"] = []any{}
	if mutate != nil {
		mutate(body)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFakeGH(t *testing.T, project string) (logPath string) {
	t.Helper()
	directory := t.TempDir()
	logPath = filepath.Join(directory, "gh.log")
	mainRemote := filepath.Join(directory, "main.json")
	tagRemote := filepath.Join(directory, "tags.json")
	mainDrift := filepath.Join(directory, "main-drift.json")
	mainConditionsDrift := filepath.Join(directory, "main-conditions-drift.json")
	mainRulesDrift := filepath.Join(directory, "main-rules-drift.json")
	mainBypass := filepath.Join(directory, "main-bypass.json")
	mainWrongSource := filepath.Join(directory, "main-wrong-source.json")
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "main.json"), mainRemote, 101, nil)
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "release-tags.json"), tagRemote, 102, nil)
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "main.json"), mainDrift, 101, func(body map[string]any) {
		body["target"] = "tag"
	})
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "main.json"), mainConditionsDrift, 101, func(body map[string]any) {
		body["conditions"] = map[string]any{"ref_name": map[string]any{"include": []any{"~ALL"}, "exclude": []any{}}}
	})
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "main.json"), mainRulesDrift, 101, func(body map[string]any) {
		rules := body["rules"].([]any)
		body["rules"] = rules[:len(rules)-1]
	})
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "main.json"), mainBypass, 101, func(body map[string]any) {
		body["bypass_actors"] = []any{map[string]any{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}}
	})
	writeRemoteRulesetFixture(t, filepath.Join(project, ".hextap", "rulesets", "main.json"), mainWrongSource, 101, func(body map[string]any) {
		body["source_type"] = "Organization"
		body["source"] = "SijanC147"
	})
	formulaPath := filepath.Join(directory, "formula.rb")
	formulaSpoofPath := filepath.Join(directory, "formula-spoof.rb")
	formula := `class ExampleTool < Formula
  desc "class Wrong < Formula"
  def caveats
    <<~EOS
      class Wrong < Formula
    EOS
  end
end
`
	if err := os.WriteFile(formulaPath, []byte(formula), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(formulaSpoofPath, []byte("# class ExampleTool < Formula\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_GH_LOG"
mode="${FAKE_GH_MODE:-success}"
case "$*" in
  "auth status --hostname github.com")
    if [ "$mode" = auth ]; then printf '%s\n' 'ghp_1234567890secret' >&2; exit 1; fi ;;
  "api repos/SijanC147/example-tool --jq .default_branch")
    if [ "$mode" = default ]; then printf '%s\n' develop; else printf '%s\n' main; fi ;;
  "api repos/SijanC147/example-tool/immutable-releases --jq .enabled")
    if [ "$mode" = immutable ]; then printf '%s\n' false; else printf '%s\n' true; fi ;;
  "api --paginate repos/SijanC147/example-tool/actions/secrets --jq .secrets[].name")
    if [ "$mode" != secret ]; then printf '%s\n' OP_SERVICE_ACCOUNT_TOKEN; fi ;;
  "api --paginate --slurp repos/SijanC147/example-tool/rulesets?per_page=100")
    case "$mode" in
      ruleset-missing) printf '%s\n' '[[{"id":101,"name":"hextap/main","target":"branch","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"}]]' ;;
      ruleset-wrong-source) printf '%s\n' '[[{"id":101,"name":"hextap/main","target":"branch","source_type":"Organization","source":"SijanC147","enforcement":"active"},{"id":102,"name":"hextap/release-tags","target":"tag","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"}]]' ;;
      ruleset-inactive) printf '%s\n' '[[{"id":101,"name":"hextap/main","target":"branch","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"disabled"},{"id":102,"name":"hextap/release-tags","target":"tag","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"}]]' ;;
      ruleset-duplicate) printf '%s\n' '[[{"id":101,"name":"hextap/main","target":"branch","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"},{"id":103,"name":"hextap/main","target":"branch","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"},{"id":102,"name":"hextap/release-tags","target":"tag","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"}]]' ;;
      *) printf '%s\n' '[[{"id":101,"name":"hextap/main","target":"branch","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"},{"id":102,"name":"hextap/release-tags","target":"tag","source_type":"Repository","source":"SijanC147/example-tool","enforcement":"active"}]]' ;;
    esac ;;
  "api repos/SijanC147/example-tool/rulesets/101")
    case "$mode" in
      ruleset-drift) cat "$FAKE_MAIN_DRIFT" ;;
      ruleset-conditions-drift) cat "$FAKE_MAIN_CONDITIONS_DRIFT" ;;
      ruleset-rules-drift) cat "$FAKE_MAIN_RULES_DRIFT" ;;
      ruleset-bypass) cat "$FAKE_MAIN_BYPASS" ;;
      ruleset-detail-source) cat "$FAKE_MAIN_WRONG_SOURCE" ;;
      *) cat "$FAKE_MAIN_RULESET" ;;
    esac ;;
  "api repos/SijanC147/example-tool/rulesets/102") cat "$FAKE_TAG_RULESET" ;;
  "api repos/SijanC147/hextap-toolkit/git/ref/tags/v1.2.3")
    case "$mode" in
      tag-missing) exit 44 ;;
      tag-malformed) printf '%s\n' '{"ref":"refs/tags/v1.2.3","object":{"type":"tag","sha":"bad"}}' ;;
      tag-annotated|tag-nested|tag-cycle|tag-type) printf '%s\n' '{"ref":"refs/tags/v1.2.3","object":{"type":"tag","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}' ;;
      tag-depth) printf '%s\n' '{"ref":"refs/tags/v1.2.3","object":{"type":"tag","sha":"1111111111111111111111111111111111111111"}}' ;;
      tag-commit-drift) printf '%s\n' '{"ref":"refs/tags/v1.2.3","object":{"type":"commit","sha":"ffffffffffffffffffffffffffffffffffffffff"}}' ;;
      *) printf '%s\n' '{"ref":"refs/tags/v1.2.3","object":{"type":"commit","sha":"0123456789abcdef0123456789abcdef01234567"}}' ;;
    esac ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    case "$mode" in
      tag-annotated) printf '%s\n' '{"tag":"v1.2.3","object":{"type":"commit","sha":"0123456789abcdef0123456789abcdef01234567"}}' ;;
      tag-type) printf '%s\n' '{"tag":"v1.2.3","object":{"type":"blob","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}' ;;
      *) printf '%s\n' '{"tag":"v1.2.3","object":{"type":"tag","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}' ;;
    esac ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    if [ "$mode" = tag-cycle ]; then printf '%s\n' '{"tag":"nested","object":{"type":"tag","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}'; else printf '%s\n' '{"tag":"nested","object":{"type":"commit","sha":"0123456789abcdef0123456789abcdef01234567"}}'; fi ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/1111111111111111111111111111111111111111") printf '%s\n' '{"tag":"depth-1","object":{"type":"tag","sha":"2222222222222222222222222222222222222222"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/2222222222222222222222222222222222222222") printf '%s\n' '{"tag":"depth-2","object":{"type":"tag","sha":"3333333333333333333333333333333333333333"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/3333333333333333333333333333333333333333") printf '%s\n' '{"tag":"depth-3","object":{"type":"tag","sha":"4444444444444444444444444444444444444444"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/4444444444444444444444444444444444444444") printf '%s\n' '{"tag":"depth-4","object":{"type":"tag","sha":"5555555555555555555555555555555555555555"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/5555555555555555555555555555555555555555") printf '%s\n' '{"tag":"depth-5","object":{"type":"tag","sha":"6666666666666666666666666666666666666666"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/6666666666666666666666666666666666666666") printf '%s\n' '{"tag":"depth-6","object":{"type":"tag","sha":"7777777777777777777777777777777777777777"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/7777777777777777777777777777777777777777") printf '%s\n' '{"tag":"depth-7","object":{"type":"tag","sha":"8888888888888888888888888888888888888888"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/8888888888888888888888888888888888888888") printf '%s\n' '{"tag":"depth-8","object":{"type":"tag","sha":"9999999999999999999999999999999999999999"}}' ;;
  "api repos/SijanC147/hextap-toolkit/git/tags/9999999999999999999999999999999999999999") printf '%s\n' '{"tag":"depth-9","object":{"type":"commit","sha":"0123456789abcdef0123456789abcdef01234567"}}' ;;
  "api repos/SijanC147/hextap-toolkit/commits/v1.2.3 --jq .sha") printf '%s\n' '0123456789abcdef0123456789abcdef01234567' ;;
  "api -H Accept: application/vnd.github.raw+json repos/SijanC147/homebrew-hextap/contents/Projects/example-tool.json")
    if [ "$mode" = tap ]; then printf '%s\n' '{"schema":1}'; else cat "$FAKE_TAP_PATH"; fi ;;
  "api -H Accept: application/vnd.github.raw+json repos/SijanC147/homebrew-hextap/contents/Formula/example-tool.rb")
    case "$mode" in
      formula-missing) exit 44 ;;
      formula-spoof) cat "$FAKE_FORMULA_SPOOF" ;;
      *) cat "$FAKE_FORMULA_PATH" ;;
    esac ;;
  *) exit 44 ;;
esac
`
	path := filepath.Join(directory, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GH_LOG", logPath)
	t.Setenv("FAKE_TAP_PATH", filepath.Join(project, ".hextap.json"))
	t.Setenv("FAKE_MAIN_RULESET", mainRemote)
	t.Setenv("FAKE_TAG_RULESET", tagRemote)
	t.Setenv("FAKE_MAIN_DRIFT", mainDrift)
	t.Setenv("FAKE_MAIN_CONDITIONS_DRIFT", mainConditionsDrift)
	t.Setenv("FAKE_MAIN_RULES_DRIFT", mainRulesDrift)
	t.Setenv("FAKE_MAIN_BYPASS", mainBypass)
	t.Setenv("FAKE_MAIN_WRONG_SOURCE", mainWrongSource)
	t.Setenv("FAKE_FORMULA_PATH", formulaPath)
	t.Setenv("FAKE_FORMULA_SPOOF", formulaSpoofPath)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func TestDoctorLocalMakesNoGHCallsAndOnlineIsReadOnly(t *testing.T) {
	project := writeGoProject(t)
	if _, err := Onboard(validOptions(project)); err != nil {
		t.Fatal(err)
	}
	logPath := writeFakeGH(t, project)
	local, err := Doctor(DoctorOptions{Project: project})
	if err != nil {
		t.Fatalf("Doctor(local) error = %v", err)
	}
	if len(local.Checks) != 4 {
		t.Fatalf("local checks = %v", local.Checks)
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("local doctor invoked gh: %s", data)
	}
	online, err := Doctor(DoctorOptions{Project: project, Online: true})
	if err != nil {
		t.Fatalf("Doctor(online) error = %v", err)
	}
	if len(online.Checks) != 11 {
		t.Fatalf("online checks = %v", online.Checks)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"--method", "POST", "PUT", "PATCH", "DELETE", "secret set"} {
		if strings.Contains(string(log), mutation) {
			t.Fatalf("online doctor used mutation verb %q:\n%s", mutation, log)
		}
	}
	if strings.Contains(string(log), "/commits/v1.2.3") {
		t.Fatalf("online doctor used ambiguous commit-ish resolution:\n%s", log)
	}
}

func TestDoctorOnlineAcceptsLightweightAndBoundedAnnotatedTags(t *testing.T) {
	for _, mode := range []string{"success", "tag-annotated", "tag-nested"} {
		t.Run(mode, func(t *testing.T) {
			project := writeGoProject(t)
			if _, err := Onboard(validOptions(project)); err != nil {
				t.Fatal(err)
			}
			writeFakeGH(t, project)
			t.Setenv("FAKE_GH_MODE", mode)
			if _, err := Doctor(DoctorOptions{Project: project, Online: true}); err != nil {
				t.Fatalf("Doctor(online, %s) = %v", mode, err)
			}
		})
	}
}

func TestDoctorOnlineReportsEveryRemoteInvariantFailure(t *testing.T) {
	tests := map[string]string{
		"auth":                      "auth",
		"default branch":            "default",
		"immutable releases":        "immutable",
		"secret":                    "secret",
		"ruleset missing":           "ruleset-missing",
		"ruleset wrong source":      "ruleset-wrong-source",
		"ruleset inactive":          "ruleset-inactive",
		"ruleset duplicate":         "ruleset-duplicate",
		"ruleset body drift":        "ruleset-drift",
		"ruleset conditions drift":  "ruleset-conditions-drift",
		"ruleset rules drift":       "ruleset-rules-drift",
		"ruleset bypass drift":      "ruleset-bypass",
		"ruleset detail source":     "ruleset-detail-source",
		"tag absent":                "tag-missing",
		"tag malformed":             "tag-malformed",
		"tag cycle":                 "tag-cycle",
		"tag wrong object type":     "tag-type",
		"tag peel depth":            "tag-depth",
		"tag commit drift":          "tag-commit-drift",
		"tap mismatch":              "tap",
		"Formula missing":           "formula-missing",
		"Formula declaration spoof": "formula-spoof",
	}
	for name, mode := range tests {
		t.Run(name, func(t *testing.T) {
			project := writeGoProject(t)
			if _, err := Onboard(validOptions(project)); err != nil {
				t.Fatal(err)
			}
			logPath := writeFakeGH(t, project)
			t.Setenv("FAKE_GH_MODE", mode)
			if _, err := Doctor(DoctorOptions{Project: project, Online: true}); err == nil {
				t.Fatal("Doctor(online) unexpectedly succeeded")
			} else if strings.Contains(err.Error(), "ghp_1234567890secret") {
				t.Fatalf("Doctor(online) leaked discarded auth diagnostics: %v", err)
			}
			if mode == "tag-missing" {
				log, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(log), "/commits/v1.2.3") {
					t.Fatalf("missing exact tag fell back to ambiguous commit-ish lookup:\n%s", log)
				}
			}
		})
	}
}
