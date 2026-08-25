package onboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	localCommandTimeout  = 10 * time.Second
	maximumCommandOutput = 64 << 10
)

var (
	repositorySlugPattern = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)/([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)$`)
	httpsOriginPattern    = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)/([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)(?:\.git)?$`)
	sshOriginPattern      = regexp.MustCompile(`^git@github\.com:([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)/([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)(?:\.git)?$`)
	sshURLOriginPattern   = regexp.MustCompile(`^ssh://git@github\.com/([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)/([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)(?:\.git)?$`)
	stableTagPattern      = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	fullCommitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goPackagePattern      = regexp.MustCompile(`^(?:\.|(?:\./)?[A-Za-z0-9][A-Za-z0-9._-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)*)$`)
	goSymbolPackage       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	goIdentifierPattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	statusCheckPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/@:+()\-]{0,99}$`)
	credentialLikePattern = regexp.MustCompile(`(?i)(github_pat_[A-Za-z0-9_]{10,}|gh[pousr]_[A-Za-z0-9]{10,}|ops_[A-Za-z0-9_-]{10,}|(^|[^A-Za-z0-9])sk-[A-Za-z0-9]{20,})`)
)

var errCommandOutputLimit = errors.New("command output limit exceeded")

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		return 0, errCommandOutputLimit
	}
	if len(data) > remaining {
		_, _ = b.buffer.Write(data[:remaining])
		return remaining, errCommandOutputLimit
	}
	return b.buffer.Write(data)
}

func (b *boundedBuffer) String() string {
	return b.buffer.String()
}

func resolveProject(project string) (string, string, error) {
	if project == "" {
		project = "."
	}
	absolute, err := filepath.Abs(project)
	if err != nil {
		return "", "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", "", fmt.Errorf("inspect project %q: %w", absolute, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", fmt.Errorf("project %q must be a real directory, not a symlink", absolute)
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve project directory: %w", err)
	}
	topLevel, err := runCommandOutput(localCommandTimeout, maximumCommandOutput, "git", "-C", root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", errors.New("project must be the root of a Git repository")
	}
	topLevel = strings.TrimSpace(topLevel)
	resolvedTop, err := filepath.EvalSymlinks(topLevel)
	if err != nil || resolvedTop != root {
		return "", "", errors.New("--project must identify the Git repository root")
	}
	origin, err := runCommandOutput(localCommandTimeout, maximumCommandOutput, "git", "-C", root, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", "", errors.New("Git remote origin is required")
	}
	repository, err := parseGitHubOrigin(strings.TrimSpace(origin))
	if err != nil {
		return "", "", err
	}
	return root, repository, nil
}

func parseGitHubOrigin(value string) (string, error) {
	for _, pattern := range []*regexp.Regexp{httpsOriginPattern, sshOriginPattern, sshURLOriginPattern} {
		matches := pattern.FindStringSubmatch(value)
		if len(matches) == 3 {
			return matches[1] + "/" + strings.TrimSuffix(matches[2], ".git"), nil
		}
	}
	return "", errors.New("Git remote origin must be an exact canonical github.com repository URL")
}

func parseRepository(value string) (owner, name string, err error) {
	matches := repositorySlugPattern.FindStringSubmatch(value)
	if len(matches) != 3 || len(matches[1]) > 39 || len(matches[2]) > 100 || matches[2] == "." || matches[2] == ".." {
		return "", "", errors.New("repository must be OWNER/REPO with a safe GitHub identity")
	}
	return matches[1], matches[2], nil
}

func runCommandOutput(timeout time.Duration, maximum int, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = 2 * time.Second
	output := &boundedBuffer{limit: maximum}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%s timed out", name)
		}
		return "", fmt.Errorf("%s failed", name)
	}
	return output.String(), nil
}

func readLocalFile(path, label string, maximum int64, singleLink bool) ([]byte, fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s %q must be a regular non-symlink file", label, path)
	}
	if singleLink && hardLinked(info) {
		return nil, nil, fmt.Errorf("%s %q must not be hard-linked", label, path)
	}
	if info.Size() < 0 || info.Size() > maximum {
		return nil, nil, fmt.Errorf("%s %q exceeds %d bytes", label, path, maximum)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s %q: %w", label, path, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("inspect opened %s %q: %w", label, path, err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() || singleLink && hardLinked(openedInfo) {
		return nil, nil, fmt.Errorf("%s %q changed while opening", label, path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s %q: %w", label, path, err)
	}
	if int64(len(data)) > maximum || int64(len(data)) != openedInfo.Size() {
		return nil, nil, fmt.Errorf("%s %q changed size while reading", label, path)
	}
	return data, openedInfo, nil
}

func hardLinked(info fs.FileInfo) bool {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	links := value.FieldByName("Nlink")
	if !links.IsValid() {
		return false
	}
	switch links.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return links.Uint() > 1
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return links.Int() > 1
	default:
		return false
	}
}

func hasSpecialFileMode(info fs.FileInfo) bool {
	return info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0
}

func exactFileMode(info fs.FileInfo, mode fs.FileMode) bool {
	return !hasSpecialFileMode(info) && info.Mode().Perm() == mode.Perm()
}

func ensureFinalNewline(data []byte, label string) error {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return fmt.Errorf("%s must end with a newline", label)
	}
	return nil
}

func validateToolkitPin(version, commit string) error {
	if !stableTagPattern.MatchString(version) {
		return errors.New("toolkit version must be exact stable v-prefixed SemVer vX.Y.Z")
	}
	if !fullCommitPattern.MatchString(commit) {
		return errors.New("toolkit SHA must be exactly 40 lowercase hexadecimal characters")
	}
	return nil
}

func validateGoPackage(value string) error {
	if len(value) > 256 || !goPackagePattern.MatchString(value) || strings.Contains(value, "//") || strings.Contains(value, "..") || strings.HasPrefix(value, "-") {
		return errors.New("Go package must be a narrow lexical package path such as . or ./cmd/tool")
	}
	return nil
}

func validateLinkerSymbol(value string) error {
	if len(value) > 256 {
		return errors.New("linker symbol exceeds 256 bytes")
	}
	separator := strings.LastIndexByte(value, '.')
	if separator < 1 || separator == len(value)-1 {
		return errors.New("linker symbol must be a package-qualified Go variable such as main.version")
	}
	packageName, identifier := value[:separator], value[separator+1:]
	if !goSymbolPackage.MatchString(packageName) || strings.Contains(packageName, "//") || strings.Contains(packageName, "..") || strings.HasPrefix(packageName, "-") || !goIdentifierPattern.MatchString(identifier) {
		return errors.New("linker symbol must be a narrow package-qualified Go variable")
	}
	for _, part := range strings.Split(packageName, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, "-") {
			return errors.New("linker symbol contains an unsafe package path")
		}
	}
	return nil
}

func validateRequiredChecks(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one --required-check is required")
	}
	for _, value := range values {
		if value != strings.TrimSpace(value) || !statusCheckPattern.MatchString(value) {
			return nil, errors.New("a required status check contains unsafe characters")
		}
	}
	return normalizeChecks(values), nil
}

func containsCredentialLike(value string) bool {
	return credentialLikePattern.MatchString(value)
}

func inferGoPackage(root, binary string) (string, error) {
	candidate := filepath.Join(root, "cmd", binary)
	if hasGoMainPackage(candidate) {
		return "./cmd/" + binary, nil
	}
	if hasGoMainPackage(root) {
		return ".", nil
	}
	return "", errors.New("--go-package is required because no narrow Go main package could be inferred")
}

func hasGoMainPackage(directory string) bool {
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, _, err := readLocalFile(path, "Go source", maximumLocalFile, false)
		if err != nil {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.PackageClauseOnly)
		if err == nil && file.Name.Name == "main" {
			return true
		}
	}
	return false
}

func parseManifestBytes(data []byte) (manifest.Manifest, error) {
	if err := ensureFinalNewline(data, "manifest"); err != nil {
		return manifest.Manifest{}, err
	}
	project, err := manifest.Parse(data)
	if err != nil {
		return manifest.Manifest{}, err
	}
	return project, nil
}
