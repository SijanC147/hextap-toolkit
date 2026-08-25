package onboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const testToolkitSHA = "0123456789abcdef0123456789abcdef01234567"

func writeGoProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module github.com/SijanC147/example-tool\n\ngo 1.26\n",
		"main.go":   "package main\n\nimport \"fmt\"\n\nvar version = \"dev\"\nvar commit = \"unknown\"\n\nfunc main() { fmt.Printf(\"example-tool %s (commit %s)\\n\", version, commit) }\n",
		"LICENSE":   "MIT License\n",
		"README.md": "# Example Tool\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	commands := [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", "git@github.com:SijanC147/example-tool.git"},
	}
	for _, args := range commands {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return root
}

func TestGeneratedAdapterBuildsAndVerifiesPrerelease(t *testing.T) {
	project := writeGoProject(t)
	if _, err := Onboard(validOptions(project)); err != nil {
		t.Fatalf("Onboard() error = %v", err)
	}
	result, err := Validate(ValidateOptions{Project: project, Build: true})
	if err != nil {
		t.Fatalf("Validate(build) error = %v", err)
	}
	if !result.BuildVerified {
		t.Fatal("Validate(build) did not report build verification")
	}
	adapter, err := os.ReadFile(filepath.Join(project, "scripts", "hextap-build"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"CGO_ENABLED=0", "-mod=readonly", "-trimpath", "-buildvcs=false", "HEXTAP_VERSION", "HEXTAP_COMMIT"} {
		if !bytes.Contains(adapter, []byte(required)) {
			t.Fatalf("generated adapter lacks %q:\n%s", required, adapter)
		}
	}
	if bytes.Contains(adapter, []byte("1.2.3")) || bytes.Contains(adapter, []byte("stable")) {
		t.Fatalf("generated adapter embeds a stable-only version restriction:\n%s", adapter)
	}
}

func TestExistingCustomAdapterIsPreservedAndValidated(t *testing.T) {
	project := writeGoProject(t)
	options := validOptions(project)
	if _, err := Onboard(options); err != nil {
		t.Fatal(err)
	}
	adapterPath := filepath.Join(project, "scripts", "hextap-build")
	custom := []byte("#!/bin/sh\nset -eu\nCGO_ENABLED=0 GOOS=\"$HEXTAP_TARGET_OS\" GOARCH=\"$HEXTAP_TARGET_ARCH\" go build -mod=readonly -trimpath -buildvcs=false -ldflags \"-X=main.version=$HEXTAP_VERSION -X=main.commit=$HEXTAP_COMMIT\" -o \"$HEXTAP_OUTPUT\" .\n")
	if err := os.WriteFile(adapterPath, custom, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Onboard(options)
	if err != nil {
		t.Fatalf("Onboard(custom adapter) error = %v", err)
	}
	found := false
	for _, entry := range result.Entries {
		if entry.Path == "scripts/hextap-build" {
			found = true
			if entry.Action != ActionValidated {
				t.Fatalf("custom adapter action = %s", entry.Action)
			}
		}
	}
	if !found {
		t.Fatal("custom adapter missing from plan")
	}
	data, err := os.ReadFile(adapterPath)
	if err != nil || !bytes.Equal(data, custom) {
		t.Fatalf("custom adapter changed: %v, %q", err, data)
	}
	if _, err := Validate(ValidateOptions{Project: project}); err != nil {
		t.Fatalf("Validate(custom adapter) error = %v", err)
	}
}

func TestAuthoritativeManifestWithoutNewlineAndBinaryCustomAdapterArePreserved(t *testing.T) {
	project := writeGoProject(t)
	options := validOptions(project)
	if _, err := Onboard(options); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(project, ".hextap.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestData = bytes.TrimSuffix(manifestData, []byte("\n"))
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		".github/workflows/hextap-release.yml",
		".hextap/tap-registration.json",
		".hextap/rulesets/main.json",
		".hextap/rulesets/release-tags.json",
		".hextap/SETUP.md",
	} {
		if err := os.Remove(filepath.Join(project, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	custom := []byte("\x00\xffbinary custom adapter ghp_1234567890secret")
	adapterPath := filepath.Join(project, "scripts", "hextap-build")
	if err := os.WriteFile(adapterPath, custom, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Onboard(options)
	if err != nil {
		t.Fatalf("Onboard(authoritative no-LF/custom binary) = %v", err)
	}
	for _, entry := range result.Entries {
		if entry.Path == "scripts/hextap-build" && entry.Action != ActionValidated {
			t.Fatalf("custom binary action = %s", entry.Action)
		}
	}
	gotManifest, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(gotManifest, manifestData) {
		t.Fatalf("authoritative manifest changed: %v, %q", err, gotManifest)
	}
	tap, err := os.ReadFile(filepath.Join(project, ".hextap", "tap-registration.json"))
	if err != nil || !bytes.Equal(tap, manifestData) {
		t.Fatalf("tap is not exact no-LF manifest bytes: %v, %q", err, tap)
	}
	gotAdapter, err := os.ReadFile(adapterPath)
	if err != nil || !bytes.Equal(gotAdapter, custom) {
		t.Fatalf("custom binary adapter changed: %v, %q", err, gotAdapter)
	}
	if _, err := Validate(ValidateOptions{Project: project}); err != nil {
		t.Fatalf("Validate(authoritative no-LF/custom binary) = %v", err)
	}
}

func TestAuthoritativeManifestCredentialMetadataIsRejectedWithoutWritesOrLeakage(t *testing.T) {
	const decodedCredential = "github_pat_1234567890authoritative"
	tests := map[string]string{
		"plain":   decodedCredential,
		"escaped": `\u0067\u0069\u0074\u0068\u0075\u0062\u005f\u0070\u0061\u0074\u005f1234567890authoritative`,
	}
	for name, encodedCredential := range tests {
		t.Run(name, func(t *testing.T) {
			root := writeGoProject(t)
			item := expectedArtifact(t, root, manifestPath)
			manifestData := bytes.Replace(item.data, []byte("A deterministic example CLI"), []byte(encodedCredential), 1)
			manifestData = bytes.TrimSuffix(manifestData, []byte("\n"))
			if err := os.WriteFile(filepath.Join(root, manifestPath), manifestData, 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshotProjectTree(t, root)
			options := validOptions(root)
			options.Description = ""
			result, err := Onboard(options)
			if err == nil {
				t.Fatal("Onboard() unexpectedly accepted credential metadata")
			}
			if strings.Contains(err.Error(), decodedCredential) || strings.Contains(err.Error(), encodedCredential) {
				t.Fatalf("credential value leaked in error: %v", err)
			}
			if _, validateErr := Validate(ValidateOptions{Project: root}); validateErr == nil {
				t.Fatal("Validate() unexpectedly accepted credential metadata")
			} else if strings.Contains(validateErr.Error(), decodedCredential) || strings.Contains(validateErr.Error(), encodedCredential) {
				t.Fatalf("credential value leaked in validation error: %v", validateErr)
			}
			if result.Project != "" || len(result.Entries) != 0 || result.DryRun {
				t.Fatalf("failed onboarding returned a plan/result: %#v", result)
			}
			after := snapshotProjectTree(t, root)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("credential rejection changed project tree\nbefore=%v\nafter=%v", before, after)
			}
			for _, relative := range []string{workflowPath, tapPath, mainRulesetPath, tagRulesetPath, setupPath, defaultAdapterPath} {
				if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(statErr) {
					t.Fatalf("credential rejection created %s: %v", relative, statErr)
				}
			}
			if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if relative == ".git" {
					return filepath.SkipDir
				}
				if !entry.Type().IsRegular() {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if relative != manifestPath && (bytes.Contains(data, []byte(decodedCredential)) || bytes.Contains(data, []byte(encodedCredential))) {
					return fmt.Errorf("credential leaked from manifest into %s", relative)
				}
				if name == "escaped" && bytes.Contains(data, []byte(decodedCredential)) {
					return fmt.Errorf("decoded escaped credential appeared in %s", relative)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManifestCredentialScanRecursesThroughSlicesPointersAndMaps(t *testing.T) {
	project := manifest.Manifest{
		Homebrew: manifest.Homebrew{
			TestArgs: []string{"--version"},
			Service: &manifest.Service{
				Environment: map[string]string{"SAFE_NAME": "safe-value"},
			},
		},
	}
	if manifestContainsCredential(project) {
		t.Fatal("safe typed manifest metadata was classified as a credential")
	}
	project.Homebrew.Service.Environment["SAFE_NAME"] = "ops_1234567890abcdefghijklmnopqrstuvwxyzABCDEF"
	if !manifestContainsCredential(project) {
		t.Fatal("nested service environment credential was not detected")
	}
	project.Homebrew.Service.Environment["SAFE_NAME"] = "safe-value"
	project.Homebrew.TestArgs = append(project.Homebrew.TestArgs, "github_pat_1234567890slicecredential")
	if !manifestContainsCredential(project) {
		t.Fatal("slice credential was not detected")
	}
}

func TestCredentialClassifierUsesBoundedHighConfidenceFamilies(t *testing.T) {
	awsAccessKey := "A" + "KIA" + strings.Repeat("A", 16)
	awsSessionKey := "A" + "SIA" + strings.Repeat("B", 16)
	awsShortKey := "A" + "KIA" + strings.Repeat("C", 15)
	positives := []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz0123456789",
		"sk-svcacct-abcdefghijklmnopqrstuvwxyz0123456789",
		"sk-abcdefghijklmnopqrstuvwxyz0123456789",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat_abcdefghijklmnopqrstuvwxyz0123456789",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		awsAccessKey,
		awsSessionKey,
		"ops_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		"token: sk-proj-abcdefghijklmnopqrstuvwxyz0123456789.",
	}
	for _, value := range positives {
		if !containsCredentialLike(value) {
			t.Errorf("credential family was not detected: %q", value)
		}
	}
	negatives := []string{
		"devops_platform_configuration",
		"ordinary_sk-proj-abcdefghijklmnopqrstuvwxyz0123456789_embedded",
		"prefixgithub_pat_abcdefghijklmnopqrstuvwxyz0123456789suffix",
		"ghp_1234567890short",
		"ops_short_configuration",
		awsShortKey,
		"listen at 127.0.0.1:9801",
	}
	for _, value := range negatives {
		if containsCredentialLike(value) {
			t.Errorf("ordinary metadata was classified as a credential: %q", value)
		}
	}
}

func TestManifestCredentialScanRejectsObviousSecretEnvironmentNames(t *testing.T) {
	secretNames := []string{"API_KEY", "GITHUB_TOKEN", "DEPLOY_PAT", "CLIENT_SECRET", "DATABASE_PASSWORD"}
	for _, name := range secretNames {
		project := manifest.Manifest{Homebrew: manifest.Homebrew{Service: &manifest.Service{Environment: map[string]string{name: "not-a-secret"}}}}
		if !manifestContainsCredential(project) {
			t.Errorf("secret environment key %q was not rejected", name)
		}
	}
	safe := manifest.Manifest{Homebrew: manifest.Homebrew{Service: &manifest.Service{Environment: map[string]string{
		"CLAUDE_RC_PROXY_LISTEN": "127.0.0.1:9801",
		"RUNTIME_MODE":           "service",
	}}}}
	if manifestContainsCredential(safe) {
		t.Fatal("ordinary runtime configuration was rejected")
	}
}

func TestReadLocalFileRejectsSameSizeInPlaceRewriteDuringRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed")
	original := []byte("first-state\n")
	replacement := []byte("other-state\n")
	if len(replacement) != len(original) {
		t.Fatal("test replacement changed size")
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLocalFileWithHook(path, "managed", maximumLocalFile, true, func() {
		if writeErr := os.WriteFile(path, replacement, 0o644); writeErr != nil {
			t.Fatalf("rewrite managed file: %v", writeErr)
		}
	}); err == nil {
		t.Fatal("readLocalFile() accepted a same-size in-place rewrite")
	}
}

func TestManagedConflictsCauseZeroWrites(t *testing.T) {
	tests := map[string]func(*testing.T, string){
		"different managed file": func(t *testing.T, root string) {
			path := filepath.Join(root, ".github", "workflows", "hextap-release.yml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("sentinel\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"symlink target": func(t *testing.T, root string) {
			path := filepath.Join(root, ".github", "workflows", "hextap-release.yml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "README.md"), path); err != nil {
				t.Fatal(err)
			}
		},
		"different managed mode": func(t *testing.T, root string) {
			path := filepath.Join(root, ".github", "workflows", "hextap-release.yml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, workflowBytes("v1.2.3", testToolkitSHA), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"hard-linked target": func(t *testing.T, root string) {
			path := filepath.Join(root, ".github", "workflows", "hextap-release.yml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "sentinel")
			if err := os.WriteFile(source, []byte("sentinel\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(source, path); err != nil {
				t.Fatal(err)
			}
		},
		"symlink parent": func(t *testing.T, root string) {
			external := t.TempDir()
			if err := os.Symlink(external, filepath.Join(root, ".hextap")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, arrange := range tests {
		t.Run(name, func(t *testing.T) {
			project := writeGoProject(t)
			arrange(t, project)
			if _, err := Onboard(validOptions(project)); err == nil {
				t.Fatal("Onboard() unexpectedly succeeded")
			}
			for _, absent := range []string{".hextap.json", "scripts/hextap-build"} {
				if _, err := os.Lstat(filepath.Join(project, filepath.FromSlash(absent))); !os.IsNotExist(err) {
					t.Fatalf("conflicting preflight wrote %s: %v", absent, err)
				}
			}
		})
	}
}

func TestEveryManagedTargetConflictLeavesWholeProjectUnchanged(t *testing.T) {
	paths := []string{
		manifestPath,
		workflowPath,
		tapPath,
		mainRulesetPath,
		tagRulesetPath,
		setupPath,
		defaultAdapterPath,
	}
	classes := []string{"different", "symlink", "hard-link", "mode", "directory", "special-mode"}
	for _, relative := range paths {
		for _, class := range classes {
			if relative == defaultAdapterPath && class == "different" {
				continue // Different executable adapter bytes are the documented custom-adapter exception.
			}
			t.Run(strings.ReplaceAll(relative, "/", "_")+"/"+class, func(t *testing.T) {
				root := writeGoProject(t)
				item := expectedArtifact(t, root, relative)
				target := filepath.Join(root, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				mode := item.mode.Perm()
				switch class {
				case "different":
					if err := os.WriteFile(target, []byte("conflicting sentinel\n"), mode); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(filepath.Join(root, "README.md"), target); err != nil {
						t.Fatal(err)
					}
				case "hard-link":
					source := filepath.Join(root, "hard-link-source")
					if err := os.WriteFile(source, item.data, mode); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(source, target); err != nil {
						t.Fatal(err)
					}
				case "mode":
					wrongMode := os.FileMode(0o600)
					if mode == wrongMode {
						wrongMode = 0o644
					}
					if err := os.WriteFile(target, item.data, wrongMode); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(target, 0o755); err != nil {
						t.Fatal(err)
					}
				case "special-mode":
					if err := os.WriteFile(target, item.data, mode); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(target, mode|os.ModeSetuid); err != nil {
						t.Fatal(err)
					}
					info, err := os.Lstat(target)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode()&os.ModeSetuid == 0 {
						t.Skip("filesystem did not retain setuid mode for test fixture")
					}
				}
				before := snapshotProjectTree(t, root)
				if _, err := Onboard(validOptions(root)); err == nil {
					t.Fatal("Onboard() unexpectedly succeeded")
				}
				after := snapshotProjectTree(t, root)
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("project changed after %s conflict at %s\nbefore=%v\nafter=%v", class, relative, before, after)
				}
			})
		}
	}
}

func TestEveryManagedParentConflictLeavesProjectAndExternalTreeUnchanged(t *testing.T) {
	parents := []string{".github", ".github/workflows", ".hextap", ".hextap/rulesets", "scripts"}
	for _, relative := range parents {
		for _, class := range []string{"symlink", "regular-file"} {
			t.Run(strings.ReplaceAll(relative, "/", "_")+"/"+class, func(t *testing.T) {
				root := writeGoProject(t)
				external := t.TempDir()
				parent := filepath.Join(root, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
					t.Fatal(err)
				}
				if class == "symlink" {
					if err := os.Symlink(external, parent); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(parent, []byte("unsafe parent\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				before := snapshotProjectTree(t, root)
				externalBefore := snapshotProjectTree(t, external)
				if _, err := Onboard(validOptions(root)); err == nil {
					t.Fatal("Onboard() unexpectedly succeeded")
				}
				if after := snapshotProjectTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("project changed after unsafe parent conflict\nbefore=%v\nafter=%v", before, after)
				}
				if after := snapshotProjectTree(t, external); !reflect.DeepEqual(after, externalBefore) {
					t.Fatalf("external tree changed after unsafe parent conflict\nbefore=%v\nafter=%v", externalBefore, after)
				}
			})
		}
	}
}

func TestApplyFailureRemovesOnlyInvocationOwnedFiles(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, "z-parent")); err != nil {
		t.Fatal(err)
	}
	artifacts := []artifact{
		{path: "a-created", data: []byte("owned\n"), mode: 0o644},
		{path: "z-parent/blocked", data: []byte("blocked\n"), mode: 0o644},
	}
	entries := []Entry{{Action: ActionCreate, Path: "a-created"}, {Action: ActionCreate, Path: "z-parent/blocked"}}
	if err := applyArtifacts(root, artifacts, entries); err == nil {
		t.Fatal("applyArtifacts() unexpectedly succeeded")
	}
	if _, err := os.Lstat(filepath.Join(root, "a-created")); !os.IsNotExist(err) {
		t.Fatalf("owned earlier file survived rollback: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(root, "z-parent")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("competing symlink was changed: %v, %v", info, err)
	}
	entriesOutside, err := os.ReadDir(external)
	if err != nil || len(entriesOutside) != 0 {
		t.Fatalf("unsafe external directory was changed: %v, %v", entriesOutside, err)
	}
}

func TestArtifactsContainNoCredentialValues(t *testing.T) {
	project := writeGoProject(t)
	options := validOptions(project)
	options.Description = "github_pat_1234567890secretcredential"
	if _, err := Onboard(options); err == nil || strings.Contains(err.Error(), options.Description) {
		t.Fatalf("credential input error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(project, ".hextap.json")); !os.IsNotExist(statErr) {
		t.Fatalf("credential rejection wrote a manifest: %v", statErr)
	}
}

func TestWholeGeneratedTreeAndPlanContainOnlyAllowedSecretNameAndNoCredentialValue(t *testing.T) {
	root := writeGoProject(t)
	const credential = "ops_1234567890suppliedcredential"
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", credential)
	result, err := Onboard(validOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	secretNamePattern := regexp.MustCompile(`[A-Z][A-Z0-9_]*(?:SECRET|TOKEN)[A-Z0-9_]*`)
	allowed := map[string]bool{"OP_SERVICE_ACCOUNT_TOKEN": true}
	for _, entry := range result.Entries {
		planLine := fmt.Sprintf("%s %s", entry.Action, entry.Path)
		if strings.Contains(planLine, credential) {
			t.Fatalf("plan leaked supplied credential: %q", planLine)
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(credential)) {
			return fmt.Errorf("%s contains supplied credential", relative)
		}
		for _, name := range secretNamePattern.FindAllString(string(data), -1) {
			if !allowed[name] {
				return fmt.Errorf("%s contains unapproved secret name %q", relative, name)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNarrowOnboardingInputContracts(t *testing.T) {
	for _, origin := range []string{
		"https://github.com/SijanC147/example-tool.git",
		"git@github.com:SijanC147/example-tool.git",
		"ssh://git@github.com/SijanC147/example-tool.git",
	} {
		if repository, err := parseGitHubOrigin(origin); err != nil || repository != "SijanC147/example-tool" {
			t.Errorf("parseGitHubOrigin(%q) = %q, %v", origin, repository, err)
		}
	}
	for _, origin := range []string{
		"https://token@github.com/SijanC147/example-tool.git",
		"https://github.com.evil/SijanC147/example-tool.git",
		"git@example.com:SijanC147/example-tool.git",
		"https://github.com/SijanC147/example-tool.git/extra",
	} {
		if _, err := parseGitHubOrigin(origin); err == nil {
			t.Errorf("parseGitHubOrigin(%q) unexpectedly succeeded", origin)
		}
	}
	for _, value := range []string{".", "./cmd/tool", "github.com/SijanC147/tool/cmd/tool"} {
		if err := validateGoPackage(value); err != nil {
			t.Errorf("validateGoPackage(%q) = %v", value, err)
		}
	}
	for _, value := range []string{"-ldflags=evil", "../tool", "./cmd/../tool", "./cmd/tool;id", ""} {
		if err := validateGoPackage(value); err == nil {
			t.Errorf("validateGoPackage(%q) unexpectedly succeeded", value)
		}
	}
	for _, value := range []string{"main.version", "github.com/SijanC147/tool/internal/build.commit"} {
		if err := validateLinkerSymbol(value); err != nil {
			t.Errorf("validateLinkerSymbol(%q) = %v", value, err)
		}
	}
	for _, value := range []string{"main", "main.version value", "main.version;id", "../main.version", "main.9version"} {
		if err := validateLinkerSymbol(value); err == nil {
			t.Errorf("validateLinkerSymbol(%q) unexpectedly succeeded", value)
		}
	}
	for _, input := range [][2]string{{"v1.2.3", testToolkitSHA}, {"1.2.3", testToolkitSHA}, {"v1.2.3-rc.1", testToolkitSHA}, {"v1.2.3", strings.ToUpper(testToolkitSHA)}, {"v1.2.3", "0123456"}} {
		err := validateToolkitPin(input[0], input[1])
		wantValid := input[0] == "v1.2.3" && input[1] == testToolkitSHA
		if wantValid && err != nil || !wantValid && err == nil {
			t.Errorf("validateToolkitPin(%q, %q) = %v", input[0], input[1], err)
		}
	}
	checks, err := validateRequiredChecks([]string{"test (linux)", "lint/check", "test (linux)"})
	if err != nil || strings.Join(checks, ",") != "lint/check,test (linux)" {
		t.Fatalf("validateRequiredChecks() = %v, %v", checks, err)
	}
	for _, values := range [][]string{nil, {" leading"}, {"test;id"}, {"line\nbreak"}} {
		if _, err := validateRequiredChecks(values); err == nil {
			t.Errorf("validateRequiredChecks(%q) unexpectedly succeeded", values)
		}
	}
}

func validOptions(project string) Options {
	return Options{
		Project:        project,
		Description:    "A deterministic example CLI",
		License:        "MIT",
		GoPackage:      ".",
		VersionSymbol:  "main.version",
		CommitSymbol:   "main.commit",
		ToolkitVersion: "v1.2.3",
		ToolkitSHA:     testToolkitSHA,
		RequiredChecks: []string{"test", "lint", "test"},
		Linux:          true,
	}
}

func snapshotProjectTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			return filepath.SkipDir
		}
		if relative == "." {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		description := fmt.Sprintf("%v:%o:%d", info.Mode().Type(), info.Mode(), info.Size())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			description += ":link=" + target
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			description += fmt.Sprintf(":sha256=%x", sha256.Sum256(data))
		}
		result[filepath.ToSlash(relative)] = description
		return nil
	}); err != nil {
		t.Fatalf("snapshot project: %v", err)
	}
	return result
}

func expectedArtifact(t *testing.T, root, relative string) artifact {
	t.Helper()
	state, err := prepareOnboarding(validOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range state.artifacts {
		if item.path == relative {
			return item
		}
	}
	t.Fatalf("artifact %s not found", relative)
	return artifact{}
}

func TestOnboardFreshProjectValidateAndRerun(t *testing.T) {
	project := writeGoProject(t)
	result, err := Onboard(validOptions(project))
	if err != nil {
		t.Fatalf("Onboard() error = %v", err)
	}
	if len(result.Entries) != 7 {
		t.Fatalf("Onboard() entries = %#v", result.Entries)
	}
	for _, entry := range result.Entries {
		if entry.Action != ActionCreate {
			t.Fatalf("first action for %s = %s, want CREATE", entry.Path, entry.Action)
		}
		info, statErr := os.Lstat(filepath.Join(project, filepath.FromSlash(entry.Path)))
		if statErr != nil {
			t.Fatalf("Lstat(%s): %v", entry.Path, statErr)
		}
		wantMode := os.FileMode(0o644)
		if entry.Path == "scripts/hextap-build" {
			wantMode = 0o755
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("mode for %s = %o, want %o", entry.Path, info.Mode().Perm(), wantMode)
		}
	}
	validated, err := Validate(ValidateOptions{Project: project})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if validated.Manifest.RepositorySlug() != "SijanC147/example-tool" {
		t.Fatalf("repository = %q", validated.Manifest.RepositorySlug())
	}

	rerun, err := Onboard(validOptions(project))
	if err != nil {
		t.Fatalf("second Onboard() error = %v", err)
	}
	for _, entry := range rerun.Entries {
		if entry.Action != ActionUnchanged {
			t.Fatalf("rerun action for %s = %s", entry.Path, entry.Action)
		}
	}
	workflow, err := os.ReadFile(filepath.Join(project, ".github", "workflows", "hextap-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "@"+testToolkitSHA+" # v1.2.3") || strings.Contains(string(workflow), "@main") {
		t.Fatalf("workflow pin is unsafe:\n%s", workflow)
	}
	if strings.Count(string(workflow), "OP_SERVICE_ACCOUNT_TOKEN") != 1 || strings.Contains(string(workflow), "secrets: inherit") {
		t.Fatalf("workflow secret mapping is unsafe:\n%s", workflow)
	}
	manifestData, err := os.ReadFile(filepath.Join(project, ".hextap.json"))
	if err != nil {
		t.Fatal(err)
	}
	tapData, err := os.ReadFile(filepath.Join(project, ".hextap", "tap-registration.json"))
	if err != nil || !bytes.Equal(tapData, manifestData) {
		t.Fatalf("tap payload is not the exact manifest: %v", err)
	}
	mainData, err := os.ReadFile(filepath.Join(project, ".hextap", "rulesets", "main.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mainBody map[string]any
	if err := json.Unmarshal(mainData, &mainBody); err != nil {
		t.Fatal(err)
	}
	if mainBody["name"] != "hextap/main" || mainBody["target"] != "branch" || mainBody["enforcement"] != "active" || !bytes.Contains(mainData, []byte(`"strict_required_status_checks_policy": true`)) {
		t.Fatalf("main ruleset invariants are missing:\n%s", mainData)
	}
	lintOffset := bytes.Index(mainData, []byte(`"context": "lint"`))
	testOffset := bytes.Index(mainData, []byte(`"context": "test"`))
	if lintOffset < 0 || testOffset <= lintOffset || bytes.Count(mainData, []byte(`"context": "test"`)) != 1 {
		t.Fatalf("status checks are not sorted and deduplicated:\n%s", mainData)
	}
	tagData, err := os.ReadFile(filepath.Join(project, ".hextap", "rulesets", "release-tags.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(tagData, []byte(`"type": "deletion"`)) || !bytes.Contains(tagData, []byte(`"type": "update"`)) || bytes.Contains(tagData, []byte(`"type": "creation"`)) {
		t.Fatalf("release tag rules are unsafe:\n%s", tagData)
	}
	setup, err := os.ReadFile(filepath.Join(project, ".hextap", "SETUP.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"gh secret set OP_SERVICE_ACCOUNT_TOKEN --repo github.com/SijanC147/example-tool",
		"gh api --hostname github.com --method POST repos/SijanC147/example-tool/rulesets",
		"Projects/example-tool.json", "Formula/example-tool.rb",
		"class ExampleTool < Formula", "homebrew-only", "did not inspect or mutate any remote",
	} {
		if !bytes.Contains(setup, []byte(required)) {
			t.Fatalf("SETUP.md lacks %q:\n%s", required, setup)
		}
	}
}

func TestOnboardDryRunWritesNothing(t *testing.T) {
	project := writeGoProject(t)
	options := validOptions(project)
	options.DryRun = true
	result, err := Onboard(options)
	if err != nil {
		t.Fatalf("Onboard(dry-run) error = %v", err)
	}
	for _, entry := range result.Entries {
		if entry.Action != ActionCreate {
			t.Fatalf("dry-run action = %#v", entry)
		}
		if _, statErr := os.Lstat(filepath.Join(project, filepath.FromSlash(entry.Path))); !os.IsNotExist(statErr) {
			t.Fatalf("dry-run wrote %s: %v", entry.Path, statErr)
		}
	}
}
