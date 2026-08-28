package commandmeta

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/skillinstall"
)

var validShortFlag = regexp.MustCompile(`^[A-Za-z0-9]$`)

func TestCommandTreeIsCompleteAndEveryFlagHasOneUniqueShorthand(t *testing.T) {
	root := Root()
	if root.Name != "" {
		t.Fatalf("root name = %q, want empty", root.Name)
	}
	walkCommands(t, root, nil, func(t *testing.T, command Command, path []string) {
		if strings.TrimSpace(command.Summary) == "" || strings.TrimSpace(command.Description) == "" {
			t.Fatalf("command %q lacks a summary or detailed description", strings.Join(path, " "))
		}
		if len(command.Safety) == 0 || len(command.Examples) == 0 {
			t.Fatalf("command %q lacks safety notes or examples", strings.Join(path, " "))
		}
		seenShort := make(map[string]string)
		seenLong := make(map[string]bool)
		for _, option := range command.Options {
			if option.Long == "" || seenLong[option.Long] {
				t.Fatalf("command %q has missing or duplicate long flag %q", strings.Join(path, " "), option.Long)
			}
			seenLong[option.Long] = true
			if !validShortFlag.MatchString(option.Short) {
				t.Fatalf("%s --%s shorthand = %q, want exactly one alphanumeric character", strings.Join(path, " "), option.Long, option.Short)
			}
			if previous := seenShort[option.Short]; previous != "" {
				t.Fatalf("command %q reuses -%s for --%s and --%s", strings.Join(path, " "), option.Short, previous, option.Long)
			}
			seenShort[option.Short] = option.Long
			if strings.TrimSpace(option.Description) == "" {
				t.Fatalf("%s --%s lacks a description", strings.Join(path, " "), option.Long)
			}
			if option.Repeatable && option.ValueName == "" {
				t.Fatalf("%s --%s is repeatable without a value", strings.Join(path, " "), option.Long)
			}
		}
		if !seenLong["help"] {
			t.Fatalf("command %q does not declare --help/-h", strings.Join(path, " "))
		}
	})

	rootSpec, ok := Lookup(nil)
	if !ok || !hasOption(rootSpec, "version", "V") {
		t.Fatal("root does not declare --version/-V")
	}
}

func TestHelpAndZshCompletionTraverseTheSameMetadata(t *testing.T) {
	completion := Zsh()
	unquotedCompletion := strings.ReplaceAll(completion, `'\''`, `'`)
	for _, required := range []string{
		"#compdef hextap brew-hextap",
		"local context state state_descr line",
		"typeset -A opt_args",
		"_arguments -C",
		"->command",
		"_describe",
		"_values",
	} {
		if !strings.Contains(completion, required) {
			t.Errorf("Zsh completion lacks %q", required)
		}
	}

	walkCommands(t, Root(), nil, func(t *testing.T, command Command, path []string) {
		help := Help("hextap", path)
		for _, section := range []string{"usage: ", "Purpose:\n", "Arguments:\n", "Options:\n", "Safety:\n", "Examples:\n"} {
			if !strings.Contains(help, section) {
				t.Errorf("help for %q lacks %q", strings.Join(path, " "), section)
			}
		}
		if !strings.Contains(help, command.Summary) || !strings.Contains(help, command.Description) {
			t.Errorf("help for %q is not derived from its summary and description", strings.Join(path, " "))
		}
		for _, option := range command.Options {
			if !strings.Contains(help, "-"+option.Short+", --"+option.Long) || !strings.Contains(help, option.Description) {
				t.Errorf("help for %q lacks complete --%s/- %s metadata", strings.Join(path, " "), option.Long, option.Short)
			}
			if !strings.Contains(completion, "-"+option.Short) || !strings.Contains(completion, "--"+option.Long) {
				t.Errorf("completion lacks %q option --%s/-%s", strings.Join(path, " "), option.Long, option.Short)
			}
			if !strings.Contains(unquotedCompletion, zshDescription(option.Description)) {
				t.Errorf("completion lacks --%s description derived from metadata", option.Long)
			}
		}
		for _, child := range command.Children {
			if !strings.Contains(help, child.Name) || !strings.Contains(help, child.Summary) {
				t.Errorf("help for %q lacks child %q", strings.Join(path, " "), child.Name)
			}
			if !strings.Contains(completion, child.Name) || !strings.Contains(completion, zshDescription(child.Summary)) {
				t.Errorf("completion lacks child %q metadata", child.Name)
			}
		}
	})
}

func TestCheckedInZshCompletionMatchesAuthoritativeMetadata(t *testing.T) {
	path := filepath.Join("..", "..", "completions", "_hextap")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in completion: %v", err)
	}
	if string(data) != Zsh() {
		t.Fatal("completions/_hextap drifted from command metadata; regenerate it with `go run ./cmd/brew-hextap completion zsh`")
	}
}

func TestAgentCompletionValuesMatchTheInstallerRegistry(t *testing.T) {
	command, ok := Lookup([]string{"skills", "install"})
	if !ok {
		t.Fatal("skills install metadata is missing")
	}
	var got []string
	for _, option := range command.Options {
		if option.Long == "agent" {
			for _, choice := range option.Choices {
				got = append(got, choice.Name)
			}
		}
	}
	var want []string
	for _, target := range skillinstall.Targets() {
		want = append(want, target.ID)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("agent completion values = %v, installer targets = %v", got, want)
	}
}

func walkCommands(t *testing.T, command Command, path []string, visit func(*testing.T, Command, []string)) {
	t.Helper()
	visit(t, command, path)
	for _, child := range command.Children {
		childPath := append(append([]string(nil), path...), child.Name)
		walkCommands(t, child, childPath, visit)
	}
}

func hasOption(command Command, long, short string) bool {
	for _, option := range command.Options {
		if option.Long == long && option.Short == short {
			return true
		}
	}
	return false
}
