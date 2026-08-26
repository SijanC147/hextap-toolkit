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
	"sort"
	"strings"
)

type upgradePlan struct {
	inspection targetInspection
	entry      UpgradeEntry
}

type upgradeControl struct {
	beforeSwap        func(index int, entry UpgradeEntry)
	afterSourceRename func(index int, entry UpgradeEntry, backupPath string)
}

// Upgrade replaces explicitly selected intact older marker-owned Hextap skill
// bundles. It never installs an absent skill, downgrades, repairs drift, or
// deletes the preserved recovery directory.
func Upgrade(options Options) (UpgradeResult, error) {
	return upgrade(options, upgradeControl{})
}

func upgrade(options Options, control upgradeControl) (UpgradeResult, error) {
	targets, err := resolveTargets(options)
	if err != nil {
		return UpgradeResult{}, err
	}
	rootPath, root, err := openOptionsRoot(options)
	if err != nil {
		return UpgradeResult{}, err
	}
	defer root.Close()
	bundle, err := loadBundle()
	if err != nil {
		return UpgradeResult{}, err
	}

	plans := make([]upgradePlan, 0, len(targets))
	entries := make([]UpgradeEntry, 0, len(targets))
	for _, target := range targets {
		inspection, inspectErr := inspectTarget(root, rootPath, target, bundle)
		if inspectErr != nil {
			return UpgradeResult{}, inspectErr
		}
		entry := UpgradeEntry{
			Agent:       target.agent,
			Path:        inspection.absoluteDir,
			ToVersion:   bundle.version,
			FromVersion: inspection.marker.Version,
		}
		switch inspection.state {
		case CurrentState:
			entry.Action = UnchangedAction
			entries = append(entries, entry)
			continue
		case UpdateAvailableState:
			if err := requireExactManagedTree(root, inspection); err != nil {
				return UpgradeResult{}, fmt.Errorf("agent %q target %q: %w", target.agent, inspection.absoluteDir, err)
			}
			entry.Action = UpgradeAction
		case NotInstalledState:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q is not installed; use skills install", target.agent, inspection.absoluteDir)
		case UnmanagedState:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q is unmanaged; refusing to upgrade", target.agent, inspection.absoluteDir)
		case DriftedState:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q is drifted; refusing to upgrade local changes", target.agent, inspection.absoluteDir)
		case InvalidState:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q has an invalid marker; refusing to upgrade", target.agent, inspection.absoluteDir)
		case NewerThanCLIState:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q contains a newer skill version; refusing to downgrade", target.agent, inspection.absoluteDir)
		case SameVersionDifferentState:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q has the available version with different managed bytes; refusing to upgrade", target.agent, inspection.absoluteDir)
		default:
			return UpgradeResult{}, fmt.Errorf("agent %q target %q has unsupported state %q", target.agent, inspection.absoluteDir, inspection.state)
		}
		plans = append(plans, upgradePlan{inspection: inspection, entry: entry})
		entries = append(entries, entry)
	}
	result := UpgradeResult{Entries: entries}
	if options.DryRun || len(plans) == 0 {
		return result, nil
	}

	completed := make([]UpgradeEntry, 0, len(plans))
	recovery := make([]string, 0, len(plans)*2)
	for index, plan := range plans {
		entry, stageRecovery, applyErr := applyUpgrade(root, rootPath, bundle, plan, index, control)
		recovery = append(recovery, stageRecovery...)
		if applyErr != nil {
			if len(recovery) == 0 && len(completed) == 0 {
				return UpgradeResult{}, applyErr
			}
			return UpgradeResult{}, &PartialUpgradeError{
				Cause:         applyErr,
				Completed:     append([]UpgradeEntry(nil), completed...),
				RecoveryPaths: append([]string(nil), recovery...),
			}
		}
		completed = append(completed, entry)
		for resultIndex := range result.Entries {
			if result.Entries[resultIndex].Agent == entry.Agent && result.Entries[resultIndex].Path == entry.Path {
				result.Entries[resultIndex] = entry
				break
			}
		}
	}
	return result, nil
}

func applyUpgrade(root *os.Root, rootPath string, bundle loadedBundle, plan upgradePlan, index int, control upgradeControl) (UpgradeEntry, []string, error) {
	agentRoot := filepath.Dir(filepath.Dir(plan.inspection.skillDir))
	transactionRoot := filepath.Join(agentRoot, ".hextap-transactions")
	if err := ensureTransactionRoot(root, plan.entry.Agent, transactionRoot); err != nil {
		return UpgradeEntry{}, nil, err
	}
	stageRelative, err := createTransactionDirectory(root, transactionRoot, "stage-hextap-")
	if err != nil {
		return UpgradeEntry{}, nil, fmt.Errorf("create skill upgrade staging directory: %w", err)
	}
	stageAbsolute := filepath.Join(rootPath, stageRelative)
	recovery := []string{stageAbsolute}
	if err := writeBundleTree(root, stageRelative, bundle); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("stage skill upgrade: %w", err)
	}
	if err := verifyBundleTree(root, stageRelative, bundle); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("verify staged skill upgrade: %w", err)
	}
	if control.beforeSwap != nil {
		control.beforeSwap(index, plan.entry)
	}
	if err := revalidateUpgradeSource(root, rootPath, bundle, plan); err != nil {
		return UpgradeEntry{}, recovery, err
	}
	backupRelative, err := unusedTransactionPath(root, transactionRoot, "backup-hextap-"+plan.entry.FromVersion+"-")
	if err != nil {
		return UpgradeEntry{}, recovery, err
	}
	backupAbsolute := filepath.Join(rootPath, backupRelative)
	if err := root.Rename(plan.inspection.skillDir, backupRelative); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("preserve existing managed skill at %q: %w", backupAbsolute, err)
	}
	recovery = append(recovery, backupAbsolute)
	if control.afterSourceRename != nil {
		control.afterSourceRename(index, plan.entry, backupAbsolute)
	}
	if err := verifyPreservedManagedTree(root, backupRelative, plan.inspection.marker, plan.inspection.markerData); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("preserved managed skill changed during swap: %w", err)
	}
	if err := syncDirectory(root, filepath.Dir(plan.inspection.skillDir)); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("sync skill directory after preserving previous version: %w", err)
	}
	if err := syncDirectory(root, transactionRoot); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("sync transaction directory after preserving previous version: %w", err)
	}
	if err := root.Rename(stageRelative, plan.inspection.skillDir); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("publish staged skill upgrade: %w", err)
	}
	recovery[0] = backupAbsolute
	recovery = recovery[:1]
	if err := syncDirectory(root, filepath.Dir(plan.inspection.skillDir)); err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("sync skill directory after upgrade publication: %w", err)
	}
	fresh, err := inspectTarget(root, rootPath, plan.inspection.target, bundle)
	if err != nil {
		return UpgradeEntry{}, recovery, fmt.Errorf("verify upgraded skill: %w", err)
	}
	if fresh.state != CurrentState {
		return UpgradeEntry{}, recovery, fmt.Errorf("verify upgraded skill: state is %s", fresh.state)
	}
	entry := plan.entry
	entry.BackupPath = backupAbsolute
	return entry, recovery, nil
}

func revalidateUpgradeSource(root *os.Root, rootPath string, bundle loadedBundle, plan upgradePlan) error {
	fresh, err := inspectTarget(root, rootPath, plan.inspection.target, bundle)
	if err != nil {
		return fmt.Errorf("revalidate skill upgrade source: %w", err)
	}
	if fresh.state != UpdateAvailableState || !bytes.Equal(fresh.markerData, plan.inspection.markerData) {
		return fmt.Errorf("agent %q target %q changed after preflight", plan.entry.Agent, plan.entry.Path)
	}
	if err := requireExactManagedTree(root, fresh); err != nil {
		return fmt.Errorf("agent %q target %q changed after preflight: %w", plan.entry.Agent, plan.entry.Path, err)
	}
	return nil
}

func requireExactManagedTree(root *os.Root, inspection targetInspection) error {
	return requireExactManagedTreeAt(root, inspection.skillDir, inspection.marker)
}

func requireExactManagedTreeAt(root *os.Root, skillDir string, marker installMarker) error {
	rootInfo, err := root.Lstat(skillDir)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&fs.ModeSymlink != 0 || rootInfo.Mode().Perm() != 0o755 {
		return fmt.Errorf("managed skill root must be a mode-0755 directory")
	}
	actual, err := collectTreePaths(root, skillDir)
	if err != nil {
		return err
	}
	expected := map[string]bool{markerFileName: true}
	for _, file := range marker.Files {
		expected[filepath.FromSlash(file.Path)] = true
		parent := filepath.Dir(filepath.FromSlash(file.Path))
		for parent != "." {
			expected[parent+string(filepath.Separator)] = true
			parent = filepath.Dir(parent)
		}
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("managed skill contains untracked paths")
	}
	for _, path := range actual {
		if !expected[path] {
			return fmt.Errorf("managed skill contains untracked path %q", path)
		}
	}
	return nil
}

func verifyBundleTree(root *os.Root, stage string, bundle loadedBundle) error {
	markerData, err := root.ReadFile(filepath.Join(stage, markerFileName))
	if err != nil || !bytes.Equal(markerData, bundle.markerBytes) {
		return fmt.Errorf("staged ownership marker differs")
	}
	if intact, err := markerFilesAreIntact(root, stage, bundle.marker); err != nil || !intact {
		return fmt.Errorf("staged managed files differ")
	}
	return requireExactManagedTreeAt(root, stage, bundle.marker)
}

func verifyPreservedManagedTree(root *os.Root, skillDir string, marker installMarker, markerData []byte) error {
	data, err := root.ReadFile(filepath.Join(skillDir, markerFileName))
	if err != nil || !bytes.Equal(data, markerData) {
		return fmt.Errorf("preserved ownership marker differs")
	}
	if intact, err := markerFilesAreIntact(root, skillDir, marker); err != nil || !intact {
		return fmt.Errorf("preserved managed files differ")
	}
	return requireExactManagedTreeAt(root, skillDir, marker)
}

func collectTreePaths(root *os.Root, skillDir string) ([]string, error) {
	var result []string
	var visit func(relative, display string) error
	visit = func(relative, display string) error {
		directory, err := root.Open(relative)
		if err != nil {
			return err
		}
		entries, readErr := directory.ReadDir(-1)
		closeErr := directory.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			childRelative := filepath.Join(relative, entry.Name())
			childDisplay := filepath.Join(display, entry.Name())
			info, err := root.Lstat(childRelative)
			if err != nil {
				return err
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("managed skill contains symbolic link %q", childDisplay)
			}
			if info.IsDir() {
				if info.Mode().Perm() != 0o755 {
					return fmt.Errorf("managed skill directory %q must have mode 0755", childDisplay)
				}
				result = append(result, childDisplay+string(filepath.Separator))
				if err := visit(childRelative, childDisplay); err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("managed skill contains non-regular path %q", childDisplay)
			}
			result = append(result, childDisplay)
		}
		return nil
	}
	if err := visit(skillDir, ""); err != nil {
		return nil, err
	}
	return result, nil
}

func ensureTransactionRoot(root *os.Root, agent, relative string) error {
	if err := preflightParents(root, agent, relative); err != nil {
		return err
	}
	if err := root.MkdirAll(relative, 0o700); err != nil {
		return fmt.Errorf("create transaction root: %w", err)
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("transaction root %q must be a mode-0700 directory", relative)
	}
	return nil
}

func createTransactionDirectory(root *os.Root, parent, prefix string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		path, err := unusedTransactionPath(root, parent, prefix)
		if err != nil {
			return "", err
		}
		if err := root.Mkdir(path, 0o755); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate transaction directory")
}

func unusedTransactionPath(root *os.Root, parent, prefix string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		candidate := filepath.Join(parent, prefix+hex.EncodeToString(random))
		_, err := root.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate unused transaction path")
}

func writeBundleTree(root *os.Root, stage string, bundle loadedBundle) error {
	files := append([]bundleFile(nil), bundle.files...)
	files = append(files, bundleFile{name: markerFileName, data: bundle.markerBytes})
	for _, file := range files {
		relative := filepath.Join(stage, filepath.FromSlash(file.name))
		if err := root.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
			return err
		}
		handle, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, installedFileMode)
		if err != nil {
			return err
		}
		for remaining := file.data; len(remaining) != 0; {
			written, writeErr := handle.Write(remaining)
			if writeErr != nil {
				handle.Close()
				return writeErr
			}
			if written == 0 {
				handle.Close()
				return fmt.Errorf("zero-byte staged skill write")
			}
			remaining = remaining[written:]
		}
		if err := handle.Sync(); err != nil {
			handle.Close()
			return err
		}
		if err := handle.Close(); err != nil {
			return err
		}
	}
	return syncBundleDirectories(root, stage, files)
}

func syncBundleDirectories(root *os.Root, stage string, files []bundleFile) error {
	directories := map[string]bool{stage: true}
	for _, file := range files {
		directory := filepath.Dir(filepath.Join(stage, filepath.FromSlash(file.name)))
		for directory != "." && strings.HasPrefix(directory, stage) {
			directories[directory] = true
			if directory == stage {
				break
			}
			directory = filepath.Dir(directory)
		}
	}
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, directory := range ordered {
		if err := syncDirectory(root, directory); err != nil {
			return err
		}
	}
	return nil
}
