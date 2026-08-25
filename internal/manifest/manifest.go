// Package manifest defines and validates the versioned Hextap project manifest.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	CurrentSchema         = 1
	maxPathComponentBytes = 255
	maxRelativePathBytes  = 1024
	maxFormulaNameBytes   = maxPathComponentBytes - len("-darwin-arm64.tar.gz")
)

var (
	formulaNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z][a-z0-9]*)*$`)
	classNamePattern    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	repositoryPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	ownerPattern        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	fileNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	pathPartPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	relativePathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*(?:/[A-Za-z0-9][A-Za-z0-9._+-]*)*$`)
	environmentPattern  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	argumentPattern     = regexp.MustCompile(`^[-A-Za-z0-9_./:=+,%@]+$`)
	stableVersionRE     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	placeholderRE       = regexp.MustCompile(`\{\{[^{}]*\}\}`)
)

// Manifest is the schema=1 declarative contract shared by release and tap tooling.
type Manifest struct {
	Schema   int      `json:"schema"`
	Formula  Formula  `json:"formula"`
	Release  Release  `json:"release"`
	Homebrew Homebrew `json:"homebrew"`
}

type Formula struct {
	Name        string     `json:"name"`
	Class       string     `json:"class"`
	Description string     `json:"description"`
	Homepage    string     `json:"homepage"`
	License     string     `json:"license"`
	Repository  Repository `json:"repository"`
	Binary      string     `json:"binary"`
	Assets      Assets     `json:"assets"`
}

type Repository struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type Assets struct {
	DarwinARM64 string `json:"darwin_arm64"`
	DarwinAMD64 string `json:"darwin_amd64"`
}

type Release struct {
	BuildScript string `json:"build_script"`
	Linux       bool   `json:"linux"`
}

type Homebrew struct {
	MacOSOnly bool     `json:"macos_only"`
	TestArgs  []string `json:"test_args"`
	Service   *Service `json:"service,omitempty"`
	Caveats   string   `json:"caveats"`
}

type Service struct {
	Enabled      bool              `json:"enabled"`
	RunArgs      []string          `json:"run_args,omitempty"`
	KeepAlive    KeepAlive         `json:"keep_alive"`
	RestartDelay int               `json:"restart_delay"`
	Environment  map[string]string `json:"environment,omitempty"`
	LogPath      string            `json:"log_path"`
	ErrorLogPath string            `json:"error_log_path"`
}

type KeepAlive struct {
	SuccessfulExit *bool `json:"successful_exit,omitempty"`
	Crashed        *bool `json:"crashed,omitempty"`
}

// Parse decodes exactly one manifest, rejects unknown fields, requires the
// documented schema keys, and validates all values before returning.
func Parse(data []byte) (Manifest, error) {
	if !utf8.Valid(data) {
		return Manifest{}, errors.New("decode manifest: input is not valid UTF-8")
	}
	if err := rejectDuplicateObjectKeys(data); err != nil {
		return Manifest{}, err
	}
	if err := validateRequiredFields(data); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var result Manifest
	if err := decoder.Decode(&result); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return Manifest{}, err
	}
	if err := result.Validate(); err != nil {
		return Manifest{}, err
	}
	return result, nil
}

func rejectDuplicateObjectKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := inspectJSONValue(decoder); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	return nil
}

func inspectJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		if value, ok := token.(string); ok && strings.ContainsRune(value, utf8.RuneError) {
			return errors.New("string contains a replacement character or invalid surrogate")
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			if strings.ContainsRune(key, utf8.RuneError) {
				return errors.New("object key contains a replacement character or invalid surrogate")
			}
			seen[key] = struct{}{}
			if err := inspectJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object has invalid closing delimiter")
		}
	case '[':
		for decoder.More() {
			if err := inspectJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array has invalid closing delimiter")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("decode manifest: multiple JSON values are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode manifest: trailing data: %w", err)
	}
	return nil
}

func validateRequiredFields(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if err := validateExactObjectFields(root, "root", []string{"schema", "formula", "release", "homebrew"}, nil); err != nil {
		return err
	}
	formula, err := requireExactObjectFields(root["formula"], "formula", []string{"name", "class", "description", "homepage", "license", "repository", "binary", "assets"}, nil)
	if err != nil {
		return err
	}
	if _, err := requireExactObjectFields(root["release"], "release", []string{"build_script", "linux"}, nil); err != nil {
		return err
	}
	homebrew, err := requireExactObjectFields(root["homebrew"], "homebrew", []string{"macos_only", "test_args", "caveats"}, []string{"service"})
	if err != nil {
		return err
	}
	if _, err := requireExactObjectFields(formula["repository"], "formula.repository", []string{"owner", "name"}, nil); err != nil {
		return err
	}
	if _, err := requireExactObjectFields(formula["assets"], "formula.assets", []string{"darwin_arm64", "darwin_amd64"}, nil); err != nil {
		return err
	}
	if serviceJSON, ok := homebrew["service"]; ok && !bytes.Equal(bytes.TrimSpace(serviceJSON), []byte("null")) {
		service, err := requireExactObjectFields(serviceJSON, "homebrew.service", []string{"enabled"}, []string{"run_args", "keep_alive", "restart_delay", "environment", "log_path", "error_log_path"})
		if err != nil {
			return err
		}
		var enabled bool
		if err := json.Unmarshal(service["enabled"], &enabled); err != nil {
			return fmt.Errorf("validate manifest: homebrew.service.enabled must be a boolean")
		}
		if enabled {
			for _, field := range []string{"run_args", "keep_alive", "restart_delay", "environment", "log_path", "error_log_path"} {
				if _, ok := service[field]; !ok {
					return fmt.Errorf("validate manifest: required field %q is missing", "homebrew.service."+field)
				}
			}
			if _, err := requireExactObjectFields(service["keep_alive"], "homebrew.service.keep_alive", nil, []string{"successful_exit", "crashed"}); err != nil {
				return err
			}
		} else if len(service) != 1 {
			return errors.New("validate manifest: a disabled homebrew.service may contain only enabled=false")
		}
	}
	return nil
}

func requireExactObjectFields(data json.RawMessage, object string, required, optional []string) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	if len(data) == 0 || json.Unmarshal(data, &values) != nil || values == nil {
		return nil, fmt.Errorf("validate manifest: %s must be an object", object)
	}
	if err := validateExactObjectFields(values, object, required, optional); err != nil {
		return nil, err
	}
	return values, nil
}

func validateExactObjectFields(values map[string]json.RawMessage, object string, required, optional []string) error {
	allowed := make(map[string]struct{}, len(required)+len(optional))
	for _, field := range append(append([]string(nil), required...), optional...) {
		allowed[field] = struct{}{}
	}
	for field := range values {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("validate manifest: %s contains an unknown field or mis-cased field", object)
		}
	}
	for _, field := range required {
		if _, ok := values[field]; !ok {
			name := field
			if object != "root" {
				name = object + "." + field
			}
			return fmt.Errorf("validate manifest: required field %q is missing", name)
		}
	}
	return nil
}

// Validate checks every manifest value before it may enter Ruby, shell, URL,
// filesystem, or release metadata contexts.
func (m Manifest) Validate() error {
	if m.Schema != CurrentSchema {
		return fmt.Errorf("validate manifest: schema must be %d", CurrentSchema)
	}
	if len(m.Formula.Name) > maxFormulaNameBytes || !formulaNamePattern.MatchString(m.Formula.Name) {
		return errors.New("validate manifest: formula.name must be lowercase kebab-case")
	}
	if len(m.Formula.Class) > maxPathComponentBytes || !classNamePattern.MatchString(m.Formula.Class) {
		return errors.New("validate manifest: formula.class must be a single Ruby constant")
	}
	if expected := formulaClassForName(m.Formula.Name); m.Formula.Class != expected {
		return fmt.Errorf("validate manifest: formula.class must be %q as derived from formula.name", expected)
	}
	if err := validateRubyLine("formula.description", m.Formula.Description); err != nil {
		return err
	}
	if err := validateHomepage(m.Formula.Homepage); err != nil {
		return err
	}
	if err := validateRubyLine("formula.license", m.Formula.License); err != nil {
		return err
	}
	if !ownerPattern.MatchString(m.Formula.Repository.Owner) || len(m.Formula.Repository.Owner) > 39 {
		return errors.New("validate manifest: formula.repository.owner is not a safe GitHub owner")
	}
	if !repositoryPattern.MatchString(m.Formula.Repository.Name) || m.Formula.Repository.Name == "." || m.Formula.Repository.Name == ".." || len(m.Formula.Repository.Name) > 100 {
		return errors.New("validate manifest: formula.repository.name is not a safe GitHub repository name")
	}
	if len(m.Formula.Binary) > maxPathComponentBytes || !fileNamePattern.MatchString(m.Formula.Binary) || m.Formula.Binary == "." || m.Formula.Binary == ".." {
		return errors.New("validate manifest: formula.binary must be a safe basename")
	}
	if err := validateAsset("formula.assets.darwin_arm64", m.Formula.Assets.DarwinARM64); err != nil {
		return err
	}
	if err := validateAsset("formula.assets.darwin_amd64", m.Formula.Assets.DarwinAMD64); err != nil {
		return err
	}
	if m.Formula.Assets.DarwinARM64 == m.Formula.Assets.DarwinAMD64 {
		return errors.New("validate manifest: Darwin arm64 and amd64 assets must be different")
	}
	if err := validateRelativePath("release.build_script", m.Release.BuildScript); err != nil {
		return err
	}
	if len(m.Homebrew.TestArgs) == 0 {
		return errors.New("validate manifest: homebrew.test_args must contain at least one argument")
	}
	if !m.Homebrew.MacOSOnly {
		return errors.New("validate manifest: homebrew.macos_only must be true for schema 1 Darwin assets")
	}
	for i, value := range m.Homebrew.TestArgs {
		if !argumentPattern.MatchString(value) {
			return fmt.Errorf("validate manifest: homebrew.test_args[%d] contains unsafe characters", i)
		}
	}
	if m.Homebrew.Service != nil {
		if err := m.Homebrew.Service.validate(); err != nil {
			return err
		}
	}
	if err := validateCaveats(m.Homebrew.Caveats); err != nil {
		return err
	}
	return nil
}

func formulaClassForName(name string) string {
	parts := strings.Split(name, "-")
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func (s Service) validate() error {
	if !s.Enabled {
		if len(s.RunArgs) != 0 || s.KeepAlive != (KeepAlive{}) || s.RestartDelay != 0 || len(s.Environment) != 0 || s.LogPath != "" || s.ErrorLogPath != "" {
			return errors.New("validate manifest: a disabled homebrew.service may contain only enabled=false")
		}
		return nil
	}
	if s.RunArgs == nil {
		return errors.New("validate manifest: homebrew.service.run_args must be an array")
	}
	for i, value := range s.RunArgs {
		if !argumentPattern.MatchString(value) {
			return fmt.Errorf("validate manifest: homebrew.service.run_args[%d] contains unsafe characters", i)
		}
	}
	keepAlivePolicies := 0
	if s.KeepAlive.SuccessfulExit != nil {
		keepAlivePolicies++
	}
	if s.KeepAlive.Crashed != nil {
		keepAlivePolicies++
	}
	if keepAlivePolicies != 1 {
		return errors.New("validate manifest: homebrew.service.keep_alive must select exactly one of successful_exit or crashed")
	}
	if s.RestartDelay < 1 || s.RestartDelay > 3600 {
		return errors.New("validate manifest: homebrew.service.restart_delay must be between 1 and 3600 seconds")
	}
	if s.Environment == nil {
		return errors.New("validate manifest: homebrew.service.environment must be an object")
	}
	for key, value := range s.Environment {
		if !environmentPattern.MatchString(key) {
			return fmt.Errorf("validate manifest: homebrew.service.environment key %q is invalid", key)
		}
		if value == "" || containsRubyInjection(value) || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("validate manifest: homebrew.service.environment[%q] contains unsafe characters", key)
		}
	}
	if err := validateRelativePath("homebrew.service.log_path", s.LogPath); err != nil {
		return err
	}
	if err := validateRelativePath("homebrew.service.error_log_path", s.ErrorLogPath); err != nil {
		return err
	}
	return nil
}

func validateRubyLine(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("validate manifest: %s must not be empty", field)
	}
	if !utf8.ValidString(value) || containsRubyInjection(value) || containsC0Control(value) {
		return fmt.Errorf("validate manifest: %s contains unsafe Ruby characters", field)
	}
	return nil
}

func containsC0Control(value string) bool {
	for _, character := range value {
		if character <= '\x1f' {
			return true
		}
	}
	return false
}

func containsRubyInjection(value string) bool {
	return strings.ContainsAny(value, `"\\`) || containsRubyInterpolation(value)
}

func containsRubyInterpolation(value string) bool {
	return strings.Contains(value, "#{") || strings.Contains(value, "#@") || strings.Contains(value, "#$")
}

func validateHomepage(value string) error {
	if containsRubyInjection(value) || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("validate manifest: formula.homepage contains unsafe characters")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("validate manifest: formula.homepage must be an HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func validateAsset(field, value string) error {
	if len(value) > maxPathComponentBytes || !fileNamePattern.MatchString(value) || !strings.HasSuffix(value, ".tar.gz") || value == "." || value == ".." {
		return fmt.Errorf("validate manifest: %s must be a safe .tar.gz basename", field)
	}
	return nil
}

func validateRelativePath(field, value string) error {
	if value == "" || len(value) > maxRelativePathBytes || !relativePathPattern.MatchString(value) || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("validate manifest: %s must be a clean relative path", field)
	}
	for _, part := range strings.Split(value, "/") {
		if len(part) > maxPathComponentBytes || !pathPartPattern.MatchString(part) || part == "." || part == ".." {
			return fmt.Errorf("validate manifest: %s contains an unsafe path component", field)
		}
	}
	return nil
}

func validateCaveats(value string) error {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\x00") || containsRubyInterpolation(value) {
		return errors.New("validate manifest: homebrew.caveats contains unsafe Ruby heredoc content")
	}
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) == "EOS" {
			return errors.New("validate manifest: homebrew.caveats may not contain the EOS heredoc terminator")
		}
	}
	for _, placeholder := range placeholderRE.FindAllString(value, -1) {
		if placeholder != "{{var}}" && placeholder != "{{home}}" {
			return fmt.Errorf("validate manifest: unsupported caveats placeholder %q", placeholder)
		}
	}
	withoutKnown := strings.NewReplacer("{{var}}", "", "{{home}}", "").Replace(value)
	if strings.Contains(withoutKnown, "{{") || strings.Contains(withoutKnown, "}}") {
		return errors.New("validate manifest: homebrew.caveats contains a malformed placeholder")
	}
	return nil
}

// RepositorySlug returns the canonical GitHub owner/name string.
func (m Manifest) RepositorySlug() string {
	return m.Formula.Repository.Owner + "/" + m.Formula.Repository.Name
}

// ValidateStableVersion accepts only stable SemVer without a leading v.
func ValidateStableVersion(version string) error {
	if !stableVersionRE.MatchString(version) {
		return fmt.Errorf("version %q must be stable SemVer X.Y.Z without a leading v", version)
	}
	return nil
}

// CompareStableVersions compares two validated stable SemVer strings.
func CompareStableVersions(a, b string) (int, error) {
	if err := ValidateStableVersion(a); err != nil {
		return 0, err
	}
	if err := ValidateStableVersion(b); err != nil {
		return 0, err
	}
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := range aParts {
		var av, bv big.Int
		av.SetString(aParts[i], 10)
		bv.SetString(bParts[i], 10)
		if cmp := av.Cmp(&bv); cmp != 0 {
			return cmp, nil
		}
	}
	return 0, nil
}
