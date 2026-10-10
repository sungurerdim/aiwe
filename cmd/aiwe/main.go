// Command aiwe is the CLI front: it wires the command registry and prints each result envelope.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// version is the one exception to the package-variable ban (SPEC §5.4): the release build sets it
// with -ldflags "-X main.version=<tag>", which only works on a package-level string variable.
var version string

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout))
}

// registry returns the commands this binary serves.
func registry() []core.Command {
	return []core.Command{
		{Module: "front", Object: "version", Params: []string{"format"}, Run: runVersion},
	}
}

// runVersion reports the version injected at build time, or unknown when none was injected.
func runVersion(_ context.Context, _ core.Input) (core.Result[any], error) {
	v := version
	if v == "" {
		v = "unknown"
	}
	return core.Result[any]{Status: core.StatusOK, Outcome: core.Outcome("ok"),
		Data: map[string]string{"version": v}, Detail: v}, nil
}

// run executes one command call and writes its result envelope to out; it returns the exit code.
func run(ctx context.Context, args []string, out io.Writer) int {
	cmds := registry()
	if err := core.CheckParams(core.Dictionary(), cmds); err != nil {
		return write(out, core.FormatText, errorResult(err))
	}
	if len(args) == 0 {
		return write(out, core.FormatText, usage("no command given"))
	}
	var cmd *core.Command
	for i := range cmds {
		if cmds[i].Object == args[0] {
			cmd = &cmds[i]
		}
	}
	if cmd == nil {
		return write(out, core.FormatText, usage(fmt.Sprintf("unknown command %q", args[0])))
	}
	in, err := parseParams(args[1:], cmd.Params)
	if err != nil {
		return write(out, core.FormatText, usage(err.Error()))
	}
	format := core.FormatText
	if f, ok := in.Params["format"]; ok {
		format = core.Format(f)
	}
	if format != core.FormatText && format != core.FormatJSON {
		return write(out, core.FormatText, usage(fmt.Sprintf("unknown output format %q: want text or json", format)))
	}
	res, err := cmd.Run(ctx, in)
	if err != nil {
		res = errorResult(err)
	}
	return write(out, format, res)
}

// parseParams reads "--name value" and "--name=value" pairs, accepting only the given names.
func parseParams(args, allowed []string) (core.Input, error) {
	in := core.Input{Params: map[string]string{}}
	for i := 0; i < len(args); i++ {
		name, ok := strings.CutPrefix(args[i], "--")
		if !ok {
			return in, fmt.Errorf("unexpected argument %q", args[i])
		}
		name, value, hasValue := strings.Cut(name, "=")
		if !hasValue {
			if i+1 >= len(args) {
				return in, fmt.Errorf("parameter --%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if !contains(allowed, name) {
			return in, fmt.Errorf("unknown parameter --%s", name)
		}
		in.Params[name] = value
	}
	return in, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func usage(detail string) core.Result[any] {
	return core.Result[any]{Status: core.StatusUsage, Outcome: core.Outcome("usage"), Detail: detail}
}

func errorResult(err error) core.Result[any] {
	return core.Result[any]{Status: core.StatusError, Outcome: core.Outcome("error"), Detail: err.Error()}
}

// write prints r and returns its exit code; a failed write exits as an error.
func write(out io.Writer, f core.Format, r core.Result[any]) int {
	if err := core.WriteResult(out, f, r); err != nil {
		return core.StatusError.ExitCode()
	}
	return r.Status.ExitCode()
}
