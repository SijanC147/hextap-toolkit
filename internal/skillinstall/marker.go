package skillinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	markerFileName = ".hextap-install.json"
	markerSchema   = 1
	maxMarkerSize  = 1024 * 1024
)

var (
	sha256Pattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	bundleVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

type installMarker struct {
	Schema       int          `json:"schema"`
	Bundle       string       `json:"bundle"`
	Version      string       `json:"version"`
	BundleSHA256 string       `json:"bundle_sha256"`
	Files        []markerFile `json:"files"`
}

type markerFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func markerForBundle(name, version string, files []bundleFile) (installMarker, error) {
	if name == "" || !bundleVersionPattern.MatchString(version) || len(files) == 0 {
		return installMarker{}, fmt.Errorf("bundle name, strict version, and files are required")
	}
	ordered := append([]bundleFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].name < ordered[j].name })
	bundleHash := sha256.New()
	fmt.Fprintf(bundleHash, "version:%s\n", version)
	markerFiles := make([]markerFile, 0, len(ordered))
	previous := ""
	for _, file := range ordered {
		if !safeBundlePath(file.name) || file.name == markerFileName {
			return installMarker{}, fmt.Errorf("unsafe bundle path %q", file.name)
		}
		if file.name == previous {
			return installMarker{}, fmt.Errorf("duplicate bundle path %q", file.name)
		}
		previous = file.name
		fileHash := sha256.Sum256(file.data)
		encodedHash := hex.EncodeToString(fileHash[:])
		markerFiles = append(markerFiles, markerFile{Path: file.name, SHA256: encodedHash})
		fmt.Fprintf(bundleHash, "%d:%s:%s\n", len(file.name), file.name, encodedHash)
	}
	return installMarker{
		Schema:       markerSchema,
		Bundle:       name,
		Version:      version,
		BundleSHA256: hex.EncodeToString(bundleHash.Sum(nil)),
		Files:        markerFiles,
	}, nil
}

func encodeMarker(name, version string, files []bundleFile) ([]byte, error) {
	marker, err := markerForBundle(name, version, files)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode install marker: %w", err)
	}
	return append(data, '\n'), nil
}

func decodeMarker(data []byte) (installMarker, error) {
	if len(data) > maxMarkerSize {
		return installMarker{}, fmt.Errorf("marker exceeds %d bytes", maxMarkerSize)
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return installMarker{}, fmt.Errorf("decode marker: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker installMarker
	if err := decoder.Decode(&marker); err != nil {
		return installMarker{}, fmt.Errorf("decode marker: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return installMarker{}, fmt.Errorf("decode marker: trailing JSON value")
		}
		return installMarker{}, fmt.Errorf("decode marker: %w", err)
	}
	if err := validateMarker(marker); err != nil {
		return installMarker{}, err
	}
	return marker, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return walkJSONValue(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = true
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object has invalid closing delimiter")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array has invalid closing delimiter")
		}
	default:
		return fmt.Errorf("unexpected closing delimiter %q", delimiter)
	}
	return nil
}

func validateMarker(marker installMarker) error {
	if marker.Schema != markerSchema {
		return fmt.Errorf("unsupported marker schema %d", marker.Schema)
	}
	if marker.Bundle != "hextap" {
		return fmt.Errorf("marker bundle %q is not hextap", marker.Bundle)
	}
	if !bundleVersionPattern.MatchString(marker.Version) {
		return fmt.Errorf("marker bundle version is invalid")
	}
	if !sha256Pattern.MatchString(marker.BundleSHA256) {
		return fmt.Errorf("marker bundle hash is invalid")
	}
	if len(marker.Files) == 0 {
		return fmt.Errorf("marker has no managed files")
	}
	previous := ""
	bundleHash := sha256.New()
	fmt.Fprintf(bundleHash, "version:%s\n", marker.Version)
	for _, file := range marker.Files {
		if !safeBundlePath(file.Path) || file.Path == markerFileName {
			return fmt.Errorf("marker contains unsafe file path %q", file.Path)
		}
		if previous != "" && file.Path <= previous {
			return fmt.Errorf("marker file paths are not strictly sorted")
		}
		if !sha256Pattern.MatchString(file.SHA256) {
			return fmt.Errorf("marker hash for %q is invalid", file.Path)
		}
		fmt.Fprintf(bundleHash, "%d:%s:%s\n", len(file.Path), file.Path, file.SHA256)
		previous = file.Path
	}
	if hex.EncodeToString(bundleHash.Sum(nil)) != marker.BundleSHA256 {
		return fmt.Errorf("marker bundle hash does not match its file hashes")
	}
	return nil
}

func markerMatchesBundle(marker installMarker, current installMarker) bool {
	if marker.Schema != current.Schema || marker.Bundle != current.Bundle || marker.Version != current.Version || marker.BundleSHA256 != current.BundleSHA256 || len(marker.Files) != len(current.Files) {
		return false
	}
	for index := range marker.Files {
		if marker.Files[index] != current.Files[index] {
			return false
		}
	}
	return true
}

func safeBundlePath(name string) bool {
	return name != "." && name != ".." && !path.IsAbs(name) && path.Clean(name) == name && !strings.HasPrefix(name, "../") && !strings.Contains(name, "\\")
}
