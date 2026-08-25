package onboard

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	formulaClassPattern = regexp.MustCompile(`\bclass[ \t]+((?:::)?[A-Z][A-Za-z0-9]*(?:::[A-Z][A-Za-z0-9]*)*)[ \t]*<[ \t]*Formula\b`)
	heredocPattern      = regexp.MustCompile(`<<[-~]?[ \t]*["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)
	rubyBlockPattern    = regexp.MustCompile(`^(?:class|module|def|if|unless|case|begin|while|until|for)\b`)
	rubyDoPattern       = regexp.MustCompile(`\bdo(?:[ \t]*\|[^|]*\|)?[ \t]*$`)
)

type rubyFormulaState struct {
	blockComment bool
	heredoc      string
	quote        byte
	escaped      bool
	depth        int
}

func validateFormulaClass(data []byte, expected string) error {
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return errors.New("tap Formula must be UTF-8 text without NUL bytes")
	}
	if !goIdentifierPattern.MatchString(expected) || expected[0] < 'A' || expected[0] > 'Z' {
		return errors.New("registered Formula class is invalid")
	}
	state := rubyFormulaState{}
	declarations := 0
	wantLine := "class " + expected + " < Formula"
	for lineNumber, line := range strings.Split(string(data), "\n") {
		if state.blockComment {
			if isRubyBlockCommentMarker(line, "=end") {
				state.blockComment = false
			}
			continue
		}
		if state.heredoc != "" {
			if strings.TrimSpace(line) == state.heredoc {
				state.heredoc = ""
			}
			continue
		}
		if state.quote == 0 && isRubyBlockCommentMarker(line, "=begin") {
			state.blockComment = true
			continue
		}
		sanitized := sanitizeRubyFormulaLine(line, &state)
		trimmed := strings.TrimSpace(sanitized)
		if trimmed == "__END__" {
			return fmt.Errorf("tap Formula contains unsupported dead-data marker at line %d", lineNumber+1)
		}
		if declarations == 0 && trimmed != "" && line != wantLine {
			return fmt.Errorf("tap Formula first semantic line must be the canonical class declaration at line %d", lineNumber+1)
		}
		matches := formulaClassPattern.FindAllStringSubmatch(sanitized, -1)
		for _, match := range matches {
			if state.depth != 0 || line != wantLine || match[1] != expected {
				return fmt.Errorf("tap Formula has an alternate, nested, or noncanonical Formula class at line %d", lineNumber+1)
			}
			declarations++
		}
		if trimmed == "end" {
			if state.depth == 0 {
				return fmt.Errorf("tap Formula has unmatched end at line %d", lineNumber+1)
			}
			state.depth--
		} else if rubyBlockPattern.MatchString(trimmed) || rubyDoPattern.MatchString(trimmed) {
			state.depth++
		}
		if match := heredocPattern.FindStringSubmatch(sanitized); len(match) == 2 {
			state.heredoc = match[1]
		}
	}
	if state.blockComment || state.heredoc != "" || state.quote != 0 || state.depth != 0 {
		return errors.New("tap Formula has unterminated structural syntax")
	}
	if declarations != 1 {
		return fmt.Errorf("tap Formula must contain exactly one top-level class %s < Formula declaration", expected)
	}
	return nil
}

func isRubyBlockCommentMarker(line, marker string) bool {
	if !strings.HasPrefix(line, marker) {
		return false
	}
	return len(line) == len(marker) || line[len(marker)] == ' ' || line[len(marker)] == '\t'
}

func sanitizeRubyFormulaLine(line string, state *rubyFormulaState) string {
	result := []byte(line)
	for index := range result {
		result[index] = ' '
	}
	for index := 0; index < len(line); index++ {
		character := line[index]
		if state.quote != 0 {
			if state.escaped {
				state.escaped = false
				continue
			}
			if character == '\\' {
				state.escaped = true
				continue
			}
			if character == state.quote {
				state.quote = 0
			}
			continue
		}
		if character == '#' {
			break
		}
		if character == '\'' || character == '"' {
			state.quote = character
			continue
		}
		result[index] = character
	}
	state.escaped = false
	return string(result)
}
