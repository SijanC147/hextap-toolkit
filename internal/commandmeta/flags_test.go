package commandmeta

import (
	"flag"
	"io"
	"strings"
	"testing"
)

type collectedValues []string

func (values *collectedValues) String() string { return strings.Join(*values, ",") }
func (values *collectedValues) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func TestBinderRegistersEveryMetadataFlagWithBothSpellings(t *testing.T) {
	for _, path := range SortedPaths() {
		command, _ := Lookup(path)
		for _, option := range command.Options {
			if option.Long == "help" || option.Long == "version" {
				continue
			}
			for _, spelling := range []string{"--" + option.Long, "-" + option.Short} {
				t.Run(strings.Join(append(path, spelling), "/"), func(t *testing.T) {
					flags := flag.NewFlagSet("coverage", flag.ContinueOnError)
					flags.SetOutput(io.Discard)
					binder := Bind(flags, path...)
					args := []string{spelling}
					switch {
					case option.Repeatable:
						var values collectedValues
						binder.Var(&values, option.Long)
						value := optionTestValue(option)
						args = []string{spelling, value, spelling, value}
						if err := flags.Parse(args); err != nil || len(values) != 2 {
							t.Fatalf("Parse(%v) values = %v, error = %v", args, values, err)
						}
					case option.ValueName != "":
						value := binder.String(option.Long, "")
						args = append(args, optionTestValue(option))
						if err := flags.Parse(args); err != nil || *value == "" {
							t.Fatalf("Parse(%v) value = %q, error = %v", args, *value, err)
						}
					default:
						value := binder.Bool(option.Long, false)
						if err := flags.Parse(args); err != nil || !*value {
							t.Fatalf("Parse(%v) value = %t, error = %v", args, *value, err)
						}
					}
					if !binder.WasSet(option.Long) {
						t.Fatalf("WasSet(%q) = false after parsing %s", option.Long, spelling)
					}
				})
			}
		}
	}
}

func optionTestValue(option Option) string {
	if len(option.Choices) != 0 {
		return option.Choices[0].Name
	}
	return "value"
}
