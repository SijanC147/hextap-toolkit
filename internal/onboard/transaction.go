package onboard

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	onboardLockPath  = ".hextap-onboard.lock"
	onboardStagePath = ".hextap-onboard-stage"
)

type transactionHooks struct {
	apply             applyHooks
	afterPreflight    func(*os.Root) error
	afterLock         func(*os.Root) error
	beforePublication func(*os.Root) error
	beforePublish     func(*os.Root, string, int) error
	beforeSuccess     func(*os.Root) error
	cleanupFailure    func() error
}

type transactionLock struct {
	info fs.FileInfo
}

type transactionSnapshot struct {
	path      string
	info      fs.FileInfo
	digest    [sha256.Size]byte
	directory bool
	links     uint64
	hasLinks  bool
}

type stagedArtifact struct {
	artifact artifact
	path     string
	info     fs.FileInfo
}

func defaultTransactionHooks() transactionHooks {
	return transactionHooks{
		apply:             defaultApplyHooks(),
		afterPreflight:    func(*os.Root) error { return nil },
		afterLock:         func(*os.Root) error { return nil },
		beforePublication: func(*os.Root) error { return nil },
		beforePublish:     func(*os.Root, string, int) error { return nil },
		beforeSuccess:     func(*os.Root) error { return nil },
		cleanupFailure:    func() error { return nil },
	}
}

func onboardWithTransactionHooks(options Options, hooks transactionHooks) (result Result, retErr error) {
	rootPath, originRepository, err := resolveProject(options.Project)
	if err != nil {
		return Result{}, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Result{}, fmt.Errorf("open anchored project root: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return Result{}, fmt.Errorf("inspect anchored project root: %w", err)
	}
	state, err := prepareOnboardingResolved(options, rootPath, originRepository)
	if err != nil {
		return Result{}, err
	}
	entries, err := preflightArtifacts(state.root, state.artifacts)
	if err != nil {
		return Result{}, err
	}
	snapshots, err := captureTransactionSnapshots(root, state.artifacts, entries)
	if err != nil {
		return Result{}, err
	}
	result = Result{Project: state.root, Entries: entries, DryRun: options.DryRun}
	if options.DryRun {
		return result, nil
	}
	if err := hooks.afterPreflight(root); err != nil {
		return Result{}, fmt.Errorf("after onboarding preflight: %w", err)
	}
	lock, err := acquireTransactionLock(root)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		retErr = errors.Join(retErr, releaseTransactionLock(root, lock), hooks.cleanupFailure())
	}()
	if err := hooks.afterLock(root); err != nil {
		return Result{}, fmt.Errorf("after acquiring onboarding lock: %w", err)
	}
	if err := validateAnchoredRoot(rootPath, root, rootInfo); err != nil {
		return Result{}, err
	}
	if err := revalidateTransactionSnapshots(root, snapshots, true); err != nil {
		return Result{}, err
	}
	if err := stageAndPublish(rootPath, root, rootInfo, state.artifacts, entries, snapshots, hooks); err != nil {
		return Result{}, err
	}
	return result, nil
}

func captureTransactionSnapshots(root *os.Root, artifacts []artifact, entries []Entry) ([]transactionSnapshot, error) {
	actions := make(map[string]Action, len(entries))
	for _, entry := range entries {
		actions[entry.Path] = entry.Action
	}
	result := make([]transactionSnapshot, 0)
	seenParents := make(map[string]struct{})
	for _, item := range artifacts {
		if actions[item.path] != ActionCreate {
			data, info, err := readRootFile(root, item.path, maximumLocalFile)
			if err != nil {
				return nil, fmt.Errorf("snapshot managed target %s: %w", item.path, err)
			}
			switch actions[item.path] {
			case ActionUnchanged:
				if !bytes.Equal(data, item.data) || !exactFileMode(info, item.mode) {
					return nil, fmt.Errorf("snapshot unchanged target no longer matches: %s", item.path)
				}
			case ActionValidated:
				if info.Mode().Perm()&0o111 == 0 || hasSpecialFileMode(info) {
					return nil, fmt.Errorf("snapshot validated adapter is no longer executable: %s", item.path)
				}
			}
			links, hasLinks := statLinkCount(info)
			result = append(result, transactionSnapshot{path: item.path, info: info, digest: sha256.Sum256(data), links: links, hasLinks: hasLinks})
		}
		parts := strings.Split(filepath.ToSlash(filepath.Dir(filepath.FromSlash(item.path))), "/")
		if len(parts) == 1 && parts[0] == "." {
			continue
		}
		for index := range parts {
			parent := strings.Join(parts[:index+1], "/")
			if _, exists := seenParents[parent]; exists {
				continue
			}
			info, err := root.Lstat(filepath.FromSlash(parent))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("snapshot managed parent %s: parent is not a real directory", parent)
			}
			links, hasLinks := statLinkCount(info)
			result = append(result, transactionSnapshot{path: parent, info: info, directory: true, links: links, hasLinks: hasLinks})
			seenParents[parent] = struct{}{}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].path < result[j].path })
	return result, nil
}

func revalidateTransactionSnapshots(root *os.Root, snapshots []transactionSnapshot, strictLinks bool) error {
	for _, snapshot := range snapshots {
		if snapshot.directory {
			info, err := root.Lstat(filepath.FromSlash(snapshot.path))
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(snapshot.info, info) || snapshot.info.Mode() != info.Mode() {
				return fmt.Errorf("onboarding parent snapshot changed: %s", snapshot.path)
			}
			if strictLinks && snapshot.hasLinks {
				links, ok := statLinkCount(info)
				if !ok || links != snapshot.links {
					return fmt.Errorf("onboarding parent link snapshot changed: %s", snapshot.path)
				}
			}
			continue
		}
		data, info, err := readRootFile(root, snapshot.path, maximumLocalFile)
		if err != nil || !stableLocalFileInfo(snapshot.info, info) || sha256.Sum256(data) != snapshot.digest {
			return fmt.Errorf("onboarding managed target snapshot changed: %s", snapshot.path)
		}
	}
	return nil
}

func validateAnchoredRoot(rootPath string, root *os.Root, expected fs.FileInfo) error {
	anchored, err := root.Stat(".")
	pathInfo, pathErr := os.Lstat(rootPath)
	if err != nil || pathErr != nil || !anchored.IsDir() || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, anchored) || !os.SameFile(anchored, pathInfo) || expected.Mode() != anchored.Mode() || anchored.Mode() != pathInfo.Mode() {
		return errors.New("anchored project root identity or mode changed")
	}
	return nil
}

func acquireTransactionLock(root *os.Root) (transactionLock, error) {
	file, err := root.OpenFile(onboardLockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return transactionLock{}, fmt.Errorf("acquire onboarding lock: %w", err)
	}
	info, statErr := file.Stat()
	if statErr == nil {
		statErr = file.Chmod(0o600)
	}
	if statErr == nil {
		statErr = file.Sync()
	}
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		cleanupErr := removeTransactionLockIfOwned(root, info)
		return transactionLock{}, errors.Join(fmt.Errorf("initialize onboarding lock: %w", statErr), closeErr, cleanupErr)
	}
	if err := syncRootDirectory(root, "."); err != nil {
		return transactionLock{}, errors.Join(fmt.Errorf("sync onboarding lock: %w", err), removeTransactionLockIfOwned(root, info))
	}
	return transactionLock{info: info}, nil
}

func removeTransactionLockIfOwned(root *os.Root, expected fs.FileInfo) error {
	current, err := root.Lstat(onboardLockPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect failed onboarding lock cleanup: %w", err)
	}
	if expected == nil || !os.SameFile(expected, current) {
		return errors.New("failed onboarding lock cleanup preserved a replacement")
	}
	if err := root.Remove(onboardLockPath); err != nil {
		return fmt.Errorf("remove failed onboarding lock: %w", err)
	}
	if err := syncRootDirectory(root, "."); err != nil {
		return fmt.Errorf("sync failed onboarding lock cleanup: %w", err)
	}
	return nil
}

func releaseTransactionLock(root *os.Root, lock transactionLock) error {
	info, err := root.Lstat(onboardLockPath)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("onboarding lock disappeared before release")
	}
	if err != nil || lock.info == nil || !info.Mode().IsRegular() || !os.SameFile(lock.info, info) {
		return errors.New("onboarding lock was replaced before release")
	}
	if err := root.Remove(onboardLockPath); err != nil {
		return fmt.Errorf("remove onboarding lock: %w", err)
	}
	if err := syncRootDirectory(root, "."); err != nil {
		return fmt.Errorf("sync onboarding lock removal: %w", err)
	}
	return nil
}

func stageAndPublish(rootPath string, root *os.Root, rootInfo fs.FileInfo, artifacts []artifact, entries []Entry, snapshots []transactionSnapshot, hooks transactionHooks) (retErr error) {
	actions := make(map[string]Action, len(entries))
	for _, entry := range entries {
		actions[entry.Path] = entry.Action
	}
	if err := hooks.apply.mkdir(root, onboardStagePath, 0o700); err != nil {
		return fmt.Errorf("create onboarding staging directory: %w", err)
	}
	stageInfo, err := root.Lstat(onboardStagePath)
	if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("capture onboarding staging directory identity")
	}
	stageDirectory := ownedCreatedDirectory{path: onboardStagePath, info: stageInfo}
	stagedFiles := make([]ownedCreatedFile, 0)
	staged := make([]stagedArtifact, 0)
	createdParents := make([]ownedCreatedDirectory, 0)
	published := make([]string, 0)
	publishedRecords := make([]stagedArtifact, 0)
	defer func() {
		directories := []ownedCreatedDirectory{stageDirectory}
		if len(published) == 0 {
			directories = append(directories, createdParents...)
		}
		cleanupErr := cleanupCreated(root, stagedFiles, directories)
		if cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("cleanup onboarding staging: %w", cleanupErr))
		}
		if retErr != nil && len(published) != 0 {
			retErr = fmt.Errorf("onboarding publication retained prefix %v: %w", published, retErr)
		}
	}()
	stageHandle, err := hooks.apply.openDirectory(root, onboardStagePath)
	if err != nil {
		return fmt.Errorf("open onboarding staging directory: %w", err)
	}
	stageDirectory.directory = stageHandle
	if err := hooks.apply.directoryChmod(stageHandle, 0o700); err != nil {
		return errors.Join(fmt.Errorf("chmod onboarding staging directory: %w", err), stageHandle.Close())
	}
	if err := hooks.apply.directorySync(stageHandle); err != nil {
		return errors.Join(fmt.Errorf("sync onboarding staging directory: %w", err), stageHandle.Close())
	}
	stageInfo, err = stageHandle.Stat()
	closeErr := hooks.apply.directoryClose(stageHandle)
	if err != nil || closeErr != nil || stageInfo.Mode().Perm() != 0o700 {
		return errors.Join(errors.New("verify onboarding staging directory"), err, closeErr)
	}
	createIndex := 0
	for _, item := range artifacts {
		if actions[item.path] != ActionCreate {
			continue
		}
		stagePath := fmt.Sprintf("%s/file-%03d", onboardStagePath, createIndex)
		createIndex++
		file, err := hooks.apply.openFile(root, stagePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, item.mode.Perm())
		if err != nil {
			return fmt.Errorf("create staged artifact %s: %w", item.path, err)
		}
		identity, err := file.Stat()
		stagedFiles = append(stagedFiles, ownedCreatedFile{path: stagePath, file: file, info: identity})
		if err != nil || !identity.Mode().IsRegular() || hardLinked(identity) {
			return fmt.Errorf("capture staged artifact %s identity", item.path)
		}
		if err := hooks.apply.fileChmod(file, item.mode.Perm()); err != nil {
			return fmt.Errorf("chmod staged artifact %s: %w", item.path, err)
		}
		if err := writeAllWith(file, item.data, hooks.apply.fileWrite); err != nil {
			return fmt.Errorf("write staged artifact %s: %w", item.path, err)
		}
		if err := hooks.apply.fileSync(file); err != nil {
			return fmt.Errorf("sync staged artifact %s: %w", item.path, err)
		}
		if err := hooks.apply.fileClose(file); err != nil {
			return fmt.Errorf("close staged artifact %s: %w", item.path, err)
		}
		data, finalInfo, err := hooks.apply.verifyFile(root, stagePath, maximumLocalFile)
		if err != nil || !os.SameFile(identity, finalInfo) || !exactFileMode(finalInfo, item.mode) || !bytes.Equal(data, item.data) {
			return fmt.Errorf("verify staged artifact %s", item.path)
		}
		staged = append(staged, stagedArtifact{artifact: item, path: stagePath, info: finalInfo})
	}
	if err := syncRootDirectory(root, onboardStagePath); err != nil {
		return fmt.Errorf("sync onboarding staging directory: %w", err)
	}
	if err := hooks.beforePublication(root); err != nil {
		return fmt.Errorf("before onboarding publication: %w", err)
	}
	if err := validateAnchoredRoot(rootPath, root, rootInfo); err != nil {
		return err
	}
	if err := revalidateTransactionSnapshots(root, snapshots, true); err != nil {
		return err
	}
	for _, item := range artifacts {
		if actions[item.path] == ActionCreate {
			if err := createArtifactParents(rootPath, root, item.path, &createdParents, hooks.apply); err != nil {
				return err
			}
		}
	}
	if err := validateAnchoredRoot(rootPath, root, rootInfo); err != nil {
		return err
	}
	if err := revalidateTransactionSnapshots(root, snapshots, false); err != nil {
		return err
	}
	for index, item := range staged {
		if err := hooks.beforePublish(root, item.artifact.path, index); err != nil {
			return fmt.Errorf("before publishing %s: %w", item.artifact.path, err)
		}
		if err := root.Link(filepath.FromSlash(item.path), filepath.FromSlash(item.artifact.path)); err != nil {
			return fmt.Errorf("publish %s create-only: %w", item.artifact.path, err)
		}
		publishedInfo, err := root.Lstat(filepath.FromSlash(item.artifact.path))
		if err != nil || !os.SameFile(item.info, publishedInfo) {
			return fmt.Errorf("verify published %s identity", item.artifact.path)
		}
		published = append(published, item.artifact.path)
		publishedRecords = append(publishedRecords, item)
		if err := root.Remove(filepath.FromSlash(item.path)); err != nil {
			return fmt.Errorf("remove staged link for %s: %w", item.artifact.path, err)
		}
		if err := hooks.apply.syncDirectory(root, filepath.ToSlash(filepath.Dir(filepath.FromSlash(item.artifact.path)))); err != nil {
			return fmt.Errorf("sync published %s: %w", item.artifact.path, err)
		}
	}
	if err := hooks.beforeSuccess(root); err != nil {
		return fmt.Errorf("before onboarding success: %w", err)
	}
	if err := validateAnchoredRoot(rootPath, root, rootInfo); err != nil {
		return err
	}
	if err := revalidateTransactionSnapshots(root, snapshots, false); err != nil {
		return err
	}
	for _, item := range publishedRecords {
		data, info, err := readRootFile(root, item.artifact.path, maximumLocalFile)
		if err != nil || !os.SameFile(item.info, info) || !exactFileMode(info, item.artifact.mode) || !bytes.Equal(data, item.artifact.data) {
			return fmt.Errorf("published onboarding artifact changed before success: %s", item.artifact.path)
		}
	}
	return nil
}
