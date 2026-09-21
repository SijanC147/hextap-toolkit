package gitmodules

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateAllowedURL reports whether one entry of
// release.checkout.submodules_allowed is an exact, safe URL to seal.
//
// Exact is the whole point. The check compares a declared URL against this
// list by string equality, so anything that could match more than one
// repository, or that could be read one way by this toolkit and another way by
// git, is refused here rather than sealed. That rules out patterns, redundant
// encodings, credentials in the URL, and anything whose canonical form differs
// from what the adopter wrote.
//
// https only, because the credential is an http.<origin>/.extraheader that
// actions/checkout writes before it fetches: an ssh, scp-like, relative or
// file URL cannot carry it, so sealing one would promise an authentication
// that cannot happen (SB23-2506).
func ValidateAllowedURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("is empty")
	}
	if strings.TrimSpace(raw) != raw {
		return fmt.Errorf("has leading or trailing whitespace")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return fmt.Errorf("contains whitespace")
	}
	// A pattern is not an exact URL. Sealing one would let a tagged commit
	// choose any repository the pattern spans.
	if i := strings.IndexAny(raw, "*?[]{}"); i >= 0 {
		return fmt.Errorf("contains the pattern character %q; the list seals exact URLs, not patterns", string(raw[i]))
	}
	if !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("is not an https:// URL; the submodule credential is written into an http.<origin>/.extraheader, so an ssh, scp-like, relative or file URL cannot carry it (SB23-2506)")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("is not a URL: %v", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("has scheme %q, want https", parsed.Scheme)
	}
	if parsed.User != nil {
		return fmt.Errorf("carries credentials in the URL; a manifest is a tracked file and a secret does not belong in one")
	}
	if parsed.Host == "" {
		return fmt.Errorf("names no host")
	}
	if parsed.Host != strings.ToLower(parsed.Host) {
		return fmt.Errorf("has an uppercase host; write it in lower case so the exact comparison against .gitmodules is not defeated by case")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return fmt.Errorf("names no repository path")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("carries a query string")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("carries a fragment")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == ".." || segment == "." {
			return fmt.Errorf("has a relative path segment %q", segment)
		}
	}
	// A percent-encoded path survives the round trip below unchanged, because
	// url.Parse keeps RawPath and String re-emits it. So the round trip does
	// NOT reduce a URL to one spelling, and the earlier version of this
	// comment claimed it did. Both spellings reach the same repository once
	// the path is decoded, so the encoded form is refused here explicitly.
	// Raised by the security reviewer of PR #30 as P2: the code failed closed
	// by luck, and the comment asserted an invariant the code did not hold.
	if strings.Contains(parsed.EscapedPath(), "%") {
		return fmt.Errorf("carries a percent-encoded path segment; write the decoded form, because both spellings reach the same repository and the comparison is by string")
	}
	// What the adopter wrote must already be the canonical form, so a
	// redundant spelling is refused rather than silently normalised into the
	// seal.
	if parsed.String() != raw {
		return fmt.Errorf("is not in canonical form; write it exactly as %q", parsed.String())
	}
	return nil
}

// parsedHost returns the host of a URL that ValidateAllowedURL has already
// accepted. It cannot fail for such a string, and returns the empty string
// rather than an error for one it has not, which never matches a caller host.
func parsedHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// Host returns the host of an allowed or declared URL, for comparing a
// submodule against the caller's own server.
func Host(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("is not a URL: %v", err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("names no host")
	}
	return strings.ToLower(parsed.Host), nil
}

// CheckDeclared reports whether every submodule the tagged source declares is
// sealed by the allowlist and lives on the caller's own server.
//
// It returns every reason it found rather than the first, because an adopter
// filling the field in should see the whole list once instead of one entry per
// release run.
//
// A declared URL that is outside the list is SB23-2504: the credential would
// be presented to a repository nobody sealed. A declared URL on another host
// is SB23-2506: the credential cannot authenticate it at all, and the release
// would fail at checkout rather than here.
func CheckDeclared(declared []Submodule, allowed []string, callerHost string) error {
	if callerHost == "" {
		return fmt.Errorf("the caller's host is empty; the same-host check cannot run without it")
	}
	callerHost = strings.ToLower(callerHost)

	sealed := make(map[string]bool, len(allowed))
	for _, entry := range allowed {
		sealed[entry] = true
	}

	var problems []string
	for _, submodule := range declared {
		if err := ValidateAllowedURL(submodule.URL); err != nil {
			problems = append(problems, fmt.Sprintf("submodule %q at %q declares url %q, which %v", submodule.Name, submodule.Path, submodule.URL, err))
			continue
		}
		// One parse, not two. ValidateAllowedURL above already accepted this
		// string, and its rules guarantee a lower-case host, so re-parsing
		// here would be a second chance for the package whose whole point is
		// one answer to disagree with itself. Raised by the reviewer as P3.
		host := parsedHost(submodule.URL)
		if host != callerHost {
			problems = append(problems, fmt.Sprintf("submodule %q declares url %q on host %q, but the credential is scoped to the caller's own server %q and cannot authenticate another host (SB23-2506)", submodule.Name, submodule.URL, host, callerHost))
			continue
		}
		if !sealed[submodule.URL] {
			problems = append(problems, fmt.Sprintf("submodule %q at %q declares url %q, which release.checkout.submodules_allowed does not seal", submodule.Name, submodule.Path, submodule.URL))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the tagged .gitmodules declares submodules the manifest does not seal:\n  %s\n\n"+
		"The submodule credential is presented to every repository .gitmodules names, so the set of repositories it reaches is decided by this list and not by the tagged commit. "+
		"Add each URL above to release.checkout.submodules_allowed in the manifest, at a commit you have reviewed, or remove the submodule.",
		strings.Join(problems, "\n  "))
}
