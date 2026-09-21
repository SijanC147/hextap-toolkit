package manifest

import (
	"encoding/json"
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

// The Go validator is authoritative, but the checked-in machine schema is what
// an editor shows an adopter. If the two disagree on release.checkout, an
// adopter sees a green editor and a rejected release, or the reverse. The
// shape cross-check in schema_test.go proves only that the property names and
// the required list match. This runs whole documents through both and requires
// the same answer, which is the difference between arguing it from reading the
// schema and having run it.
func TestCheckoutAgreesBetweenGoAndMachineSchema(t *testing.T) {
	schema := loadProjectSchema(t)
	for name, testCase := range map[string]struct {
		body  string
		valid bool
	}{
		"valid false":             {body: `{"submodules": "false"}`, valid: true},
		"valid true":              {body: `{"submodules": "true"}`, valid: true},
		"valid recursive":         {body: `{"submodules": "recursive"}`, valid: true},
		"invalid capital True":    {body: `{"submodules": "True"}`},
		"invalid recurse":         {body: `{"submodules": "recurse"}`},
		"invalid empty string":    {body: `{"submodules": ""}`},
		"invalid boolean":         {body: `{"submodules": true}`},
		"invalid unknown field":   {body: `{"submodules": "true", "depth": 1}`},
		"invalid missing field":   {body: `{}`},
		"invalid mis-cased field": {body: `{"Submodules": "true"}`},
	} {
		t.Run(name, func(t *testing.T) {
			encoded := []byte(withCheckout(testCase.body))
			_, goError := Parse(encoded)
			var schemaValue any
			if err := json.Unmarshal(encoded, &schemaValue); err != nil {
				t.Fatalf("Unmarshal(fixture): %v", err)
			}
			schemaError := validateFixtureShape(schema, schema, schemaValue, "$")
			if testCase.valid {
				if goError != nil || schemaError != nil {
					t.Fatalf("valid case rejected: Go=%v schema=%v", goError, schemaError)
				}
				return
			}
			if goError == nil || schemaError == nil {
				t.Fatalf("invalid case accepted: Go=%v schema=%v", goError, schemaError)
			}
		})
	}
}

// Schema 1 predates release.checkout, so the field is unknown there. Both the
// Go validator and the machine schema must say so, or an adopter on the legacy
// contract sets a field that does nothing.
func TestCheckoutOnSchemaOneIsRejectedByBothContracts(t *testing.T) {
	legacy := strings.Replace(validManifest, `    "build_script": "scripts/hextap-build",`,
		`    "build_script": "scripts/hextap-build",
    "checkout": {"submodules": "recursive"},`, 1)
	if legacy == validManifest {
		t.Fatal("schema 1 fixture no longer carries the expected build_script line")
	}
	if _, err := Parse([]byte(legacy)); err == nil {
		t.Fatal("the Go validator accepted release.checkout on schema 1")
	}
	var schemaValue any
	if err := json.Unmarshal([]byte(legacy), &schemaValue); err != nil {
		t.Fatalf("Unmarshal(legacy fixture): %v", err)
	}
	schema := loadProjectSchema(t)
	if err := validateFixtureShape(schema, schema, schemaValue, "$"); err == nil {
		t.Fatal("the machine schema accepted release.checkout on schema 1")
	}
}

// MarshalJSON projects an explicit field set per schema rather than marshalling
// the struct, so a field added to the struct and not to the projection is
// dropped in silence. The result is still a valid manifest, which is what makes
// it dangerous: a recursive selection becomes the default false on a round trip
// and nothing reports it. Raised by Codex on PR #23 as P2.
func TestCheckoutSurvivesAMarshalRoundTrip(t *testing.T) {
	for _, mode := range []string{SubmodulesTop, SubmodulesRecursive, SubmodulesNone} {
		project, err := Parse([]byte(withCheckout(`{"submodules": "` + mode + `"}`)))
		if err != nil {
			t.Fatalf("Parse(submodules = %q) = %v", mode, err)
		}
		encoded, err := json.Marshal(project)
		if err != nil {
			t.Fatalf("Marshal(submodules = %q) = %v", mode, err)
		}
		if !strings.Contains(string(encoded), `"checkout":{"submodules":"`+mode+`"}`) {
			t.Fatalf("release.checkout was dropped when marshalling submodules = %q:\n%s", mode, encoded)
		}
		round, err := Parse(encoded)
		if err != nil {
			t.Fatalf("Parse(round trip, submodules = %q) = %v", mode, err)
		}
		if got := round.Release.SubmodulesMode(); got != mode {
			t.Fatalf("round trip turned submodules %q into %q", mode, got)
		}
	}
}

// A manifest that never declared the block must not gain one, or every
// generated document changes shape for adopters who do not use this.
func TestAManifestWithoutCheckoutDoesNotGainOneWhenMarshalled(t *testing.T) {
	project, err := Parse([]byte(bunProfileManifest))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v", err)
	}
	encoded, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("Marshal(fixture) = %v", err)
	}
	if strings.Contains(string(encoded), `"checkout"`) {
		t.Fatalf("marshalling invented a release.checkout block:\n%s", encoded)
	}
}

// The workflow refuses a run where the caller input and the sealed manifest
// disagree, and it can only do that if the export carries the manifest value.
func TestWorkflowExportCarriesTheSealedSubmoduleMode(t *testing.T) {
	for _, mode := range []string{SubmodulesTop, SubmodulesRecursive} {
		project, err := Parse([]byte(withCheckout(`{"submodules": "` + mode + `"}`)))
		if err != nil {
			t.Fatalf("Parse(submodules = %q) = %v", mode, err)
		}
		values, err := project.WorkflowExport(project.RepositorySlug())
		if err != nil {
			t.Fatalf("WorkflowExport(submodules = %q) = %v", mode, err)
		}
		if values.Submodules != mode {
			t.Fatalf("WorkflowExport().Submodules = %q, want %q", values.Submodules, mode)
		}
	}
	project, err := Parse([]byte(bunProfileManifest))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v", err)
	}
	values, err := project.WorkflowExport(project.RepositorySlug())
	if err != nil {
		t.Fatalf("WorkflowExport(fixture) = %v", err)
	}
	if values.Submodules != SubmodulesNone {
		t.Fatalf("a manifest without release.checkout exported %q, want %q; the workflow default is %q and the two must agree", values.Submodules, SubmodulesNone, SubmodulesNone)
	}
}
