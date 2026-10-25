package hook

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// hookVerbs are the five events the harness installation wires (SPEC §10.4): the stop hook, the
// session-start handoff, the heartbeat, the direct-heavy-command refusal and the return check.
func hookVerbs() []string {
	return []string{"stop", "session-start", "post-tool", "pre-tool", "return"}
}

// runHook runs the registered `hook <verb>` command with stdin as the harness input.
func runHook(t *testing.T, verb, stdin string) (core.Result[any], error) {
	t.Helper()
	for _, c := range Commands(strings.NewReader(stdin)) {
		if c.Object == "hook" && c.Verb == verb {
			return c.Run(t.Context(), core.Input{Params: map[string]string{}})
		}
	}
	t.Fatalf("no registered command %q %q", "hook", verb)
	return core.Result[any]{}, nil
}

// TestHookRegistersEveryEvent proves each hook event is one command of the one registry (DR-16),
// with a purpose for --help and parameters the dictionary defines.
func TestHookRegistersEveryEvent(t *testing.T) {
	t.Parallel()
	cmds := Commands(strings.NewReader(""))
	if err := core.CheckParams(core.Dictionary(), cmds); err != nil {
		t.Fatalf("the hook commands use a parameter the dictionary does not define: %v", err)
	}
	for _, verb := range hookVerbs() {
		found := false
		for _, c := range cmds {
			if c.Object != "hook" || c.Verb != verb {
				continue
			}
			found = true
			if c.Purpose == "" || c.Run == nil {
				t.Errorf("hook %s has purpose %q and a nil Run = %v; want both set", verb, c.Purpose, c.Run == nil)
			}
		}
		if !found {
			t.Errorf("hook %s is not registered: a harness entry that calls it would fail with an unknown command", verb)
		}
	}
	if len(cmds) != len(hookVerbs()) {
		t.Errorf("Commands returns %d commands, want %d: an event without an installed entry is dead code", len(cmds), len(hookVerbs()))
	}
}

// TestHookReadsHarnessInput proves every event reads the session identity from the harness's JSON
// on stdin and passes the loop-guard and tool fields on, so the later decision reads one shape.
func TestHookReadsHarnessInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		catches string
		stdin   string
		want    Input
	}{
		{
			name:    "session id only",
			catches: "a hook that needs more than the session id to accept the input",
			stdin:   `{"session_id":"s1"}`,
			want:    Input{SessionID: "s1"},
		},
		{
			name:    "stop hook already active",
			catches: "the loop-guard field dropped, so a stop hook cannot tell it already blocked once",
			stdin:   `{"session_id":"s2","stop_hook_active":true,"cwd":"/work"}`,
			want:    Input{SessionID: "s2", StopHookActive: true, Cwd: "/work"},
		},
		{
			name:    "tool call with unknown extra fields",
			catches: "an unknown harness field refused, so a harness update breaks every hook",
			stdin:   `{"session_id":"s3","tool_name":"Bash","tool_input":{"command":"ls"},"future":[1,2]}`,
			want:    Input{SessionID: "s3", ToolName: "Bash"},
		},
	}
	for _, tc := range cases {
		for _, verb := range hookVerbs() {
			t.Run(tc.name+"/"+verb, func(t *testing.T) {
				t.Parallel()
				res, err := runHook(t, verb, tc.stdin)
				if err != nil {
					t.Fatalf("hook %s on %s returned %v; catches: %s", verb, tc.stdin, err, tc.catches)
				}
				if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK {
					t.Errorf("hook %s status = %q outcome = %q, want ok; catches: %s", verb, res.Status, res.Outcome, tc.catches)
				}
				got, ok := res.Data.(Input)
				if !ok || got != tc.want {
					t.Errorf("hook %s data = %#v, want %#v; catches: %s", verb, res.Data, tc.want, tc.catches)
				}
			})
		}
	}
}

// TestHookRefusesBadInput proves the hook fails closed: input it cannot trust is an error, never an
// allow (DR-6), so a broken harness cannot turn the stop hook into a silent pass.
func TestHookRefusesBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		catches string
		stdin   string
	}{
		{"empty input", "an empty stdin read as an allow", ""},
		{"blank input", "whitespace read as an allow", " \n\t "},
		{"not json", "text input accepted as an event", "stop now"},
		{"json array", "an array accepted as the event object", `[{"session_id":"s1"}]`},
		{"json null", "null accepted as the event object", `null`},
		{"missing session id", "an event without a session accepted, so its record has no owner", `{"cwd":"/work"}`},
		{"empty session id", "an empty session id accepted as an identity", `{"session_id":""}`},
		{"session id not a string", "a number accepted as the session identity", `{"session_id":7}`},
		{"stop flag not a bool", "a string accepted as the loop-guard flag", `{"session_id":"s1","stop_hook_active":"yes"}`},
		{"invalid utf-8", "bytes that are not UTF-8 accepted (DR-18)", "{\"session_id\":\"s\xff\"}"},
		{"oversized input", "an unbounded stdin read whole", `{"session_id":"s1","pad":"` + strings.Repeat("x", 2<<20) + `"}`},
		{"trailing second object", "two objects read as one event", `{"session_id":"s1"}{"session_id":"s2"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := runHook(t, "stop", tc.stdin)
			if err == nil {
				t.Fatalf("hook stop accepted %.40q with result %#v; catches: %s", tc.stdin, res, tc.catches)
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
				t.Errorf("hook stop refusal = %v, want a *core.Error with outcome %q (SPEC §5.3)", err, core.OutcomeError)
			}
		})
	}
}

// TestHookFailsClosedPerEvent proves each event turns input it cannot trust into the exit its
// harness needs and one reason line on stderr (the reason Claude Code shows). pre-tool exits 2,
// so Claude Code blocks the tool call, where exit 1 is a non-blocking error; every other event
// exits 1, so a harness bug never keeps a session from stopping.
func TestHookFailsClosedPerEvent(t *testing.T) {
	t.Parallel()
	inputs := []struct {
		name    string
		catches string
		stdin   string
	}{
		{"empty stdin", "an empty stdin that lets the tool call run", ""},
		{"over 1 MiB", "an oversized input that lets the tool call run",
			`{"session_id":"s1","pad":"` + strings.Repeat("x", 2<<20) + `"}`},
		{"invalid utf-8", "bytes that are not UTF-8 that let the tool call run", "{\"session_id\":\"s\xff\"}"},
		{"missing session_id", "an event without a session that lets the tool call run", `{"cwd":"/work"}`},
	}
	wantCode := map[string]int{"stop": 1, "session-start": 1, "post-tool": 1, "pre-tool": 2, "return": 1}
	for _, in := range inputs {
		for _, verb := range hookVerbs() {
			t.Run(in.name+"/"+verb, func(t *testing.T) {
				t.Parallel()
				var out, stderr bytes.Buffer
				code := core.RunCLI(t.Context(), Commands(strings.NewReader(in.stdin)), []string{"hook", verb}, &out, &stderr)
				if code != wantCode[verb] {
					t.Errorf("hook %s exit = %d, want %d; catches: %s", verb, code, wantCode[verb], in.catches)
				}
				reason := stderr.String()
				if strings.Count(reason, "\n") != 1 || !strings.HasSuffix(reason, "\n") || !strings.Contains(reason, "hook "+verb) {
					t.Errorf("hook %s stderr = %q, want one line naming the event and the reason; "+
						"catches: a refusal the harness shows without a reason", verb, reason)
				}
			})
		}
	}
}

// failingReader is a stdin whose read fails, as a broken pipe does.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

// TestHookRefusesAnUnreadableStdin proves a stdin that cannot be read is refused; catches: a read
// error swallowed, so a broken pipe reaches the hook as an allow.
func TestHookRefusesAnUnreadableStdin(t *testing.T) {
	t.Parallel()
	in, err := ReadInput(failingReader{})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
		t.Fatalf("ReadInput on a failing stdin = %#v, %v; want a *core.Error with outcome %q", in, err, core.OutcomeError)
	}
}

// FuzzParseInput catches a parser of the untrusted harness input that panics, that accepts input
// which is not UTF-8 or names no session, or that refuses with anything but a refusal (SPEC §5.13).
func FuzzParseInput(f *testing.F) {
	f.Add([]byte(`{"session_id":"s1"}`))
	f.Add([]byte(`{"session_id":"s3","tool_name":"Bash","tool_input":{"command":"ls"},"future":[1,2]}`))
	f.Add([]byte(`{"session_id":"s1"}{"session_id":"s2"}`))
	f.Add([]byte("{\"session_id\":\"s\xff\"}"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		in, err := ParseInput(b)
		if err != nil {
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
				t.Fatalf("ParseInput(%q) error = %v, want a *core.Error with outcome %q", b, err, core.OutcomeError)
			}
			return
		}
		if in.SessionID == "" || !utf8.Valid(b) {
			t.Fatalf("ParseInput accepted %q as %#v: no session or not UTF-8", b, in)
		}
	})
}
