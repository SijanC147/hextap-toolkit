package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
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
	assertStructProperties(t, definition(t, definitions, "homebrew"), reflect.TypeOf(Homebrew{}))
	assertStructProperties(t, definition(t, definitions, "serviceEnabled"), reflect.TypeOf(Service{}))
	for _, name := range []string{
		"formula",
		"repository",
		"assets",
		"release",
		"homebrew",
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
	assertRequired(t, definition(t, definitions, "release"), "build_script", "linux")
	assertRequired(t, definition(t, definitions, "homebrew"), "macos_only", "test_args", "caveats")
	assertRequired(t, definition(t, definitions, "serviceEnabled"), "enabled", "run_args", "keep_alive", "restart_delay", "environment", "log_path", "error_log_path")
	assertRequired(t, definition(t, definitions, "serviceDisabled"), "enabled")

	if got := nestedNumber(t, schema, "properties", "schema", "const"); got != CurrentSchema {
		t.Fatalf("schema const = %v, Go schema = %d", got, CurrentSchema)
	}
	assertDefinitionPattern(t, definitions, "formulaName", formulaNamePattern.String())
	assertDefinitionPattern(t, definitions, "className", classNamePattern.String())
	assertDefinitionPattern(t, definitions, "repositoryName", repositoryPattern.String())
	assertDefinitionPattern(t, definitions, "repositoryOwner", ownerPattern.String())
	assertDefinitionPattern(t, definitions, "fileName", fileNamePattern.String())
	assertDefinitionPattern(t, definitions, "relativePath", relativePathPattern.String())
	assertDefinitionPattern(t, definitions, "environmentKey", environmentPattern.String())
	assertDefinitionPattern(t, definitions, "argument", argumentPattern.String())
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

func TestMachineSchemaDeclaresEveryRubyInterpolationGuard(t *testing.T) {
	definitions := object(t, loadProjectSchema(t)["$defs"], "$defs")
	for _, definitionName := range []string{"rubyLine", "environmentValue", "caveats"} {
		pattern, ok := definition(t, definitions, definitionName)["pattern"].(string)
		if !ok {
			t.Fatalf("$defs.%s.pattern is missing", definitionName)
		}
		for _, introducer := range []string{`#\{`, "#@", `#\$`} {
			if !strings.Contains(pattern, introducer) {
				t.Errorf("$defs.%s.pattern does not guard %q", definitionName, introducer)
			}
		}
	}
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
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, child := range arrayValue {
				if err := validateFixtureShape(root, itemSchema, child, fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: expected string", location)
		}
		if minimum, ok := schema["minLength"].(float64); ok && len(text) < int(minimum) {
			return fmt.Errorf("%s: string too short", location)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			if compiled, err := regexp.Compile(pattern); err == nil && !compiled.MatchString(text) {
				return fmt.Errorf("%s: does not match %s", location, pattern)
			}
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
	return nil
}
