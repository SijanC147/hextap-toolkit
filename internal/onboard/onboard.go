package onboard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

type artifact struct {
	path          string
	data          []byte
	mode          fs.FileMode
	customAdapter bool
	generatedText bool
}

type onboardingState struct {
	root      string
	artifacts []artifact
}

type ownedCreatedFile struct {
	path string
	file *os.File
	info fs.FileInfo
	data []byte
}

type ownedCreatedDirectory struct {
	path      string
	directory *os.File
	info      fs.FileInfo
}

type applyHooks struct {
	openFile             func(*os.Root, string, int, fs.FileMode) (*os.File, error)
	afterFileOpen        func(string, *os.File) error
	fileStat             func(*os.File) (fs.FileInfo, error)
	fileChmod            func(*os.File, fs.FileMode) error
	fileWrite            func(*os.File, []byte) (int, error)
	fileSync             func(*os.File) error
	fileClose            func(*os.File) error
	verifyFile           func(*os.Root, string, int64) ([]byte, fs.FileInfo, error)
	mkdir                func(*os.Root, string, fs.FileMode) error
	openDirectory        func(*os.Root, string) (*os.File, error)
	afterDirectoryCreate func(string, *os.File) error
	directoryStat        func(*os.File) (fs.FileInfo, error)
	directoryChmod       func(*os.File, fs.FileMode) error
	directorySync        func(*os.File) error
	directoryClose       func(*os.File) error
	syncDirectory        func(*os.Root, string) error
}

// Onboard preflights every local artifact, then either reports a dry-run plan
// or creates only absent files with create-only semantics.
func Onboard(options Options) (Result, error) {
	return onboardWithTransactionHooks(options, defaultTransactionHooks())
}

func prepareOnboarding(options Options) (onboardingState, error) {
	root, originRepository, err := resolveProject(options.Project)
	if err != nil {
		return onboardingState{}, err
	}
	return prepareOnboardingResolved(options, root, originRepository)
}

func prepareOnboardingResolved(options Options, root, originRepository string) (onboardingState, error) {
	for _, value := range append([]string{
		options.Repository, options.Formula, options.Binary, options.Description,
		options.License, options.GoPackage, options.VersionSymbol,
		options.CommitSymbol, options.ToolkitVersion, options.ToolkitSHA,
	}, options.RequiredChecks...) {
		if containsCredentialLike(value) {
			return onboardingState{}, errors.New("onboarding input appears to contain a credential and was rejected")
		}
	}
	repository := originRepository
	if options.Repository != "" {
		if _, _, err := parseRepository(options.Repository); err != nil {
			return onboardingState{}, err
		}
		if options.Repository != originRepository {
			return onboardingState{}, fmt.Errorf("repository %q does not match Git remote origin identity %q", options.Repository, originRepository)
		}
		repository = options.Repository
	}
	owner, repositoryName, err := parseRepository(repository)
	if err != nil {
		return onboardingState{}, err
	}
	if owner != supportedOwner {
		return onboardingState{}, fmt.Errorf("repository owner %q is unsupported; the current publisher contract supports only %s", owner, supportedOwner)
	}
	if err := validateToolkitPin(options.ToolkitVersion, options.ToolkitSHA); err != nil {
		return onboardingState{}, err
	}
	checks, err := validateRequiredChecks(options.RequiredChecks)
	if err != nil {
		return onboardingState{}, err
	}

	manifestFile := filepath.Join(root, manifestPath)
	manifestData, project, generatedManifest, err := resolveManifest(manifestFile, repository, repositoryName, options)
	if err != nil {
		return onboardingState{}, err
	}
	adapterArtifact, err := resolveAdapter(root, project, options)
	if err != nil {
		return onboardingState{}, err
	}
	workflow := workflowBytes(options.ToolkitVersion, options.ToolkitSHA)
	mainRuleset, err := mainRulesetBytes(checks)
	if err != nil {
		return onboardingState{}, err
	}
	tagRuleset, err := tagRulesetBytes()
	if err != nil {
		return onboardingState{}, err
	}
	artifacts := []artifact{
		{path: manifestPath, data: manifestData, mode: 0o644, generatedText: generatedManifest},
		adapterArtifact,
		{path: workflowPath, data: workflow, mode: 0o644, generatedText: true},
		{path: tapPath, data: append([]byte(nil), manifestData...), mode: 0o644, generatedText: generatedManifest},
		{path: mainRulesetPath, data: mainRuleset, mode: 0o644, generatedText: true},
		{path: tagRulesetPath, data: tagRuleset, mode: 0o644, generatedText: true},
		{path: setupPath, data: setupDocument(repository, project.Formula.Name, options.ToolkitVersion, options.ToolkitSHA), mode: 0o644, generatedText: true},
	}
	for _, item := range artifacts {
		if len(item.data) > maximumLocalFile {
			return onboardingState{}, fmt.Errorf("generated artifact %s exceeds %d bytes", item.path, maximumLocalFile)
		}
		if item.generatedText && len(item.data) != 0 {
			if err := ensureFinalNewline(item.data, item.path); err != nil {
				return onboardingState{}, err
			}
			if containsCredentialLike(string(item.data)) {
				return onboardingState{}, errors.New("a generated artifact appears to contain a credential and was rejected")
			}
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].path < artifacts[j].path })
	return onboardingState{root: root, artifacts: artifacts}, nil
}

func resolveManifest(path, repository, repositoryName string, options Options) ([]byte, manifest.Manifest, bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		data, _, readErr := readLocalFile(path, "manifest", maximumLocalFile, true)
		if readErr != nil {
			return nil, manifest.Manifest{}, false, readErr
		}
		project, parseErr := parseManifestBytes(data)
		if parseErr != nil {
			return nil, manifest.Manifest{}, false, parseErr
		}
		if manifestContainsCredential(project) {
			return nil, manifest.Manifest{}, false, errors.New("manifest metadata appears to contain a credential and was rejected")
		}
		if project.RepositorySlug() != repository {
			return nil, manifest.Manifest{}, false, fmt.Errorf("manifest repository %q does not match Git remote origin identity %q", project.RepositorySlug(), repository)
		}
		if err := generationFlagsAgree(project, options); err != nil {
			return nil, manifest.Manifest{}, false, err
		}
		return data, project, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, manifest.Manifest{}, false, fmt.Errorf("inspect manifest %q: %w", path, err)
	}
	formula := options.Formula
	if formula == "" {
		formula = repositoryName
	}
	binary := options.Binary
	if binary == "" {
		binary = repositoryName
	}
	if options.Description == "" {
		return nil, manifest.Manifest{}, false, errors.New("--description is required when .hextap.json is absent")
	}
	if options.License == "" {
		return nil, manifest.Manifest{}, false, errors.New("--license is required when .hextap.json is absent")
	}
	owner, name, _ := parseRepository(repository)
	project := manifest.Manifest{
		Schema: manifest.CurrentSchema,
		Formula: manifest.Formula{
			Name:        formula,
			Class:       classForFormula(formula),
			Description: options.Description,
			Homepage:    "https://github.com/" + repository,
			License:     options.License,
			Repository:  manifest.Repository{Owner: owner, Name: name},
			Binary:      binary,
			Assets: manifest.Assets{
				DarwinARM64: formula + "-darwin-arm64.tar.gz",
				DarwinAMD64: formula + "-darwin-amd64.tar.gz",
			},
		},
		Release: manifest.Release{BuildScript: defaultAdapterPath, Linux: options.Linux},
		Homebrew: manifest.Homebrew{
			MacOSOnly: true,
			TestArgs:  []string{"--version"},
			Service:   nil,
			Caveats:   "",
		},
	}
	if err := project.Validate(); err != nil {
		return nil, manifest.Manifest{}, false, err
	}
	if manifestContainsCredential(project) {
		return nil, manifest.Manifest{}, false, errors.New("manifest metadata appears to contain a credential and was rejected")
	}
	data, err := manifestBytes(project)
	if err != nil {
		return nil, manifest.Manifest{}, false, err
	}
	return data, project, true, nil
}

func generationFlagsAgree(project manifest.Manifest, options Options) error {
	tests := []struct {
		name     string
		provided string
		actual   string
		set      bool
	}{
		{"--formula", options.Formula, project.Formula.Name, options.FormulaSet || options.Formula != ""},
		{"--binary", options.Binary, project.Formula.Binary, options.BinarySet || options.Binary != ""},
		{"--description", options.Description, project.Formula.Description, options.DescriptionSet || options.Description != ""},
		{"--license", options.License, project.Formula.License, options.LicenseSet || options.License != ""},
	}
	for _, test := range tests {
		if test.set && test.provided != test.actual {
			return fmt.Errorf("%s conflicts with authoritative .hextap.json", test.name)
		}
	}
	if options.LinuxSet && options.Linux != project.Release.Linux {
		return fmt.Errorf("--linux value conflicts with authoritative .hextap.json value %t", project.Release.Linux)
	}
	return nil
}

func resolveAdapter(root string, project manifest.Manifest, options Options) (artifact, error) {
	relative := project.Release.BuildScript
	if err := inspectArtifactParents(root, relative); err != nil {
		return artifact{}, err
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	_, statErr := os.Lstat(path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return artifact{}, fmt.Errorf("inspect build adapter %q: %w", path, statErr)
	}
	goPackage := options.GoPackage
	if goPackage == "" {
		inferred, inferErr := inferGoPackage(root, project.Formula.Binary)
		if inferErr == nil {
			goPackage = inferred
		} else if !exists {
			return artifact{}, inferErr
		}
	}
	versionSymbol := options.VersionSymbol
	if versionSymbol == "" {
		versionSymbol = "main.version"
	}
	commitSymbol := options.CommitSymbol
	if commitSymbol == "" {
		commitSymbol = "main.commit"
	}
	var expected []byte
	if goPackage != "" {
		if err := validateGoPackage(goPackage); err != nil {
			return artifact{}, err
		}
		if err := validateLinkerSymbol(versionSymbol); err != nil {
			return artifact{}, fmt.Errorf("validate --version-symbol: %w", err)
		}
		if err := validateLinkerSymbol(commitSymbol); err != nil {
			return artifact{}, fmt.Errorf("validate --commit-symbol: %w", err)
		}
		if versionSymbol == commitSymbol {
			return artifact{}, errors.New("version and commit linker symbols must be different")
		}
		expected = adapterBytes(goPackage, versionSymbol, commitSymbol)
	}
	if !exists {
		return artifact{path: relative, data: expected, mode: 0o755, generatedText: true}, nil
	}
	data, info, err := readLocalFile(path, "build adapter", maximumLocalFile, true)
	if err != nil {
		return artifact{}, err
	}
	if info.Mode().Perm()&0o111 == 0 {
		return artifact{}, errors.New("existing build adapter must be executable")
	}
	if hasSpecialFileMode(info) {
		return artifact{}, errors.New("existing build adapter has unsafe special mode bits")
	}
	if goPackage == "" && (options.GoPackageSet || options.VersionSymbolSet || options.CommitSymbolSet) {
		return artifact{}, errors.New("generation-only adapter flags cannot be checked against the existing custom build adapter")
	}
	if len(expected) != 0 && (options.GoPackageSet || options.VersionSymbolSet || options.CommitSymbolSet) && (!bytes.Equal(data, expected) || info.Mode().Perm() != 0o755) {
		return artifact{}, errors.New("generation-only adapter flags conflict with the existing custom build adapter")
	}
	return artifact{path: relative, data: expected, mode: 0o755, customAdapter: true}, nil
}

func preflightArtifacts(root string, artifacts []artifact) ([]Entry, error) {
	entries := make([]Entry, 0, len(artifacts))
	for _, item := range artifacts {
		if err := inspectArtifactParents(root, item.path); err != nil {
			return nil, err
		}
		path := filepath.Join(root, filepath.FromSlash(item.path))
		data, info, err := readLocalFile(path, "managed artifact", maximumLocalFile, true)
		if errors.Is(err, fs.ErrNotExist) {
			entries = append(entries, Entry{Action: ActionCreate, Path: item.path})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("conflict at %s: %w", item.path, err)
		}
		if item.customAdapter {
			if info.Mode().Perm()&0o111 == 0 || hasSpecialFileMode(info) {
				return nil, fmt.Errorf("conflict at %s: custom adapter is not executable", item.path)
			}
			if len(item.data) != 0 && bytes.Equal(data, item.data) {
				if !exactFileMode(info, item.mode) {
					return nil, fmt.Errorf("conflict at %s: generated adapter mode differs", item.path)
				}
				entries = append(entries, Entry{Action: ActionUnchanged, Path: item.path})
				continue
			}
			entries = append(entries, Entry{Action: ActionValidated, Path: item.path})
			continue
		}
		if !bytes.Equal(data, item.data) || !exactFileMode(info, item.mode) {
			return nil, fmt.Errorf("conflict at %s: existing managed file differs in bytes or mode", item.path)
		}
		entries = append(entries, Entry{Action: ActionUnchanged, Path: item.path})
	}
	return entries, nil
}

func inspectArtifactParents(root, relative string) error {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if relative == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != relative {
		return fmt.Errorf("unsafe managed artifact path %q", relative)
	}
	parts := strings.Split(filepath.ToSlash(filepath.Dir(clean)), "/")
	current := root
	if len(parts) == 1 && parts[0] == "." {
		return nil
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe managed artifact path %q", relative)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect parent for %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("conflict at %s: parent component %q must be a real directory", relative, current)
		}
	}
	return nil
}

func defaultApplyHooks() applyHooks {
	return applyHooks{
		openFile: func(root *os.Root, name string, flag int, mode fs.FileMode) (*os.File, error) {
			return root.OpenFile(filepath.FromSlash(name), flag, mode)
		},
		afterFileOpen: func(string, *os.File) error { return nil },
		fileStat:      func(file *os.File) (fs.FileInfo, error) { return file.Stat() },
		fileChmod:     func(file *os.File, mode fs.FileMode) error { return file.Chmod(mode) },
		fileWrite:     func(file *os.File, data []byte) (int, error) { return file.Write(data) },
		fileSync:      func(file *os.File) error { return file.Sync() },
		fileClose:     func(file *os.File) error { return file.Close() },
		verifyFile:    readRootFile,
		mkdir: func(root *os.Root, name string, mode fs.FileMode) error {
			return root.Mkdir(filepath.FromSlash(name), mode)
		},
		openDirectory: func(root *os.Root, name string) (*os.File, error) {
			return root.Open(filepath.FromSlash(name))
		},
		afterDirectoryCreate: func(string, *os.File) error { return nil },
		directoryStat:        func(directory *os.File) (fs.FileInfo, error) { return directory.Stat() },
		directoryChmod:       func(directory *os.File, mode fs.FileMode) error { return directory.Chmod(mode) },
		directorySync:        func(directory *os.File) error { return directory.Sync() },
		directoryClose:       func(directory *os.File) error { return directory.Close() },
		syncDirectory:        syncRootDirectory,
	}
}

func applyArtifacts(rootPath string, artifacts []artifact, entries []Entry) error {
	return applyArtifactsWithHooks(rootPath, artifacts, entries, defaultApplyHooks())
}

func applyArtifactsWithHooks(rootPath string, artifacts []artifact, entries []Entry, hooks applyHooks) (retErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open project root: %w", err)
	}
	createdFiles := make([]ownedCreatedFile, 0)
	createdDirectories := make([]ownedCreatedDirectory, 0)
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, cleanupCreated(root, createdFiles, createdDirectories))
		}
		retErr = errors.Join(retErr, root.Close())
	}()
	actions := make(map[string]Action, len(entries))
	for _, entry := range entries {
		actions[entry.Path] = entry.Action
	}
	for _, item := range artifacts {
		if actions[item.path] != ActionCreate {
			continue
		}
		if err := createArtifactParents(rootPath, root, item.path, &createdDirectories, hooks); err != nil {
			return err
		}
		if err := inspectArtifactParents(rootPath, item.path); err != nil {
			return err
		}
		file, err := hooks.openFile(root, item.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, item.mode.Perm())
		if err != nil {
			return fmt.Errorf("create %s: %w", item.path, err)
		}
		createdFiles = append(createdFiles, ownedCreatedFile{path: item.path, file: file, data: item.data})
		ownedIndex := len(createdFiles) - 1
		identity, identityErr := file.Stat()
		if identityErr != nil {
			return fmt.Errorf("capture ownership for %s: %w", item.path, identityErr)
		}
		createdFiles[ownedIndex].info = identity
		if !identity.Mode().IsRegular() || identity.Mode()&os.ModeSymlink != 0 || hardLinked(identity) {
			return fmt.Errorf("capture ownership for %s: file is not a regular single-link file", item.path)
		}
		if err := hooks.afterFileOpen(item.path, file); err != nil {
			return fmt.Errorf("after creating %s: %w", item.path, err)
		}
		info, statErr := hooks.fileStat(file)
		if statErr != nil {
			return fmt.Errorf("inspect created %s: %w", item.path, statErr)
		}
		if !os.SameFile(identity, info) || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || hardLinked(info) {
			return fmt.Errorf("inspect created %s: file is not a regular single-link file", item.path)
		}
		if err := hooks.fileChmod(file, item.mode.Perm()); err != nil {
			return fmt.Errorf("set mode for %s: %w", item.path, err)
		}
		if err := writeAllWith(file, item.data, hooks.fileWrite); err != nil {
			return fmt.Errorf("write %s: %w", item.path, err)
		}
		if err := hooks.fileSync(file); err != nil {
			return fmt.Errorf("sync %s: %w", item.path, err)
		}
		if err := hooks.fileClose(file); err != nil {
			return fmt.Errorf("close %s: %w", item.path, err)
		}
		if err := inspectArtifactParents(rootPath, item.path); err != nil {
			return err
		}
		data, finalInfo, err := hooks.verifyFile(root, item.path, maximumLocalFile)
		if err != nil || !os.SameFile(info, finalInfo) || !bytes.Equal(data, item.data) || !exactFileMode(finalInfo, item.mode) {
			if err != nil {
				return fmt.Errorf("verify created %s: %w", item.path, err)
			}
			return fmt.Errorf("verify created %s: file identity, bytes, or mode changed", item.path)
		}
		if err := hooks.syncDirectory(root, filepath.ToSlash(filepath.Dir(filepath.FromSlash(item.path)))); err != nil {
			return fmt.Errorf("sync parent for %s: %w", item.path, err)
		}
	}
	return nil
}

func createArtifactParents(rootPath string, root *os.Root, relative string, owned *[]ownedCreatedDirectory, hooks applyHooks) error {
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
	if directory == "." {
		return nil
	}
	parts := strings.Split(directory, "/")
	for index := range parts {
		prefix := strings.Join(parts[:index+1], "/")
		info, err := root.Lstat(filepath.FromSlash(prefix))
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("create parent %s: existing component is not a real directory", prefix)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect parent %s: %w", prefix, err)
		}
		if err := hooks.mkdir(root, prefix, 0o755); err != nil {
			return fmt.Errorf("create parent %s: %w", prefix, err)
		}
		*owned = append(*owned, ownedCreatedDirectory{path: prefix})
		ownedIndex := len(*owned) - 1
		identity, identityErr := root.Lstat(filepath.FromSlash(prefix))
		if identityErr != nil {
			return fmt.Errorf("capture ownership for created parent %s: %w", prefix, identityErr)
		}
		(*owned)[ownedIndex].info = identity
		if !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("capture ownership for created parent %s: not a real directory", prefix)
		}
		directory, err := hooks.openDirectory(root, prefix)
		if err != nil {
			return fmt.Errorf("open created parent %s: %w", prefix, err)
		}
		(*owned)[ownedIndex].directory = directory
		openedIdentity, identityErr := directory.Stat()
		if identityErr != nil || !os.SameFile(identity, openedIdentity) {
			return fmt.Errorf("open created parent %s: identity changed", prefix)
		}
		if err := hooks.afterDirectoryCreate(prefix, directory); err != nil {
			return fmt.Errorf("after creating parent %s: %w", prefix, err)
		}
		createdInfo, err := hooks.directoryStat(directory)
		if err != nil {
			return fmt.Errorf("inspect created parent %s: %w", prefix, err)
		}
		if !os.SameFile(identity, createdInfo) || !createdInfo.IsDir() || createdInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("verify created parent %s: not a real directory", prefix)
		}
		if err := hooks.directoryChmod(directory, 0o755); err != nil {
			return fmt.Errorf("set mode for created parent %s: %w", prefix, err)
		}
		if err := hooks.directorySync(directory); err != nil {
			return fmt.Errorf("sync created parent %s: %w", prefix, err)
		}
		if err := hooks.directoryClose(directory); err != nil {
			return fmt.Errorf("close created parent %s: %w", prefix, err)
		}
		currentInfo, err := root.Lstat(filepath.FromSlash(prefix))
		if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 || !currentInfo.IsDir() || !os.SameFile(createdInfo, currentInfo) || currentInfo.Mode().Perm() != 0o755 {
			return fmt.Errorf("created parent %s changed after creation", prefix)
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(prefix)))
		if err := hooks.syncDirectory(root, parent); err != nil {
			return fmt.Errorf("sync parent for directory %s: %w", prefix, err)
		}
	}
	return nil
}

func writeAllWith(file *os.File, data []byte, write func(*os.File, []byte) (int, error)) error {
	if len(data) == 0 {
		return nil
	}
	written, err := write(file, data)
	if written < 0 || written > len(data) {
		return errors.New("invalid write count")
	}
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func cleanupCreated(root *os.Root, files []ownedCreatedFile, directories []ownedCreatedDirectory) error {
	var cleanupErrors []error
	for index := len(files) - 1; index >= 0; index-- {
		owned := files[index]
		pinned := owned.info
		descriptorLive := false
		if owned.file != nil {
			if currentPinned, err := owned.file.Stat(); err == nil {
				pinned = currentPinned
				descriptorLive = true
			}
		}
		current, err := root.Lstat(filepath.FromSlash(owned.path))
		remove := err == nil && pinned != nil && current.Mode()&os.ModeSymlink == 0 && current.Mode().IsRegular() && os.SameFile(pinned, current)
		// A closed descriptor no longer pins the inode against immediate reuse.
		// Linux filesystems can assign the same device/inode pair to a competing
		// replacement, so require the invocation's expected bytes as a second
		// ownership factor before unlinking that pathname.
		if remove && !descriptorLive {
			candidate, openErr := root.Open(filepath.FromSlash(owned.path))
			if openErr != nil {
				remove = false
			} else {
				candidateInfo, statErr := candidate.Stat()
				candidateData, readErr := io.ReadAll(io.LimitReader(candidate, maximumLocalFile+1))
				closeErr := candidate.Close()
				remove = statErr == nil && readErr == nil && closeErr == nil &&
					os.SameFile(pinned, candidateInfo) && int64(len(candidateData)) <= maximumLocalFile &&
					bytes.Equal(candidateData, owned.data)
			}
		}
		if owned.file != nil {
			if err := owned.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("close owned file %s: %w", owned.path, err))
			}
		}
		if remove {
			current, err = root.Lstat(filepath.FromSlash(owned.path))
			if err == nil && current.Mode()&os.ModeSymlink == 0 && current.Mode().IsRegular() && os.SameFile(pinned, current) {
				if err := root.Remove(filepath.FromSlash(owned.path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("remove owned file %s: %w", owned.path, err))
				}
			}
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		owned := directories[index]
		pinned := owned.info
		if owned.directory != nil {
			if currentPinned, err := owned.directory.Stat(); err == nil {
				pinned = currentPinned
			}
		}
		current, err := root.Lstat(filepath.FromSlash(owned.path))
		remove := err == nil && pinned != nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 && os.SameFile(pinned, current)
		if owned.directory != nil {
			if err := owned.directory.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("close owned directory %s: %w", owned.path, err))
			}
		}
		if remove {
			current, err = root.Lstat(filepath.FromSlash(owned.path))
			if err == nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 && os.SameFile(pinned, current) {
				if err := root.Remove(filepath.FromSlash(owned.path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("remove owned directory %s: %w", owned.path, err))
				}
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func readRootFile(root *os.Root, relative string, maximum int64) ([]byte, fs.FileInfo, error) {
	info, err := root.Lstat(filepath.FromSlash(relative))
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || hardLinked(info) || info.Size() < 0 || info.Size() > maximum {
		return nil, nil, errors.New("rooted file is not a bounded regular single-link file")
	}
	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil || !stableLocalFileInfo(info, openedInfo) || !openedInfo.Mode().IsRegular() || hardLinked(openedInfo) {
		return nil, nil, errors.New("rooted file changed while opening")
	}
	first, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(first)) != openedInfo.Size() || int64(len(first)) > maximum {
		return nil, nil, errors.New("rooted file changed size during first read")
	}
	middleInfo, err := file.Stat()
	if err != nil || !stableLocalFileInfo(openedInfo, middleInfo) {
		return nil, nil, errors.New("rooted file changed after first read")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	second, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, nil, err
	}
	finalInfo, err := file.Stat()
	pathInfo, pathErr := root.Lstat(filepath.FromSlash(relative))
	if err != nil || pathErr != nil || !stableLocalFileInfo(middleInfo, finalInfo) || !stableLocalFileInfo(finalInfo, pathInfo) || !bytes.Equal(first, second) {
		return nil, nil, errors.New("rooted file changed during stable read")
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	closed = true
	return first, finalInfo, nil
}

func syncRootDirectory(root *os.Root, relative string) error {
	if relative == "" {
		relative = "."
	}
	directory, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
