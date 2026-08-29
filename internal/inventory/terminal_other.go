//go:build !darwin && !linux

package inventory

import "os"

func fileIsTerminal(*os.File) bool {
	return false
}
