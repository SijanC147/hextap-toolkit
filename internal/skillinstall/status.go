package skillinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type targetInspection struct {
	target      resolvedTarget
	skillDir    string
	absoluteDir string
	state       State
	marker      installMarker
	markerData  []byte
	markerMode  fs.FileMode
}

// Status inspects marker ownership and bundle hashes without writing.
func Status(options Options) (StatusResult, error) {
	targets, err := resolveTargets(options)
	if err != nil {
		return StatusResult{}, err
	}
	rootPath, root, err := openOptionsRoot(options)
	if err != nil {
		return StatusResult{}, err
	}
	defer root.Close()
	bundle, err := loadBundle()
	if err != nil {
		return StatusResult{}, err
	}
	entries := make([]StatusEntry, 0, len(targets))
	for _, target := range targets {
		inspection, err := inspectTarget(root, rootPath, target, bundle)
		if err != nil {
			return StatusResult{}, err
		}
		entries = append(entries, StatusEntry{State: inspection.state, Agent: target.agent, Path: inspection.absoluteDir})
	}
	return StatusResult{Entries: entries}, nil
}

func openOptionsRoot(options Options) (string, *os.Root, error) {
	rootPath := options.HomeDir
	rootLabel := "home"
	if options.Scope == ProjectScope {
		rootPath = options.ProjectDir
		rootLabel = "project"
	}
	if strings.TrimSpace(rootPath) == "" {
		return "", nil, fmt.Errorf("%s directory is required", rootLabel)
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return "", nil, fmt.Errorf("resolve %s directory: %w", rootLabel, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", nil, fmt.Errorf("open %s directory %q: %w", rootLabel, absolute, err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("%s path %q is not a directory", rootLabel, absolute)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return "", nil, fmt.Errorf("open %s directory %q: %w", rootLabel, absolute, err)
	}
	return absolute, root, nil
}

func inspectTarget(root *os.Root, absoluteRoot string, target resolvedTarget, bundle loadedBundle) (targetInspection, error) {
	skillDir := filepath.Join(filepath.FromSlash(target.skillsDir), bundle.name)
	inspection := targetInspection{
		target:      target,
		skillDir:    skillDir,
		absoluteDir: filepath.Join(absoluteRoot, skillDir),
		state:       NotInstalledState,
	}
	if err := preflightParents(root, target.agent, skillDir); err != nil {
		return targetInspection{}, err
	}
	markerPath := filepath.Join(skillDir, markerFileName)
	info, err := root.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) {
		_, directoryErr := root.Lstat(skillDir)
		if errors.Is(directoryErr, fs.ErrNotExist) {
			return inspection, nil
		}
		if directoryErr != nil {
			return targetInspection{}, fmt.Errorf("inspect agent %q skill directory: %w", target.agent, directoryErr)
		}
		inspection.state = UnmanagedState
		return inspection, nil
	}
	if err != nil {
		return targetInspection{}, fmt.Errorf("inspect agent %q marker %q: %w", target.agent, filepath.Join(absoluteRoot, markerPath), err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxMarkerSize {
		inspection.state = InvalidState
		return inspection, nil
	}
	markerData, err := root.ReadFile(markerPath)
	if err != nil {
		return targetInspection{}, fmt.Errorf("read agent %q marker %q: %w", target.agent, filepath.Join(absoluteRoot, markerPath), err)
	}
	marker, err := decodeMarker(markerData)
	if err != nil {
		inspection.state = InvalidState
		return inspection, nil
	}
	canonicalMarker, err := encodeExistingMarker(marker)
	if err != nil || !bytes.Equal(canonicalMarker, markerData) || info.Mode().Perm() != installedFileMode {
		inspection.state = DriftedState
		return inspection, nil
	}
	inspection.marker = marker
	inspection.markerData = markerData
	inspection.markerMode = info.Mode().Perm()
	intact, err := markerFilesAreIntact(root, skillDir, marker)
	if err != nil {
		return targetInspection{}, fmt.Errorf("inspect agent %q managed files: %w", target.agent, err)
	}
	if !intact {
		inspection.state = DriftedState
		return inspection, nil
	}
	if markerMatchesBundle(marker, bundle.marker) {
		inspection.state = CurrentState
	} else {
		inspection.state = DifferentState
	}
	return inspection, nil
}

func markerFilesAreIntact(root *os.Root, skillDir string, marker installMarker) (bool, error) {
	for _, managed := range marker.Files {
		relative := filepath.Join(skillDir, filepath.FromSlash(managed.Path))
		if err := preflightParents(root, "managed", filepath.Dir(relative)); err != nil {
			return false, nil
		}
		info, err := root.Lstat(relative)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != installedFileMode {
			return false, nil
		}
		data, err := root.ReadFile(relative)
		if err != nil {
			return false, err
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != managed.SHA256 {
			return false, nil
		}
	}
	return true, nil
}

func encodeExistingMarker(marker installMarker) ([]byte, error) {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func preflightParents(root *os.Root, agent, relativeDirectory string) error {
	clean := filepath.Clean(relativeDirectory)
	if clean == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect agent %q target parent %q: %w", agent, current, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("agent %q target parent %q is a symbolic link", agent, current)
		}
		if !info.IsDir() {
			return fmt.Errorf("agent %q target parent %q is not a directory", agent, current)
		}
	}
	return nil
}
