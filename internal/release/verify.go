package release

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"archive/tar"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

const (
	// These limits leave room for the largest artifact accepted by Build while
	// bounding all verifier allocations before an archive is trusted.
	maxVerifyCompressedSize   int64 = 512 << 20
	maxVerifyMemberSize       int64 = maxBinarySize
	maxVerifyUncompressedSize int64 = maxBinarySize + maxLicenseSize + maxReadmeSize + maxZshCompletionSize + 4<<10
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
	return verifyWithHook(options, nil)
}

func verifyWithHook(options VerifyOptions, afterChecksums func()) (VerifyResult, error) {
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
	if err := validateProfileCommit(project, options.Commit); err != nil {
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
	assets := make([]string, 0, len(targets)*2)
	for _, item := range targets {
		for _, artifact := range item.Artifacts {
			assets = append(assets, artifact.Name)
		}
	}
	sort.Strings(assets)
	if err := verifyDirectoryEntries(directory, append(append([]string(nil), assets...), "SHA256SUMS")); err != nil {
		return VerifyResult{}, err
	}
	assetBytes, err := verifyChecksums(directory, assets)
	if err != nil {
		return VerifyResult{}, err
	}
	if afterChecksums != nil {
		afterChecksums()
	}

	binaries := make(map[string][]byte, len(targets))
	executionAssets := make(map[string]string, len(targets))
	for _, item := range targets {
		key := item.OS + "-" + item.Arch
		var targetBinary []byte
		for _, artifact := range item.Artifacts {
			var binary []byte
			var err error
			switch artifact.Format {
			case artifactBinary:
				binary, err = verifyRawBinary(assetBytes[artifact.Name], item.OS, item.Arch)
			case artifactBinaryArchive, artifactBundleArchive:
				binary, err = verifyArchive(assetBytes[artifact.Name], project.Formula.Binary, project.Homebrew.ZshCompletion, item.OS, item.Arch, artifact.Format)
			default:
				err = fmt.Errorf("unsupported artifact format %q", artifact.Format)
			}
			if err != nil {
				return VerifyResult{}, fmt.Errorf("verify %s: %w", artifact.Name, err)
			}
			if targetBinary != nil && !bytes.Equal(targetBinary, binary) {
				return VerifyResult{}, fmt.Errorf("verify %s: executable bytes differ across %s-%s assets", artifact.Name, item.OS, item.Arch)
			}
			targetBinary = binary
			if executionAssets[key] == "" || artifact.Format == artifactBinary {
				executionAssets[key] = artifact.Name
			}
		}
		binaries[key] = targetBinary
	}
	if selected != nil {
		key := selected.OS + "-" + selected.Arch
		if err := executeVerifiedBinary(selected.Executable, project.Formula.Binary, options.Version, options.Commit, executionAssets[key], binaries[key]); err != nil {
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
	data, err := readBoundedRegularFile(path, "verify manifest", 1<<20, true)
	if err != nil {
		return manifest.Manifest{}, err
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

func verifyChecksums(directory string, assets []string) (map[string][]byte, error) {
	path := filepath.Join(directory, "SHA256SUMS")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect SHA256SUMS: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("SHA256SUMS must be a regular non-symlink file")
	}
	data, err := readBoundedVerifyFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, errors.New("SHA256SUMS must end with a newline")
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	want := append([]string(nil), assets...)
	sort.Strings(want)
	if len(lines) != len(want) {
		return nil, fmt.Errorf("SHA256SUMS entries = %d, want %d", len(lines), len(want))
	}
	seen := make(map[string]bool, len(lines))
	archives := make(map[string][]byte, len(lines))
	previous := ""
	for i, line := range lines {
		if len(line) < 67 || line[64:66] != "  " || !isLowerHex(line[:64]) {
			return nil, fmt.Errorf("SHA256SUMS line %d has invalid format", i+1)
		}
		name := line[66:]
		if name == "" || filepath.Base(name) != name || filepath.Clean(name) != name || strings.ContainsAny(name, "\\\r\t") {
			return nil, fmt.Errorf("SHA256SUMS line %d has unsafe asset name", i+1)
		}
		if name <= previous {
			return nil, errors.New("SHA256SUMS entries must be strictly sorted")
		}
		if seen[name] {
			return nil, fmt.Errorf("SHA256SUMS contains duplicate asset %q", name)
		}
		seen[name] = true
		if i >= len(want) || name != want[i] {
			return nil, fmt.Errorf("SHA256SUMS contains unexpected asset %q", name)
		}
		raw, err := readBoundedVerifyFile(filepath.Join(directory, name), maxVerifyCompressedSize)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != line[:64] {
			return nil, fmt.Errorf("SHA256SUMS checksum mismatch for %q", name)
		}
		archives[name] = raw
		previous = name
	}
	return archives, nil
}

func readBoundedVerifyFile(path string, maximum int64) ([]byte, error) {
	return readBoundedRegularFile(path, "release file", maximum, true)
}

func readBoundedRegularFile(path, label string, maximum int64, checkOuter bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q must be a regular non-symlink file", label, path)
	}
	if checkOuter {
		if err := verifyOuterFileLayout(info); err != nil {
			return nil, err
		}
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
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() {
		return nil, fmt.Errorf("%s %q changed while opening", label, path)
	}
	if checkOuter {
		if err := verifyOuterFileLayout(openedInfo); err != nil {
			return nil, err
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", label, path, err)
	}
	if int64(len(data)) > maximum || int64(len(data)) != openedInfo.Size() {
		return nil, fmt.Errorf("%s %q changed size while reading", label, path)
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

func verifyRawBinary(raw []byte, targetOS, targetArch string) ([]byte, error) {
	if len(raw) > maxBinarySize {
		return nil, fmt.Errorf("binary exceeds %d bytes", maxBinarySize)
	}
	if err := verifyExecutable(raw, targetOS, targetArch); err != nil {
		return nil, err
	}
	return raw, nil
}

func verifyArchive(raw []byte, binaryName, zshCompletion, targetOS, targetArch, format string) ([]byte, error) {
	var err error
	rawReader := bytes.NewReader(raw)
	gzipReader, err := gzip.NewReader(rawReader)
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	gzipReader.Multistream(false)
	if len(raw) < 10 || !bytes.Equal(raw[:10], []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff}) {
		return nil, errors.New("gzip header is not canonical")
	}
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
	expectedNames := []string{binaryName}
	if format == artifactBundleArchive {
		expectedNames = append(expectedNames, "LICENSE", "README.md")
		if zshCompletion != "" {
			expectedNames = append(expectedNames, zshCompletion)
		}
	} else if format != artifactBinaryArchive {
		return nil, fmt.Errorf("unsupported archive format %q", format)
	}
	var binary []byte
	members := make([][]byte, len(expectedNames))
	for index, expected := range expectedNames {
		header, err := tarReader.Next()
		if err != nil {
			return nil, fmt.Errorf("read tar member %d: %w", index+1, err)
		}
		if err := verifyTarHeader(header, expected, index); err != nil {
			return nil, err
		}
		memberMaximum := int64(maxVerifyMemberSize)
		switch expected {
		case "LICENSE":
			memberMaximum = maxLicenseSize
		case "README.md":
			memberMaximum = maxReadmeSize
		case zshCompletion:
			memberMaximum = maxZshCompletionSize
		}
		if header.Size > memberMaximum {
			return nil, fmt.Errorf("tar member %q exceeds %d bytes", header.Name, memberMaximum)
		}
		member, err := io.ReadAll(io.LimitReader(tarReader, memberMaximum+1))
		if err != nil {
			return nil, fmt.Errorf("read tar member %q: %w", header.Name, err)
		}
		if int64(len(member)) != header.Size {
			return nil, fmt.Errorf("tar member %q size changed", header.Name)
		}
		members[index] = member
		if expected == zshCompletion && (len(member) == 0 || !utf8.Valid(member) || member[len(member)-1] != '\n') {
			return nil, errors.New("Zsh completion must be nonempty UTF-8 ending with a newline")
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
	canonical, err := canonicalTarPayload(expectedNames, members)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(payload, canonical) {
		return nil, errors.New("tar payload is not canonical")
	}
	if err := verifyExecutable(binary, targetOS, targetArch); err != nil {
		return nil, err
	}
	return binary, nil
}

func canonicalTarPayload(names []string, members [][]byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if len(names) != len(members) || len(members) == 0 {
		return nil, fmt.Errorf("canonical tar names = %d, members = %d", len(names), len(members))
	}
	for index, name := range names {
		mode := int64(0o644)
		if index == 0 {
			mode = 0o755
		}
		header := &tar.Header{
			Name:       name,
			Mode:       mode,
			Uid:        0,
			Gid:        0,
			Size:       int64(len(members[index])),
			ModTime:    time.Unix(0, 0).UTC(),
			Typeflag:   tar.TypeReg,
			Uname:      "",
			Gname:      "",
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
			Format:     tar.FormatUSTAR,
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("serialize canonical tar member %q: %w", name, err)
		}
		if _, err := writer.Write(members[index]); err != nil {
			return nil, fmt.Errorf("serialize canonical tar member %q: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("serialize canonical tar: %w", err)
	}
	return buffer.Bytes(), nil
}

func verifyTarHeader(header *tar.Header, expected string, index int) error {
	if header.Name != expected || path.IsAbs(header.Name) || path.Clean(header.Name) != header.Name || strings.HasPrefix(header.Name, "../") || strings.Contains(header.Name, "\\") {
		return fmt.Errorf("tar member %d has unsafe or unexpected name %q", index+1, header.Name)
	}
	if header.Typeflag != tar.TypeReg || header.Linkname != "" || header.Format != tar.FormatUSTAR || header.PAXRecords != nil || header.Xattrs != nil {
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
		wantSubCPU := uint32(3) // CPU_SUBTYPE_X86_64_ALL.
		if targetArch == "arm64" {
			wantSubCPU = 0 // CPU_SUBTYPE_ARM64_ALL.
		}
		if file.Cpu != want || file.SubCpu != wantSubCPU {
			return fmt.Errorf("Mach-O architecture is %s, want %s", file.Cpu, want)
		}
		hasExecutableSegment := false
		for _, load := range file.Loads {
			segment, ok := load.(*macho.Segment)
			if !ok {
				continue
			}
			if segment.Filesz != 0 && segment.Memsz != 0 && segment.Prot&0x4 != 0 { // VM_PROT_EXECUTE.
				hasExecutableSegment = true
				break
			}
		}
		if !hasExecutableSegment {
			return errors.New("Mach-O binary has no nonempty executable segment")
		}
		// debug/macho exposes load commands and segments but does not expose
		// LC_MAIN/LC_UNIXTHREAD entry state. The executable-segment invariant is
		// therefore the strongest entry-structure check available in stdlib.
		return nil
	}
	if targetOS == "windows" {
		if targetArch != "amd64" {
			return fmt.Errorf("unsupported Windows architecture %q", targetArch)
		}
		file, err := pe.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("invalid PE binary: %w", err)
		}
		defer file.Close()
		if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 {
			return errors.New("PE binary is not an amd64 executable image")
		}
		header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
		if !ok || header.Magic != 0x20b || header.AddressOfEntryPoint == 0 {
			return errors.New("PE binary is not PE32+ or has no entry point")
		}
		hasExecutableEntry := false
		for _, section := range file.Sections {
			if section.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE == 0 {
				continue
			}
			start := section.VirtualAddress
			size := section.VirtualSize
			if size < section.Size {
				size = section.Size
			}
			end := start + size
			if end >= start && header.AddressOfEntryPoint >= start && header.AddressOfEntryPoint < end {
				hasExecutableEntry = true
				break
			}
		}
		if !hasExecutableEntry {
			return errors.New("PE entry is not inside an executable section")
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
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Type != elf.ET_EXEC {
		return errors.New("ELF binary is not a 64-bit little-endian ET_EXEC executable")
	}
	if file.OSABI != elf.ELFOSABI_NONE && file.OSABI != elf.ELFOSABI_LINUX {
		return errors.New("ELF binary has unsupported OS ABI")
	}
	want := elf.EM_X86_64
	if targetArch == "arm64" {
		want = elf.EM_AARCH64
	}
	if file.Machine != want {
		return fmt.Errorf("ELF architecture is %s, want %s", file.Machine, want)
	}
	if file.Entry == 0 {
		return errors.New("ELF binary has no entry address")
	}
	hasExecutableEntry := false
	for _, program := range file.Progs {
		if program.Type != elf.PT_LOAD || program.Flags&elf.PF_X == 0 || program.Memsz == 0 {
			continue
		}
		end := program.Vaddr + program.Memsz
		if end < program.Vaddr {
			continue
		}
		if file.Entry >= program.Vaddr && file.Entry < end {
			hasExecutableEntry = true
			break
		}
	}
	if !hasExecutableEntry {
		return errors.New("ELF entry is not inside an executable PT_LOAD")
	}
	return nil
}

func executeVerifiedBinary(executableName, identityName, version, commit, asset string, data []byte) error {
	return executeVerifiedBinaryWithTimeout(executableName, identityName, version, commit, asset, data, verifyCommandTimeout)
}

type liveBoundedBuffer struct {
	mu       sync.Mutex
	data     []byte
	maximum  int
	overflow chan<- struct{}
}

func (b *liveBoundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	remaining := b.maximum - len(b.data)
	if remaining > len(data) {
		remaining = len(data)
	}
	if remaining > 0 {
		b.data = append(b.data, data[:remaining]...)
	}
	overflowed := remaining < len(data)
	b.mu.Unlock()
	if overflowed {
		select {
		case b.overflow <- struct{}{}:
		default:
		}
	}
	return len(data), nil
}

func (b *liveBoundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func executeVerifiedBinaryWithTimeout(executableName, identityName, version, commit, asset string, data []byte, timeout time.Duration) error {
	directory, err := os.MkdirTemp("", ".hextap-verify-*")
	if err != nil {
		return fmt.Errorf("create verification temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, executableName)
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
	overflow := make(chan struct{}, 1)
	stdout := &liveBoundedBuffer{maximum: maxVerifyCommandOutput, overflow: overflow}
	stderr := &liveBoundedBuffer{maximum: maxVerifyCommandOutput, overflow: overflow}
	command := exec.Command(path, "--version")
	command.Dir = directory
	command.Env = verifiedCommandEnvironment()
	command.Stdin = nil
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	prepareVerifiedCommand(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("execute %s: start command: %w", asset, err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err = <-wait:
		select {
		case <-overflow:
			return fmt.Errorf("execute %s: output limit exceeded", asset)
		default:
		}
	case <-overflow:
		terminateVerifiedCommand(command)
		<-wait
		return fmt.Errorf("execute %s: output limit exceeded", asset)
	case <-timer.C:
		terminateVerifiedCommand(command)
		<-wait
		return fmt.Errorf("execute %s: timed out", asset)
	}
	if err != nil {
		return fmt.Errorf("execute %s: command failed", asset)
	}
	stdoutData := stdout.Bytes()
	stderrData := stderr.Bytes()
	stdoutData = normalizeVersionOutput(runtime.GOOS, stdoutData)
	want := identityName + " " + version + " (commit " + commit + ")\n"
	if len(stderrData) != 0 {
		return fmt.Errorf("execute %s: stderr was not empty", asset)
	}
	if string(stdoutData) != want {
		return fmt.Errorf("execute %s: stdout did not match expected version", asset)
	}
	return nil
}

func normalizeVersionOutput(targetOS string, data []byte) []byte {
	if targetOS != "windows" || !bytes.HasSuffix(data, []byte("\r\n")) {
		return data
	}
	result := append([]byte(nil), data[:len(data)-2]...)
	return append(result, '\n')
}

func verifiedCommandEnvironment() []string {
	if runtime.GOOS != "windows" {
		return []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	}
	result := []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	for _, name := range []string{"PATH", "SYSTEMROOT", "TEMP", "TMP"} {
		if value, exists := os.LookupEnv(name); exists {
			result = append(result, name+"="+value)
		}
	}
	return result
}
