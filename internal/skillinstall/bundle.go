package skillinstall

import (
	"fmt"
	"io/fs"
	"sort"

	toolskills "github.com/SijanC147/hextap-toolkit/skills"
)

type bundleFile struct {
	name string
	data []byte
}

type loadedBundle struct {
	name        string
	version     string
	files       []bundleFile
	marker      installMarker
	markerBytes []byte
}

func loadBundle() (loadedBundle, error) {
	bundle := toolskills.Hextap()
	var files []bundleFile
	err := fs.WalkDir(bundle.Files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("embedded skill contains non-regular file %q", name)
		}
		if !safeBundlePath(name) {
			return fmt.Errorf("embedded skill contains unsafe path %q", name)
		}
		data, err := fs.ReadFile(bundle.Files, name)
		if err != nil {
			return fmt.Errorf("read embedded skill file %q: %w", name, err)
		}
		files = append(files, bundleFile{name: name, data: data})
		return nil
	})
	if err != nil {
		return loadedBundle{}, fmt.Errorf("load embedded %s skill: %w", bundle.Name, err)
	}
	if len(files) == 0 {
		return loadedBundle{}, fmt.Errorf("embedded %s skill contains no files", bundle.Name)
	}
	sort.Slice(files, func(i, j int) bool { return publicationPathLess(files[i].name, files[j].name) })
	marker, err := markerForBundle(bundle.Name, bundle.Version, files)
	if err != nil {
		return loadedBundle{}, err
	}
	markerBytes, err := encodeMarker(bundle.Name, bundle.Version, files)
	if err != nil {
		return loadedBundle{}, err
	}
	return loadedBundle{name: bundle.Name, version: bundle.Version, files: files, marker: marker, markerBytes: markerBytes}, nil
}

func publicationPathLess(left, right string) bool {
	if left == "SKILL.md" {
		return false
	}
	if right == "SKILL.md" {
		return true
	}
	return left < right
}
