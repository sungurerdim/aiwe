// Command gatesteps prints the gate steps a change runs, for the CI workflow (SPEC §12.2, #316). It
// reads gate.json from the working directory through an os.Root and the changed paths from stdin,
// separated by NUL or newline as `git diff --no-renames --name-only -z` prints them, and calls
// gates.StepsForChange, so gate.json is the one source of the CI selection and the workflow never
// names a step or a change class itself.
//
// It writes one GitHub step output, build, true when the selection runs a `go test` step, so the
// build job tests every OS only when the change touches an input of the test step; build is the one
// output a job reads, so it is the only one written. The JSON list of the selected step ids goes to
// stderr, the run log, for a reader of the run. The gate job takes no list and runs every step: the
// runner counts a required step left out of --step as not measured (DR-6). No path, as on a manual
// run or a diff that could not be read, selects every step (fail closed).
//
// Usage: go run ./internal/cmd/gatesteps < paths >> "$GITHUB_OUTPUT"
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sungurerdim/aiwe/internal/gates"
)

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "gatesteps: %v\n", err)
		os.Exit(1)
	}
}

// run reads gate.json and the changed paths, writes the build output of their selection to out and
// logs the selected step ids to log. A selection with no step falls back to every step, so a change
// no step claims still sets build to true and runs the build matrix (fail closed).
func run(in io.Reader, out, log io.Writer) error {
	root, err := os.OpenRoot(".")
	if err != nil {
		return fmt.Errorf("open the working directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile("gate.json")
	if err != nil {
		return fmt.Errorf("read gate.json: %w", err)
	}
	def, err := gates.ParseDefinition(data)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read the changed paths: %w", err)
	}
	paths := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 || r == '\n' || r == '\r' })
	steps := gates.StepsForChange(def, paths)
	if len(steps) == 0 {
		steps = gates.StepsForChange(def, nil)
	}
	list, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("encode the step list: %w", err)
	}
	if _, err = fmt.Fprintf(log, "gatesteps: selected steps %s\n", list); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "build=%t\n", gates.SelectsCommand(def, steps, "go", "test"))
	return err
}
