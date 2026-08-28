// Package skills exposes the agent-neutral skill bundles shipped inside
// brew-hextap.
package skills

import (
	"embed"
	"io/fs"
)

// Bundle is an immutable, embedded agent skill tree.
type Bundle struct {
	Name    string
	Version string
	Files   fs.FS
}

//go:embed hextap
var bundled embed.FS

// Hextap returns the embedded Hextap skill bundle rooted at its installable
// files. The returned filesystem is read-only.
func Hextap() Bundle {
	files, err := fs.Sub(bundled, "hextap")
	if err != nil {
		panic("embedded Hextap skill is unavailable: " + err.Error())
	}
	return Bundle{Name: "hextap", Version: "1.3.0", Files: files}
}
