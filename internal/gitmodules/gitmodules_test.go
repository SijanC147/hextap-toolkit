package gitmodules_test

import (
	"strings"
	"testing"

	"github.com/SijanC147/hextap-toolkit/internal/gitmodules"
)

// The form every adopter actually writes, taken from claude-peers-suite.
func TestTheOrdinaryFormIsReadInOrder(t *testing.T) {
	declared, err := gitmodules.Parse([]byte("[submodule \"components/claude-peers\"]\n" +
		"\tpath = components/claude-peers\n" +
		"\turl = https://github.com/SijanC147/claude-peers-mcp.git\n" +
		"[submodule \"components/worktree-peers\"]\n" +
		"\tpath = components/worktree-peers\n" +
		"\turl = https://github.com/SijanC147/worktree-peers.git\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(declared) != 2 {
		t.Fatalf("read %d submodules, want 2: %+v", len(declared), declared)
	}
	if declared[0].Name != "components/claude-peers" ||
		declared[0].Path != "components/claude-peers" ||
		declared[0].URL != "https://github.com/SijanC147/claude-peers-mcp.git" {
		t.Errorf("first submodule read as %+v", declared[0])
	}
	if declared[1].URL != "https://github.com/SijanC147/worktree-peers.git" {
		t.Errorf("second submodule read as %+v", declared[1])
	}
}

// Everything below is the same question asked once per grammar feature: does a
// URL git would fetch survive this reader? A feature the reader does not know
// is a submodule that the allowlist check never sees, and the credential is
// presented to it anyway. So each of these must either be read correctly or
// refused, and never dropped in silence.
func TestGitGrammarThisReaderMustNotDropOnTheFloor(t *testing.T) {
	const target = "https://github.com/SijanC147/evil.git"
	for _, test := range []struct {
		name    string
		content string
	}{
		{
			// Git matches section and variable names case-insensitively, so
			// this is a real submodule with a real url.
			name:    "uppercase section and variable names",
			content: "[SUBMODULE \"x\"]\n\tPATH = x\n\tURL = " + target + "\n",
		},
		{
			// A section header and its variables on one line is valid git
			// config. A line-oriented reader that expects one key per line
			// reads no url here at all.
			name:    "section and variables on one line",
			content: "[submodule \"x\"] path = x\n url = " + target + "\n",
		},
		{
			// The dotted subsection form. Git accepts it and so must this.
			name:    "dotted subsection form",
			content: "[submodule.x]\n\tpath = x\n\turl = " + target + "\n",
		},
		{
			// A quoted value. The quotes are syntax, not part of the URL, so
			// a reader that keeps them compares a string no allowlist holds
			// and would fail an honest file rather than catch a hostile one.
			name:    "quoted value",
			content: "[submodule \"x\"]\n\tpath = x\n\turl = \"" + target + "\"\n",
		},
		{
			// A line continuation splits the URL across two physical lines.
			name:    "line continuation inside the url",
			content: "[submodule \"x\"]\n\tpath = x\n\turl = https://github.com/SijanC147/\\\nevil.git\n",
		},
		{
			// No spaces around '='.
			name:    "no spaces around the equals sign",
			content: "[submodule \"x\"]\n\tpath=x\n\turl=" + target + "\n",
		},
		{
			// A trailing comment is not part of the value.
			name:    "trailing comment after the url",
			content: "[submodule \"x\"]\n\tpath = x\n\turl = " + target + " # components\n",
		},
		{
			// Windows line endings.
			name:    "CRLF line endings",
			content: "[submodule \"x\"]\r\n\tpath = x\r\n\turl = " + target + "\r\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			declared, err := gitmodules.Parse([]byte(test.content))
			if err != nil {
				// Refusing is safe: the release fails and nothing is cloned.
				t.Logf("refused, which is the safe direction: %v", err)
				return
			}
			if len(declared) != 1 {
				t.Fatalf("read %d submodules, want 1; git reads one url here and a reader that reads none hands the credential to an unsealed repository: %+v", len(declared), declared)
			}
			if declared[0].URL != target {
				t.Fatalf("read url %q, want %q; the comparison against the sealed list is exact, so a mangled value fails an honest file and, worse, would not equal the hostile one either", declared[0].URL, target)
			}
		})
	}
}

// A file this reader cannot read in full must fail the release, never parse to
// a shorter list. Each of these is a way to hide a declaration from a lenient
// parser.
func TestUnreadableContentIsRefusedRatherThanShortened(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{"unterminated section header", "[submodule \"x\"\n\turl = https://example.com/x.git\n"},
		{"unterminated quoted subsection", "[submodule \"x\n\turl = https://example.com/x.git\n"},
		{"unterminated quoted value", "[submodule \"x\"]\n\tpath = x\n\turl = \"https://example.com/x.git\n"},
		{"variable outside any section", "url = https://example.com/x.git\n[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/y.git\n"},
		{"variable name starting with a digit", "[submodule \"x\"]\n\t1url = https://example.com/x.git\n"},
		{"invalid escape in a value", "[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/\\qx.git\n"},
		{"value ending in a bare backslash", "[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/x.git\\"},
		{"submodule declaring no url", "[submodule \"x\"]\n\tpath = x\n"},
		{"submodule declaring no path", "[submodule \"x\"]\n\turl = https://example.com/x.git\n"},
		{"empty url", "[submodule \"x\"]\n\tpath = x\n\turl =\n"},
		{"NUL byte", "[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/x.git\x00\n"},
		{"invalid UTF-8", "[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/\xff.git\n"},
		{"junk before a section", "!!!\n[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/x.git\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			declared, err := gitmodules.Parse([]byte(test.content))
			if err == nil {
				t.Fatalf("read %+v without error; content this reader cannot account for must fail the release, because a declaration it silently skipped is a repository the credential reaches and nothing sealed", declared)
			}
		})
	}
}

// Git takes the last of two url lines in one section. Choosing silently means
// the sealed list is compared against one value while git fetches another, so
// this reader refuses instead of picking.
func TestARepeatedKeyIsRefusedRatherThanResolved(t *testing.T) {
	content := "[submodule \"x\"]\n\tpath = x\n\turl = https://github.com/SijanC147/sealed.git\n\turl = https://github.com/SijanC147/unsealed.git\n"
	declared, err := gitmodules.Parse([]byte(content))
	if err == nil {
		t.Fatalf("read %+v without error; git would fetch the second url and a reader that reports the first checks a value nothing fetches", declared)
	}
	if !strings.Contains(err.Error(), "url twice") {
		t.Errorf("error %q does not say which key repeated", err)
	}
}

// The same section name reopened later is one submodule in git, and its keys
// merge. Reading it as two entries, or reading only the first, both give the
// wrong set of URLs.
func TestAReopenedSectionIsOneSubmodule(t *testing.T) {
	content := "[submodule \"x\"]\n\tpath = x\n[other]\n\tkey = value\n[submodule \"x\"]\n\turl = https://github.com/SijanC147/x.git\n"
	declared, err := gitmodules.Parse([]byte(content))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(declared) != 1 {
		t.Fatalf("read %d submodules, want 1: %+v", len(declared), declared)
	}
	if declared[0].URL != "https://github.com/SijanC147/x.git" || declared[0].Path != "x" {
		t.Fatalf("keys did not merge across the reopened section: %+v", declared[0])
	}
}

// A section that is not a named submodule declares nothing to fetch, but its
// contents must still parse, so a syntax error cannot be hidden inside one.
func TestSectionsThatAreNotSubmodulesDeclareNothing(t *testing.T) {
	declared, err := gitmodules.Parse([]byte("[core]\n\tfilemode = true\n[submodule]\n\turl = https://example.com/ignored.git\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(declared) != 0 {
		t.Fatalf("read %+v, want none: a bare [submodule] section has no name and git fetches nothing from it", declared)
	}
}

func TestAnEmptyFileDeclaresNoSubmodules(t *testing.T) {
	for _, content := range []string{"", "\n", "# only a comment\n", "; also a comment\n"} {
		declared, err := gitmodules.Parse([]byte(content))
		if err != nil {
			t.Fatalf("parse %q: %v", content, err)
		}
		if len(declared) != 0 {
			t.Fatalf("parse %q read %+v, want none", content, declared)
		}
	}
}

// A comment character inside a quoted value is part of the value, and a
// reader that truncates there compares a prefix against the sealed list.
func TestACommentCharacterInsideQuotesIsPartOfTheValue(t *testing.T) {
	declared, err := gitmodules.Parse([]byte("[submodule \"x\"]\n\tpath = x\n\turl = \"https://example.com/x.git#frag\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(declared) != 1 || declared[0].URL != "https://example.com/x.git#frag" {
		t.Fatalf("read %+v, want the whole quoted value", declared)
	}
}
