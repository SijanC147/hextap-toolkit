package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/gitmodules"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

// runReleaseSubmodules checks the submodules the TAGGED source declares
// against the list the manifest seals, and fails the release when they
// disagree.
//
// It exists because actions/checkout presents the submodule credential to
// every repository the tagged .gitmodules names. Without this the set of
// repositories that credential reaches is chosen by the tagged commit, and a
// commit that edits .gitmodules makes the checkout clone any private
// repository the credential can read (SB23-2504). The quality job then runs
// project-declared commands, with network, against a tree holding that
// repository's contents, so the attack never needs the credential to leak.
//
// The workflow runs this in the validate job, after the source is detached to
// the resolved tag and the manifest is sealed, and before either
// credential-bearing checkout exists.
func runReleaseSubmodules(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("release submodules")
	manifestPath := flags.String("manifest", "", "sealed project manifest path")
	gitmodulesPath := flags.String("gitmodules", "", "tagged .gitmodules path")
	serverURL := flags.String("server-url", "", "the caller's own server, e.g. https://github.com")
	if err := flags.Parse(args); err != nil {
		return fail(stderr, "release submodules: %v", err)
	}
	if flags.NArg() != 0 {
		return fail(stderr, "release submodules: unexpected positional arguments")
	}
	if missing := missingFlags([]namedValue{
		{"--manifest", *manifestPath},
		{"--gitmodules", *gitmodulesPath},
		{"--server-url", *serverURL},
	}); len(missing) != 0 {
		return fail(stderr, "release submodules: required flag missing: %s", strings.Join(missing, ", "))
	}

	project, err := readManifest(*manifestPath)
	if err != nil {
		return fail(stderr, "check submodules: %v", err)
	}

	mode := project.Release.SubmodulesMode()
	if mode == manifest.SubmodulesNone {
		// Nothing is fetched, so the credential is presented to nothing, and
		// the manifest already refuses a sealed list in this mode. The
		// caller's input is held to this same value by the step that binds
		// it to the sealed manifest, so this is not a way to opt out.
		fmt.Fprintf(stdout, "submodules checked: release.checkout.submodules is %q, so no submodule is fetched and no credential is presented\n", mode)
		return 0
	}

	// The seal covers exactly one file: the top-level .gitmodules at the tag.
	// Under "recursive", actions/checkout also updates submodules of
	// submodules, whose URLs come from a nested .gitmodules inside a sealed
	// submodule, at the gitlink commit the tag pins. This check never reads
	// that file, and reading it would mean cloning the submodule first, which
	// is the very step the credential is presented at. So the set below the
	// top level is decided by the tagged gitlink and not by this list, which
	// is the same defect one level down.
	//
	// Refusing is the honest position while the nested set is unsealed.
	// "true" fetches the top-level submodules, which the list does bound.
	// Raised by the security reviewer of PR #30 as P1; sealing the nested set
	// is SB23-2560.
	if mode == manifest.SubmodulesRecursive {
		return fail(stderr, "check submodules: release.checkout.submodules is %q, and this check reads only the top-level .gitmodules. "+
			"A nested .gitmodules, at the gitlink commit the tag pins inside a sealed submodule, would steer the credential at a repository nothing sealed. "+
			"Set release.checkout.submodules to %q, which fetches the top-level submodules this list does bound, or seal the nested set first (SB23-2560)",
			mode, manifest.SubmodulesTop)
	}

	host, err := callerHost(*serverURL)
	if err != nil {
		return fail(stderr, "check submodules: --server-url %q %v", *serverURL, err)
	}

	declared, err := readGitmodules(*gitmodulesPath)
	if err != nil {
		return fail(stderr, "check submodules: %v", err)
	}

	if err := gitmodules.CheckDeclared(declared, project.Release.SubmodulesAllowed(), host); err != nil {
		return fail(stderr, "check submodules: %v", err)
	}

	fmt.Fprintf(stdout, "submodules checked: %d declared, all sealed by release.checkout.submodules_allowed and on %s\n", len(declared), host)
	return 0
}

// readGitmodules reads the tagged .gitmodules. A missing file declares no
// submodules, which is safe: nothing is fetched and so nothing is unsealed.
// A file that is not a regular file is refused rather than followed, matching
// how the workflow treats the manifest it seals.
func readGitmodules(path string) ([]gitmodules.Submodule, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("read %s: is a symbolic link; the tagged .gitmodules must be a regular tracked file", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", path, err)
	}
	declared, err := gitmodules.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", path, err)
	}
	return declared, nil
}

// callerHost reduces GITHUB_SERVER_URL to the host the credential is scoped
// to. actions/checkout writes the token into an http.<origin>/.extraheader,
// which is one server, so a submodule anywhere else cannot be authenticated
// by it (SB23-2506).
func callerHost(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("is not a URL: %v", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("has scheme %q, want https", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("names no host")
	}
	return strings.ToLower(parsed.Host), nil
}
