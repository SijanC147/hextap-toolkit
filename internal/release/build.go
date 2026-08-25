package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SijanC147/hextap-toolkit/internal/atomicfile"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	maxBinarySize       = 256 << 20
	maxLicenseSize      = 1 << 20
	maxReadmeSize       = 8 << 20
	defaultBuildTimeout = 15 * time.Minute
	releaseLockName     = ".hextap-release.lock"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// BuildOptions defines one complete deterministic release build.
type BuildOptions struct {
	ManifestPath string
	Version      string
	Commit       string
	SourceDir    string
	OutputDir    string
}

// BuildResult lists the sorted release archive basenames written by Build.
type BuildResult struct {
	Formula string
	Version string
	Assets  []string
}

type target struct {
	OS    string
	Arch  string
	Asset string
}

type archiveMember struct {
	name string
	mode int64
	data []byte
}

type ownedOutput struct {
	path string
	info fs.FileInfo
}

type buildHooks struct {
	afterPublish       func(name, path string) error
	beforeOutputCommit func(outputDir string) error
}

// Build executes the project adapter exactly once per declared target and
// creates canonical archives plus SHA256SUMS. OutputDir must already exist and
// be empty. After a failed build, cleanup removes only output paths still
// owned by that build.
func Build(options BuildOptions) (result BuildResult, retErr error) {
	return build(options, buildHooks{})
}

func build(options BuildOptions, hooks buildHooks) (result BuildResult, retErr error) {
	if _, err := ParseVersion(options.Version); err != nil {
		return BuildResult{}, fmt.Errorf("validate build version: %w", err)
	}
	if !commitPattern.MatchString(options.Commit) {
		return BuildResult{}, errors.New("validate build commit: commit must be 7 to 64 lowercase hexadecimal characters")
	}

	sourceDir, err := validateDirectory(options.SourceDir, "source", false)
	if err != nil {
		return BuildResult{}, err
	}
	outputDir, err := validateDirectory(options.OutputDir, "output", true)
	if err != nil {
		return BuildResult{}, err
	}
	lock, err := acquireReleaseLock(outputDir)
	if err != nil {
		return BuildResult{}, err
	}
	owned := map[string]ownedOutput{releaseLockName: lock}
	defer func() {
		if retErr != nil {
			cleanupOwnedOutputs(owned)
		}
	}()
	project, err := readManifestWithin(options.ManifestPath, sourceDir)
	if err != nil {
		return BuildResult{}, err
	}
	targets, err := buildTargets(project)
	if err != nil {
		return BuildResult{}, err
	}
	if project.Formula.Binary == "LICENSE" || project.Formula.Binary == "README.md" {
		return BuildResult{}, errors.New("validate build manifest: formula.binary collides with a required archive member")
	}

	license, err := readRegularFile(filepath.Join(sourceDir, "LICENSE"), "LICENSE", maxLicenseSize, false)
	if err != nil {
		return BuildResult{}, err
	}
	readme, err := readRegularFile(filepath.Join(sourceDir, "README.md"), "README.md", maxReadmeSize, false)
	if err != nil {
		return BuildResult{}, err
	}
	adapterPath := filepath.Join(sourceDir, filepath.FromSlash(project.Release.BuildScript))
	if err := validateProjectLocalExecutable(adapterPath, sourceDir); err != nil {
		return BuildResult{}, fmt.Errorf("validate build adapter: %w", err)
	}

	temporaryRoot, err := os.MkdirTemp(filepath.Dir(outputDir), ".hextap-release-*")
	if err != nil {
		return BuildResult{}, fmt.Errorf("create release staging directory: %w", err)
	}
	defer os.RemoveAll(temporaryRoot)
	distDir := filepath.Join(temporaryRoot, "dist")
	if err := os.Mkdir(distDir, 0o700); err != nil {
		return BuildResult{}, fmt.Errorf("create staged distribution directory: %w", err)
	}

	assets := make([]string, 0, len(targets))
	for _, buildTarget := range targets {
		stageDir := filepath.Join(temporaryRoot, "target-"+buildTarget.OS+"-"+buildTarget.Arch)
		if err := os.Mkdir(stageDir, 0o700); err != nil {
			return BuildResult{}, fmt.Errorf("create %s-%s staging directory: %w", buildTarget.OS, buildTarget.Arch, err)
		}
		binaryPath := filepath.Join(stageDir, project.Formula.Binary)
		if err := runAdapter(adapterPath, sourceDir, binaryPath, buildTarget, options.Version, options.Commit); err != nil {
			return BuildResult{}, err
		}
		binary, err := validateAdapterOutput(stageDir, binaryPath, project.Formula.Binary)
		if err != nil {
			return BuildResult{}, fmt.Errorf("validate %s-%s adapter output: %w", buildTarget.OS, buildTarget.Arch, err)
		}
		members := []archiveMember{
			{name: project.Formula.Binary, mode: 0o755, data: binary},
			{name: "LICENSE", mode: 0o644, data: license},
			{name: "README.md", mode: 0o644, data: readme},
		}
		if err := writeArchive(filepath.Join(distDir, buildTarget.Asset), members); err != nil {
			return BuildResult{}, fmt.Errorf("package %s-%s archive: %w", buildTarget.OS, buildTarget.Arch, err)
		}
		assets = append(assets, buildTarget.Asset)
	}
	sort.Strings(assets)
	if err := writeChecksums(distDir, assets); err != nil {
		return BuildResult{}, err
	}

	// The exclusive lock closes the race after the initial empty-directory
	// check. Requiring that it remains our only entry also detects writers that
	// do not honor the lock before publication begins.
	if err := requireExactOwnedOutputs(outputDir, []string{releaseLockName}, owned); err != nil {
		return BuildResult{}, err
	}
	files := append(append([]string(nil), assets...), "SHA256SUMS")
	sort.Strings(files)
	for _, name := range files {
		source := filepath.Join(distDir, name)
		destination := filepath.Join(outputDir, name)
		sourceInfo, err := os.Lstat(source)
		if err != nil {
			return BuildResult{}, fmt.Errorf("inspect staged release output %q: %w", name, err)
		}
		if err := os.Link(source, destination); err != nil {
			return BuildResult{}, fmt.Errorf("publish release output %q: %w", name, err)
		}
		destinationInfo, err := os.Lstat(destination)
		if err != nil {
			return BuildResult{}, fmt.Errorf("inspect published release output %q: %w", name, err)
		}
		if !os.SameFile(sourceInfo, destinationInfo) {
			return BuildResult{}, fmt.Errorf("published release output %q changed before ownership could be recorded", name)
		}
		owned[name] = ownedOutput{path: destination, info: destinationInfo}
		if err := os.Remove(source); err != nil {
			return BuildResult{}, fmt.Errorf("finalize staged release output %q: %w", name, err)
		}
		if hooks.afterPublish != nil {
			if err := hooks.afterPublish(name, destination); err != nil {
				return BuildResult{}, fmt.Errorf("after publishing release output %q: %w", name, err)
			}
		}
	}
	if hooks.beforeOutputCommit != nil {
		if err := hooks.beforeOutputCommit(outputDir); err != nil {
			return BuildResult{}, fmt.Errorf("before committing release output: %w", err)
		}
	}
	withLock := append(append([]string(nil), files...), releaseLockName)
	sort.Strings(withLock)
	if err := requireExactOwnedOutputs(outputDir, withLock, owned); err != nil {
		return BuildResult{}, err
	}
	if err := removeOwnedOutput(lock); err != nil {
		return BuildResult{}, fmt.Errorf("remove release output lock: %w", err)
	}
	delete(owned, releaseLockName)
	if err := syncDirectory(outputDir); err != nil {
		return BuildResult{}, fmt.Errorf("sync release output directory: %w", err)
	}
	if err := requireExactOwnedOutputs(outputDir, files, owned); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Formula: project.Formula.Name, Version: options.Version, Assets: assets}, nil
}

func acquireReleaseLock(outputDir string) (ownedOutput, error) {
	path := filepath.Join(outputDir, releaseLockName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ownedOutput{}, fmt.Errorf("acquire release output lock: %w", err)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return ownedOutput{}, fmt.Errorf("inspect release output lock: %w", statErr)
	}
	lock := ownedOutput{path: path, info: info}
	if closeErr != nil {
		_ = removeOwnedOutput(lock)
		return ownedOutput{}, fmt.Errorf("close release output lock: %w", closeErr)
	}
	return lock, nil
}

func cleanupOwnedOutputs(owned map[string]ownedOutput) {
	names := make([]string, 0, len(owned))
	for name := range owned {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_ = removeOwnedOutput(owned[name])
	}
}

func removeOwnedOutput(owned ownedOutput) error {
	current, err := os.Lstat(owned.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(current, owned.info) {
		return fmt.Errorf("output path %q is no longer owned by this build", owned.path)
	}
	return os.Remove(owned.path)
}

func requireExactOwnedOutputs(outputDir string, expected []string, owned map[string]ownedOutput) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("read output directory %q: %w", outputDir, err)
	}
	actual := directoryEntryNames(entries)
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("release output directory changed during build: entries = %v, want %v", actual, want)
	}
	for _, name := range want {
		record, exists := owned[name]
		if !exists {
			return fmt.Errorf("release output %q has no ownership record", name)
		}
		current, err := os.Lstat(filepath.Join(outputDir, name))
		if err != nil {
			return fmt.Errorf("inspect release output %q: %w", name, err)
		}
		if !os.SameFile(current, record.info) {
			return fmt.Errorf("release output %q is no longer owned by this build", name)
		}
	}
	return nil
}

func directoryEntryNames(entries []os.DirEntry) []string {
	result := make([]string, len(entries))
	for index, entry := range entries {
		result[index] = entry.Name()
	}
	return result
}

func validateDirectory(path, label string, requireEmpty bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s directory path must not be empty", label)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s directory: %w", label, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect %s directory %q: %w", label, absolute, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s directory %q must be a real directory, not a symlink", label, absolute)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s directory symlinks: %w", label, err)
	}
	if requireEmpty {
		if err := requireEmptyDirectory(resolved); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

func requireEmptyDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read output directory %q: %w", path, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %q must start empty", path)
	}
	return nil
}

func readManifestWithin(path, sourceDir string) (manifest.Manifest, error) {
	if path == "" {
		return manifest.Manifest{}, errors.New("manifest path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("resolve manifest path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("resolve manifest path: %w", err)
	}
	if !pathWithin(sourceDir, resolved) {
		return manifest.Manifest{}, errors.New("manifest symlink resolution escapes the source directory")
	}
	data, err := readRegularFile(absolute, "manifest", 1<<20, false)
	if err != nil {
		return manifest.Manifest{}, err
	}
	project, err := manifest.Parse(data)
	if err != nil {
		return manifest.Manifest{}, err
	}
	return project, nil
}

func buildTargets(project manifest.Manifest) ([]target, error) {
	result := []target{
		{OS: "darwin", Arch: "arm64", Asset: project.Formula.Assets.DarwinARM64},
		{OS: "darwin", Arch: "amd64", Asset: project.Formula.Assets.DarwinAMD64},
	}
	if project.Release.Linux {
		result = append(result,
			target{OS: "linux", Arch: "arm64", Asset: project.Formula.Name + "-linux-arm64.tar.gz"},
			target{OS: "linux", Arch: "amd64", Asset: project.Formula.Name + "-linux-amd64.tar.gz"},
		)
	}
	seen := make(map[string]struct{}, len(result))
	for _, item := range result {
		if _, exists := seen[item.Asset]; exists {
			return nil, fmt.Errorf("validate release assets: duplicate asset name %q", item.Asset)
		}
		seen[item.Asset] = struct{}{}
	}
	return result, nil
}

func validateProjectLocalExecutable(path, sourceDir string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", path, err)
	}
	if !pathWithin(sourceDir, resolved) {
		return errors.New("adapter must resolve within the source directory")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("adapter must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("adapter must be executable")
	}
	if hardLinked(info) {
		return errors.New("adapter must not be hard-linked")
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func runAdapter(adapterPath, sourceDir, binaryPath string, buildTarget target, version, commit string) error {
	timer := time.NewTimer(defaultBuildTimeout)
	defer timer.Stop()
	return runAdapterWithControl(adapterPath, sourceDir, binaryPath, buildTarget, version, commit, timer.C, terminateProcessTree)
}

func runAdapterWithControl(adapterPath, sourceDir, binaryPath string, buildTarget target, version, commit string, timeout <-chan time.Time, terminate func(*exec.Cmd)) error {
	command := exec.Command(adapterPath)
	command.Dir = sourceDir
	command.Env = buildEnvironment(buildTarget, binaryPath, version, commit)
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	configureProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start build adapter for %s-%s: %w", buildTarget.OS, buildTarget.Arch, err)
	}
	wait := make(chan error, 1)
	go func() {
		wait <- command.Wait()
	}()
	select {
	case err := <-wait:
		if err != nil {
			return fmt.Errorf("build adapter failed for %s-%s: %w", buildTarget.OS, buildTarget.Arch, err)
		}
		return nil
	case <-timeout:
		// Prefer a completed Wait if timeout and exit became ready together.
		// terminateProcessTree additionally verifies that the leader has not
		// already been reaped before signaling its process group.
		select {
		case err := <-wait:
			if err != nil {
				return fmt.Errorf("build adapter failed for %s-%s: %w", buildTarget.OS, buildTarget.Arch, err)
			}
			return nil
		default:
		}
		terminate(command)
		<-wait
		return fmt.Errorf("build adapter timed out for %s-%s", buildTarget.OS, buildTarget.Arch)
	}
}

func buildEnvironment(buildTarget target, output, version, commit string) []string {
	allow := []string{
		"CC", "CGO_ENABLED", "CXX", "DEVELOPER_DIR", "GOCACHE", "GOENV",
		"GOMODCACHE", "GOPATH", "GOPROXY", "GOROOT", "GOSUMDB", "GOTOOLCHAIN",
		"HOME", "LANG", "LC_ALL", "LOGNAME", "PATH", "SDKROOT", "SHELL",
		"SYSTEMROOT", "TEMP", "TERM", "TMP", "TMPDIR", "TZ", "USER",
	}
	result := make([]string, 0, len(allow)+5)
	for _, name := range allow {
		if value, exists := os.LookupEnv(name); exists {
			result = append(result, name+"="+value)
		}
	}
	result = append(result,
		"HEXTAP_TARGET_OS="+buildTarget.OS,
		"HEXTAP_TARGET_ARCH="+buildTarget.Arch,
		"HEXTAP_OUTPUT="+output,
		"HEXTAP_VERSION="+version,
		"HEXTAP_COMMIT="+commit,
	)
	return result
}

func validateAdapterOutput(stageDir, binaryPath, binaryName string) ([]byte, error) {
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return nil, fmt.Errorf("read staging directory: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != binaryName {
		return nil, fmt.Errorf("adapter must create exactly %q and no other staged entries", binaryName)
	}
	if entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
		return nil, errors.New("adapter output must be a regular non-symlink file")
	}
	info, err := os.Lstat(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("inspect adapter output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("adapter output must be a regular non-symlink file")
	}
	if hardLinked(info) {
		return nil, errors.New("adapter output must not be hard-linked")
	}
	if info.Size() < 0 || info.Size() > maxBinarySize {
		return nil, fmt.Errorf("adapter output exceeds %d bytes", maxBinarySize)
	}
	file, err := os.OpenFile(binaryPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open adapter output: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened adapter output: %w", err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || hardLinked(openedInfo) {
		return nil, errors.New("adapter output changed while opening")
	}
	if err := file.Chmod(0o755); err != nil {
		return nil, fmt.Errorf("set adapter output executable mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync adapter output mode: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBinarySize+1))
	if err != nil {
		return nil, fmt.Errorf("read adapter output: %w", err)
	}
	if int64(len(data)) > maxBinarySize || int64(len(data)) != openedInfo.Size() {
		return nil, errors.New("adapter output changed size while reading")
	}
	return data, nil
}

func readRegularFile(path, label string, maximum int64, requireSingleLink bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q must be a regular non-symlink file", label, path)
	}
	if requireSingleLink && hardLinked(info) {
		return nil, fmt.Errorf("%s %q must not be hard-linked", label, path)
	}
	if info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("%s %q exceeds %d bytes", label, path, maximum)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s %q: %w", label, path, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened %s %q: %w", label, path, err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q changed while opening", label, path)
	}
	limited := io.LimitReader(file, maximum+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", label, path, err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("%s %q exceeds %d bytes", label, path, maximum)
	}
	if int64(len(data)) != openedInfo.Size() {
		return nil, fmt.Errorf("%s %q changed size while reading", label, path)
	}
	return data, nil
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
	nlink := value.FieldByName("Nlink")
	if !nlink.IsValid() {
		return false
	}
	switch nlink.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nlink.Uint() > 1
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return nlink.Int() > 1
	default:
		return false
	}
}

func writeArchive(path string, members []archiveMember) (retErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if retErr != nil {
			_ = os.Remove(path)
		}
	}()

	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = time.Time{}
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	epoch := time.Unix(0, 0).UTC()
	for _, member := range members {
		header := &tar.Header{
			Name:       member.name,
			Mode:       member.mode,
			Uid:        0,
			Gid:        0,
			Size:       int64(len(member.data)),
			ModTime:    epoch,
			Typeflag:   tar.TypeReg,
			Uname:      "",
			Gname:      "",
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
			Format:     tar.FormatUSTAR,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := io.Copy(tarWriter, bytes.NewReader(member.data)); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func writeChecksums(directory string, assets []string) error {
	var output strings.Builder
	for _, asset := range assets {
		hash, err := hashRegularFile(filepath.Join(directory, asset), maxBinarySize+maxLicenseSize+maxReadmeSize+8<<20)
		if err != nil {
			return err
		}
		output.WriteString(hash)
		output.WriteString("  ")
		output.WriteString(asset)
		output.WriteByte('\n')
	}
	if err := atomicfile.Write(filepath.Join(directory, "SHA256SUMS"), []byte(output.String()), 0o644); err != nil {
		return fmt.Errorf("write SHA256SUMS: %w", err)
	}
	return nil
}

func hashRegularFile(path string, maximum int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect release archive %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || hardLinked(info) {
		return "", fmt.Errorf("release archive %q must be a regular single-link file", path)
	}
	if info.Size() < 0 || info.Size() > maximum {
		return "", fmt.Errorf("release archive %q exceeds %d bytes", path, maximum)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open release archive %q: %w", path, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect opened release archive %q: %w", path, err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return "", fmt.Errorf("release archive %q changed while opening", path)
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil {
		return "", fmt.Errorf("hash release archive %q: %w", path, err)
	}
	if written > maximum || written != openedInfo.Size() {
		return "", fmt.Errorf("release archive %q changed size while hashing", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
