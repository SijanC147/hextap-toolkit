package onboard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

var errInjectedApply = errors.New("injected apply failure")

func singleArtifactPlan(path string) ([]artifact, []Entry) {
	return []artifact{{path: path, data: []byte("managed bytes\n"), mode: 0o644, generatedText: true}}, []Entry{{Action: ActionCreate, Path: path}}
}

func TestApplyFileFailuresRemoveInvocationOwnedPartialFileAndParents(t *testing.T) {
	tests := map[string]func(*applyHooks){
		"open": func(hooks *applyHooks) {
			hooks.openFile = func(*os.Root, string, int, fs.FileMode) (*os.File, error) {
				return nil, errInjectedApply
			}
		},
		"after open": func(hooks *applyHooks) {
			hooks.afterFileOpen = func(string, *os.File) error { return errInjectedApply }
		},
		"stat": func(hooks *applyHooks) {
			hooks.fileStat = func(*os.File) (fs.FileInfo, error) { return nil, errInjectedApply }
		},
		"chmod": func(hooks *applyHooks) {
			hooks.fileChmod = func(*os.File, fs.FileMode) error { return errInjectedApply }
		},
		"partial write": func(hooks *applyHooks) {
			hooks.fileWrite = func(file *os.File, data []byte) (int, error) {
				written, err := file.Write(data[:len(data)/2])
				if err != nil {
					return written, err
				}
				return written, errInjectedApply
			}
		},
		"zero short write": func(hooks *applyHooks) {
			hooks.fileWrite = func(*os.File, []byte) (int, error) { return 0, nil }
		},
		"nonzero short write": func(hooks *applyHooks) {
			hooks.fileWrite = func(file *os.File, data []byte) (int, error) {
				return file.Write(data[:len(data)-1])
			}
		},
		"sync": func(hooks *applyHooks) {
			hooks.fileSync = func(*os.File) error { return errInjectedApply }
		},
		"close": func(hooks *applyHooks) {
			hooks.fileClose = func(file *os.File) error {
				_ = file.Close()
				return errInjectedApply
			}
		},
		"verify": func(hooks *applyHooks) {
			hooks.verifyFile = func(*os.Root, string, int64) ([]byte, fs.FileInfo, error) {
				return nil, nil, errInjectedApply
			}
		},
		"parent sync": func(hooks *applyHooks) {
			hooks.syncDirectory = func(*os.Root, string) error { return errInjectedApply }
		},
	}
	for name, inject := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			relative := "created/managed"
			if name == "parent sync" {
				relative = "managed"
			}
			artifacts, entries := singleArtifactPlan(relative)
			hooks := defaultApplyHooks()
			inject(&hooks)
			if err := applyArtifactsWithHooks(root, artifacts, entries, hooks); err == nil {
				t.Fatal("applyArtifactsWithHooks() unexpectedly succeeded")
			}
			for _, candidate := range []string{relative, "created"} {
				if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(candidate))); !os.IsNotExist(err) {
					t.Fatalf("owned residue %s survived %s failure: %v", candidate, name, err)
				}
			}
		})
	}
}

func TestApplyDirectoryFailuresRemoveInvocationOwnedDirectory(t *testing.T) {
	tests := map[string]func(*applyHooks){
		"open": func(hooks *applyHooks) {
			hooks.openDirectory = func(*os.Root, string) (*os.File, error) { return nil, errInjectedApply }
		},
		"after mkdir": func(hooks *applyHooks) {
			hooks.afterDirectoryCreate = func(string, *os.File) error { return errInjectedApply }
		},
		"stat": func(hooks *applyHooks) {
			hooks.directoryStat = func(*os.File) (fs.FileInfo, error) { return nil, errInjectedApply }
		},
		"chmod": func(hooks *applyHooks) {
			hooks.directoryChmod = func(*os.File, fs.FileMode) error { return errInjectedApply }
		},
		"sync": func(hooks *applyHooks) {
			hooks.directorySync = func(*os.File) error { return errInjectedApply }
		},
		"close": func(hooks *applyHooks) {
			hooks.directoryClose = func(directory *os.File) error {
				_ = directory.Close()
				return errInjectedApply
			}
		},
		"parent sync": func(hooks *applyHooks) {
			hooks.syncDirectory = func(*os.Root, string) error { return errInjectedApply }
		},
	}
	for name, inject := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			artifacts, entries := singleArtifactPlan("created/managed")
			hooks := defaultApplyHooks()
			inject(&hooks)
			if err := applyArtifactsWithHooks(root, artifacts, entries, hooks); err == nil {
				t.Fatal("applyArtifactsWithHooks() unexpectedly succeeded")
			}
			if _, err := os.Lstat(filepath.Join(root, "created")); !os.IsNotExist(err) {
				t.Fatalf("owned directory survived %s failure: %v", name, err)
			}
		})
	}
}

func TestApplyCleanupPreservesCompetingReplacement(t *testing.T) {
	root := t.TempDir()
	artifacts, entries := singleArtifactPlan("managed")
	hooks := defaultApplyHooks()
	const replacement = "competing replacement\n"
	hooks.verifyFile = func(project *os.Root, relative string, _ int64) ([]byte, fs.FileInfo, error) {
		if err := project.Remove(relative); err != nil {
			t.Fatalf("remove invocation file: %v", err)
		}
		file, err := project.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatalf("create replacement: %v", err)
		}
		if _, err := file.Write([]byte(replacement)); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return nil, nil, errInjectedApply
	}
	if err := applyArtifactsWithHooks(root, artifacts, entries, hooks); err == nil {
		t.Fatal("applyArtifactsWithHooks() unexpectedly succeeded")
	}
	data, err := os.ReadFile(filepath.Join(root, "managed"))
	if err != nil || string(data) != replacement {
		t.Fatalf("competing replacement was removed or changed: %v, %q", err, data)
	}
}

func TestApplyCleanupRemovesOwnedPathButPreservesCompetingHardLink(t *testing.T) {
	root := t.TempDir()
	artifacts, entries := singleArtifactPlan("managed")
	hooks := defaultApplyHooks()
	hooks.verifyFile = func(project *os.Root, relative string, maximum int64) ([]byte, fs.FileInfo, error) {
		data, info, err := readRootFile(project, relative, maximum)
		if err != nil {
			t.Fatal(err)
		}
		if err := project.Link(relative, "competing-link"); err != nil {
			t.Fatal(err)
		}
		return data, info, errInjectedApply
	}
	if err := applyArtifactsWithHooks(root, artifacts, entries, hooks); err == nil {
		t.Fatal("applyArtifactsWithHooks() unexpectedly succeeded")
	}
	if _, err := os.Lstat(filepath.Join(root, "managed")); !os.IsNotExist(err) {
		t.Fatalf("owned path survived cleanup: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "competing-link"))
	if err != nil || string(data) != "managed bytes\n" {
		t.Fatalf("competing hard link was removed or changed: %v, %q", err, data)
	}
}

func TestApplyCleanupPreservesCompetingDirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	artifacts, entries := singleArtifactPlan("created/managed")
	hooks := defaultApplyHooks()
	hooks.afterDirectoryCreate = func(relative string, _ *os.File) error {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove invocation directory: %v", err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("create replacement directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(path, "replacement"), []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return errInjectedApply
	}
	if err := applyArtifactsWithHooks(root, artifacts, entries, hooks); err == nil {
		t.Fatal("applyArtifactsWithHooks() unexpectedly succeeded")
	}
	data, err := os.ReadFile(filepath.Join(root, "created", "replacement"))
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("competing directory replacement was removed or changed: %v, %q", err, data)
	}
}
