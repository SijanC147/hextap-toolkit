package workflow

// This file implements a deliberately small YAML reader. It accepts only the
// block subset that GitHub Actions workflows are written in and returns an
// error for every construct outside that subset, because a wrong guess about
// workflow structure is exactly the failure this package exists to prevent.
// Callers must treat a parse error as a refusal to certify the file, never as
// an absence of triggers.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxDocumentBytes  = 1 << 20
	maxDocumentLines  = 20000
	maxNestingDepth   = 40
	maxMappingEntries = 512
	maxSequenceItems  = 512
	maxFlowDepth      = 8
	maxKeyLength      = 256
)

type nodeKind int

const (
	nodeNull nodeKind = iota
	nodeScalar
	nodeSequence
	nodeMapping
)

func (kind nodeKind) String() string {
	switch kind {
	case nodeNull:
		return "empty value"
	case nodeScalar:
		return "scalar"
	case nodeSequence:
		return "sequence"
	case nodeMapping:
		return "mapping"
	default:
		return "unrecognised node"
	}
}

type scalarStyle int

const (
	scalarPlain scalarStyle = iota
	scalarSingleQuoted
	scalarDoubleQuoted
	scalarBlock
)

// node is one parsed YAML value. Mapping keys are retained in document order so
// diagnostics can quote the workflow back in the order its author wrote it.
type node struct {
	kind   nodeKind
	style  scalarStyle
	value  string
	items  []*node
	keys   []string
	values map[string]*node
	line   int
}

func (parsed *node) child(key string) *node {
	if parsed == nil || parsed.kind != nodeMapping {
		return nil
	}
	return parsed.values[key]
}

func (parsed *node) has(key string) bool {
	if parsed == nil || parsed.kind != nodeMapping {
		return false
	}
	_, present := parsed.values[key]
	return present
}

// isEmpty reports whether the node carries no value, which YAML writes either
// as a bare key or as an explicit null.
func (parsed *node) isEmpty() bool {
	if parsed == nil || parsed.kind == nodeNull {
		return true
	}
	if parsed.kind != nodeScalar || parsed.style != scalarPlain {
		return false
	}
	switch parsed.value {
	case "", "~", "null", "Null", "NULL":
		return true
	default:
		return false
	}
}

type documentParser struct {
	lines []string
	index int
}

// parseWorkflowDocument parses source as a single YAML document restricted to
// the accepted block subset.
func parseWorkflowDocument(source string) (*node, error) {
	if len(source) > maxDocumentBytes {
		return nil, fmt.Errorf("workflow document exceeds %d bytes", maxDocumentBytes)
	}
	if !utf8.ValidString(source) {
		return nil, errors.New("workflow document is not valid UTF-8")
	}
	source = strings.TrimPrefix(source, "\ufeff")
	source = strings.ReplaceAll(source, "\r\n", "\n")
	if strings.ContainsRune(source, '\r') {
		return nil, errors.New("workflow document contains a bare carriage return")
	}
	lines := strings.Split(source, "\n")
	if len(lines) > maxDocumentLines {
		return nil, fmt.Errorf("workflow document exceeds %d lines", maxDocumentLines)
	}
	for offset, line := range lines {
		if index := strings.IndexFunc(line, func(candidate rune) bool {
			return candidate < 0x20 && candidate != '\t'
		}); index >= 0 {
			return nil, fmt.Errorf("line %d contains a control character", offset+1)
		}
	}

	parser := &documentParser{lines: lines}
	if err := parser.consumeDocumentHeader(); err != nil {
		return nil, err
	}
	root, err := parser.parseNode(0, 0)
	if err != nil {
		return nil, err
	}
	if err := parser.expectEndOfDocument(); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("workflow document is empty")
	}
	return root, nil
}

// consumeDocumentHeader accepts at most one leading document start marker and
// rejects YAML directives outright.
func (parser *documentParser) consumeDocumentHeader() error {
	line, _, present, err := parser.significantLine()
	if err != nil || !present {
		return err
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "%") {
		return fmt.Errorf("line %d uses a YAML directive", parser.index+1)
	}
	if trimmed == "---" {
		parser.index++
	}
	return nil
}

// expectEndOfDocument refuses any content after the single accepted document,
// which is how multi-document streams are rejected.
func (parser *documentParser) expectEndOfDocument() error {
	_, _, present, err := parser.significantLine()
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("line %d continues past the end of a single workflow document", parser.index+1)
	}
	return nil
}

// significantLine advances past blank and comment-only lines and returns the
// next structural line together with its indentation, without consuming it.
func (parser *documentParser) significantLine() (string, int, bool, error) {
	for parser.index < len(parser.lines) {
		line := parser.lines[parser.index]
		unindented := strings.TrimLeft(line, " ")
		if unindented == "" || strings.HasPrefix(unindented, "#") {
			parser.index++
			continue
		}
		if strings.ContainsRune(line, '\t') {
			return "", 0, false, fmt.Errorf("line %d contains a tab outside a block scalar", parser.index+1)
		}
		return line, len(line) - len(unindented), true, nil
	}
	return "", 0, false, nil
}

// parseNode parses the block node whose entries begin at the next structural
// line, provided that line is indented at least minimumIndent. It returns a nil
// node when the next structural line belongs to an outer level.
func (parser *documentParser) parseNode(minimumIndent, depth int) (*node, error) {
	if depth > maxNestingDepth {
		return nil, fmt.Errorf("workflow document nests deeper than %d levels", maxNestingDepth)
	}
	line, indent, present, err := parser.significantLine()
	if err != nil || !present {
		return nil, err
	}
	if indent < minimumIndent {
		return nil, nil
	}
	content := line[indent:]
	if isDocumentMarker(content) {
		return nil, fmt.Errorf("line %d carries a YAML document marker; only a single document is accepted", parser.index+1)
	}
	if isSequenceEntry(content) {
		return parser.parseSequence(indent, depth)
	}
	number := parser.index + 1
	stripped, err := stripComment(content, number)
	if err != nil {
		return nil, err
	}
	_, _, isEntry, err := splitMappingKey(stripped, number)
	if err != nil {
		return nil, err
	}
	if isEntry {
		return parser.parseMapping(indent, depth)
	}
	return parser.parseScalarLine(indent, stripped, number)
}

// isDocumentMarker reports whether a structural line is a YAML document
// boundary. Only one document is accepted, so encountering a marker after the
// optional leading one is a refusal rather than the start of a second parse.
func isDocumentMarker(content string) bool {
	return content == "---" || content == "..." ||
		strings.HasPrefix(content, "--- ") || strings.HasPrefix(content, "... ")
}

func isSequenceEntry(content string) bool {
	return content == "-" || strings.HasPrefix(content, "- ")
}

func (parser *documentParser) parseScalarLine(indent int, stripped string, number int) (*node, error) {
	value, err := parseInlineValue(stripped, number)
	if err != nil {
		return nil, err
	}
	value.line = number
	parser.index++
	return value, parser.rejectContinuation(indent)
}

// rejectContinuation refuses a following line indented deeper than the value it
// would continue. Multi-line plain scalars are outside the accepted subset
// because their folding rules make quiet misreadings easy.
func (parser *documentParser) rejectContinuation(indent int) error {
	_, next, present, err := parser.significantLine()
	if err != nil {
		return err
	}
	if present && next > indent {
		return fmt.Errorf("line %d continues a scalar across lines", parser.index+1)
	}
	return nil
}

func (parser *documentParser) parseSequence(indent, depth int) (*node, error) {
	result := &node{kind: nodeSequence, line: parser.index + 1}
	for {
		line, lineIndent, present, err := parser.significantLine()
		if err != nil {
			return nil, err
		}
		if !present || lineIndent < indent {
			return result, nil
		}
		if lineIndent > indent {
			return nil, fmt.Errorf("line %d is indented deeper than its sequence", parser.index+1)
		}
		content := line[indent:]
		if isDocumentMarker(content) {
			return nil, fmt.Errorf("line %d carries a YAML document marker; only a single document is accepted", parser.index+1)
		}
		if !isSequenceEntry(content) {
			return nil, fmt.Errorf("line %d mixes a mapping key into a sequence", parser.index+1)
		}
		if len(result.items) >= maxSequenceItems {
			return nil, fmt.Errorf("sequence at line %d exceeds %d items", result.line, maxSequenceItems)
		}
		item, err := parser.parseSequenceItem(indent, content, depth)
		if err != nil {
			return nil, err
		}
		result.items = append(result.items, item)
	}
}

// parseSequenceItem parses one entry of a block sequence. An entry whose body
// starts on the dash line is reparsed from the column that body occupies, which
// is what makes the common step shape written as "- name: ..." readable.
func (parser *documentParser) parseSequenceItem(indent int, content string, depth int) (*node, error) {
	number := parser.index + 1
	afterDash := content[1:]
	body := strings.TrimLeft(afterDash, " ")
	stripped, err := stripComment(body, number)
	if err != nil {
		return nil, err
	}
	if stripped == "" {
		parser.index++
		item, err := parser.parseNode(indent+1, depth+1)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &node{kind: nodeNull, line: number}, nil
		}
		return item, nil
	}
	bodyColumn := indent + 1 + (len(afterDash) - len(body))
	if bodyColumn <= indent+1 {
		return nil, fmt.Errorf("line %d has no separator after its sequence dash", number)
	}
	parser.lines[parser.index] = strings.Repeat(" ", bodyColumn) + body
	item, err := parser.parseNode(bodyColumn, depth+1)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("line %d has an unreadable sequence item", number)
	}
	return item, nil
}

func (parser *documentParser) parseMapping(indent, depth int) (*node, error) {
	result := &node{kind: nodeMapping, line: parser.index + 1, values: make(map[string]*node)}
	for {
		line, lineIndent, present, err := parser.significantLine()
		if err != nil {
			return nil, err
		}
		if !present || lineIndent < indent {
			return result, nil
		}
		if lineIndent > indent {
			return nil, fmt.Errorf("line %d is indented deeper than its mapping", parser.index+1)
		}
		content := line[indent:]
		if isDocumentMarker(content) {
			return nil, fmt.Errorf("line %d carries a YAML document marker; only a single document is accepted", parser.index+1)
		}
		if isSequenceEntry(content) {
			return nil, fmt.Errorf("line %d mixes a sequence entry into a mapping", parser.index+1)
		}
		number := parser.index + 1
		stripped, err := stripComment(content, number)
		if err != nil {
			return nil, err
		}
		key, rest, isEntry, err := splitMappingKey(stripped, number)
		if err != nil {
			return nil, err
		}
		if !isEntry {
			return nil, fmt.Errorf("line %d is not a mapping entry", number)
		}
		if _, duplicate := result.values[key]; duplicate {
			return nil, fmt.Errorf("line %d duplicates mapping key %q", number, key)
		}
		if len(result.keys) >= maxMappingEntries {
			return nil, fmt.Errorf("mapping at line %d exceeds %d entries", result.line, maxMappingEntries)
		}
		value, err := parser.parseMappingValue(indent, rest, depth)
		if err != nil {
			return nil, err
		}
		result.keys = append(result.keys, key)
		result.values[key] = value
	}
}

func (parser *documentParser) parseMappingValue(indent int, rest string, depth int) (*node, error) {
	number := parser.index + 1
	rest = strings.TrimSpace(rest)
	if rest == "" {
		parser.index++
		value, err := parser.parseNode(indent+1, depth+1)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return &node{kind: nodeNull, line: number}, nil
		}
		return value, nil
	}
	if rest[0] == '|' || rest[0] == '>' {
		return parser.parseBlockScalar(indent, rest, number)
	}
	value, err := parseInlineValue(rest, number)
	if err != nil {
		return nil, err
	}
	value.line = number
	parser.index++
	return value, parser.rejectContinuation(indent)
}

// parseBlockScalar consumes a literal or folded block scalar. Its content is
// captured verbatim and is never interpreted: the security-relevant property is
// only that the content cannot be mistaken for document structure. Policy code
// refuses to read a block scalar as a value rather than depend on folding.
func (parser *documentParser) parseBlockScalar(indent int, header string, number int) (*node, error) {
	indicators := header[1:]
	seenChomping := false
	seenIndent := false
	for len(indicators) > 0 {
		character := indicators[0]
		switch {
		case character == '+' || character == '-':
			if seenChomping {
				return nil, fmt.Errorf("line %d repeats a block scalar chomping indicator", number)
			}
			seenChomping = true
		case character >= '1' && character <= '9':
			if seenIndent {
				return nil, fmt.Errorf("line %d repeats a block scalar indentation indicator", number)
			}
			seenIndent = true
		case character == ' ':
			if strings.TrimSpace(indicators) != "" {
				return nil, fmt.Errorf("line %d has unreadable text after a block scalar header", number)
			}
			indicators = ""
			continue
		default:
			return nil, fmt.Errorf("line %d has an unreadable block scalar header", number)
		}
		indicators = indicators[1:]
	}

	parser.index++
	var content []string
	for parser.index < len(parser.lines) {
		line := parser.lines[parser.index]
		unindented := strings.TrimLeft(line, " ")
		if unindented == "" {
			content = append(content, "")
			parser.index++
			continue
		}
		if len(line)-len(unindented) <= indent {
			break
		}
		content = append(content, line)
		parser.index++
	}
	return &node{
		kind:  nodeScalar,
		style: scalarBlock,
		value: strings.Join(content, "\n"),
		line:  number,
	}, nil
}

// stripComment removes a trailing YAML comment from a structural line. A hash
// starts a comment only at the start of the line or after a space, and never
// inside a quoted scalar - the distinction a substring search cannot make.
func stripComment(content string, number int) (string, error) {
	for index := 0; index < len(content); {
		character := content[index]
		switch {
		case (character == '\'' || character == '"') && isQuoteOpener(content, index):
			end, err := scanQuoted(content, index, number)
			if err != nil {
				return "", err
			}
			index = end
		case character == '#' && (index == 0 || content[index-1] == ' '):
			return strings.TrimRight(content[:index], " "), nil
		default:
			index++
		}
	}
	return strings.TrimRight(content, " "), nil
}

// isQuoteOpener reports whether the quote at index begins a quoted scalar
// rather than sitting inside a plain one, as an apostrophe does.
func isQuoteOpener(content string, index int) bool {
	if index == 0 {
		return true
	}
	switch content[index-1] {
	case ' ', '[', '{', ',':
		return true
	default:
		return false
	}
}

// splitMappingKey splits a comment-free structural line into a mapping key and
// the remainder after its colon. The third result is false when the line is not
// a mapping entry at all.
func splitMappingKey(content string, number int) (string, string, bool, error) {
	if content == "" {
		return "", "", false, nil
	}
	if content[0] == '\'' || content[0] == '"' {
		end, err := scanQuoted(content, 0, number)
		if err != nil {
			return "", "", false, err
		}
		if end >= len(content) || content[end] != ':' {
			return "", "", false, nil
		}
		if end+1 < len(content) && content[end+1] != ' ' {
			return "", "", false, fmt.Errorf("line %d needs a space after its mapping colon", number)
		}
		key, err := decodeQuoted(content[:end], number)
		if err != nil {
			return "", "", false, err
		}
		if err := validateKeyLength(key, number); err != nil {
			return "", "", false, err
		}
		return key, strings.TrimLeft(content[end+1:], " "), true, nil
	}
	for index := 0; index < len(content); index++ {
		if content[index] != ':' {
			continue
		}
		if index+1 < len(content) && content[index+1] != ' ' {
			continue
		}
		key := strings.TrimRight(content[:index], " ")
		if err := validatePlainKey(key, number); err != nil {
			return "", "", false, err
		}
		return key, strings.TrimLeft(content[index+1:], " "), true, nil
	}
	return "", "", false, nil
}

func validateKeyLength(key string, number int) error {
	if key == "" {
		return fmt.Errorf("line %d has an empty mapping key", number)
	}
	if len(key) > maxKeyLength {
		return fmt.Errorf("line %d has a mapping key longer than %d bytes", number, maxKeyLength)
	}
	return nil
}

func validatePlainKey(key string, number int) error {
	if err := validateKeyLength(key, number); err != nil {
		return err
	}
	if strings.HasPrefix(key, "<<") {
		return fmt.Errorf("line %d uses a YAML merge key", number)
	}
	switch key[0] {
	case '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '%', '@', '`', '\'', '"':
		return fmt.Errorf("line %d has an unreadable mapping key", number)
	}
	if strings.ContainsAny(key, "#\"'") {
		return fmt.Errorf("line %d has an unreadable mapping key", number)
	}
	return nil
}

func parseInlineValue(text string, number int) (*node, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return &node{kind: nodeNull, line: number}, nil
	}
	switch text[0] {
	case '[', '{':
		return parseFlow(text, number)
	case '&', '*', '!':
		return nil, fmt.Errorf("line %d uses a YAML anchor, alias or tag", number)
	case '\'', '"':
		end, err := scanQuoted(text, 0, number)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text[end:]) != "" {
			return nil, fmt.Errorf("line %d has unreadable text after a quoted value", number)
		}
		value, err := decodeQuoted(text[:end], number)
		if err != nil {
			return nil, err
		}
		style := scalarSingleQuoted
		if text[0] == '"' {
			style = scalarDoubleQuoted
		}
		return &node{kind: nodeScalar, style: style, value: value, line: number}, nil
	}
	value, err := plainScalar(text, number)
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeScalar, style: scalarPlain, value: value, line: number}, nil
}

// plainScalar validates an unquoted value. A colon followed by a space would
// make the text a nested mapping to a real YAML reader, so it is refused rather
// than silently absorbed.
func plainScalar(text string, number int) (string, error) {
	switch text[0] {
	case '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '%', '@', '`':
		return "", fmt.Errorf("line %d starts a plain value with a YAML indicator", number)
	}
	if strings.Contains(text, ": ") || strings.HasSuffix(text, ":") {
		return "", fmt.Errorf("line %d has an unreadable plain value", number)
	}
	return text, nil
}

func parseFlow(text string, number int) (*node, error) {
	value, end, err := scanFlowNode(text, 0, number, 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text[end:]) != "" {
		return nil, fmt.Errorf("line %d has unreadable text after a flow collection", number)
	}
	return value, nil
}

// scanFlowNode reads one flow node starting at index and returns the index just
// past it. Flow collections must be complete on their own line; a collection
// spanning lines is refused.
func scanFlowNode(text string, index, number, depth int) (*node, int, error) {
	if depth > maxFlowDepth {
		return nil, 0, fmt.Errorf("line %d nests flow collections deeper than %d levels", number, maxFlowDepth)
	}
	index = skipFlowSpaces(text, index)
	if index >= len(text) {
		return nil, 0, fmt.Errorf("line %d has an unterminated flow collection", number)
	}
	switch text[index] {
	case '[':
		return scanFlowSequence(text, index, number, depth)
	case '{':
		return scanFlowMapping(text, index, number, depth)
	default:
		return scanFlowScalar(text, index, number)
	}
}

func scanFlowSequence(text string, index, number, depth int) (*node, int, error) {
	result := &node{kind: nodeSequence, line: number}
	index = skipFlowSpaces(text, index+1)
	if index < len(text) && text[index] == ']' {
		return result, index + 1, nil
	}
	for {
		item, next, err := scanFlowNode(text, index, number, depth+1)
		if err != nil {
			return nil, 0, err
		}
		if len(result.items) >= maxSequenceItems {
			return nil, 0, fmt.Errorf("line %d has a flow sequence exceeding %d items", number, maxSequenceItems)
		}
		result.items = append(result.items, item)
		index = skipFlowSpaces(text, next)
		if index >= len(text) {
			return nil, 0, fmt.Errorf("line %d has an unterminated flow sequence", number)
		}
		switch text[index] {
		case ']':
			return result, index + 1, nil
		case ',':
			index = skipFlowSpaces(text, index+1)
			if index < len(text) && text[index] == ']' {
				return nil, 0, fmt.Errorf("line %d has a trailing comma in a flow sequence", number)
			}
		default:
			return nil, 0, fmt.Errorf("line %d has an unreadable flow sequence", number)
		}
	}
}

func scanFlowMapping(text string, index, number, depth int) (*node, int, error) {
	result := &node{kind: nodeMapping, line: number, values: make(map[string]*node)}
	index = skipFlowSpaces(text, index+1)
	if index < len(text) && text[index] == '}' {
		return result, index + 1, nil
	}
	for {
		key, next, err := scanFlowScalar(text, skipFlowSpaces(text, index), number)
		if err != nil {
			return nil, 0, err
		}
		index = skipFlowSpaces(text, next)
		if index+1 >= len(text) || text[index] != ':' || text[index+1] != ' ' {
			return nil, 0, fmt.Errorf("line %d needs a space after a flow mapping colon", number)
		}
		if _, duplicate := result.values[key.value]; duplicate {
			return nil, 0, fmt.Errorf("line %d duplicates flow mapping key %q", number, key.value)
		}
		if len(result.keys) >= maxMappingEntries {
			return nil, 0, fmt.Errorf("line %d has a flow mapping exceeding %d entries", number, maxMappingEntries)
		}
		value, next, err := scanFlowNode(text, index+1, number, depth+1)
		if err != nil {
			return nil, 0, err
		}
		result.keys = append(result.keys, key.value)
		result.values[key.value] = value
		index = skipFlowSpaces(text, next)
		if index >= len(text) {
			return nil, 0, fmt.Errorf("line %d has an unterminated flow mapping", number)
		}
		switch text[index] {
		case '}':
			return result, index + 1, nil
		case ',':
			index = skipFlowSpaces(text, index+1)
			if index < len(text) && text[index] == '}' {
				return nil, 0, fmt.Errorf("line %d has a trailing comma in a flow mapping", number)
			}
		default:
			return nil, 0, fmt.Errorf("line %d has an unreadable flow mapping", number)
		}
	}
}

func scanFlowScalar(text string, index, number int) (*node, int, error) {
	if index >= len(text) {
		return nil, 0, fmt.Errorf("line %d has an unterminated flow collection", number)
	}
	if text[index] == '\'' || text[index] == '"' {
		end, err := scanQuoted(text, index, number)
		if err != nil {
			return nil, 0, err
		}
		value, err := decodeQuoted(text[index:end], number)
		if err != nil {
			return nil, 0, err
		}
		style := scalarSingleQuoted
		if text[index] == '"' {
			style = scalarDoubleQuoted
		}
		return &node{kind: nodeScalar, style: style, value: value, line: number}, end, nil
	}
	end := index
	for end < len(text) && !strings.ContainsRune(",[]{}", rune(text[end])) {
		end++
	}
	raw := strings.TrimSpace(text[index:end])
	if raw == "" {
		return nil, 0, fmt.Errorf("line %d has an empty flow entry", number)
	}
	value, err := plainScalar(raw, number)
	if err != nil {
		return nil, 0, err
	}
	return &node{kind: nodeScalar, style: scalarPlain, value: value, line: number}, end, nil
}

func skipFlowSpaces(text string, index int) int {
	for index < len(text) && text[index] == ' ' {
		index++
	}
	return index
}

func scanQuoted(text string, start, number int) (int, error) {
	if text[start] == '\'' {
		return scanSingleQuoted(text, start, number)
	}
	return scanDoubleQuoted(text, start, number)
}

func scanSingleQuoted(text string, start, number int) (int, error) {
	for index := start + 1; index < len(text); index++ {
		if text[index] != '\'' {
			continue
		}
		if index+1 < len(text) && text[index+1] == '\'' {
			index++
			continue
		}
		return index + 1, nil
	}
	return 0, fmt.Errorf("line %d has an unterminated single-quoted value", number)
}

func scanDoubleQuoted(text string, start, number int) (int, error) {
	for index := start + 1; index < len(text); index++ {
		switch text[index] {
		case '\\':
			index++
			if index >= len(text) {
				return 0, fmt.Errorf("line %d has an unterminated escape in a double-quoted value", number)
			}
		case '"':
			return index + 1, nil
		}
	}
	return 0, fmt.Errorf("line %d has an unterminated double-quoted value", number)
}

func decodeQuoted(text string, number int) (string, error) {
	if len(text) < 2 {
		return "", fmt.Errorf("line %d has an unreadable quoted value", number)
	}
	body := text[1 : len(text)-1]
	if text[0] == '\'' {
		return strings.ReplaceAll(body, "''", "'"), nil
	}
	return decodeDoubleQuoted(body, number)
}

var doubleQuotedEscapes = map[byte]string{
	'0':  "\x00",
	'a':  "\a",
	'b':  "\b",
	't':  "\t",
	'n':  "\n",
	'v':  "\v",
	'f':  "\f",
	'r':  "\r",
	'e':  "\x1b",
	' ':  " ",
	'"':  "\"",
	'/':  "/",
	'\\': "\\",
	'N':  "\u0085",
	'_':  "\u00a0",
	'L':  "\u2028",
	'P':  "\u2029",
}

func decodeDoubleQuoted(body string, number int) (string, error) {
	var result strings.Builder
	result.Grow(len(body))
	for index := 0; index < len(body); index++ {
		if body[index] != '\\' {
			result.WriteByte(body[index])
			continue
		}
		index++
		if index >= len(body) {
			return "", fmt.Errorf("line %d has an unterminated escape in a double-quoted value", number)
		}
		if replacement, known := doubleQuotedEscapes[body[index]]; known {
			result.WriteString(replacement)
			continue
		}
		width := 0
		switch body[index] {
		case 'x':
			width = 2
		case 'u':
			width = 4
		case 'U':
			width = 8
		default:
			return "", fmt.Errorf("line %d uses the unsupported escape %q", number, "\\"+string(body[index]))
		}
		if index+width >= len(body) {
			return "", fmt.Errorf("line %d has a truncated numeric escape", number)
		}
		digits := body[index+1 : index+1+width]
		code, err := strconv.ParseUint(digits, 16, 32)
		if err != nil {
			return "", fmt.Errorf("line %d has an unreadable numeric escape %q: %w", number, digits, err)
		}
		if code > utf8.MaxRune {
			return "", fmt.Errorf("line %d escapes a value outside Unicode", number)
		}
		result.WriteRune(rune(code))
		index += width
	}
	return result.String(), nil
}
