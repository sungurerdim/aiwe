// Package hook serves `aiwe hook <event>`: the one entry a harness calls on an event of a lead
// session (SPEC §10.4). Each event reads the harness input on stdin and fails closed on input it
// cannot trust. The hooks are thin adapters and an early warning; the rules stay in the core (K36).
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// maxInputBytes caps what one hook call reads from stdin; larger input is refused unread further.
const maxInputBytes = 1 << 20

// Input is what a hook reads of the harness input. The JSON tags are the one data row that names
// the harness's fields (DR-3; Claude Code names): the session, the field that says a stop hook
// already blocked once, the tool of a tool event and the working directory. Every other field of
// the input is ignored, so a harness update does not break a hook.
type Input struct {
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
	ToolName       string `json:"tool_name"`
	Cwd            string `json:"cwd"`
}

// event is one hook event: the verb that follows `hook` and its one-line purpose.
type event struct {
	verb    string
	purpose string
}

// events returns the hook events `aiwe install` wires, one registry command each (DR-16).
func events() []event {
	return []event{
		{"stop", "decide at the end of a turn whether the session may stop"},
		{"session-start", "hand the open items to a new session"},
		{"post-tool", "record that the session is alive after a tool call"},
		{"pre-tool", "refuse a direct heavy command before the tool runs"},
		{"return", "check a subagent's final message when it stops"},
	}
}

// Commands returns the hook commands of the one command registry. stdin is the stream the harness
// writes its input to; the binary passes the process's standard input.
func Commands(stdin io.Reader) []core.Command {
	list := events()
	cmds := make([]core.Command, 0, len(list))
	for _, ev := range list {
		cmds = append(cmds, core.Command{Module: "hook", Object: "hook", Verb: ev.verb,
			Params: []string{"format"}, Purpose: ev.purpose, Run: runner(ev.verb, stdin)})
	}
	return cmds
}

// runner returns the Run of one hook event: it reads the harness input and passes it on.
func runner(verb string, stdin io.Reader) func(context.Context, core.Input) (core.Result[any], error) {
	return func(_ context.Context, _ core.Input) (core.Result[any], error) {
		in, err := ReadInput(stdin)
		if err != nil {
			return core.Result[any]{}, err
		}
		return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: in,
			Detail: "allow " + verb}, nil
	}
}

// ReadInput reads the harness input from r, at most maxInputBytes, and parses it.
func ReadInput(r io.Reader) (Input, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return Input{}, refuse("readable harness input on stdin", err.Error(), err)
	}
	if len(b) > maxInputBytes {
		return Input{}, refuse(fmt.Sprintf("harness input of at most %d bytes", maxInputBytes),
			"more than that", nil)
	}
	return ParseInput(b)
}

// ParseInput parses b as the harness's input: one UTF-8 JSON object with a non-empty session
// identity. Anything else is refused, never read as an allow (DR-6).
func ParseInput(b []byte) (Input, error) {
	if _, err := core.ReadText(b); err != nil {
		return Input{}, refuse("harness input that is UTF-8", err.Error(), err)
	}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Input{}, refuse("one JSON object on stdin", describe(trimmed), nil)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var in Input
	if err := dec.Decode(&in); err != nil {
		return Input{}, refuse("a JSON object whose known fields have their types", err.Error(), err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Input{}, refuse("one JSON object on stdin", "more content after the object", nil)
	}
	if in.SessionID == "" {
		return Input{}, refuse("a non-empty session_id in the harness input", "none", nil)
	}
	return in, nil
}

// describe names what stdin held, for a refusal: nothing, or its first bytes.
func describe(trimmed []byte) string {
	if len(trimmed) == 0 {
		return "no input"
	}
	return fmt.Sprintf("%q", strings.ToValidUTF8(string(trimmed[:min(len(trimmed), 20)]), "?"))
}

// refuse builds the error of an input the hook cannot trust: the harness call fails, so a broken
// harness cannot turn a hook into a silent pass.
func refuse(expected, found string, cause error) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found, Err: cause,
		Next: "check that the harness passes its event JSON on stdin, then run the event again"}
}
