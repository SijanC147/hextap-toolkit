package onboard

import (
	"bytes"
	"errors"
	"fmt"
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
}

type onboardingState struct {
	root      string
	artifacts []artifact
}

type ownedCreatedFile struct {
	path string
	info fs.FileInfo
	data []byte
	mode fs.FileMode
}

type ownedCreatedDirectory struct {
	path string
	info fs.FileInfo
}

// Onboard preflights every local artifact, then either reports a dry-run plan
// or creates only absent files with create-only semantics.
func Onboard(options Options) (Result, error) {
	state, err := prepareOnboarding(options)
	if err != nil {
		return Result{}, err
	}
	entries, err := preflightArtifacts(state.root, state.artifacts)
	if err != nil {
		return Result{}, err
	}
	result := Result{Project: state.root, Entries: entries, DryRun: options.DryRun}
	if options.DryRun {
		return result, nil
	}
	if err := applyArtifacts(state.root, state.artifacts, entries); err != nil {
		return Result{}, err
	}
	return result, nil
}

func prepareOnboarding(options Options) (onboardingState, error) {
	for _, value := range append([]string{
		options.Repository, options.Formula, options.Binary, options.Description,
		options.License, options.GoPackage, options.VersionSymbol,
		options.CommitSymbol, options.ToolkitVersion, options.ToolkitSHA,
	}, options.RequiredChecks...) {
		if containsCredentialLike(value) {
			return onboardingState{}, errors.New("onboarding input appears to contain a credential and was rejected")
		}
	}
	root, originRepository, err := resolveProject(options.Project)
	if err != nil {
		return onboardingState{}, err
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
	manifestData, project, err := resolveManifest(manifestFile, repository, repositoryName, options)
	if err != nil {
		return onboardingState{}, err
	}
	canonicalManifest, err := manifestBytes(project)
	if err != nil {
		return onboardingState{}, err
	}
	if containsCredentialLike(string(canonicalManifest)) {
		return onboardingState{}, errors.New("manifest metadata appears to contain a credential and was rejected")
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
		{path: manifestPath, data: manifestData, mode: 0o644},
		adapterArtifact,
		{path: workflowPath, data: workflow, mode: 0o644},
		{path: tapPath, data: append([]byte(nil), manifestData...), mode: 0o644},
		{path: mainRulesetPath, data: mainRuleset, mode: 0o644},
		{path: tagRulesetPath, data: tagRuleset, mode: 0o644},
		{path: setupPath, data: setupDocument(repository, project.Formula.Name, options.ToolkitVersion, options.ToolkitSHA), mode: 0o644},
	}
	for _, item := range artifacts {
		if len(item.data) > maximumLocalFile {
			return onboardingState{}, fmt.Errorf("generated artifact %s exceeds %d bytes", item.path, maximumLocalFile)
		}
		if len(item.data) != 0 {
			if err := ensureFinalNewline(item.data, item.path); err != nil {
				return onboardingState{}, err
			}
		}
		if containsCredentialLike(string(item.data)) {
			return onboardingState{}, errors.New("a generated artifact appears to contain a credential and was rejected")
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].path < artifacts[j].path })
	return onboardingState{root: root, artifacts: artifacts}, nil
}

func resolveManifest(path, repository, repositoryName string, options Options) ([]byte, manifest.Manifest, error) {
	_, err := os.Lstat(path)
	if err == nil {
		data, _, readErr := readLocalFile(path, "manifest", maximumLocalFile, true)
		if readErr != nil {
			return nil, manifest.Manifest{}, readErr
		}
		project, parseErr := parseManifestBytes(data)
		if parseErr != nil {
			return nil, manifest.Manifest{}, parseErr
		}
		if project.RepositorySlug() != repository {
			return nil, manifest.Manifest{}, fmt.Errorf("manifest repository %q does not match Git remote origin identity %q", project.RepositorySlug(), repository)
		}
		if err := generationFlagsAgree(project, options); err != nil {
			return nil, manifest.Manifest{}, err
		}
		return data, project, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, manifest.Manifest{}, fmt.Errorf("inspect manifest %q: %w", path, err)
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
		return nil, manifest.Manifest{}, errors.New("--description is required when .hextap.json is absent")
	}
	if options.License == "" {
		return nil, manifest.Manifest{}, errors.New("--license is required when .hextap.json is absent")
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
		return nil, manifest.Manifest{}, err
	}
	data, err := manifestBytes(project)
	if err != nil {
		return nil, manifest.Manifest{}, err
	}
	return data, project, nil
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
		return artifact{path: relative, data: expected, mode: 0o755}, nil
	}
	data, info, err := readLocalFile(path, "build adapter", maximumLocalFile, true)
	if err != nil {
		return artifact{}, err
	}
	if err := ensureFinalNewline(data, "existing build adapter"); err != nil {
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

func applyArtifacts(rootPath string, artifacts []artifact, entries []Entry) (retErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open project root: %w", err)
	}
	createdFiles := make([]ownedCreatedFile, 0)
	createdDirectories := make([]ownedCreatedDirectory, 0)
	defer func() {
		if retErr != nil {
			cleanupCreated(rootPath, root, createdFiles, createdDirectories)
		}
		_ = root.Close()
	}()
	actions := make(map[string]Action, len(entries))
	for _, entry := range entries {
		actions[entry.Path] = entry.Action
	}
	for _, item := range artifacts {
		if actions[item.path] != ActionCreate {
			continue
		}
		if err := createArtifactParents(rootPath, root, item.path, &createdDirectories); err != nil {
			return err
		}
		if err := inspectArtifactParents(rootPath, item.path); err != nil {
			return err
		}
		file, err := root.OpenFile(filepath.FromSlash(item.path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, item.mode.Perm())
		if err != nil {
			return fmt.Errorf("create %s: %w", item.path, err)
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return fmt.Errorf("inspect created %s: %w", item.path, statErr)
		}
		createdFiles = append(createdFiles, ownedCreatedFile{path: item.path, info: info, data: item.data, mode: item.mode})
		if err := file.Chmod(item.mode.Perm()); err != nil {
			_ = file.Close()
			return fmt.Errorf("set mode for %s: %w", item.path, err)
		}
		if err := writeAll(file, item.data); err != nil {
			_ = file.Close()
			return fmt.Errorf("write %s: %w", item.path, err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync %s: %w", item.path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close %s: %w", item.path, err)
		}
		if err := inspectArtifactParents(rootPath, item.path); err != nil {
			return err
		}
		data, finalInfo, err := readRootFile(root, item.path, maximumLocalFile)
		if err != nil || !os.SameFile(info, finalInfo) || !bytes.Equal(data, item.data) || !exactFileMode(finalInfo, item.mode) {
			if err != nil {
				return fmt.Errorf("verify created %s: %w", item.path, err)
			}
			return fmt.Errorf("verify created %s: file identity, bytes, or mode changed", item.path)
		}
		if err := syncRootDirectory(root, filepath.ToSlash(filepath.Dir(filepath.FromSlash(item.path)))); err != nil {
			return fmt.Errorf("sync parent for %s: %w", item.path, err)
		}
	}
	return nil
}

func createArtifactParents(rootPath string, root *os.Root, relative string, owned *[]ownedCreatedDirectory) error {
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
		if err := root.Mkdir(filepath.FromSlash(prefix), 0o755); err != nil {
			return fmt.Errorf("create parent %s: %w", prefix, err)
		}
		directory, err := root.Open(filepath.FromSlash(prefix))
		if err != nil {
			return fmt.Errorf("open created parent %s: %w", prefix, err)
		}
		if err := directory.Chmod(0o755); err != nil {
			_ = directory.Close()
			return fmt.Errorf("set mode for created parent %s: %w", prefix, err)
		}
		if err := directory.Sync(); err != nil {
			_ = directory.Close()
			return fmt.Errorf("sync created parent %s: %w", prefix, err)
		}
		createdInfo, err := directory.Stat()
		closeErr := directory.Close()
		if err != nil || closeErr != nil || !createdInfo.IsDir() || createdInfo.Mode()&os.ModeSymlink != 0 || createdInfo.Mode().Perm() != 0o755 {
			return fmt.Errorf("verify created parent %s", prefix)
		}
		currentInfo, err := root.Lstat(filepath.FromSlash(prefix))
		if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(createdInfo, currentInfo) {
			return fmt.Errorf("created parent %s changed before ownership was recorded", prefix)
		}
		*owned = append(*owned, ownedCreatedDirectory{path: prefix, info: createdInfo})
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(prefix)))
		if err := syncRootDirectory(root, parent); err != nil {
			return fmt.Errorf("sync parent for directory %s: %w", prefix, err)
		}
	}
	return nil
}

func writeAll(file *os.File, data []byte) error {
	for len(data) != 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return errors.New("short write")
		}
		data = data[written:]
	}
	return nil
}

func cleanupCreated(rootPath string, root *os.Root, files []ownedCreatedFile, directories []ownedCreatedDirectory) {
	for index := len(files) - 1; index >= 0; index-- {
		owned := files[index]
		if inspectArtifactParents(rootPath, owned.path) != nil {
			continue
		}
		data, info, err := readRootFile(root, owned.path, maximumLocalFile)
		if err != nil || hardLinked(info) || !os.SameFile(owned.info, info) || !bytes.Equal(data, owned.data) || !exactFileMode(info, owned.mode) {
			continue
		}
		_ = root.Remove(filepath.FromSlash(owned.path))
	}
	for index := len(directories) - 1; index >= 0; index-- {
		owned := directories[index]
		if inspectArtifactParents(rootPath, owned.path+"/ownership-check") != nil {
			continue
		}
		info, err := root.Lstat(filepath.FromSlash(owned.path))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(owned.info, info) {
			continue
		}
		_ = root.Remove(filepath.FromSlash(owned.path))
	}
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
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || hardLinked(openedInfo) || openedInfo.Size() != info.Size() {
		return nil, nil, errors.New("rooted file changed while opening")
	}
	data := make([]byte, openedInfo.Size())
	if _, err := file.ReadAt(data, 0); err != nil && len(data) != 0 {
		return nil, nil, err
	}
	return data, openedInfo, nil
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
