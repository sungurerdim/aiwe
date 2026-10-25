// Package install serves `aiwe install`: it wires the hook commands of `aiwe hook <event>` into a
// harness's own settings and repairs drift, rewriting only aiwe's own entries (SPEC §10.4). The
// merge here is a function of bytes; install_io.go holds the file system calls (STD-10).
package install

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The two kinds of change a merge reports.
const (
	actionAdd    = "add"
	actionRepair = "repair"
)

// Wire is one event of a harness: the harness's name for it, the matcher its group needs ("" for
// none) and the verb of the `aiwe hook` command it calls.
type Wire struct {
	Event   string
	Matcher string
	Verb    string
}

// Harness is the one adapter data row of a harness (DR-3): the directory and file its settings
// live in, relative to the user's home, and the events it wires. A harness without a hook is served
// by the work loop and has no row.
type Harness struct {
	Name  string
	Dir   string
	File  string
	Wires []Wire
}

// Entry is one hook entry the row asks for: the event, the group matcher and the exact command.
type Entry struct {
	Event   string
	Matcher string
	Command string
}

// Change is one thing install did or would do to a harness's settings.
type Change struct {
	Harness string `json:"harness,omitempty"`
	Event   string `json:"event"`
	Action  string `json:"action"`
	Command string `json:"command"`
}

// Harnesses returns the adapter rows. The pre-tool entry watches Bash only: the heavy commands and
// the destructive ones the hook refuses run there.
func Harnesses() []Harness {
	return []Harness{{
		Name: "claude-code", Dir: ".claude", File: "settings.json",
		Wires: []Wire{
			{Event: "Stop", Verb: "stop"},
			{Event: "SessionStart", Verb: "session-start"},
			{Event: "PostToolUse", Verb: "post-tool"},
			{Event: "PreToolUse", Matcher: "Bash", Verb: "pre-tool"},
			{Event: "SubagentStop", Verb: "return"},
		},
	}}
}

// Entries returns the hook entries of the row, one per wire.
func (h Harness) Entries() []Entry {
	out := make([]Entry, 0, len(h.Wires))
	for _, w := range h.Wires {
		out = append(out, Entry{Event: w.Event, Matcher: w.Matcher, Command: core.Product + " hook " + w.Verb})
	}
	return out
}

// Commands returns the install command of the one command registry (DR-16). home resolves the
// user's home directory; the binary passes the process's, a test passes a temporary one.
func Commands(home func() (string, error)) []core.Command {
	return []core.Command{{Module: "install", Object: "install", Params: []string{"format"},
		Purpose: "wire the hook commands into each harness's settings and repair drift", Run: runner(home)}}
}

// runner returns the Run of `install`: it applies every row and lists what changed.
func runner(home func() (string, error)) func(context.Context, core.Input) (core.Result[any], error) {
	return func(_ context.Context, _ core.Input) (core.Result[any], error) {
		dir, err := home()
		if err != nil || dir == "" {
			return core.Result[any]{}, refuse("a resolved home directory", fmt.Sprint("none: ", err), err)
		}
		all := []Change{}
		for _, h := range Harnesses() {
			changes, err := Apply(dir, h)
			if err != nil {
				return core.Result[any]{}, fmt.Errorf("harness %s: %w", h.Name, err)
			}
			for _, c := range changes {
				c.Harness = h.Name
				all = append(all, c)
			}
		}
		return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: all, Detail: describe(all)}, nil
	}
}

// describe renders the changes as the diff install prints, one line each.
func describe(changes []Change) string {
	if len(changes) == 0 {
		return "up to date: no change"
	}
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, fmt.Sprintf("%s %s %s: %s", c.Action, c.Harness, c.Event, c.Command))
	}
	return strings.Join(lines, "\n")
}

// Merge wires entries into settings, a harness's settings file (nil or empty for none), and
// returns the new bytes and what changed. Only aiwe's own handlers are touched: a foreign handler,
// group, event and top-level key stay as they were. When nothing changed the bytes are returned
// as given, so a second run is a no-op. Settings that are not one JSON object, or whose hooks are
// not an object of lists, are refused (DR-6).
func Merge(settings []byte, entries []Entry) ([]byte, []Change, error) {
	if _, err := core.ReadText(settings); err != nil {
		return nil, nil, refuse("settings that are UTF-8", err.Error(), err)
	}
	top, err := parseObject(settings, "the settings")
	if err != nil {
		return nil, nil, err
	}
	hooks, err := parseObject(top["hooks"], "hooks")
	if err != nil {
		return nil, nil, err
	}
	changes, err := mergeEvents(hooks, entries)
	if err != nil {
		return nil, nil, err
	}
	if len(changes) == 0 {
		return settings, nil, nil
	}
	if top["hooks"], err = encode(hooks, ""); err != nil {
		return nil, nil, err
	}
	merged, err := encode(top, "  ")
	return append(merged, '\n'), changes, err
}

// mergeEvents wires each entry into its event of hooks, in place, and returns what changed.
func mergeEvents(hooks map[string]json.RawMessage, entries []Entry) ([]Change, error) {
	var changes []Change
	for _, e := range entries {
		list, err := parseList(hooks[e.Event], "hooks."+e.Event)
		if err != nil {
			return nil, err
		}
		out, action, err := wireEvent(list, e)
		if err != nil {
			return nil, err
		}
		if action == "" {
			continue
		}
		if hooks[e.Event], err = encode(out, ""); err != nil {
			return nil, err
		}
		changes = append(changes, Change{Event: e.Event, Action: action, Command: e.Command})
	}
	return changes, nil
}

// slot is the state of one event while its groups are scanned for aiwe's handlers.
type slot struct {
	matcher string
	command string
	kept    bool
	found   bool
	changed bool
}

// wireEvent returns the groups of one event with aiwe's handler exact: the first own handler that
// is exact under the right matcher stays, every other own handler goes, and a fresh group is added
// when none stayed. The action is "add" when no own handler existed, "repair" when one was
// dropped or replaced, and "" when the event was already exact.
func wireEvent(list []json.RawMessage, e Entry) ([]json.RawMessage, string, error) {
	s := &slot{matcher: e.Matcher, command: e.Command}
	out := make([]json.RawMessage, 0, len(list)+1)
	for _, raw := range list {
		g, keep, err := s.scan(raw)
		if err != nil {
			return nil, "", err
		}
		if keep {
			out = append(out, g)
		}
	}
	if !s.kept {
		group := map[string]any{"hooks": []any{map[string]string{"type": "command", "command": e.Command}}}
		if e.Matcher != "" {
			group["matcher"] = e.Matcher
		}
		raw, err := encode(group, "")
		if err != nil {
			return nil, "", err
		}
		out = append(out, raw)
	}
	switch {
	case !s.found:
		return out, actionAdd, nil
	case s.changed:
		return out, actionRepair, nil
	}
	return out, "", nil
}

// scan reads one matcher group. It returns the group without the own handlers that must go, and
// false when nothing but own handlers was in it and it is now empty. A group it does not change is
// returned as the raw bytes it came in.
func (s *slot) scan(raw json.RawMessage) (json.RawMessage, bool, error) {
	g, err := parseObject(raw, "a matcher group")
	if err != nil {
		return nil, false, err
	}
	handlers, err := parseList(g["hooks"], "a group's hooks")
	if err != nil {
		return nil, false, err
	}
	matcher := matcherOf(g["matcher"])
	keep := make([]json.RawMessage, 0, len(handlers))
	for _, h := range handlers {
		switch {
		case !isOwn(h):
			keep = append(keep, h)
		case !s.kept && matcher == s.matcher && isExact(h, s.command):
			s.found, s.kept = true, true
			keep = append(keep, h)
		default:
			s.found, s.changed = true, true
		}
	}
	if len(keep) == len(handlers) {
		return raw, true, nil
	}
	if len(keep) == 0 {
		return nil, false, nil
	}
	if g["hooks"], err = encode(keep, ""); err != nil {
		return nil, false, err
	}
	out, err := encode(g, "")
	return out, true, err
}

// matcherOf returns the matcher of a group; an absent one is "". A matcher that is not a string
// matches nothing, so the handler under it is moved.
func matcherOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "\x00" + string(raw)
	}
	return s
}

// isOwn reports whether a handler is aiwe's: a command whose first two words are the product and
// "hook". A foreign command that merely mentions the product is not.
func isOwn(h json.RawMessage) bool {
	var v struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(h, &v) != nil {
		return false
	}
	f := strings.Fields(v.Command)
	return len(f) >= 2 && f[0] == core.Product && f[1] == "hook"
}

// isExact reports whether an own handler is exactly the row's: type command, the command, no other
// field.
func isExact(h json.RawMessage, command string) bool {
	var v map[string]any
	if json.Unmarshal(h, &v) != nil {
		return false
	}
	return len(v) == 2 && v["type"] == "command" && v["command"] == command
}

// parseObject parses raw as a JSON object; an absent, blank or null value is an empty object.
func parseObject(raw []byte, what string) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, refuse(what+" as one JSON object", err.Error(), err)
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

// parseList parses raw as a JSON list; an absent or null value is an empty list.
func parseList(raw []byte, what string) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var l []json.RawMessage
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, refuse(what+" as a JSON list", err.Error(), err)
	}
	return l, nil
}

// encode renders v as JSON without HTML escaping, so a foreign value keeps its bytes; indent is
// the indent of the whole text, or "" for one line.
func encode(v any, indent string) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, refuse("settings that encode", err.Error(), err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// refuse builds the error of settings install will not touch: it fails closed, and the file stays
// as it was.
func refuse(expected, found string, cause error) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found, Err: cause,
		Next: "fix the settings file by hand, or move it aside, then run install again"}
}
