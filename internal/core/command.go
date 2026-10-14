package core

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Sentinel errors of the parameter dictionary check.
var (
	ErrParamDuplicate  = errors.New("param_duplicate")
	ErrParamUndeclared = errors.New("param_undeclared")
)

// ParamDef defines one parameter name and its one meaning across all commands.
type ParamDef struct {
	Name    string
	Meaning string
}

// Input holds the parameter values of one command call, keyed by parameter name, and the writer
// its progress output streams to. Stream is never the result envelope's writer, so --format json
// stays one parseable envelope (SPEC §5).
type Input struct {
	Params map[string]string
	Stream io.Writer
}

// Command is one entry of the command registry; every surface is built from it.
type Command struct {
	Module string
	Object string
	Verb   string
	Params []string
	// Purpose is the one-line purpose that --help prints next to the command name (DR-16).
	Purpose string
	Run     func(ctx context.Context, in Input) (Result[any], error)
}

// Dictionary returns the one parameter dictionary: one name, one meaning, everywhere.
func Dictionary() []ParamDef {
	return []ParamDef{
		{Name: "body", Meaning: "the unit issue body to check"},
		{Name: "format", Meaning: "output format of the result envelope: text or json"},
		{Name: "repo", Meaning: "repository root; a relative value resolves once against the working directory"},
		{Name: "step", Meaning: "comma-separated gate step ids to run; every other step is reported not_measured"},
	}
}

// CheckParams refuses a dictionary that defines a name twice or without a meaning, and a command
// that uses a name the dictionary does not define.
func CheckParams(dict []ParamDef, cmds []Command) error {
	defined := make(map[string]bool, len(dict))
	for _, p := range dict {
		if defined[p.Name] {
			return fmt.Errorf("parameter %q: %w", p.Name, ErrParamDuplicate)
		}
		defined[p.Name] = true
		if p.Name == "" || p.Meaning == "" {
			return fmt.Errorf("parameter %q has no meaning: %w", p.Name, ErrParamUndeclared)
		}
	}
	for _, c := range cmds {
		for _, name := range c.Params {
			if !defined[name] {
				return fmt.Errorf("command %s %s-%s parameter %q: %w", c.Module, c.Object, c.Verb, name, ErrParamUndeclared)
			}
		}
	}
	return nil
}
