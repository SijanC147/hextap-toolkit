// Package gitmodules reads the submodule declarations in a .gitmodules file.
//
// This is the toolkit's only reader of that file. Two checks need it and they
// must agree: the sealed-allowlist check in the release workflow's validate
// job, which decides the set of repositories the submodule credential is
// presented to, and the same-host check that rejects a submodule the
// credential cannot authenticate at all. A second parser is a second answer to
// "what does this tag declare", and the whole point of the allowlist is that
// there is one.
//
// .gitmodules is a git config file, and git's parser accepts considerably more
// than the three-line form everyone writes by hand: one-line sections, dotted
// subsections, quoted values, escapes, line continuations and comments. This
// parser accepts that grammar and REFUSES anything outside it. Refusing is the
// safe direction: a declaration this reader silently skipped would be a
// repository the credential reaches and nothing sealed, which is the defect
// the allowlist exists to close. So every error here fails the release rather
// than producing a shorter list.
package gitmodules

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Submodule is one submodule declared in .gitmodules.
type Submodule struct {
	// Name is the section subsection, which git uses as the key for the
	// submodule's own config and which need not equal Path.
	Name string
	// Path is the work tree location the submodule is checked out at.
	Path string
	// URL is the declared clone URL, exactly as written after unquoting.
	// This is the value the credential would be presented to.
	URL string
}

// Parse reads the submodule declarations from .gitmodules content, in the
// order they appear.
//
// Every syntax error is returned rather than skipped, and a submodule section
// that declares no url is an error too: a file this reader cannot read in full
// must not be summarised as a short list of URLs.
func Parse(data []byte) ([]Submodule, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("gitmodules: contains a NUL byte")
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("gitmodules: is not valid UTF-8")
	}

	p := &parser{src: string(data), line: 1}
	sections := make(map[string]*Submodule)
	var order []*Submodule
	var current *Submodule
	inSubmoduleSection := false
	sawSection := false

	for {
		p.skipBlanksAndComments()
		if p.eof() {
			break
		}
		if p.peek() == '[' {
			section, subsection, named, err := p.parseSectionHeader()
			if err != nil {
				return nil, err
			}
			sawSection = true
			// Git matches section names case-insensitively.
			if !strings.EqualFold(section, "submodule") || !named {
				// A section that is not a named submodule declares no
				// submodule. Its keys are still parsed below so a syntax
				// error inside it is not skipped.
				inSubmoduleSection = false
				current = nil
				continue
			}
			inSubmoduleSection = true
			existing, seen := sections[subsection]
			if seen {
				current = existing
				continue
			}
			entry := &Submodule{Name: subsection}
			sections[subsection] = entry
			order = append(order, entry)
			current = entry
			continue
		}

		keyLine := p.line
		if !sawSection {
			// Git refuses a variable that precedes every section header, so
			// a file carrying one is not a file git would read. Accepting it
			// here would mean reading a .gitmodules git itself rejects.
			return nil, fmt.Errorf("gitmodules: line %d: a variable appears before any section header", keyLine)
		}
		key, value, err := p.parseKeyValue()
		if err != nil {
			return nil, err
		}
		if !inSubmoduleSection || current == nil {
			continue
		}
		// Git matches variable names case-insensitively.
		switch {
		case strings.EqualFold(key, "url"):
			if current.URL != "" {
				return nil, fmt.Errorf("gitmodules: line %d: submodule %q declares url twice; git would take the last one and this reader will not choose for you", keyLine, current.Name)
			}
			if value == "" {
				return nil, fmt.Errorf("gitmodules: line %d: submodule %q declares an empty url", keyLine, current.Name)
			}
			current.URL = value
		case strings.EqualFold(key, "path"):
			if current.Path != "" {
				return nil, fmt.Errorf("gitmodules: line %d: submodule %q declares path twice; git would take the last one and this reader will not choose for you", keyLine, current.Name)
			}
			if value == "" {
				return nil, fmt.Errorf("gitmodules: line %d: submodule %q declares an empty path", keyLine, current.Name)
			}
			current.Path = value
		}
	}

	result := make([]Submodule, 0, len(order))
	for _, entry := range order {
		if entry.URL == "" {
			return nil, fmt.Errorf("gitmodules: submodule %q declares no url", entry.Name)
		}
		if entry.Path == "" {
			return nil, fmt.Errorf("gitmodules: submodule %q declares no path", entry.Name)
		}
		result = append(result, *entry)
	}
	return result, nil
}

type parser struct {
	src  string
	pos  int
	line int
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) next() byte {
	c := p.src[p.pos]
	p.pos++
	if c == '\n' {
		p.line++
	}
	return c
}

// skipBlanksAndComments advances past whitespace, newlines and comment lines.
func (p *parser) skipBlanksAndComments() {
	for !p.eof() {
		c := p.peek()
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.next()
		case c == '#' || c == ';':
			for !p.eof() && p.peek() != '\n' {
				p.next()
			}
		default:
			return
		}
	}
}

// parseSectionHeader reads a [section] or [section "subsection"] or
// [section.subsection] header. named reports whether a subsection was given,
// which for a submodule is its name.
func (p *parser) parseSectionHeader() (section, subsection string, named bool, err error) {
	startLine := p.line
	p.next() // consume '['

	var name strings.Builder
	for {
		if p.eof() {
			return "", "", false, fmt.Errorf("gitmodules: line %d: section header is not closed", startLine)
		}
		c := p.peek()
		if c == ']' || c == ' ' || c == '\t' || c == '.' {
			break
		}
		if c == '\n' {
			return "", "", false, fmt.Errorf("gitmodules: line %d: section header is not closed", startLine)
		}
		// Git allows alphanumeric, '-' and '.' in a section name.
		if !isAlphanumeric(c) && c != '-' {
			return "", "", false, fmt.Errorf("gitmodules: line %d: invalid character %q in a section name", startLine, string(c))
		}
		name.WriteByte(p.next())
	}
	if name.Len() == 0 {
		return "", "", false, fmt.Errorf("gitmodules: line %d: empty section name", startLine)
	}

	switch p.peek() {
	case '.':
		// Dotted form: [submodule.name]. The subsection is the rest, taken
		// literally, with no escapes and no quoting.
		p.next()
		var sub strings.Builder
		for {
			if p.eof() {
				return "", "", false, fmt.Errorf("gitmodules: line %d: section header is not closed", startLine)
			}
			c := p.peek()
			if c == ']' {
				break
			}
			if c == '\n' {
				return "", "", false, fmt.Errorf("gitmodules: line %d: section header is not closed", startLine)
			}
			sub.WriteByte(p.next())
		}
		p.next() // consume ']'
		if sub.Len() == 0 {
			return "", "", false, fmt.Errorf("gitmodules: line %d: empty subsection name", startLine)
		}
		p.skipHeaderPadding()
		return name.String(), sub.String(), true, nil
	case ' ', '\t':
		for p.peek() == ' ' || p.peek() == '\t' {
			p.next()
		}
		if p.peek() != '"' {
			return "", "", false, fmt.Errorf("gitmodules: line %d: a subsection name must be quoted", startLine)
		}
		sub, err := p.parseQuotedSubsection(startLine)
		if err != nil {
			return "", "", false, err
		}
		for p.peek() == ' ' || p.peek() == '\t' {
			p.next()
		}
		if p.eof() || p.peek() != ']' {
			return "", "", false, fmt.Errorf("gitmodules: line %d: section header is not closed", startLine)
		}
		p.next()
		p.skipHeaderPadding()
		return name.String(), sub, true, nil
	case ']':
		p.next()
		p.skipHeaderPadding()
		return name.String(), "", false, nil
	default:
		return "", "", false, fmt.Errorf("gitmodules: line %d: invalid character %q in a section header", startLine, string(p.peek()))
	}
}

// parseQuotedSubsection reads "name", honouring git's two escapes. Git accepts
// only \" and \\ inside a subsection name and rejects any other backslash.
func (p *parser) parseQuotedSubsection(startLine int) (string, error) {
	p.next() // consume '"'
	var sub strings.Builder
	for {
		if p.eof() {
			return "", fmt.Errorf("gitmodules: line %d: subsection name is not closed", startLine)
		}
		c := p.next()
		switch c {
		case '"':
			return sub.String(), nil
		case '\n':
			return "", fmt.Errorf("gitmodules: line %d: subsection name is not closed", startLine)
		case '\\':
			if p.eof() {
				return "", fmt.Errorf("gitmodules: line %d: subsection name is not closed", startLine)
			}
			escaped := p.next()
			if escaped != '"' && escaped != '\\' {
				return "", fmt.Errorf("gitmodules: line %d: invalid escape %q in a subsection name", startLine, `\`+string(escaped))
			}
			sub.WriteByte(escaped)
		default:
			sub.WriteByte(c)
		}
	}
}

// skipHeaderPadding consumes the whitespace after a section header. Git
// allows a variable to follow a header on the same line, so nothing else is
// consumed here: the main loop reads whatever comes next.
func (p *parser) skipHeaderPadding() {
	for p.peek() == ' ' || p.peek() == '\t' || p.peek() == '\r' {
		p.next()
	}
}

// parseKeyValue reads one `key = value` pair. A key with no '=' is a boolean
// true in git, and the value is returned empty; no key this reader cares about
// is a boolean, so such a line is simply carried through.
func (p *parser) parseKeyValue() (key, value string, err error) {
	startLine := p.line
	var name strings.Builder
	if !isLetter(p.peek()) {
		return "", "", fmt.Errorf("gitmodules: line %d: a variable name must start with a letter, found %q", startLine, string(p.peek()))
	}
	for !p.eof() {
		c := p.peek()
		if !isAlphanumeric(c) && c != '-' {
			break
		}
		name.WriteByte(p.next())
	}

	for p.peek() == ' ' || p.peek() == '\t' {
		p.next()
	}

	switch {
	case p.eof(), p.peek() == '\n', p.peek() == '\r':
		return name.String(), "", nil
	case p.peek() == '#' || p.peek() == ';':
		for !p.eof() && p.peek() != '\n' {
			p.next()
		}
		return name.String(), "", nil
	case p.peek() != '=':
		return "", "", fmt.Errorf("gitmodules: line %d: expected '=' after the variable name %q, found %q", startLine, name.String(), string(p.peek()))
	}
	p.next() // consume '='

	val, err := p.parseValue(startLine)
	if err != nil {
		return "", "", err
	}
	return name.String(), val, nil
}

// parseValue reads a git config value: leading and trailing whitespace are
// stripped unless quoted, a backslash at end of line continues onto the next,
// '#' and ';' outside quotes begin a comment, and \n \t \b \\ \" are escapes.
func (p *parser) parseValue(startLine int) (string, error) {
	for p.peek() == ' ' || p.peek() == '\t' {
		p.next()
	}

	var value strings.Builder
	// trailingSpace holds whitespace seen outside quotes, appended only if
	// more non-space content follows, so unquoted values lose their tail.
	var trailingSpace strings.Builder
	quoted := false

	for {
		if p.eof() {
			if quoted {
				return "", fmt.Errorf("gitmodules: line %d: value is not closed; a quote is unterminated", startLine)
			}
			return value.String(), nil
		}
		c := p.peek()
		switch {
		case c == '\n':
			if quoted {
				return "", fmt.Errorf("gitmodules: line %d: value is not closed; a quote is unterminated", startLine)
			}
			return value.String(), nil
		case c == '\r':
			p.next()
		case c == '"':
			p.next()
			quoted = !quoted
			value.WriteString(trailingSpace.String())
			trailingSpace.Reset()
		case !quoted && (c == '#' || c == ';'):
			for !p.eof() && p.peek() != '\n' {
				p.next()
			}
			return value.String(), nil
		case !quoted && (c == ' ' || c == '\t'):
			trailingSpace.WriteByte(p.next())
		case c == '\\':
			p.next()
			if p.eof() {
				return "", fmt.Errorf("gitmodules: line %d: value ends with a trailing backslash", startLine)
			}
			escaped := p.next()
			if escaped == '\n' {
				// Line continuation. Whitespace held before it is kept,
				// matching git, which only strips at the very end.
				value.WriteString(trailingSpace.String())
				trailingSpace.Reset()
				continue
			}
			value.WriteString(trailingSpace.String())
			trailingSpace.Reset()
			switch escaped {
			case 'n':
				value.WriteByte('\n')
			case 't':
				value.WriteByte('\t')
			case 'b':
				value.WriteByte('\b')
			case '\\':
				value.WriteByte('\\')
			case '"':
				value.WriteByte('"')
			default:
				return "", fmt.Errorf("gitmodules: line %d: invalid escape %q in a value", startLine, `\`+string(escaped))
			}
		default:
			value.WriteString(trailingSpace.String())
			trailingSpace.Reset()
			value.WriteByte(p.next())
		}
	}
}

func isLetter(c byte) bool {
	return unicode.IsLetter(rune(c)) && c < utf8.RuneSelf
}

func isAlphanumeric(c byte) bool {
	return c < utf8.RuneSelf && (unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)))
}
