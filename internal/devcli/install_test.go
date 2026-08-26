package devcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSelectsOwningHomebrewAndUpgradesOnlyHextap(t *testing.T) {
	project := createToolkitFixture(t)
	root := t.TempDir()
	armBrew := filepath.Join(root, "opt", "homebrew", "bin", "brew")
	intelBrew := filepath.Join(root, "usr", "local", "bin", "brew")
	prefix := filepath.Join(root, "opt", "homebrew", "opt", "hextap")
	installed := filepath.Join(prefix, "bin", "brew-hextap")
	for _, path := range []string{armBrew, intelBrew, installed} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	commit := strings.Repeat("b", 40)
	runner := &scriptedRunner{}
	runner.handler = func(command Command) (Result, error) {
		switch commandKey(command) {
		case "git -C " + project + " remote get-url origin":
			return Result{Stdout: ToolkitOriginHTTPS + "\n"}, nil
		case "git -C " + project + " ls-remote --tags origin refs/tags/v0.3.0 refs/tags/v0.3.0^{}":
			return Result{Stdout: strings.Repeat("e", 40) + "\trefs/tags/v0.3.0\n" + commit + "\trefs/tags/v0.3.0^{}\n"}, nil
		case "gh release view v0.3.0 --repo " + ToolkitRepository + " --json tagName,isDraft,isPrerelease,isImmutable,assets,url":
			return Result{Stdout: validReleaseViewJSON("v0.3.0")}, nil
		case "gh release verify v0.3.0 --repo " + ToolkitRepository:
			return Result{}, nil
		case intelBrew + " --prefix sean/hextap/hextap":
			return Result{}, os.ErrNotExist
		case armBrew + " --prefix sean/hextap/hextap":
			return Result{Stdout: prefix + "\n"}, nil
		case armBrew + " update":
			return Result{}, nil
		case armBrew + " info sean/hextap/hextap --json=v2":
			return Result{Stdout: `{"formulae":[{"versions":{"stable":"0.3.0"}}]}` + "\n"}, nil
		case armBrew + " upgrade sean/hextap/hextap":
			if command.Env["HOMEBREW_NO_AUTO_UPDATE"] != "1" {
				t.Fatal("upgrade did not disable automatic update")
			}
			return Result{}, nil
		case installed + " --version":
			return Result{Stdout: "brew-hextap 0.3.0 (commit " + commit + ")\n"}, nil
		case armBrew + " test sean/hextap/hextap":
			return Result{}, nil
		default:
			return Result{}, os.ErrNotExist
		}
	}
	service := Service{Runner: runner, BrewCandidates: []string{intelBrew, armBrew}, InstalledBinary: installed}
	result, err := service.Install(context.Background(), InstallOptions{Project: project, Tag: "v0.3.0", ExpectedCommit: commit, Execute: true})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.Brew != armBrew || result.Binary != installed || result.Version != "0.3.0" || result.Commit != commit {
		t.Fatalf("Install() = %#v", result)
	}
	for _, command := range runner.commands {
		key := commandKey(command)
		if strings.Contains(key, "services") || strings.Contains(key, "claude-rc-proxy") || strings.Contains(key, "better-ccflare") {
			t.Fatalf("Install() touched forbidden command %q", key)
		}
	}
}

func validReleaseViewJSON(tag string) string {
	return `{"tagName":"` + tag + `","isDraft":false,"isPrerelease":false,"isImmutable":true,"url":"https://github.example/release/` + tag + `","assets":[` +
		`{"name":"SHA256SUMS","state":"uploaded","digest":"sha256:` + strings.Repeat("1", 64) + `"},` +
		`{"name":"hextap-darwin-amd64.tar.gz","state":"uploaded","digest":"sha256:` + strings.Repeat("2", 64) + `"},` +
		`{"name":"hextap-darwin-arm64.tar.gz","state":"uploaded","digest":"sha256:` + strings.Repeat("3", 64) + `"},` +
		`{"name":"hextap-linux-amd64.tar.gz","state":"uploaded","digest":"sha256:` + strings.Repeat("4", 64) + `"},` +
		`{"name":"hextap-linux-arm64.tar.gz","state":"uploaded","digest":"sha256:` + strings.Repeat("5", 64) + `"}]}` + "\n"
}

func TestInstallRequiresExplicitExecutionAndExactReleasedIdentity(t *testing.T) {
	service := Service{Runner: &scriptedRunner{handler: func(command Command) (Result, error) {
		return Result{}, os.ErrNotExist
	}}}
	for _, options := range []InstallOptions{
		{Tag: "v0.3.0", ExpectedCommit: strings.Repeat("b", 40)},
		{Tag: "v0.3.0-rc.1", ExpectedCommit: strings.Repeat("b", 40), Execute: true},
		{Tag: "v0.3.0", ExpectedCommit: "short", Execute: true},
	} {
		if _, err := service.Install(context.Background(), options); err == nil {
			t.Errorf("Install(%#v) unexpectedly succeeded", options)
		}
	}
}
