package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// cliRegistry is a registry of commands that only use dictionary parameters.
func cliRegistry() []core.Command {
	ok := func(_ context.Context, _ core.Input) (core.Result[any], error) {
		return core.Result[any]{Status: core.StatusOK, Outcome: "ok",
			Data: map[string]string{"version": "v0.2.0"}, Detail: "v0.2.0"}, nil
	}
	return []core.Command{
		{Module: "front", Object: "version", Params: []string{"format"},
			Purpose: "print the version of this binary", Run: ok},
		{Module: "gates", Object: "gates", Verb: "gate", Params: []string{"format"},
			Purpose: "run the gate steps and print their evidence", Run: ok},
		{Module: "engine", Object: "unit", Verb: "check", Params: []string{"format"},
			Purpose: "check a unit issue against the unit template", Run: ok},
	}
}

func TestHelp(t *testing.T) {
	cmds := cliRegistry()
	var out bytes.Buffer
	code := core.RunCLI(context.Background(), cmds, []string{"--help"}, &out, io.Discard)
	if code != 0 {
		t.Errorf("%s --help exits %d, want 0; output:\n%s", core.Product, code, out.String())
	}
	lines := strings.Split(out.String(), "\n")
	for _, c := range cmds {
		t.Run("help omits "+strings.TrimSpace(c.Object+" "+c.Verb)+" or its purpose", func(t *testing.T) {
			for _, line := range lines {
				if !strings.Contains(line, c.Purpose) {
					continue
				}
				words := strings.Fields(line)
				if !containsWord(words, c.Object) || (c.Verb != "" && !containsWord(words, c.Verb)) {
					t.Errorf("help line %q carries the purpose of %s %s but not its name", line, c.Object, c.Verb)
				}
				return
			}
			t.Errorf("%s --help has no line with the purpose %q of %s %s; output:\n%s",
				core.Product, c.Purpose, c.Object, c.Verb, out.String())
		})
	}
}

// TestCLIProductName catches help and refusal text that names the product by a literal instead of
// core.Product, so a renamed product would print a stale name.
func TestCLIProductName(t *testing.T) {
	var help bytes.Buffer
	args := []string{"--help", "--format", "json"}
	if code := core.RunCLI(context.Background(), cliRegistry(), args, &help, io.Discard); code != 0 {
		t.Fatalf("%s --help exits %d, want 0; output:\n%s", core.Product, code, help.String())
	}
	var env struct{ Detail string }
	if err := json.Unmarshal(help.Bytes(), &env); err != nil {
		t.Fatalf("%s --help --format json printed %q, want one JSON result envelope: %v", core.Product, help.String(), err)
	}
	wantUsage := "usage: " + core.Product + " <command> [--name value]..."
	if first, _, _ := strings.Cut(env.Detail, "\n"); first != wantUsage {
		t.Errorf("help usage line = %q, want %q", first, wantUsage)
	}
	refusals := []struct {
		name string
		args []string
		next string
	}{
		{name: "no command name", args: []string{}, next: "run " + core.Product + " --help"},
		{name: "unknown command", args: []string{"versoin"}, next: "run " + core.Product + " --help"},
		{name: "undeclared parameter", args: []string{"version", "--bogus", "x"},
			next: "run " + core.Product + " --help and pass only its parameters"},
	}
	for _, tc := range refusals {
		t.Run(tc.name+" refusal names the product by a literal", func(t *testing.T) {
			var out bytes.Buffer
			code := core.RunCLI(context.Background(), cliRegistry(), tc.args, &out, io.Discard)
			if code != 2 || !strings.Contains(out.String(), tc.next) {
				t.Errorf("%s %s exits %d with %q, want exit 2 and the next step %q",
					core.Product, strings.Join(tc.args, " "), code, out.String(), tc.next)
			}
		})
	}
}

func containsWord(words []string, w string) bool {
	for _, x := range words {
		if strings.Trim(x, ":,") == w {
			return true
		}
	}
	return false
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantJSON bool
		status   core.Status
		exitCode int
	}{
		{name: "valid --format json call not printed as JSON",
			args: []string{"version", "--format", "json"}, wantJSON: true, status: core.StatusOK, exitCode: 0},
		{name: "parse error after --format json printed as text",
			args: []string{"version", "--format", "json", "--bogus", "x"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "parse error before --format json printed as text",
			args: []string{"version", "--bogus", "x", "--format", "json"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "parse error with --format=json printed as text",
			args: []string{"version", "--format=json", "--bogus=x"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "stray positional argument after --format json printed as text",
			args: []string{"version", "--format", "json", "extra"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "unknown command with --format json printed as text",
			args: []string{"versoin", "--format", "json"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "repeated --format with the same value accepted silently",
			args: []string{"version", "--format", "json", "--format", "json"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "repeated --format in both spellings accepted silently",
			args: []string{"version", "--format=json", "--format", "json"}, wantJSON: true, status: core.StatusUsage, exitCode: 2},
		{name: "repeated --format with two values accepted with the last one winning",
			args: []string{"version", "--format", "text", "--format", "json"}, status: core.StatusUsage, exitCode: 2},
		{name: "repeated --format with an empty second value accepted silently",
			args: []string{"version", "--format", "text", "--format="}, status: core.StatusUsage, exitCode: 2},
		{name: "unknown format accepted",
			args: []string{"version", "--format", "yaml"}, status: core.StatusUsage, exitCode: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			code := core.RunCLI(context.Background(), cliRegistry(), tc.args, &out, io.Discard)
			if code != tc.exitCode {
				t.Errorf("%s %s exits %d, want %d; output: %q", core.Product, strings.Join(tc.args, " "), code, tc.exitCode, out.String())
			}
			if tc.wantJSON {
				var env map[string]any
				if err := json.Unmarshal(out.Bytes(), &env); err != nil {
					t.Fatalf("%s %s printed %q, want one JSON result envelope: %v", core.Product, strings.Join(tc.args, " "), out.String(), err)
				}
				if env["status"] != string(tc.status) {
					t.Errorf("%s %s envelope status = %v, want %q", core.Product, strings.Join(tc.args, " "), env["status"], tc.status)
				}
				return
			}
			// The format is ambiguous here: either envelope is right if it carries the status.
			var env map[string]any
			isJSON := json.Unmarshal(out.Bytes(), &env) == nil && env["status"] == string(tc.status)
			if !isJSON && !strings.HasPrefix(out.String(), string(tc.status)+":") {
				t.Errorf("%s %s printed %q, want a result envelope with status %q", core.Product, strings.Join(tc.args, " "), out.String(), tc.status)
			}
		})
	}
}

// TestRunCLIStream catches a command that gets no stream writer, and streamed output that reaches
// stdout, where it breaks the --format json envelope, instead of the stream writer RunCLI is given
// (stderr in the binary, SPEC §5).
func TestRunCLIStream(t *testing.T) {
	const line = "cli-stream-test-line"
	cmds := []core.Command{{Module: "gates", Object: "gates", Verb: "gate", Params: []string{"format"},
		Purpose: "stream one line, then report ok",
		Run: func(_ context.Context, in core.Input) (core.Result[any], error) {
			if in.Stream == nil {
				return core.Result[any]{}, errors.New("the command got no stream writer")
			}
			if _, err := fmt.Fprintln(in.Stream, line); err != nil {
				return core.Result[any]{}, err
			}
			return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Detail: "streamed"}, nil
		}}}
	var out, stream bytes.Buffer
	code := core.RunCLI(context.Background(), cmds, []string{"gates", "gate", "--format", "json"}, &out, &stream)
	raw := out.String()
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, raw)
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, raw)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Errorf("stdout holds more than the one JSON envelope: %q", raw)
	}
	if strings.Contains(raw, line) {
		t.Errorf("streamed output reached stdout, mixed into the envelope: %q", raw)
	}
	if got := stream.String(); !strings.Contains(got, line) {
		t.Errorf("streamed output did not reach the stream writer: %q", got)
	}
}
