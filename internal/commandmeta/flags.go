package commandmeta

import "flag"

// Binder registers runtime flags from one authoritative command node. It
// panics on a missing or wrongly typed option so source and metadata drift is a
// test/build failure rather than silently incomplete help or completion.
type Binder struct {
	flags   *flag.FlagSet
	command Command
}

// Bind returns a metadata-backed flag binder for one command path.
func Bind(flags *flag.FlagSet, path ...string) Binder {
	command, ok := Lookup(path)
	if !ok {
		panic("command metadata path is missing")
	}
	return Binder{flags: flags, command: command}
}

// String registers both the long and short spellings of a string option.
func (binder Binder) String(long, defaultValue string) *string {
	option := binder.mustOption(long)
	if option.ValueName == "" {
		panic("string option metadata lacks a value")
	}
	value := defaultValue
	binder.flags.StringVar(&value, option.Long, defaultValue, option.Description)
	binder.flags.StringVar(&value, option.Short, defaultValue, option.Description)
	return &value
}

// Bool registers both the long and short spellings of a boolean option.
func (binder Binder) Bool(long string, defaultValue bool) *bool {
	option := binder.mustOption(long)
	if option.ValueName != "" {
		panic("boolean option metadata unexpectedly declares a value")
	}
	value := defaultValue
	binder.flags.BoolVar(&value, option.Long, defaultValue, option.Description)
	binder.flags.BoolVar(&value, option.Short, defaultValue, option.Description)
	return &value
}

// Var registers both spellings of a repeatable or custom flag.Value option.
func (binder Binder) Var(value flag.Value, long string) {
	option := binder.mustOption(long)
	if option.ValueName == "" {
		panic("custom option metadata lacks a value")
	}
	binder.flags.Var(value, option.Long, option.Description)
	binder.flags.Var(value, option.Short, option.Description)
}

// WasSet reports whether either spelling of an option occurred during parsing.
func (binder Binder) WasSet(long string) bool {
	option := binder.mustOption(long)
	visited := false
	binder.flags.Visit(func(item *flag.Flag) {
		if item.Name == option.Long || item.Name == option.Short {
			visited = true
		}
	})
	return visited
}

func (binder Binder) mustOption(long string) Option {
	for _, option := range binder.command.Options {
		if option.Long == long {
			return option
		}
	}
	panic("command option metadata is missing: --" + long)
}
