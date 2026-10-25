package install

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestInstallRepairsOnlyOwnEntries proves a drifted aiwe entry is restored to the adapter row and
// every foreign entry, event and setting is left as it was (SPEC §10.4, F5): the repair rewrites
// only what aiwe wrote.
func TestInstallRepairsOnlyOwnEntries(t *testing.T) {
	t.Parallel()
	stop := core.Product + " hook stop"
	pre := core.Product + " hook pre-tool"
	cases := []struct {
		name    string
		catches string
		before  string
		// foreign maps an event to the foreign handler objects that must survive unchanged.
		foreign map[string][]string
		// other are top-level keys other than hooks that must survive unchanged.
		other []string
	}{
		{
			name:    "own command changed",
			catches: "a drifted command left in place, so the stop hook calls something else",
			before: `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + stop + ` --off"},` +
				`{"type":"command","command":"echo foreign-stop","timeout":3}]}]}}`,
			foreign: map[string][]string{"Stop": {`{"type":"command","command":"echo foreign-stop","timeout":3}`}},
			other:   []string{"model"},
		},
		{
			name:    "own entry deleted",
			catches: "a missing entry not restored, or the foreign event dropped while restoring",
			before:  `{"hooks":{"Notification":[{"matcher":"idle","hooks":[{"type":"command","command":"notify-send done"}]}]}}`,
			foreign: map[string][]string{"Notification": {`{"type":"command","command":"notify-send done"}`}},
		},
		{
			name:    "own handler type changed",
			catches: "a handler turned into another type kept as install's own",
			before:  `{"hooks":{"PostToolUse":[{"hooks":[{"type":"prompt","command":"` + core.Product + ` hook post-tool"}]}]}}`,
		},
		{
			name:    "own entry duplicated",
			catches: "a duplicate kept, so the hook runs twice per event",
			before: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + stop + `"}]},` +
				`{"hooks":[{"type":"command","command":"` + stop + `"}]}]}}`,
		},
		{
			name:    "own entry under a wrong matcher beside a foreign handler",
			catches: "the group matcher changed under the user's own handler, or the own entry left under the wrong matcher",
			before: `{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"` + pre + `"},` +
				`{"type":"command","command":"lint-write"}]}]}}`,
			foreign: map[string][]string{"PreToolUse": {`{"type":"command","command":"lint-write"}`}},
		},
		{
			name:    "own entry on the wrong event",
			catches: "an own command under another event treated as that event's entry, so the real event stays unwired",
			before:  `{"hooks":{"SubagentStop":[{"hooks":[{"type":"command","command":"` + stop + `"}]}]}}`,
		},
		{
			name:    "foreign handler that only mentions the product",
			catches: "a foreign command that merely contains the product name taken for install's own and rewritten",
			before: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo ` + core.Product +
				` hook stop done"}]}]},"permissions":{"deny":["Bash(rm:*)"]}}`,
			foreign: map[string][]string{"Stop": {`{"type":"command","command":"echo ` + core.Product + ` hook stop done"}`}},
			other:   []string{"permissions"},
		},
		{
			name:    "foreign handler with an own command under another case",
			catches: "a key matched without regard to case, so a foreign handler is taken for install's own and dropped",
			before: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/opt/audit/log-stop.sh","Command":"` +
				stop + `"}]}]}}`,
			foreign: map[string][]string{"Stop": {`{"type":"command","command":"/opt/audit/log-stop.sh","Command":"` + stop + `"}`}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			h := claudeCode(t)
			seed(t, home, h, tc.before)
			prior := decode(t, []byte(tc.before))
			changes, err := Apply(home, h)
			if err != nil {
				t.Fatalf("install: %v; catches: %s", err, tc.catches)
			}
			if len(changes) == 0 {
				t.Errorf("install reported no change on drifted settings; catches: %s", tc.catches)
			}
			top := decode(t, readBytes(t, home, h))
			for event, command := range wantWires() {
				own := ownHandlers(t, top, event)
				want := map[string]any{"type": "command", "command": command}
				if len(own) != 1 || !reflect.DeepEqual(own[0], want) {
					t.Errorf("event %s own handlers = %v, want exactly %v; catches: %s", event, own, want, tc.catches)
				}
			}
			assertMatchers(t, top, tc.catches)
			assertForeign(t, top, tc.foreign)
			for _, key := range tc.other {
				if mustJSON(t, top[key]) != mustJSON(t, prior[key]) {
					t.Errorf("setting %q changed to %s from %s: repair touched what is not its own",
						key, mustJSON(t, top[key]), mustJSON(t, prior[key]))
				}
			}
			if again, err := Apply(home, h); err != nil || len(again) != 0 {
				t.Errorf("a second install after the repair = %v, %v; want no change", again, err)
			}
		})
	}
}

// assertMatchers checks that every own handler sits in a group whose matcher is the row's.
func assertMatchers(t *testing.T, top map[string]any, catches string) {
	t.Helper()
	for _, e := range claudeCode(t).Entries() {
		for _, g := range groupsOf(t, top, e.Event) {
			for _, hd := range g.handlers {
				if hd["command"] == e.Command && g.matcher != e.Matcher {
					t.Errorf("event %s own handler sits under matcher %q, want %q; catches: %s",
						e.Event, g.matcher, e.Matcher, catches)
				}
			}
		}
	}
}

// assertForeign checks that each foreign handler object is still present, equal to its input.
func assertForeign(t *testing.T, top map[string]any, foreign map[string][]string) {
	t.Helper()
	for event, handlers := range foreign {
		var have []map[string]any
		for _, g := range groupsOf(t, top, event) {
			have = append(have, g.handlers...)
		}
		for _, raw := range handlers {
			var want map[string]any
			if err := json.Unmarshal([]byte(raw), &want); err != nil {
				t.Fatalf("bad test handler %s: %v", raw, err)
			}
			found := false
			for _, hd := range have {
				found = found || reflect.DeepEqual(hd, want)
			}
			if !found {
				t.Errorf("foreign handler %s of event %s is gone or changed; event now holds %v", raw, event, have)
			}
		}
	}
}

// TestMergeReportsEachChange proves Merge names what it changed, so install can print the diff
// before the write (SPEC §10.4): an add for a missing entry, a repair for a drifted one, and
// nothing for an entry already exact.
func TestMergeReportsEachChange(t *testing.T) {
	t.Parallel()
	entries := claudeCode(t).Entries()
	merged, changes, err := Merge(nil, entries)
	if err != nil {
		t.Fatalf("merge into nothing: %v", err)
	}
	for _, c := range changes {
		if c.Action != "add" {
			t.Errorf("change %+v into empty settings, want action %q", c, "add")
		}
	}
	if len(changes) != len(entries) {
		t.Errorf("merge into nothing reported %d changes, want %d", len(changes), len(entries))
	}
	drifted := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + core.Product + ` hook stop -x"}]}]}}`)
	_, changes, err = Merge(drifted, entries)
	if err != nil {
		t.Fatalf("merge into drifted settings: %v", err)
	}
	repairs := 0
	for _, c := range changes {
		if c.Event == "Stop" && c.Action == "repair" {
			repairs++
		}
	}
	if repairs != 1 {
		t.Errorf("drifted Stop reported %d repairs in %v, want 1", repairs, changes)
	}
	again, changes, err := Merge(merged, entries)
	if err != nil || len(changes) != 0 {
		t.Errorf("merging an installed file = %v, %v; want no change", changes, err)
	}
	if string(again) != string(merged) {
		t.Errorf("merging an installed file changed its bytes:\n%s\n%s", merged, again)
	}
}

// installed returns settings with every entry of the row exact, except PreToolUse, whose one
// handler is handler under matcher.
func installed(t *testing.T, matcher, handler string) string {
	t.Helper()
	hooks := map[string]json.RawMessage{}
	for _, e := range claudeCode(t).Entries() {
		h := `{"type":"command","command":"` + e.Command + `"}`
		m := e.Matcher
		if e.Event == "PreToolUse" {
			h, m = handler, matcher
		}
		group := `{"hooks":[` + h + `]}`
		if m != "" {
			group = `{"matcher":` + mustJSON(t, m) + `,"hooks":[` + h + `]}`
		}
		hooks[e.Event] = json.RawMessage(`[` + group + `]`)
	}
	return `{"hooks":` + mustJSON(t, hooks) + `}`
}

// TestInstallKeepsOwnerFields proves install rewrites only what is its own in its entry, the
// command: every field the owner added that the row keeps, a timeout or a status message, stays,
// and an entry that already runs the row's command is no drift (security review B2).
func TestInstallKeepsOwnerFields(t *testing.T) {
	t.Parallel()
	pre := core.Product + " hook pre-tool"
	var row Entry
	for _, e := range claudeCode(t).Entries() {
		if e.Event == "PreToolUse" {
			row = e
		}
	}
	cases := []struct {
		name    string
		catches string
		matcher string
		handler string
		change  bool
	}{
		{"owner tuned the entry", "an owner's timeout and status message stripped as drift",
			row.Matcher, `{"type":"command","command":"` + pre + `","statusMessage":"guard","timeout":5}`, false},
		{"command drifted", "a repair that drops the owner's fields while it restores the command",
			row.Matcher, `{"type":"command","command":"` + pre + ` --off","statusMessage":"guard","timeout":5}`, true},
		{"entry under the old matcher", "the owner's fields lost when the entry moves to the row's matcher",
			"Bash", `{"type":"command","command":"` + pre + `","statusMessage":"guard","timeout":5}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			h := claudeCode(t)
			before := installed(t, tc.matcher, tc.handler)
			seed(t, home, h, before)
			changes, err := Apply(home, h)
			if err != nil || (len(changes) != 0) != tc.change {
				t.Fatalf("install = %v, %v; want a change %v; catches: %s", changes, err, tc.change, tc.catches)
			}
			own := ownHandlers(t, decode(t, readBytes(t, home, h)), "PreToolUse")
			want := map[string]any{"type": "command", "command": pre, "statusMessage": "guard", "timeout": float64(5)}
			if len(own) != 1 || !reflect.DeepEqual(own[0], want) {
				t.Errorf("PreToolUse own handlers = %v, want exactly %v; catches: %s", own, want, tc.catches)
			}
			assertMatchers(t, decode(t, readBytes(t, home, h)), tc.catches)
			if !tc.change && string(readBytes(t, home, h)) != before {
				t.Errorf("install rewrote settings it reported no change for; catches: %s", tc.catches)
			}
		})
	}
}

// TestInstallRepairsFieldsThatDisarm proves an own handler with a field that changes whether or how
// it runs is drift: repair removes the field and reports it, so the guard is never "up to date"
// while it no longer blocks (security re-review N1).
func TestInstallRepairsFieldsThatDisarm(t *testing.T) {
	t.Parallel()
	pre := core.Product + " hook pre-tool"
	cases := []struct {
		name    string
		catches string
		field   string
	}{
		{"async", "a handler run in the background, whose exit 2 blocks nothing, read as exact", `"async":true`},
		{"if", "a handler narrowed to a rule that never matches read as exact", `"if":"Bash(zz-never-matches *)"`},
		{"args", "a handler spawned as a program that does not exist read as exact", `"args":[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			h := claudeCode(t)
			seed(t, home, h, installed(t, preToolMatcher(t), `{"type":"command","command":"`+pre+`",`+tc.field+`}`))
			changes, err := Apply(home, h)
			want := []Change{{Event: "PreToolUse", Action: actionRepair, Command: pre}}
			if err != nil || !reflect.DeepEqual(changes, want) {
				t.Fatalf("install = %v, %v; want %v; catches: %s", changes, err, want, tc.catches)
			}
			own := ownHandlers(t, decode(t, readBytes(t, home, h)), "PreToolUse")
			if exact := map[string]any{"type": "command", "command": pre}; len(own) != 1 || !reflect.DeepEqual(own[0], exact) {
				t.Errorf("PreToolUse own handlers = %v, want exactly %v; catches: %s", own, exact, tc.catches)
			}
		})
	}
}
