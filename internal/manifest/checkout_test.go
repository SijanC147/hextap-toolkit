package manifest

import (
	"strings"
	"testing"
)

// withCheckout returns the schema 2 fixture carrying a release.checkout block.
func withCheckout(body string) string {
	const anchor = `  "release": {
    "build_script": "scripts/hextap-build",`
	replacement := anchor + "\n    \"checkout\": " + body + ","
	updated := strings.Replace(bunProfileManifest, anchor, replacement, 1)
	if updated == bunProfileManifest {
		panic("release.checkout anchor no longer matches the schema 2 fixture")
	}
	return updated
}

func TestCheckoutAcceptsEveryCheckoutModeActionsCheckoutUnderstands(t *testing.T) {
	for _, mode := range []string{SubmodulesNone, SubmodulesTop, SubmodulesRecursive} {
		project, err := Parse([]byte(withCheckout(`{"submodules": "` + mode + `"}`)))
		if err != nil {
			t.Fatalf("Parse(release.checkout.submodules = %q) = %v, want acceptance", mode, err)
		}
		if got := project.Release.SubmodulesMode(); got != mode {
			t.Fatalf("SubmodulesMode() = %q, want %q", got, mode)
		}
	}
}

// actions/checkout coerces a value it does not recognise to false and says
// nothing, so a rejected manifest is the only place a typo becomes visible.
// Without this, "True", "1" or "recurse" would produce the empty component
// directories the input exists to prevent, and the run would look healthy.
func TestCheckoutRejectsEveryValueActionsCheckoutWouldSilentlyTreatAsFalse(t *testing.T) {
	for _, mode := range []string{"True", "TRUE", "False", "yes", "1", "0", "recurse", "Recursive", "", " true", "true ", "all"} {
		if _, err := Parse([]byte(withCheckout(`{"submodules": "` + mode + `"}`))); err == nil {
			t.Fatalf("Parse(release.checkout.submodules = %q) was accepted; actions/checkout would coerce it to false and build against an empty tree", mode)
		} else if !strings.Contains(err.Error(), "release.checkout.submodules") {
			t.Fatalf("Parse(release.checkout.submodules = %q) error = %v, want the field named in the message", mode, err)
		}
	}
}

func TestCheckoutRejectsNonStringAndMalformedBlocks(t *testing.T) {
	for name, body := range map[string]string{
		"submodules as a boolean":       `{"submodules": true}`,
		"submodules as a number":        `{"submodules": 1}`,
		"submodules as null":            `{"submodules": null}`,
		"checkout as a string":          `"recursive"`,
		"checkout as an array":          `["recursive"]`,
		"empty checkout object":         `{}`,
		"unknown sibling field":         `{"submodules": "true", "depth": 1}`,
		"mis-cased submodules":          `{"Submodules": "true"}`,
		"submodules spelled submodule":  `{"submodule": "true"}`,
		"checkout carrying a token key": `{"submodules": "true", "token": "ghp_x"}`,
	} {
		if _, err := Parse([]byte(withCheckout(body))); err == nil {
			t.Errorf("%s was accepted; want rejection", name)
		}
	}
}

// release.checkout is a schema 2 field. Schema 1 predates the profile contract
// and takes only build_script and linux, so a checkout block there is an
// unknown field rather than a silently ignored one.
func TestCheckoutIsRejectedOnSchemaOne(t *testing.T) {
	legacy := strings.Replace(validManifest, `    "build_script": "scripts/hextap-build",`,
		`    "build_script": "scripts/hextap-build",
    "checkout": {"submodules": "recursive"},`, 1)
	if legacy == validManifest {
		t.Fatal("schema 1 fixture no longer carries the expected build_script line")
	}
	if _, err := Parse([]byte(legacy)); err == nil {
		t.Fatal("a schema 1 manifest carrying release.checkout was accepted; schema 1 takes only build_script and linux")
	}
}

// The field is optional, so every manifest written before it existed still
// parses, and the default is the behaviour those manifests already had.
func TestCheckoutIsOptionalAndDefaultsToNoSubmodules(t *testing.T) {
	project, err := Parse([]byte(bunProfileManifest))
	if err != nil {
		t.Fatalf("Parse(fixture without release.checkout) = %v", err)
	}
	if project.Release.Checkout != nil {
		t.Fatalf("Release.Checkout = %#v, want nil when the manifest omits it", project.Release.Checkout)
	}
	if got := project.Release.SubmodulesMode(); got != SubmodulesNone {
		t.Fatalf("SubmodulesMode() = %q, want %q", got, SubmodulesNone)
	}
}
