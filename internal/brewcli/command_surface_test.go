package brewcli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/commandmeta"
)

func TestNamedInvocationGlobalHelpVersionAndCompletion(t *testing.T) {
	const version = "1.2.3"
	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, invocation := range []string{"brew-hextap", "hextap"} {
		for _, versionFlag := range []string{"--version", "-V"} {
			code, stdout, stderr := executeNamed(invocation, version, commit, versionFlag)
			want := invocation + " " + version + " (commit " + commit + ")\n"
			if code != 0 || stdout != want || stderr != "" {
				t.Errorf("%s %s = %d, %q, %q; want %q", invocation, versionFlag, code, stdout, stderr, want)
			}
		}
		for _, helpFlag := range []string{"--help", "-h"} {
			code, stdout, stderr := executeNamed(invocation, version, commit, helpFlag)
			if code != 0 || stderr != "" || stdout != commandmeta.Help(invocation, nil) {
				t.Errorf("%s %s = %d, %q, %q", invocation, helpFlag, code, stdout, stderr)
			}
		}
	}

	code, stdout, stderr := executeNamed("hextap", version, commit, "completion", "zsh")
	if code != 0 || stderr != "" || stdout != commandmeta.Zsh() {
		t.Fatalf("hextap completion zsh = %d, stderr %q; output matches metadata = %t", code, stderr, stdout == commandmeta.Zsh())
	}
}

func TestEveryCommandHelpAliasIsCompleteAndNeverExecutesWork(t *testing.T) {
	paths := commandPaths(commandmeta.Root(), nil)
	for _, path := range paths {
		if len(path) == 0 {
			continue
		}
		for _, helpFlag := range []string{"--help", "-h"} {
			args := append([]string(nil), path...)
			command, _ := commandmeta.Lookup(path)
			if len(command.Children) == 0 {
				args = append(args, "--project", "/path/that/must/not/be-read")
			}
			args = append(args, helpFlag)
			code, stdout, stderr := executeNamed("hextap", "dev", "unknown", args...)
			want := commandmeta.Help("hextap", path)
			if code != 0 || stderr != "" || stdout != want {
				t.Errorf("hextap %s %s = %d, stdout prefix %q, stderr %q", strings.Join(path, " "), helpFlag, code, firstLine(stdout), stderr)
			}
		}
	}
}

func executeNamed(invocation, version, commit string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := RunNamed(invocation, args, &stdout, &stderr, version, commit)
	return code, stdout.String(), stderr.String()
}

func commandPaths(command commandmeta.Command, prefix []string) [][]string {
	result := [][]string{append([]string(nil), prefix...)}
	for _, child := range command.Children {
		path := append(append([]string(nil), prefix...), child.Name)
		result = append(result, commandPaths(child, path)...)
	}
	return result
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}
