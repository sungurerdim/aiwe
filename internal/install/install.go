// Package install serves `aiwe install`: it wires the hook commands of `aiwe hook <event>` into a
// harness's own settings and repairs drift, rewriting only aiwe's own entries (SPEC §10.4). The
// merge here is a function of bytes; install_io.go holds the file system calls (STD-10).
package install

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The kinds of change a merge reports: an entry added, an entry repaired, and settings whose
// hooks the owner turned off, which install reports and leaves as they are.
const (
	actionAdd      = "add"
	actionRepair   = "repair"
	actionDisabled = "disabled"
)

// disableKey is the settings key with which the owner turns every hook off (Claude Code).
const disableKey = "disableAllHooks"

// Wire is one event of a harness: the harness's name for it, the matcher its group needs ("" for
// none) and the verb of the `aiwe hook` command it calls.
type Wire struct {
	Event   string
	Matcher string
	Verb    string
}

// Harness is the one adapter data row of a harness (DR-3): the directory and file its settings
// live in, relative to the user's home, the events it wires, and the fields of a handler the owner
// may add to aiwe's that cannot change whether or how it runs. Any other field is drift. A harness
// without a hook is served by the work loop and has no row.
type Harness struct {
	Name  string
	Dir   string
	File  string
	Wires []Wire
	Keep  []string
}

// Entry is one hook entry the row asks for: the event, the group matcher, the exact command and
// the owner's fields a repair keeps.
type Entry struct {
	Event   string
	Matcher string
	Command string
	Keep    []string
}

// Change is one thing install did or would do to a harness's settings.
type Change struct {
	Harness string `json:"harness,omitempty"`
	Event   string `json:"event"`
	Action  string `json:"action"`
	Command string `json:"command"`
}

// Harnesses returns the adapter rows. The pre-tool entry watches every tool that runs a command,
// Bash, PowerShell and Monitor (Claude Code tools reference), so a heavy or destructive command
// meets the hook whichever shell tool runs it.
func Harnesses() []Harness {
	return []Harness{{
		Name: "claude-code", Dir: ".claude", File: "settings.json",
		Wires: []Wire{
			{Event: "Stop", Verb: "stop"},
			{Event: "SessionStart", Verb: "session-start"},
			{Event: "PostToolUse", Verb: "post-tool"},
			{Event: "PreToolUse", Matcher: "Bash|PowerShell|Monitor", Verb: "pre-tool"},
			{Event: "SubagentStop", Verb: "return"},
		},
		Keep: []string{"statusMessage"},
	}}
}

// Entries returns the hook entries of the row, one per wire.
func (h Harness) Entries() []Entry {
	out := make([]Entry, 0, len(h.Wires))
	for _, w := range h.Wires {
		out = append(out, Entry{Event: w.Event, Matcher: w.Matcher, Command: core.Product + " hook " + w.Verb,
			Keep: h.Keep})
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
// as given, so a second run is a no-op. Settings whose hooks the owner turned off are returned as
// given with one disabled change: the owner's choice is never overridden. Settings that are not one
// JSON object, or whose hooks are not an object of lists, are refused (DR-6).
func Merge(settings []byte, entries []Entry) ([]byte, []Change, error) {
	if _, err := core.ReadText(settings); err != nil {
		return nil, nil, refuse("settings that are UTF-8", err.Error(), err)
	}
	top, err := parseObject(settings, "the settings")
	if err != nil {
		return nil, nil, err
	}
	var off bool
	if json.Unmarshal(top[disableKey], &off) == nil && off {
		return settings, []Change{{Event: disableKey, Action: actionDisabled,
			Command: "nothing changed: the settings turn every hook off"}}, nil
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
	keep    []string
	// carry holds the fields of the first own handler that had to go, for the fresh handler.
	carry map[string]json.RawMessage
}

// wireEvent returns the groups of one event with aiwe's handler exact: the first own handler under
// the right matcher stays with its command set to the row's, every other own handler goes, and a
// fresh group is added when none stayed. The action is "add" when no own handler existed,
// "repair" when one was dropped or changed, and "" when the event was already exact.
func wireEvent(list []json.RawMessage, e Entry) ([]json.RawMessage, string, error) {
	s := &slot{matcher: e.Matcher, command: e.Command, keep: e.Keep}
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
		raw, err := s.fresh()
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
	right := matcherOf(g["matcher"]) == s.matcher
	keep := make([]json.RawMessage, 0, len(handlers))
	edited := false
	for _, h := range handlers {
		out, ok, err := s.handler(h, right)
		if err != nil {
			return nil, false, err
		}
		if ok {
			keep = append(keep, out)
		}
		edited = edited || !ok || !bytes.Equal(out, h)
	}
	if !edited {
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

// handler decides one handler of a group, under the right matcher or not. A foreign handler stays
// as it is. The first own handler under the right matcher stays with the owner's fields the row
// keeps; its type and command are set to the row's and every other field goes. Any other own handler goes; the fields of the
// first one that goes are carried to the fresh handler, so moving an entry keeps them too.
func (s *slot) handler(h json.RawMessage, right bool) (json.RawMessage, bool, error) {
	fields, own := ownFields(h)
	if !own {
		return h, true, nil
	}
	s.found = true
	if s.kept || !right {
		s.changed = true
		if s.carry == nil {
			s.carry = fields
		}
		return nil, false, nil
	}
	s.kept = true
	if isExact(fields, s.command, s.keep) {
		return h, true, nil
	}
	s.changed = true
	out, err := withCommand(fields, s.command, s.keep)
	return out, true, err
}

// fresh returns a new group of the row's matcher with one handler: the row's command and the
// fields carried from an own handler that went.
func (s *slot) fresh() (json.RawMessage, error) {
	h, err := withCommand(s.carry, s.command, s.keep)
	if err != nil {
		return nil, err
	}
	group := map[string]any{"hooks": []json.RawMessage{h}}
	if s.matcher != "" {
		group["matcher"] = s.matcher
	}
	return encode(group, "")
}

// withCommand returns a handler with the fields of an own handler that keep names, its type
// command and its command the row's.
func withCommand(fields map[string]json.RawMessage, command string, keep []string) (json.RawMessage, error) {
	out := make(map[string]any, len(keep)+2)
	for _, k := range keep {
		if v, ok := fields[k]; ok {
			out[k] = v
		}
	}
	out["type"], out["command"] = "command", command
	return encode(out, "")
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

// ownFields returns the fields of a handler and whether it is aiwe's: a command whose first two
// words are the product and "hook". A foreign command that merely mentions the product is not, and
// the key is matched as the harness matches it, case and all, so "Command" is no command.
func ownFields(h json.RawMessage) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	var command string
	if json.Unmarshal(h, &fields) != nil || json.Unmarshal(fields["command"], &command) != nil {
		return nil, false
	}
	f := strings.Fields(command)
	return fields, len(f) >= 2 && f[0] == core.Product && f[1] == "hook"
}

// isExact reports whether an own handler runs the row's command as a command and holds no field
// but those and the ones keep names; a field such as async, if or args disarms it, so it is drift.
func isExact(fields map[string]json.RawMessage, command string, keep []string) bool {
	for k := range fields {
		if k != "type" && k != "command" && !slices.Contains(keep, k) {
			return false
		}
	}
	var typ, cmd string
	return json.Unmarshal(fields["type"], &typ) == nil && json.Unmarshal(fields["command"], &cmd) == nil &&
		typ == "command" && cmd == command
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
