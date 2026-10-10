// Command aiwe is the CLI front: it wires the command registry and hands each call to core.RunCLI.
package main

import (
	"context"
	"os"

	"github.com/sungurerdim/aiwe/internal/core"
)

// version is the one exception to the package-variable ban (SPEC §5.4): the release build sets it
// with -ldflags "-X main.version=<tag>", which only works on a package-level string variable.
var version string

func main() {
	os.Exit(core.RunCLI(context.Background(), registry(), os.Args[1:], os.Stdout))
}

// registry returns the commands this binary serves.
func registry() []core.Command {
	return []core.Command{
		{Module: "front", Object: "version", Params: []string{"format"},
			Purpose: "print the version of this binary", Run: runVersion},
	}
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
