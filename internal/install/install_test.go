package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// wantWires are the five events a lead session needs and the hook command each one calls
// (SPEC §10.4). The command is the product name, "hook" and the event's verb.
func wantWires() map[string]string {
	return map[string]string{
		"Stop":         core.Product + " hook stop",
		"SessionStart": core.Product + " hook session-start",
		"PostToolUse":  core.Product + " hook post-tool",
		"PreToolUse":   core.Product + " hook pre-tool",
		"SubagentStop": core.Product + " hook return",
	}
}

// claudeCode returns the Claude Code adapter row.
func claudeCode(t *testing.T) Harness {
	t.Helper()
	for _, h := range Harnesses() {
		if h.Name == "claude-code" {
			return h
		}
	}
	t.Fatalf("Harnesses has no row named %q", "claude-code")
	return Harness{}
}

// settingsPath is the harness settings file under a temporary home.
func settingsPath(home string, h Harness) string {
	return filepath.Join(home, h.Dir, h.File)
}

// seed writes settings into the harness configuration directory of a temporary home.
func seed(t *testing.T, home string, h Harness, settings string) {
	t.Helper()
	dir := filepath.Join(home, h.Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
	if err := os.WriteFile(settingsPath(home, h), []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

// readBytes returns the settings file, failing the test when it is absent.
func readBytes(t *testing.T, home string, h Harness) []byte {
	t.Helper()
	b, err := os.ReadFile(settingsPath(home, h))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	return b
}

// group is one decoded matcher group of an event: its matcher and its handler objects.
type group struct {
	matcher  string
	handlers []map[string]any
}

// decode returns the settings as a decoded JSON object.
func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatalf("settings are not a JSON object: %v\n%s", err, b)
	}
	return top
}

// groupsOf returns the matcher groups of one event of decoded settings.
func groupsOf(t *testing.T, top map[string]any, event string) []group {
	t.Helper()
	hooks, _ := top["hooks"].(map[string]any)
	list, _ := hooks[event].([]any)
	var out []group
	for _, item := range list {
		g, _ := item.(map[string]any)
		m, _ := g["matcher"].(string)
		out = append(out, group{matcher: m})
		raw, _ := g["hooks"].([]any)
		for _, h := range raw {
			obj, _ := h.(map[string]any)
			out[len(out)-1].handlers = append(out[len(out)-1].handlers, obj)
		}
	}
	return out
}

// ownHandlers returns the handlers of one event whose command is one of aiwe's hook commands.
func ownHandlers(t *testing.T, top map[string]any, event string) []map[string]any {
	t.Helper()
	var own []map[string]any
	for _, g := range groupsOf(t, top, event) {
		for _, h := range g.handlers {
			if c, _ := h["command"].(string); strings.HasPrefix(c, core.Product+" hook ") {
				own = append(own, h)
			}
		}
	}
	return own
}

// TestInstallWritesTheFiveEntries proves the Claude Code row wires exactly the five events, each
// with the one hook command of its verb, so a harness event never calls an unregistered command.
func TestInstallWritesTheFiveEntries(t *testing.T) {
	t.Parallel()
	h := claudeCode(t)
	got := map[string]string{}
	for _, e := range h.Entries() {
		if _, dup := got[e.Event]; dup {
			t.Errorf("event %s appears twice in the row: the harness would run both entries", e.Event)
		}
		got[e.Event] = e.Command
	}
	want := wantWires()
	if len(got) != len(want) {
		t.Fatalf("row wires %d events %v, want %d %v", len(got), got, len(want), want)
	}
	for event, command := range want {
		if got[event] != command {
			t.Errorf("event %s command = %q, want %q", event, got[event], command)
		}
	}
}

// TestInstallIdempotent proves the first run writes the five entries and a second run changes
// nothing: no change reported and the settings file byte for byte the same (STD-11).
func TestInstallIdempotent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		catches string
		before  string
	}{
		{"no settings file", "an install that needs a file to exist before it can write one", ""},
		{"empty settings object", "an install that writes the entries twice into an empty object", `{}`},
		{"other settings present", "an install that rewrites or reorders the user's other settings on every run",
			`{"model":"opus","permissions":{"allow":["Read"]},"hooks":{"Notification":[{"hooks":[{"type":"command","command":"notify-send done"}]}]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			h := claudeCode(t)
			if tc.before != "" {
				seed(t, home, h, tc.before)
			}
			first, err := Apply(home, h)
			if err != nil {
				t.Fatalf("first install: %v; catches: %s", err, tc.catches)
			}
			if len(first) != len(wantWires()) {
				t.Errorf("first install reported %d changes %v, want %d", len(first), first, len(wantWires()))
			}
			top := decode(t, readBytes(t, home, h))
			for event, command := range wantWires() {
				own := ownHandlers(t, top, event)
				if len(own) != 1 || own[0]["command"] != command || own[0]["type"] != "command" {
					t.Errorf("after the first install event %s holds own handlers %v, want exactly one command %q",
						event, own, command)
				}
			}
			written := readBytes(t, home, h)
			second, err := Apply(home, h)
			if err != nil {
				t.Fatalf("second install: %v; catches: %s", err, tc.catches)
			}
			if len(second) != 0 {
				t.Errorf("second install reported changes %v, want none; catches: %s", second, tc.catches)
			}
			if after := readBytes(t, home, h); !bytes.Equal(after, written) {
				t.Errorf("second install changed the settings file\nbefore %s\nafter  %s\ncatches: %s", written, after, tc.catches)
			}
		})
	}
}

// TestInstallKeepsOtherSettings proves a first install adds only the hook entries: every other
// top-level key and every other event survive with their values.
func TestInstallKeepsOtherSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	h := claudeCode(t)
	const before = `{"model":"opus","env":{"A":"1&2<3"},"count":12345678901234567890,` +
		`"hooks":{"Notification":[{"matcher":"x","hooks":[{"type":"command","command":"notify-send done","timeout":5}]}]}}`
	seed(t, home, h, before)
	if _, err := Apply(home, h); err != nil {
		t.Fatalf("install: %v", err)
	}
	after := readBytes(t, home, h)
	top := decode(t, after)
	prior := decode(t, []byte(before))
	for _, key := range []string{"model", "env", "count"} {
		if got, want := mustJSON(t, top[key]), mustJSON(t, prior[key]); got != want {
			t.Errorf("top-level %q = %s, want %s: install rewrote a setting that is not its own", key, got, want)
		}
	}
	got := mustJSON(t, groupsOf(t, top, "Notification")[0].handlers)
	want := mustJSON(t, groupsOf(t, prior, "Notification")[0].handlers)
	if got != want {
		t.Errorf("foreign Notification handlers = %s, want %s", got, want)
	}
	if !bytes.Contains(after, []byte("1&2<3")) || !bytes.Contains(after, []byte("12345678901234567890")) {
		t.Errorf("install re-encoded a foreign value (HTML escape or number rounding):\n%s", after)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(b)
}

// TestInstallRefusesSettingsItCannotMerge proves install fails closed: settings that are not a JSON
// object, or whose hooks are not an object, are refused and the file stays byte for byte as it was.
func TestInstallRefusesSettingsItCannotMerge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		catches string
		before  string
	}{
		{"invalid json", "a broken file replaced by a fresh one, losing the user's settings", `{"model":`},
		{"json array", "an array replaced by an object", `["a"]`},
		{"hooks not an object", "a hooks value of another type overwritten", `{"hooks":"off"}`},
		{"event not a list", "an event value of another type overwritten", `{"hooks":{"Stop":{"x":1}}}`},
		{"group not an object", "a matcher group of another type overwritten", `{"hooks":{"Stop":["x"]}}`},
		{"group hooks not a list", "a group's hooks of another type overwritten", `{"hooks":{"Stop":[{"hooks":"x"}]}}`},
		{"invalid utf-8", "bytes that are not UTF-8 merged (DR-18)", "{\"model\":\"\xff\"}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			h := claudeCode(t)
			seed(t, home, h, tc.before)
			if changes, err := Apply(home, h); err == nil {
				t.Fatalf("install accepted %.30q with changes %v; catches: %s", tc.before, changes, tc.catches)
			}
			if after := readBytes(t, home, h); string(after) != tc.before {
				t.Errorf("a refused install changed the file to %q, want %q", after, tc.before)
			}
		})
	}
}

// TestInstallCommandRunsThroughTheRegistry proves `install` is one registry command that works on
// the home it is given: it writes the entries, and a second call reports and changes nothing.
func TestInstallCommandRunsThroughTheRegistry(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	h := claudeCode(t)
	cmds := Commands(func() (string, error) { return home, nil })
	if err := core.CheckParams(core.Dictionary(), cmds); err != nil {
		t.Fatalf("the install command uses a parameter the dictionary does not define: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Object != "install" || cmds[0].Verb != "" || cmds[0].Purpose == "" {
		t.Fatalf("Commands = %+v, want the one command `install` with a purpose", cmds)
	}
	res, err := cmds[0].Run(t.Context(), core.Input{Params: map[string]string{}})
	if err != nil || res.Status != core.StatusOK {
		t.Fatalf("first install = %+v, %v; want status ok", res, err)
	}
	written := readBytes(t, home, h)
	res, err = cmds[0].Run(t.Context(), core.Input{Params: map[string]string{}})
	if err != nil || res.Status != core.StatusOK {
		t.Fatalf("second install = %+v, %v; want status ok", res, err)
	}
	if after := readBytes(t, home, h); !bytes.Equal(after, written) {
		t.Errorf("the second install call changed the settings:\n%s\n%s", written, after)
	}
	if strings.Contains(res.Detail, "add") {
		t.Errorf("the second install detail %q still reports an add", res.Detail)
	}
}

// TestInstallCommandRefusesWithoutHome proves an unresolved home is refused, not guessed.
func TestInstallCommandRefusesWithoutHome(t *testing.T) {
	t.Parallel()
	cmds := Commands(func() (string, error) { return "", os.ErrNotExist })
	if _, err := cmds[0].Run(t.Context(), core.Input{Params: map[string]string{}}); err == nil {
		t.Fatal("install ran without a home directory; want a refusal so no file lands in the working directory")
	}
}
