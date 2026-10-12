package core

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// flag is one "--name value" or "--name=value" argument; HasValue is false for a bare "--name".
type flag struct {
	Name     string
	Value    string
	HasValue bool
}

// call is one command line split into its positional words and its flags.
type call struct {
	Words []string
	Flags []flag
}

// helpEntry is one line of --help: the command name and its one-line purpose.
type helpEntry struct {
	Command string `json:"command"`
	Purpose string `json:"purpose"`
}

// RunCLI runs one command line against cmds and writes its one result envelope to out; it returns
// the process exit code. When any --format json is given, every refusal is printed as JSON too. A
// command's progress output streams to stream, which the binary sets to stderr (SPEC §5). A panic
// anywhere, argument splitting and format parsing included, prints one error envelope and exits 1.
func RunCLI(ctx context.Context, cmds []Command, args []string, out, stream io.Writer) (code int) {
	format := FormatText
	defer func() {
		if r := recover(); r != nil {
			code = write(out, format, Result[any]{Status: StatusError, Outcome: OutcomeError,
				Detail: fmt.Sprintf("crash: %v", r)})
		}
	}()
	c := split(args)
	format, err := outputFormat(c.Flags)
	if err != nil {
		return write(out, format, ErrorResult(err))
	}
	if err := CheckParams(Dictionary(), cmds); err != nil {
		return write(out, format, ErrorResult(err))
	}
	if hasFlag(c.Flags, "help") {
		return write(out, format, help(cmds))
	}
	cmd, in, err := bind(cmds, c)
	if err != nil {
		return write(out, format, ErrorResult(err))
	}
	in.Stream = stream
	res, err := cmd.Run(ctx, in)
	if err != nil {
		res = ErrorResult(err)
	}
	return write(out, format, res)
}

// split reads args into positional words and flags; a flag takes the next argument as its value
// unless that argument is itself a flag.
func split(args []string) call {
	var c call
	for i := 0; i < len(args); i++ {
		text, ok := strings.CutPrefix(args[i], "--")
		if !ok {
			c.Words = append(c.Words, args[i])
			continue
		}
		var f flag
		f.Name, f.Value, f.HasValue = strings.Cut(text, "=")
		if !f.HasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			i++
			f.Value, f.HasValue = args[i], true
		}
		c.Flags = append(c.Flags, f)
	}
	return c
}

// outputFormat returns the format of the result envelope. A repeated, empty or unknown --format is
// refused; the refusal is still printed as JSON when any of the values is json.
func outputFormat(flags []flag) (Format, error) {
	var values []string
	format := FormatText
	for _, f := range flags {
		if f.Name == "format" {
			values = append(values, f.Value)
			if Format(f.Value) == FormatJSON {
				format = FormatJSON
			}
		}
	}
	switch {
	case len(values) == 0:
		return FormatText, nil
	case len(values) > 1:
		return format, &Error{Outcome: OutcomeUsage, Expected: "--format at most once",
			Found: fmt.Sprintf("%d values %q", len(values), values), Next: "pass --format once"}
	case Format(values[0]) != FormatText && Format(values[0]) != FormatJSON:
		return format, &Error{Outcome: OutcomeUsage, Expected: "--format text or json",
			Found: fmt.Sprintf("%q", values[0]), Next: "pass --format text or --format json"}
	}
	return Format(values[0]), nil
}

// bind finds the command the words name and checks its flags: each a declared parameter, given
// once, with a value.
func bind(cmds []Command, c call) (Command, Input, error) {
	cmd, rest, err := find(cmds, c.Words)
	if err != nil {
		return Command{}, Input{}, err
	}
	if len(rest) > 0 {
		return Command{}, Input{}, &Error{Outcome: OutcomeUsage, Expected: "no argument after the command name",
			Found: fmt.Sprintf("%q", rest), Next: "pass each value as --name value"}
	}
	in := Input{Params: map[string]string{}}
	for _, f := range c.Flags {
		switch _, seen := in.Params[f.Name]; {
		case !contains(cmd.Params, f.Name):
			return Command{}, Input{}, &Error{Outcome: OutcomeUsage, Expected: "a parameter of " + name(cmd),
				Found: "--" + f.Name, Next: "run " + Product + " --help and pass only its parameters"}
		case !f.HasValue:
			return Command{}, Input{}, &Error{Outcome: OutcomeUsage, Expected: "a value for --" + f.Name,
				Found: "none", Next: "pass --" + f.Name + " <value>"}
		case seen:
			return Command{}, Input{}, &Error{Outcome: OutcomeUsage, Expected: "--" + f.Name + " at most once",
				Found: "it twice", Next: "pass --" + f.Name + " once"}
		}
		in.Params[f.Name] = f.Value
	}
	if err := resolveRepo(in); err != nil {
		return Command{}, Input{}, err
	}
	return cmd, in, nil
}

// find returns the command whose object, and verb when it has one, lead the words, and the words
// left after its name.
func find(cmds []Command, words []string) (Command, []string, error) {
	if len(words) == 0 {
		return Command{}, nil, &Error{Outcome: OutcomeUsage, Expected: "a command name", Found: "nothing",
			Next: "run " + Product + " --help"}
	}
	for _, cmd := range cmds {
		if cmd.Object != words[0] {
			continue
		}
		if cmd.Verb == "" {
			return cmd, words[1:], nil
		}
		if len(words) > 1 && cmd.Verb == words[1] {
			return cmd, words[2:], nil
		}
	}
	return Command{}, nil, &Error{Outcome: OutcomeUsage, Expected: "a registered command",
		Found: fmt.Sprintf("%q", strings.Join(words, " ")), Next: "run " + Product + " --help"}
}

// help lists every registered command with its one-line purpose (DR-23).
func help(cmds []Command) Result[any] {
	entries := make([]helpEntry, 0, len(cmds))
	var b strings.Builder
	b.WriteString("usage: " + Product + " <command> [--name value]...\n")
	for _, cmd := range cmds {
		entries = append(entries, helpEntry{Command: name(cmd), Purpose: cmd.Purpose})
		fmt.Fprintf(&b, "  %s  %s\n", name(cmd), cmd.Purpose)
	}
	return Result[any]{Status: StatusOK, Outcome: OutcomeOK, Data: entries, Detail: strings.TrimSuffix(b.String(), "\n")}
}

// name returns the command name as the user types it: the object, then the verb when there is one.
func name(cmd Command) string {
	return strings.TrimSpace(cmd.Object + " " + cmd.Verb)
}

func hasFlag(flags []flag, n string) bool {
	for _, f := range flags {
		if f.Name == n {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// write prints r and returns its exit code; a failed write exits as an error.
func write(out io.Writer, f Format, r Result[any]) int {
	if err := WriteResult(out, f, r); err != nil {
		return StatusError.ExitCode()
	}
	return r.Status.ExitCode()
}
