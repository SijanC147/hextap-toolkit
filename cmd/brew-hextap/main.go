package main

import (
	"io"
	"os"

	"github.com/SijanC147/hextap-toolkit/internal/brewcli"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return brewcli.Run(args, stdout, stderr, version, commit)
}
