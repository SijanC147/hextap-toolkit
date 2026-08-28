package manifest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func loadProjectSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "project-manifest.schema.json"))
	if err != nil {
		t.Fatalf("ReadFile(project schema): %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("project schema is not valid JSON: %v", err)
	}
	return schema
}

func TestMachineReadableSchemaMatchesGoFieldContract(t *testing.T) {
	schema := loadProjectSchema(t)
	if got := schema["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("$schema = %v", got)
	}
	if got := schema["additionalProperties"]; got != false {
		t.Fatalf("root additionalProperties = %v", got)
	}

	definitions := object(t, schema["$defs"], "$defs")
	assertStructProperties(t, schema, reflect.TypeOf(Manifest{}))
	assertStructProperties(t, definition(t, definitions, "formula"), reflect.TypeOf(Formula{}))
	assertStructProperties(t, definition(t, definitions, "repository"), reflect.TypeOf(Repository{}))
	assertStructProperties(t, definition(t, definitions, "assets"), reflect.TypeOf(Assets{}))
	assertStructProperties(t, definition(t, definitions, "release"), reflect.TypeOf(Release{}))
	assertStructProperties(t, definition(t, definitions, "releaseProfile"), reflect.TypeOf(ReleaseProfile{}))
	assertStructProperties(t, definition(t, definitions, "command"), reflect.TypeOf(Command{}))
	assertStructProperties(t, definition(t, definitions, "targetArtifacts"), reflect.TypeOf(TargetArtifacts{}))
	assertStructProperties(t, definition(t, definitions, "homebrew"), reflect.TypeOf(Homebrew{}))
	assertStructProperties(t, definition(t, definitions, "serviceEnabled"), reflect.TypeOf(Service{}))
	for _, name := range []string{
		"formula",
		"repository",
		"assets",
		"release",
		"releaseLegacy",
		"releaseProfileContract",
		"releaseProfile",
		"command",
		"targetArtifacts",
		"releaseTargets",
		"homebrew",
		"homebrewLegacy",
		"homebrewProfile",
		"serviceEnabled",
		"serviceDisabled",
		"keepAliveSuccessfulExit",
		"keepAliveCrashed",
	} {
		if got := definition(t, definitions, name)["additionalProperties"]; got != false {
			t.Errorf("$defs.%s.additionalProperties = %v, want false", name, got)
		}
	}

	keepAliveFields := jsonFieldNames(reflect.TypeOf(KeepAlive{}))
	keepAliveSchemaFields := append(
		propertyNames(t, definition(t, definitions, "keepAliveSuccessfulExit")),
		propertyNames(t, definition(t, definitions, "keepAliveCrashed"))...,
	)
	sort.Strings(keepAliveSchemaFields)
	if !reflect.DeepEqual(keepAliveSchemaFields, keepAliveFields) {
		t.Fatalf("keep_alive schema fields = %v, Go fields = %v", keepAliveSchemaFields, keepAliveFields)
	}

	assertRequired(t, schema, "schema", "formula", "release", "homebrew")
	assertRequired(t, definition(t, definitions, "formula"), "name", "class", "description", "homepage", "license", "repository", "binary", "assets")
	assertRequired(t, definition(t, definitions, "repository"), "owner", "name")
	assertRequired(t, definition(t, definitions, "assets"), "darwin_arm64", "darwin_amd64")
	assertRequired(t, definition(t, definitions, "release"), "build_script")
	assertRequired(t, definition(t, definitions, "releaseProfile"), "runtime", "runtime_version", "install", "quality", "prepare")
	assertRequired(t, definition(t, definitions, "command"), "name", "argv")
	assertRequired(t, definition(t, definitions, "releaseTargets"), "darwin_arm64", "darwin_amd64")
	assertRequired(t, definition(t, definitions, "homebrew"), "macos_only", "test_args")
	assertRequired(t, definition(t, definitions, "serviceEnabled"), "enabled", "run_args", "keep_alive", "restart_delay", "environment", "log_path", "error_log_path")
	assertRequired(t, definition(t, definitions, "serviceDisabled"), "enabled")

	schemaVersions := object(t, schema["properties"], "properties")["schema"].(map[string]any)["oneOf"].([]any)
	if len(schemaVersions) != 2 || nestedNumber(t, schemaVersions[0].(map[string]any), "const") != LegacySchema || nestedNumber(t, schemaVersions[1].(map[string]any), "const") != ProfileSchema {
		t.Fatalf("schema versions = %#v", schemaVersions)
	}
	assertDefinitionPattern(t, definitions, "formulaName", formulaNamePattern.String())
	assertDefinitionPattern(t, definitions, "className", classNamePattern.String())
	assertDefinitionPattern(t, definitions, "repositoryName", repositoryPattern.String())
	assertDefinitionPattern(t, definitions, "repositoryOwner", ownerPattern.String())
	assertDefinitionPattern(t, definitions, "fileName", fileNamePattern.String())
	assertDefinitionPattern(t, definitions, "relativePath", relativePathPattern.String())
	assertDefinitionPattern(t, definitions, "zshCompletionPath", zshCompletionPattern.String())
	assertDefinitionPattern(t, definitions, "environmentKey", environmentPattern.String())
	assertDefinitionPattern(t, definitions, "argument", argumentPattern.String())
	for name, expected := range map[string]int{
		"formulaName":       maxFormulaNameBytes,
		"className":         maxPathComponentBytes,
		"fileName":          maxPathComponentBytes,
		"assetName":         maxPathComponentBytes,
		"relativePath":      maxRelativePathBytes,
		"zshCompletionPath": maxCompletionPathBytes,
	} {
		if got := nestedNumber(t, definitions, name, "maxLength"); got != expected {
			t.Fatalf("$defs.%s.maxLength = %d, want %d", name, got, expected)
		}
	}
}

func TestBunProfileFixtureConformsToGoAndMachineSchema(t *testing.T) {
	if _, err := Parse([]byte(bunProfileManifest)); err != nil {
		t.Fatalf("Go manifest validation failed: %v", err)
	}
	var fixture any
	if err := json.Unmarshal([]byte(bunProfileManifest), &fixture); err != nil {
		t.Fatal(err)
	}
	schema := loadProjectSchema(t)
	if err := validateFixtureShape(schema, schema, fixture, "$"); err != nil {
		t.Fatalf("machine schema rejected Bun profile: %v", err)
	}
}

func TestExampleFixtureConformsToGoAndMachineSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatalf("ReadFile(example): %v", err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("Go manifest validation failed: %v", err)
	}
	var fixture any
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("example is not JSON: %v", err)
	}
	schema := loadProjectSchema(t)
	if err := validateFixtureShape(schema, schema, fixture, "$"); err != nil {
		t.Fatalf("machine schema rejected example: %v", err)
	}
}

func TestManifestConformanceCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "claude-rc-proxy.json"))
	if err != nil {
		t.Fatalf("ReadFile(example): %v", err)
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("Unmarshal(example): %v", err)
	}
	type corpusCase struct {
		name   string
		valid  bool
		mutate func(map[string]any)
	}
	cases := []corpusCase{
		{name: "valid complete service", valid: true},
		{name: "valid omitted service", valid: true, mutate: func(value map[string]any) {
			delete(object(t, value["homebrew"], "homebrew"), "service")
		}},
		{name: "valid null service", valid: true, mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["service"] = nil
		}},
		{name: "valid disabled service", valid: true, mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["service"] = map[string]any{"enabled": false}
		}},
		{name: "valid binary alias and Zsh completion", valid: true, mutate: func(value map[string]any) {
			homebrew := object(t, value["homebrew"], "homebrew")
			homebrew["binary_aliases"] = []any{"proxy"}
			homebrew["zsh_completion"] = "completions/_proxy"
		}},
		{name: "invalid schema const", mutate: func(value map[string]any) { value["schema"] = float64(2) }},
		{name: "invalid missing release", mutate: func(value map[string]any) { delete(value, "release") }},
		{name: "invalid root property", mutate: func(value map[string]any) { value["unknown"] = true }},
		{name: "invalid case-fold alias", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["Name"] = "claude-rc-proxy"
		}},
		{name: "invalid macos false", mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["macos_only"] = false
		}},
		{name: "invalid formula name pattern", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["name"] = "2tool"
		}},
		{name: "invalid formula generated suffix length", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["name"] = strings.Repeat("a", maxFormulaNameBytes+1)
		}},
		{name: "invalid binary component length", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["binary"] = strings.Repeat("a", maxPathComponentBytes+1)
		}},
		{name: "invalid owner maximum", mutate: func(value map[string]any) {
			formula := object(t, value["formula"], "formula")
			object(t, formula["repository"], "repository")["owner"] = strings.Repeat("a", 40)
		}},
		{name: "invalid empty description", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["description"] = ""
		}},
		{name: "invalid whitespace description", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["description"] = "   "
		}},
		{name: "invalid description control", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["description"] = "bad\tvalue"
		}},
		{name: "invalid ruby interpolation", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["license"] = "MIT #@danger"
		}},
		{name: "invalid homepage scheme", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["homepage"] = "http://example.com/project"
		}},
		{name: "invalid homepage credentials", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["homepage"] = "https://user@example.com/project"
		}},
		{name: "invalid homepage format", mutate: func(value map[string]any) {
			object(t, value["formula"], "formula")["homepage"] = "https://exa mple.com/project"
		}},
		{name: "invalid asset pattern", mutate: func(value map[string]any) {
			formula := object(t, value["formula"], "formula")
			object(t, formula["assets"], "assets")["darwin_arm64"] = "archive.zip"
		}},
		{name: "invalid build path", mutate: func(value map[string]any) {
			object(t, value["release"], "release")["build_script"] = "../build"
		}},
		{name: "invalid build path total length", mutate: func(value map[string]any) {
			part := strings.Repeat("a", 250)
			object(t, value["release"], "release")["build_script"] = strings.Join([]string{part, part, part, part, part}, "/")
		}},
		{name: "invalid boolean type", mutate: func(value map[string]any) {
			object(t, value["release"], "release")["linux"] = "yes"
		}},
		{name: "invalid empty test arguments", mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["test_args"] = []any{}
		}},
		{name: "invalid test argument pattern", mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["test_args"] = []any{"$(id)"}
		}},
		{name: "invalid Zsh completion pattern", mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["zsh_completion"] = "completions/hextap"
		}},
		{name: "invalid restart minimum", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			service["restart_delay"] = float64(0)
		}},
		{name: "invalid restart maximum", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			service["restart_delay"] = float64(3601)
		}},
		{name: "invalid environment property name", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			object(t, service["environment"], "environment")["lowercase"] = "value"
		}},
		{name: "invalid environment value", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			object(t, service["environment"], "environment")["RUNTIME_MODE"] = "#$danger"
		}},
		{name: "invalid empty environment value", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			object(t, service["environment"], "environment")["RUNTIME_MODE"] = ""
		}},
		{name: "invalid keep alive oneOf", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			object(t, service["keep_alive"], "keep_alive")["successful_exit"] = false
		}},
		{name: "invalid service property", mutate: func(value map[string]any) {
			service := object(t, object(t, value["homebrew"], "homebrew")["service"], "service")
			service["unknown"] = true
		}},
		{name: "invalid caveats interpolation", mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["caveats"] = "#$danger"
		}},
		{name: "invalid caveats terminator", mutate: func(value map[string]any) {
			object(t, value["homebrew"], "homebrew")["caveats"] = "before\nEOS\nafter"
		}},
	}

	schema := loadProjectSchema(t)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := cloneJSONObject(t, base)
			if testCase.mutate != nil {
				testCase.mutate(fixture)
			}
			encoded, err := json.Marshal(fixture)
			if err != nil {
				t.Fatalf("Marshal(fixture): %v", err)
			}
			_, goError := Parse(encoded)
			var schemaValue any
			if err := json.Unmarshal(encoded, &schemaValue); err != nil {
				t.Fatalf("Unmarshal(schema fixture): %v", err)
			}
			schemaError := validateFixtureShape(schema, schema, schemaValue, "$")
			if testCase.valid {
				if goError != nil || schemaError != nil {
					t.Fatalf("valid corpus case failed: Go=%v schema=%v", goError, schemaError)
				}
				return
			}
			if goError == nil || schemaError == nil {
				t.Fatalf("invalid corpus case accepted: Go=%v schema=%v", goError, schemaError)
			}
		})
	}
}

func cloneJSONObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(clone): %v", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatalf("Unmarshal(clone): %v", err)
	}
	return clone
}

func TestMachineSchemaUsesOnlySupportedEvaluatorKeywords(t *testing.T) {
	supported := map[string]bool{
		"$schema": true, "$id": true, "$defs": true, "$ref": true,
		"title": true, "description": true,
		"type": true, "const": true, "oneOf": true, "allOf": true, "not": true,
		"properties": true, "required": true, "additionalProperties": true, "propertyNames": true,
		"items": true, "minItems": true, "maxItems": true, "uniqueItems": true,
		"minLength": true, "maxLength": true, "pattern": true, "format": true,
		"minimum": true, "maximum": true,
	}
	var walk func(map[string]any, string)
	walk = func(current map[string]any, location string) {
		for keyword, raw := range current {
			if !supported[keyword] {
				t.Errorf("unsupported schema keyword %s at %s", keyword, location)
				continue
			}
			if keyword == "properties" || keyword == "$defs" {
				children, _ := raw.(map[string]any)
				for name, child := range children {
					if childSchema, ok := child.(map[string]any); ok {
						walk(childSchema, location+"/"+keyword+"/"+name)
					}
				}
				continue
			}
			switch typed := raw.(type) {
			case map[string]any:
				walk(typed, location+"/"+keyword)
			case []any:
				for index, child := range typed {
					if childSchema, ok := child.(map[string]any); ok {
						walk(childSchema, fmt.Sprintf("%s/%s/%d", location, keyword, index))
					}
				}
			}
		}
	}
	walk(loadProjectSchema(t), "#")
}

func TestMachineSchemaDeclaresEveryRubyInterpolationGuard(t *testing.T) {
	definitions := object(t, loadProjectSchema(t)["$defs"], "$defs")
	for _, definitionName := range []string{"rubyLine", "environmentValue", "caveats"} {
		patterns := schemaPatterns(definition(t, definitions, definitionName))
		for _, introducer := range []string{"#{", "#@", "#$"} {
			if !anyPatternMatches(patterns, introducer) {
				t.Errorf("$defs.%s.pattern does not guard %q", definitionName, introducer)
			}
		}
	}
}

func schemaPatterns(value any) []string {
	patterns := make([]string, 0)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if pattern, ok := typed["pattern"].(string); ok {
				patterns = append(patterns, pattern)
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return patterns
}

func anyPatternMatches(patterns []string, value string) bool {
	for _, pattern := range patterns {
		compiled, err := regexp.Compile(pattern)
		if err == nil && compiled.MatchString(value) {
			return true
		}
	}
	return false
}

func assertStructProperties(t *testing.T, schema map[string]any, structType reflect.Type) {
	t.Helper()
	want := jsonFieldNames(structType)
	got := propertyNames(t, schema)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s schema fields = %v, Go fields = %v", structType.Name(), got, want)
	}
}

func jsonFieldNames(structType reflect.Type) []string {
	fields := make([]string, 0, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		name := strings.Split(structType.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func propertyNames(t *testing.T, schema map[string]any) []string {
	t.Helper()
	properties := object(t, schema["properties"], "properties")
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assertRequired(t *testing.T, schema map[string]any, expected ...string) {
	t.Helper()
	values, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("required = %#v", schema["required"])
	}
	actual := make([]string, len(values))
	for index, value := range values {
		actual[index], ok = value.(string)
		if !ok {
			t.Fatalf("required[%d] = %#v", index, value)
		}
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("required = %v, want %v", actual, expected)
	}
}

func assertDefinitionPattern(t *testing.T, definitions map[string]any, name, expected string) {
	t.Helper()
	definition := definition(t, definitions, name)
	if got := definition["pattern"]; got != expected {
		t.Fatalf("$defs.%s.pattern = %v, want %q", name, got, expected)
	}
}

func definition(t *testing.T, definitions map[string]any, name string) map[string]any {
	t.Helper()
	return object(t, definitions[name], "$defs."+name)
}

func object(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", name, value)
	}
	return result
}

func nestedNumber(t *testing.T, root map[string]any, path ...string) int {
	t.Helper()
	current := root
	for _, part := range path[:len(path)-1] {
		current = object(t, current[part], strings.Join(path, "."))
	}
	value, ok := current[path[len(path)-1]].(float64)
	if !ok {
		t.Fatalf("%s = %#v, want number", strings.Join(path, "."), current[path[len(path)-1]])
	}
	return int(value)
}

func validateFixtureShape(root, schema map[string]any, value any, location string) error {
	if reference, ok := schema["$ref"].(string); ok {
		const prefix = "#/$defs/"
		if !strings.HasPrefix(reference, prefix) {
			return fmt.Errorf("%s: unsupported reference %q", location, reference)
		}
		definitions, _ := root["$defs"].(map[string]any)
		resolved, ok := definitions[strings.TrimPrefix(reference, prefix)].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: unresolved reference %q", location, reference)
		}
		return validateFixtureShape(root, resolved, value, location)
	}
	if alternatives, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, alternative := range alternatives {
			candidate, ok := alternative.(map[string]any)
			if ok && validateFixtureShape(root, candidate, value, location) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: matched %d oneOf alternatives", location, matches)
		}
		return nil
	}
	if requirements, ok := schema["allOf"].([]any); ok {
		for index, requirement := range requirements {
			candidate, ok := requirement.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: allOf[%d] is not an object", location, index)
			}
			if err := validateFixtureShape(root, candidate, value, location); err != nil {
				return err
			}
		}
	}
	if negated, ok := schema["not"].(map[string]any); ok {
		if validateFixtureShape(root, negated, value, location) == nil {
			return fmt.Errorf("%s: matched forbidden schema", location)
		}
	}
	if constant, exists := schema["const"]; exists && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("%s: value %v does not equal const %v", location, value, constant)
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "null":
		if value != nil {
			return fmt.Errorf("%s: expected null", location)
		}
	case "object":
		objectValue, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object", location)
		}
		properties, _ := schema["properties"].(map[string]any)
		if namesSchema, ok := schema["propertyNames"].(map[string]any); ok {
			for key := range objectValue {
				if err := validateFixtureShape(root, namesSchema, key, location+".<property>"); err != nil {
					return err
				}
			}
		}
		if required, ok := schema["required"].([]any); ok {
			for _, requiredValue := range required {
				key, _ := requiredValue.(string)
				if _, exists := objectValue[key]; !exists {
					return fmt.Errorf("%s: missing %s", location, key)
				}
			}
		}
		for key, child := range objectValue {
			childSchema, declared := properties[key].(map[string]any)
			if !declared {
				switch additional := schema["additionalProperties"].(type) {
				case bool:
					if !additional {
						return fmt.Errorf("%s: unknown property %s", location, key)
					}
					continue
				case map[string]any:
					childSchema = additional
				default:
					continue
				}
			}
			if err := validateFixtureShape(root, childSchema, child, location+"."+key); err != nil {
				return err
			}
		}
	case "array":
		arrayValue, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", location)
		}
		if minimum, ok := schema["minItems"].(float64); ok && len(arrayValue) < int(minimum) {
			return fmt.Errorf("%s: too few items", location)
		}
		if maximum, ok := schema["maxItems"].(float64); ok && len(arrayValue) > int(maximum) {
			return fmt.Errorf("%s: too many items", location)
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			for index := range arrayValue {
				for previous := 0; previous < index; previous++ {
					if reflect.DeepEqual(arrayValue[previous], arrayValue[index]) {
						return fmt.Errorf("%s: duplicate array item", location)
					}
				}
			}
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, child := range arrayValue {
				if err := validateFixtureShape(root, itemSchema, child, fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected string", location)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) {
			return fmt.Errorf("%s: expected integer", location)
		}
		if minimum, ok := schema["minimum"].(float64); ok && number < minimum {
			return fmt.Errorf("%s: below minimum", location)
		}
		if maximum, ok := schema["maximum"].(float64); ok && number > maximum {
			return fmt.Errorf("%s: above maximum", location)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean", location)
		}
	}
	if text, ok := value.(string); ok {
		length := utf8.RuneCountInString(text)
		if minimum, ok := schema["minLength"].(float64); ok && length < int(minimum) {
			return fmt.Errorf("%s: string too short", location)
		}
		if maximum, ok := schema["maxLength"].(float64); ok && length > int(maximum) {
			return fmt.Errorf("%s: string too long", location)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("%s: invalid schema pattern %q: %w", location, pattern, err)
			}
			if !compiled.MatchString(text) {
				return fmt.Errorf("%s: does not match required pattern", location)
			}
		}
		if format, ok := schema["format"].(string); ok {
			if format != "uri" {
				return fmt.Errorf("%s: unsupported format %q", location, format)
			}
			parsed, err := url.ParseRequestURI(text)
			if err != nil || !parsed.IsAbs() {
				return fmt.Errorf("%s: invalid URI", location)
			}
		}
	}
	return nil
}
