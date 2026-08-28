package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/SijanC147/hextap-toolkit/internal/brewcli"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(runNamed(filepath.Base(os.Args[0]), os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runNamed("brew-hextap", args, stdout, stderr)
}

func runNamed(invocation string, args []string, stdout, stderr io.Writer) int {
	return brewcli.RunNamed(invocation, args, stdout, stderr, version, commit)
}
