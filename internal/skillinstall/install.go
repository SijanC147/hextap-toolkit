package skillinstall

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type preparedFile struct {
	entry        Entry
	relativePath string
	data         []byte
	priorData    []byte
	priorMode    fs.FileMode
	temporary    string
}

type applyControl struct {
	beforePublish func(index int, entry Entry)
}

// Install plans and optionally installs the embedded Hextap skill for the
// explicitly selected agents. Every target is inspected before the first
// write. New files use create-only atomic publication. Existing managed
// bundles are read-only in this first installer version.
func Install(options Options) (Result, error) {
	return install(options, applyControl{})
}

func install(options Options, control applyControl) (Result, error) {
	targets, err := resolveTargets(options)
	if err != nil {
		return Result{}, err
	}
	rootPath, root, err := openOptionsRoot(options)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	bundle, err := loadBundle()
	if err != nil {
		return Result{}, err
	}

	var prepared []preparedFile
	for _, target := range targets {
		inspection, err := inspectTarget(root, rootPath, target, bundle)
		if err != nil {
			return Result{}, err
		}
		planned, err := planTarget(root, inspection, bundle)
		if err != nil {
			return Result{}, err
		}
		prepared = append(prepared, planned...)
	}
	entries := make([]Entry, len(prepared))
	for index := range prepared {
		entries[index] = prepared[index].entry
	}
	result := Result{Entries: entries}
	if options.DryRun {
		return result, nil
	}
	if err := apply(root, prepared, control); err != nil {
		return Result{}, err
	}
	return result, nil
}

func planTarget(root *os.Root, inspection targetInspection, bundle loadedBundle) ([]preparedFile, error) {
	switch inspection.state {
	case UnmanagedState:
		return nil, fmt.Errorf("agent %q target %q contains an unmanaged Hextap skill; refusing to overwrite it", inspection.target.agent, inspection.absoluteDir)
	case InvalidState:
		return nil, fmt.Errorf("agent %q target %q has an invalid Hextap ownership marker; refusing to write", inspection.target.agent, inspection.absoluteDir)
	case DriftedState:
		return nil, fmt.Errorf("agent %q target %q has drifted from its Hextap ownership marker; refusing to overwrite local changes", inspection.target.agent, inspection.absoluteDir)
	case DifferentState:
		return nil, fmt.Errorf("agent %q target %q contains a different managed Hextap bundle; managed updates are not implemented", inspection.target.agent, inspection.absoluteDir)
	}

	prepared := make([]preparedFile, 0, len(bundle.files)+1)
	for _, file := range bundle.files {
		relative := filepath.Join(inspection.skillDir, filepath.FromSlash(file.name))
		item := preparedFile{
			entry: Entry{
				Action: CreateAction,
				Agent:  inspection.target.agent,
				Path:   filepath.Join(filepath.Dir(inspection.absoluteDir), filepath.Base(inspection.absoluteDir), filepath.FromSlash(file.name)),
				Mode:   installedFileMode,
				Size:   len(file.data),
			},
			relativePath: relative,
			data:         file.data,
		}
		if inspection.state == CurrentState {
			item.priorData = file.data
			item.priorMode = installedFileMode
			item.entry.Action = UnchangedAction
		} else {
			info, err := root.Lstat(relative)
			if err == nil {
				return nil, fmt.Errorf("agent %q target %q is not marker-owned; refusing to overwrite it", inspection.target.agent, item.entry.Path)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("inspect agent %q target %q: %w", inspection.target.agent, item.entry.Path, err)
			}
			_ = info
		}
		prepared = append(prepared, item)
	}

	markerRelative := filepath.Join(inspection.skillDir, markerFileName)
	markerItem := preparedFile{
		entry: Entry{
			Action: CreateAction,
			Agent:  inspection.target.agent,
			Path:   filepath.Join(inspection.absoluteDir, markerFileName),
			Mode:   installedFileMode,
			Size:   len(bundle.markerBytes),
		},
		relativePath: markerRelative,
		data:         bundle.markerBytes,
	}
	if inspection.state != NotInstalledState {
		markerItem.priorData = inspection.markerData
		markerItem.priorMode = inspection.markerMode
		markerItem.entry.Action = UnchangedAction
	}
	prepared = append(prepared, markerItem)
	return prepared, nil
}

func apply(root *os.Root, prepared []preparedFile, control applyControl) (retErr error) {
	changed := make([]*preparedFile, 0, len(prepared))
	defer func() {
		for _, item := range changed {
			if item.temporary != "" {
				if err := root.Remove(item.temporary); retErr == nil && err != nil && !errors.Is(err, fs.ErrNotExist) {
					retErr = fmt.Errorf("remove temporary skill file: %w", err)
				}
			}
		}
	}()

	for index := range prepared {
		item := &prepared[index]
		if item.entry.Action == UnchangedAction {
			continue
		}
		if err := preflightParents(root, item.entry.Agent, filepath.Dir(item.relativePath)); err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.Dir(item.relativePath), 0o755); err != nil {
			return fmt.Errorf("create agent %q skill directory: %w", item.entry.Agent, err)
		}
		temporary, err := stage(root, item.relativePath, item.data)
		if err != nil {
			return fmt.Errorf("stage agent %q target %q: %w", item.entry.Agent, item.entry.Path, err)
		}
		item.temporary = temporary
		changed = append(changed, item)
	}
	for _, item := range prepared {
		if err := revalidate(root, item); err != nil {
			return err
		}
	}
	published := make([]*preparedFile, 0, len(changed))
	for index, item := range changed {
		if control.beforePublish != nil {
			control.beforePublish(index, item.entry)
		}
		err := root.Link(item.temporary, item.relativePath)
		if err != nil {
			return publishedError(published, fmt.Errorf("publish agent %q target %q after %d of %d files: %w", item.entry.Agent, item.entry.Path, index, len(changed), err))
		}
		published = append(published, item)
		if err := syncDirectory(root, filepath.Dir(item.relativePath)); err != nil {
			return publishedError(published, fmt.Errorf("sync agent %q target %q after publication: %w", item.entry.Agent, item.entry.Path, err))
		}
	}
	for _, item := range changed {
		if err := root.Remove(item.temporary); err != nil {
			paths := make([]string, len(published))
			for index, publishedItem := range published {
				paths[index] = publishedItem.entry.Path
			}
			return &PartialInstallError{Cause: fmt.Errorf("remove temporary link for agent %q target %q: %w", item.entry.Agent, item.entry.Path, err), Published: paths}
		}
		item.temporary = ""
	}
	return nil
}

func publishedError(published []*preparedFile, cause error) error {
	if len(published) == 0 {
		return cause
	}
	paths := make([]string, len(published))
	for index, item := range published {
		paths[index] = item.entry.Path
	}
	return &PartialInstallError{Cause: cause, Published: paths}
}

func stage(root *os.Root, destination string, data []byte) (temporary string, retErr error) {
	directory := filepath.Dir(destination)
	base := filepath.Base(destination)
	var file *os.File
	for attempt := 0; attempt < 16; attempt++ {
		suffixBytes := make([]byte, 8)
		if _, err := rand.Read(suffixBytes); err != nil {
			return "", fmt.Errorf("generate temporary name: %w", err)
		}
		temporary = filepath.Join(directory, "."+base+".hextap-tmp-"+hex.EncodeToString(suffixBytes))
		created, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create temporary file: %w", err)
		}
		file = created
		break
	}
	if file == nil {
		return "", fmt.Errorf("could not allocate a unique temporary file")
	}
	closed := false
	defer func() {
		if !closed {
			if err := file.Close(); retErr == nil && err != nil {
				retErr = fmt.Errorf("close temporary file: %w", err)
			}
		}
		if retErr != nil {
			_ = root.Remove(temporary)
		}
	}()
	if err := file.Chmod(installedFileMode); err != nil {
		return "", fmt.Errorf("set temporary file mode: %w", err)
	}
	for remaining := data; len(remaining) != 0; {
		written, err := file.Write(remaining)
		if err != nil {
			return "", fmt.Errorf("write temporary file: %w", err)
		}
		if written == 0 {
			return "", fmt.Errorf("write temporary file: zero-byte write")
		}
		remaining = remaining[written:]
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close temporary file: %w", err)
	}
	closed = true
	return temporary, nil
}

func revalidate(root *os.Root, item preparedFile) error {
	if err := preflightParents(root, item.entry.Agent, filepath.Dir(item.relativePath)); err != nil {
		return fmt.Errorf("target changed after preflight: %w", err)
	}
	info, err := root.Lstat(item.relativePath)
	if item.entry.Action == CreateAction {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("revalidate agent %q target %q: %w", item.entry.Agent, item.entry.Path, err)
		}
		return fmt.Errorf("agent %q target %q appeared after preflight; refusing to replace it", item.entry.Agent, item.entry.Path)
	}
	if err != nil {
		return fmt.Errorf("agent %q target %q changed after preflight: %w", item.entry.Agent, item.entry.Path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != item.priorMode {
		return fmt.Errorf("agent %q target %q changed after preflight", item.entry.Agent, item.entry.Path)
	}
	current, err := root.ReadFile(item.relativePath)
	if err != nil {
		return fmt.Errorf("revalidate agent %q target %q: %w", item.entry.Agent, item.entry.Path, err)
	}
	if !bytes.Equal(current, item.priorData) {
		return fmt.Errorf("agent %q target %q changed after preflight", item.entry.Agent, item.entry.Path)
	}
	return nil
}

func syncDirectory(root *os.Root, relative string) (retErr error) {
	directory, err := root.Open(relative)
	if err != nil {
		return err
	}
	defer func() {
		if err := directory.Close(); retErr == nil && err != nil {
			retErr = err
		}
	}()
	return directory.Sync()
}
