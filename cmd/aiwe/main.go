// Command aiwe is the CLI front: it wires the command registry and hands each call to core.RunCLI.
package main

import (
	"context"
	"os"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/engine"
	"github.com/sungurerdim/aiwe/internal/gates"
	"github.com/sungurerdim/aiwe/internal/hook"
	"github.com/sungurerdim/aiwe/internal/install"
)

// version is the one exception to the package-variable ban (SPEC §5.3): the release build sets it
// with -ldflags "-X main.version=<tag>", which only works on a package-level string variable.
var version string

func main() {
	os.Exit(core.RunCLI(context.Background(), registry(), os.Args[1:], os.Stdout, os.Stderr))
}

// registry returns the commands this binary serves.
func registry() []core.Command {
	cmds := []core.Command{
		{Module: "front", Object: "version", Params: []string{"format"},
			Purpose: "print the version of this binary", Run: runVersion},
	}
	cmds = append(cmds, core.ConfigCommands()...)
	cmds = append(cmds, engine.Commands()...)
	cmds = append(cmds, gates.Commands()...)
	cmds = append(cmds, hook.Commands(os.Stdin)...)
	return append(cmds, install.Commands(os.UserHomeDir)...)
}

// runVersion reports the version injected at build time, or unknown when none was injected.
func runVersion(_ context.Context, _ core.Input) (core.Result[any], error) {
	v := version
	if v == "" {
		v = "unknown"
	}
	return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK,
		Data: map[string]string{"version": v}, Detail: v}, nil
}
