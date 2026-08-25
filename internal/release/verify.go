package release

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"archive/tar"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	// These limits leave room for the largest artifact accepted by Build while
	// bounding all verifier allocations before an archive is trusted.
	maxVerifyCompressedSize   int64 = 512 << 20
	maxVerifyMemberSize       int64 = maxBinarySize
	maxVerifyUncompressedSize int64 = maxBinarySize + maxLicenseSize + maxReadmeSize + 4<<10
	verifyCommandTimeout            = 10 * time.Second
	maxVerifyCommandOutput          = 16 << 10
)

var verifyCommitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// VerifyOptions identifies one release artifact set to verify.
type VerifyOptions struct {
	ManifestPath  string
	Version       string
	Commit        string
	Directory     string
	ExecuteTarget string
}

// VerifyResult describes the validated release and, when requested, the
// target whose binary was executed.
type VerifyResult struct {
	Formula        string
	Version        string
	Assets         []string
	ExecutedTarget string
}

// Verify validates a complete release directory without modifying it. If
// ExecuteTarget is set, all artifacts are validated before its binary is
// extracted into a private temporary directory and run.
func Verify(options VerifyOptions) (VerifyResult, error) {
	if _, err := ParseVersion(options.Version); err != nil {
		return VerifyResult{}, fmt.Errorf("validate verify version: %w", err)
	}
	if !verifyCommitPattern.MatchString(options.Commit) {
		return VerifyResult{}, errors.New("validate verify commit: commit must be 7 to 64 lowercase hexadecimal characters")
	}
	project, err := readVerifyManifest(options.ManifestPath)
	if err != nil {
		return VerifyResult{}, err
	}
	targets, err := buildTargets(project)
	if err != nil {
		return VerifyResult{}, err
	}
	selected, err := selectVerifyTarget(targets, options.ExecuteTarget)
	if err != nil {
		return VerifyResult{}, err
	}
	directory, err := verifyDirectory(options.Directory)
	if err != nil {
		return VerifyResult{}, err
	}
	assets := make([]string, 0, len(targets))
	for _, item := range targets {
		assets = append(assets, item.Asset)
	}
	sort.Strings(assets)
	if err := verifyDirectoryEntries(directory, append(append([]string(nil), assets...), "SHA256SUMS")); err != nil {
		return VerifyResult{}, err
	}
	if err := verifyChecksums(directory, assets); err != nil {
		return VerifyResult{}, err
	}

	binaries := make(map[string][]byte, len(targets))
	for _, item := range targets {
		binary, err := verifyArchive(filepath.Join(directory, item.Asset), project.Formula.Binary, item.OS, item.Arch)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("verify %s: %w", item.Asset, err)
		}
		binaries[item.Asset] = binary
	}
	if selected != nil {
		if err := executeVerifiedBinary(project.Formula.Binary, options.Version, options.Commit, selected.Asset, binaries[selected.Asset]); err != nil {
			return VerifyResult{}, err
		}
	}
	return VerifyResult{Formula: project.Formula.Name, Version: options.Version, Assets: assets, ExecutedTarget: options.ExecuteTarget}, nil
}

func readVerifyManifest(path string) (manifest.Manifest, error) {
	if path == "" {
		return manifest.Manifest{}, errors.New("verify manifest path must not be empty")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("inspect verify manifest %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return manifest.Manifest{}, errors.New("verify manifest must be a regular non-symlink file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("read verify manifest: %w", err)
	}
	project, err := manifest.Parse(data)
	if err != nil {
		return manifest.Manifest{}, err
	}
	return project, nil
}

func verifyDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("verify directory path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve verify directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect verify directory %q: %w", absolute, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("verify directory must be a real directory, not a symlink")
	}
	return filepath.EvalSymlinks(absolute)
}

func selectVerifyTarget(targets []target, requested string) (*target, error) {
	if requested == "" {
		return nil, nil
	}
	var selected *target
	for i := range targets {
		candidate := targets[i].OS + "-" + targets[i].Arch
		if requested == candidate {
			selected = &targets[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("execute target %q is not an expected release target", requested)
	}
	wantOS, wantArch := runtime.GOOS, runtime.GOARCH
	if requested != wantOS+"-"+wantArch {
		return nil, fmt.Errorf("execute target %q does not match runtime %s-%s", requested, wantOS, wantArch)
	}
	return selected, nil
}

func verifyDirectoryEntries(directory string, expected []string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read verify directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := os.Lstat(filepath.Join(directory, entry.Name()))
		if infoErr != nil {
			return fmt.Errorf("inspect verify entry %q: %w", entry.Name(), infoErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("verify entry %q must be a regular file", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if strings.Join(actual, "\x00") != strings.Join(want, "\x00") {
		return fmt.Errorf("verify directory entries = %v, want %v", actual, want)
	}
	return nil
}

func verifyChecksums(directory string, assets []string) error {
	path := filepath.Join(directory, "SHA256SUMS")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect SHA256SUMS: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("SHA256SUMS must be a regular non-symlink file")
	}
	data, err := readBoundedVerifyFile(path, 1<<20)
	if err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return errors.New("SHA256SUMS must end with a newline")
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	want := append([]string(nil), assets...)
	sort.Strings(want)
	if len(lines) != len(want) {
		return fmt.Errorf("SHA256SUMS entries = %d, want %d", len(lines), len(want))
	}
	seen := make(map[string]bool, len(lines))
	previous := ""
	for i, line := range lines {
		if len(line) < 67 || line[64:66] != "  " || !isLowerHex(line[:64]) {
			return fmt.Errorf("SHA256SUMS line %d has invalid format", i+1)
		}
		name := line[66:]
		if name == "" || filepath.Base(name) != name || filepath.Clean(name) != name || strings.ContainsAny(name, "\\\r\t") {
			return fmt.Errorf("SHA256SUMS line %d has unsafe asset name", i+1)
		}
		if name <= previous {
			return errors.New("SHA256SUMS entries must be strictly sorted")
		}
		if seen[name] {
			return fmt.Errorf("SHA256SUMS contains duplicate asset %q", name)
		}
		seen[name] = true
		if i >= len(want) || name != want[i] {
			return fmt.Errorf("SHA256SUMS contains unexpected asset %q", name)
		}
		raw, err := readBoundedVerifyFile(filepath.Join(directory, name), maxVerifyCompressedSize)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != line[:64] {
			return fmt.Errorf("SHA256SUMS checksum mismatch for %q", name)
		}
		previous = name
	}
	return nil
}

func readBoundedVerifyFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect release file %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("release file %q must be a regular non-symlink file", path)
	}
	if info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("release file %q exceeds %d bytes", path, maximum)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release file %q: %w", path, err)
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("release file %q changed size while reading", path)
	}
	return data, nil
}

func isLowerHex(value string) bool {
	if len(value) == 0 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func verifyArchive(path, binaryName, targetOS, targetArch string) ([]byte, error) {
	raw, err := readBoundedVerifyFile(path, maxVerifyCompressedSize)
	if err != nil {
		return nil, err
	}
	rawReader := bytes.NewReader(raw)
	gzipReader, err := gzip.NewReader(rawReader)
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	gzipReader.Multistream(false)
	if !gzipReader.Header.ModTime.IsZero() || gzipReader.Header.Name != "" || gzipReader.Header.Comment != "" || len(gzipReader.Header.Extra) != 0 || gzipReader.Header.OS != 255 {
		return nil, errors.New("gzip header is not canonical")
	}
	payload, err := io.ReadAll(io.LimitReader(gzipReader, maxVerifyUncompressedSize+1))
	if err != nil {
		return nil, fmt.Errorf("read gzip payload: %w", err)
	}
	if int64(len(payload)) > maxVerifyUncompressedSize {
		return nil, fmt.Errorf("gzip payload exceeds %d bytes", maxVerifyUncompressedSize)
	}
	if err := gzipReader.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	if rawReader.Len() != 0 {
		return nil, errors.New("gzip contains trailing data or multiple members")
	}
	tarData := bytes.NewReader(payload)
	tarReader := tar.NewReader(tarData)
	var binary []byte
	for index, expected := range []string{binaryName, "LICENSE", "README.md"} {
		header, err := tarReader.Next()
		if err != nil {
			return nil, fmt.Errorf("read tar member %d: %w", index+1, err)
		}
		if err := verifyTarHeader(header, expected, index); err != nil {
			return nil, err
		}
		if header.Size > maxVerifyMemberSize {
			return nil, fmt.Errorf("tar member %q exceeds %d bytes", header.Name, maxVerifyMemberSize)
		}
		member, err := io.ReadAll(io.LimitReader(tarReader, maxVerifyMemberSize+1))
		if err != nil {
			return nil, fmt.Errorf("read tar member %q: %w", header.Name, err)
		}
		if int64(len(member)) != header.Size {
			return nil, fmt.Errorf("tar member %q size changed", header.Name)
		}
		if index == 0 {
			binary = member
		}
	}
	if _, err := tarReader.Next(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("archive contains unexpected extra tar member")
		}
		return nil, fmt.Errorf("read tar trailer: %w", err)
	}
	if tarData.Len() != 0 {
		return nil, errors.New("tar contains trailing data")
	}
	if err := verifyExecutable(binary, targetOS, targetArch); err != nil {
		return nil, err
	}
	return binary, nil
}

func verifyTarHeader(header *tar.Header, expected string, index int) error {
	if header.Name != expected || filepath.Base(header.Name) != header.Name || filepath.Clean(header.Name) != header.Name || strings.Contains(header.Name, "\\") {
		return fmt.Errorf("tar member %d has unsafe or unexpected name %q", index+1, header.Name)
	}
	if header.Typeflag != tar.TypeReg || header.Format != tar.FormatUSTAR || header.PAXRecords != nil || header.Xattrs != nil {
		return fmt.Errorf("tar member %q has unsupported type or format", header.Name)
	}
	wantMode := int64(0o644)
	if index == 0 {
		wantMode = 0o755
	}
	if header.Mode != wantMode || header.Uid != 0 || header.Gid != 0 || header.Devmajor != 0 || header.Devminor != 0 || header.Uname != "" || header.Gname != "" {
		return fmt.Errorf("tar member %q has noncanonical owner or mode", header.Name)
	}
	epoch := time.Unix(0, 0).UTC()
	if !header.ModTime.Equal(epoch) || !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() {
		return fmt.Errorf("tar member %q has noncanonical time", header.Name)
	}
	if header.Size < 0 {
		return fmt.Errorf("tar member %q has negative size", header.Name)
	}
	return nil
}

func verifyExecutable(data []byte, targetOS, targetArch string) error {
	if len(data) == 0 {
		return errors.New("binary is empty")
	}
	if targetOS == "darwin" {
		if len(data) >= 4 {
			magic := binary.BigEndian.Uint32(data[:4])
			if magic == 0xcafebabe || magic == 0xbebafeca || magic == 0xcafebabf || magic == 0xbfbafeca {
				return errors.New("fat or universal Mach-O binaries are not allowed")
			}
		}
		file, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("invalid Mach-O binary: %w", err)
		}
		defer file.Close()
		if file.Type != macho.TypeExec {
			return errors.New("Mach-O binary is not an executable")
		}
		want := macho.CpuAmd64
		if targetArch == "arm64" {
			want = macho.CpuArm64
		}
		if file.Cpu != want {
			return fmt.Errorf("Mach-O architecture is %s, want %s", file.Cpu, want)
		}
		return nil
	}
	if targetOS != "linux" {
		return fmt.Errorf("unsupported release target OS %q", targetOS)
	}
	file, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid ELF binary: %w", err)
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		return errors.New("ELF binary is not a 64-bit executable")
	}
	want := elf.EM_X86_64
	if targetArch == "arm64" {
		want = elf.EM_AARCH64
	}
	if file.Machine != want {
		return fmt.Errorf("ELF architecture is %s, want %s", file.Machine, want)
	}
	return nil
}

func executeVerifiedBinary(binaryName, version, commit, asset string, data []byte) error {
	directory, err := os.MkdirTemp("", ".hextap-verify-*")
	if err != nil {
		return fmt.Errorf("create verification temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, binaryName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return fmt.Errorf("extract %s: %w", asset, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("extract %s: %w", asset, err)
	}
	if err := file.Chmod(0o755); err != nil {
		_ = file.Close()
		return fmt.Errorf("set extracted binary mode: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close extracted binary: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), verifyCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	command.Stdin = nil
	stdout := &boundedBuffer{limit: maxVerifyCommandOutput}
	stderr := &boundedBuffer{limit: maxVerifyCommandOutput}
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("execute %s: timed out", asset)
	}
	if err != nil {
		return fmt.Errorf("execute %s: command failed", asset)
	}
	want := binaryName + " " + version + " (commit " + commit + ")\n"
	if stderr.Len() != 0 {
		return fmt.Errorf("execute %s: stderr was not empty", asset)
	}
	if stdout.String() != want {
		return fmt.Errorf("execute %s: stdout did not match expected version", asset)
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if buffer.Len()+len(data) > buffer.limit {
		return 0, errors.New("command output exceeds limit")
	}
	return buffer.Buffer.Write(data)
}
