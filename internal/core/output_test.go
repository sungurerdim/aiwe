package core_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

func TestOutput(t *testing.T) {
	// envelopeKeys are the only keys of the result envelope (SPEC §5.3).
	envelopeKeys := []string{"status", "outcome", "data", "detail"}
	tests := []struct {
		name   string
		result core.Result[any]
		// wantData is the JSON the data key must decode to.
		wantData any
	}{
		{
			name: "ok result printed without status, outcome, data or detail key",
			result: core.Result[any]{Status: core.StatusOK, Outcome: core.Outcome("ok"),
				Data: map[string]any{"version": "v1.4.2"}, Detail: "version read from the build"},
			wantData: map[string]any{"version": "v1.4.2"},
		},
		{
			name: "red gate printed as an error instead of a red result envelope",
			result: core.Result[any]{Status: core.StatusRed, Outcome: core.Outcome("gate_red"),
				Data: map[string]any{"step": "test", "exitCode": float64(1)}, Detail: "go test ./... exited 1"},
			wantData: map[string]any{"step": "test", "exitCode": float64(1)},
		},
		{
			name: "result without data drops the data key",
			result: core.Result[any]{Status: core.StatusNoChange, Outcome: core.Outcome("no_change"),
				Data: nil, Detail: "tag v1.4.2 already points at HEAD"},
			wantData: nil,
		},
		{
			name: "non-ASCII detail altered on the way out",
			result: core.Result[any]{Status: core.StatusUsage, Outcome: core.Outcome("usage"),
				Data: map[string]any{"param": "repo"}, Detail: "İıŞşĞğ 仓库 مستودع 🚀 é"},
			wantData: map[string]any{"param": "repo"},
		},
	}
	for _, tc := range tests {
		t.Run("json/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatJSON, tc.result); err != nil {
				t.Fatalf("WriteResult(json) error = %v", err)
			}
			out := buf.String()
			if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
				t.Fatalf("json output is not exactly one line ending in a newline: %q", out)
			}
			if strings.ContainsRune(out, '\x1b') {
				t.Fatalf("json output carries a terminal escape code: %q", out)
			}
			var env map[string]json.RawMessage
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("json output is not one JSON object: %v; output %q", err, out)
			}
			if len(env) != len(envelopeKeys) {
				t.Fatalf("envelope keys = %v, want exactly %v", keysOf(env), envelopeKeys)
			}
			for _, k := range envelopeKeys {
				if _, ok := env[k]; !ok {
					t.Fatalf("envelope misses key %q; keys %v", k, keysOf(env))
				}
			}
			assertJSONString(t, env, "status", string(tc.result.Status))
			assertJSONString(t, env, "outcome", string(tc.result.Outcome))
			assertJSONString(t, env, "detail", tc.result.Detail)
			var gotData any
			if err := json.Unmarshal(env["data"], &gotData); err != nil {
				t.Fatalf("data key is not JSON: %v", err)
			}
			if !jsonEqual(t, gotData, tc.wantData) {
				t.Fatalf("data = %s, want %v", env["data"], tc.wantData)
			}
		})
		t.Run("text/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatText, tc.result); err != nil {
				t.Fatalf("WriteResult(text) error = %v", err)
			}
			out := buf.String()
			if !utf8.ValidString(out) {
				t.Fatalf("text output is not valid UTF-8: %q", out)
			}
			if strings.ContainsRune(out, '\x1b') {
				t.Fatalf("text output carries meaning in a colour code instead of a status word: %q", out)
			}
			if !strings.Contains(out, string(tc.result.Status)) {
				t.Fatalf("text output misses the status word %q: %q", tc.result.Status, out)
			}
			if !strings.Contains(out, tc.result.Detail) {
				t.Fatalf("text output misses the detail %q byte-exact: %q", tc.result.Detail, out)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Fatalf("text output does not end in a newline: %q", out)
			}
		})
	}

	// A refusal's detail is core.Error's message, which already starts with "<outcome>: " (SPEC §5.10);
	// the text line names that outcome once (#143).
	refusals := []struct {
		name string
		err  *core.Error
		want string
	}{
		{
			name: "usage refusal printed as usage: usage:",
			err:  &core.Error{Outcome: core.OutcomeUsage, Expected: "the repo parameter", Found: "no repo parameter"},
			want: "usage: expected the repo parameter; found no repo parameter; next: run " + core.Product + " --help and correct the call\n",
		},
		{
			name: "error refusal printed as error: error:",
			err: &core.Error{Outcome: core.OutcomeError, Expected: "a readable ledger", Found: "permission denied",
				Next: "grant read access to the ledger"},
			want: "error: expected a readable ledger; found permission denied; next: grant read access to the ledger\n",
		},
		{
			name: "named outcome under the error status dropped or printed twice",
			err: &core.Error{Outcome: core.Outcome("config_invalid"), Expected: "one JSON object",
				Found: "a JSON array", Next: "correct the state file"},
			want: "error: config_invalid: expected one JSON object; found a JSON array; next: correct the state file\n",
		},
	}
	for _, tc := range refusals {
		t.Run("text refusal/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatText, core.ErrorResult(tc.err)); err != nil {
				t.Fatalf("WriteResult(text) error = %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("text refusal = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("text refusal/status word printed twice when the outcome differs from the status", func(t *testing.T) {
		var buf bytes.Buffer
		r := core.Result[any]{Status: core.StatusError, Outcome: core.Outcome("config_invalid"),
			Detail: "error: expected one JSON object; found a JSON array; next: correct the state file"}
		if err := core.WriteResult(&buf, core.FormatText, r); err != nil {
			t.Fatalf("WriteResult(text) error = %v", err)
		}
		want := "error: expected one JSON object; found a JSON array; next: correct the state file\n"
		if got := buf.String(); got != want {
			t.Fatalf("text refusal = %q, want %q", got, want)
		}
	})

	// A path or value from the file system or the environment reaches the text output with every
	// terminal control escaped, so a file name cannot drive the terminal (#200). JSON keeps it exact.
	escapes := []struct {
		name   string
		detail string
		want   string
	}{
		{
			name:   "OSC title sequence with BEL in a file name drives the terminal",
			detail: "read settings.\x1b]0;PWN\x07.json",
			want:   `ok: read settings.\x1b]0;PWN\x07.json` + "\n",
		},
		{
			name:   "CSI clear-screen sequence reaches the terminal raw",
			detail: "path a\x1b[2Jb",
			want:   `ok: path a\x1b[2Jb` + "\n",
		},
		{
			name:   "lone CR overwrites the line before it",
			detail: "found ok\rforged",
			want:   `ok: found ok\x0dforged` + "\n",
		},
		{
			name:   "C1 CSI U+009B and NEL U+0085 reach the terminal raw",
			detail: "path a\u009b2Jb\u0085c",
			want:   `ok: path a\u009b2Jb\u0085c` + "\n",
		},
		{
			name:   "DEL reaches the terminal raw",
			detail: "path a\x7fb",
			want:   `ok: path a\x7fb` + "\n",
		},
		{
			name:   "bidi override U+202E reverses the displayed path",
			detail: "file evil\u202etxt.exe",
			want:   `ok: file evil\u202etxt.exe` + "\n",
		},
		{
			name:   "bidi isolates U+2066 to U+2069 reorder the displayed path",
			detail: "a\u2066b\u2067c\u2068d\u2069e",
			want:   `ok: a\u2066b\u2067c\u2068d\u2069e` + "\n",
		},
		{
			name:   "invalid UTF-8 bytes form a control on the terminal",
			detail: "path a\xffb\xc2",
			want:   `ok: path a\xffb\xc2` + "\n",
		},
		{
			name:   "invisible, direction mark and line separator runes hide or reorder the displayed path",
			detail: "a\u200bb\u200ec\u200fd\u061ce\ufefff\u2028g\u2029h",
			want:   `ok: a\u200bb\u200ec\u200fd\u061ce\ufefff\u2028g\u2029h` + "\n",
		},
		{
			name:   "plain Unicode, RTL script, newline and tab altered on the way out",
			detail: "İıŞşĞğ 仓库 مستودع 🚀 é\ndocs\\plan.md\tnext",
			want:   "ok: İıŞşĞğ 仓库 مستودع 🚀 é\ndocs\\plan.md\tnext\n",
		},
	}
	for _, tc := range escapes {
		r := core.Result[any]{Status: core.StatusOK, Outcome: core.Outcome("ok"), Detail: tc.detail}
		t.Run("text escape/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatText, r); err != nil {
				t.Fatalf("WriteResult(text) error = %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("text output = %q, want %q", got, tc.want)
			}
		})
		t.Run("json escape/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatJSON, r); err != nil {
				t.Fatalf("WriteResult(json) error = %v", err)
			}
			if strings.ContainsAny(buf.String(), "\x1b\x07\r") {
				t.Fatalf("json output carries a raw C0 control: %q", buf.String())
			}
			var env map[string]json.RawMessage
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("json output is not one JSON object: %v; output %q", err, buf.String())
			}
			if utf8.ValidString(tc.detail) {
				assertJSONString(t, env, "detail", tc.detail)
			}
		})
	}

	t.Run("text escape/refusal with an OSC sequence in the found path drives the terminal", func(t *testing.T) {
		var buf bytes.Buffer
		e := &core.Error{Outcome: core.OutcomeError, Expected: "one JSON object",
			Found: "an unreadable file settings.\x1b]0;PWN\x07.json", Next: "correct the file"}
		if err := core.WriteResult(&buf, core.FormatText, core.ErrorResult(e)); err != nil {
			t.Fatalf("WriteResult(text) error = %v", err)
		}
		want := `error: expected one JSON object; found an unreadable file settings.\x1b]0;PWN\x07.json; next: correct the file` + "\n"
		if got := buf.String(); got != want {
			t.Fatalf("text refusal = %q, want %q", got, want)
		}
	})

	// An error result that is not a core.Error, such as an incomplete gate, lists one step per line;
	// escaping its newlines would flatten the list into one line (#200).
	t.Run("text escape/step lines of an incomplete error result flattened into one line", func(t *testing.T) {
		var buf bytes.Buffer
		r := core.Result[any]{Status: core.StatusError, Outcome: core.Outcome("incomplete"),
			Detail: "gate incomplete\n  build: green\n  test: not measured\tunknown"}
		if err := core.WriteResult(&buf, core.FormatText, r); err != nil {
			t.Fatalf("WriteResult(text) error = %v", err)
		}
		want := "error: gate incomplete\n  build: green\n  test: not measured\tunknown\n"
		if got := buf.String(); got != want {
			t.Fatalf("text output = %q, want %q", got, want)
		}
	})

	// A refusal is one line (SPEC §5.10): a newline or CR in Expected, Found or Next would fake a
	// second result line (#200).
	lineFakes := []struct {
		name string
		err  *core.Error
		want string
	}{
		{
			name: "LF in an undeclared flag fakes an ok line",
			err:  &core.Error{Outcome: core.OutcomeUsage, Expected: "a declared flag", Found: "--zz\nok: gate pass"},
			want: `usage: expected a declared flag; found --zz\x0aok: gate pass; next: run ` + core.Product + ` --help and correct the call` + "\n",
		},
		{
			name: "CR LF in expected fakes a line",
			err: &core.Error{Outcome: core.OutcomeError, Expected: "one file\r\nok: done", Found: "two files",
				Next: "remove one file"},
			want: `error: expected one file\x0d\x0aok: done; found two files; next: remove one file` + "\n",
		},
		{
			name: "LF in next fakes a line",
			err: &core.Error{Outcome: core.OutcomeError, Expected: "one file", Found: "two files",
				Next: "remove one file\nok: done"},
			want: `error: expected one file; found two files; next: remove one file\x0aok: done` + "\n",
		},
	}
	for _, tc := range lineFakes {
		t.Run("text refusal/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatText, core.ErrorResult(tc.err)); err != nil {
				t.Fatalf("WriteResult(text) error = %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("text refusal = %q, want %q", got, tc.want)
			}
		})
		t.Run("refusal message/"+tc.name, func(t *testing.T) {
			if msg := tc.err.Error(); strings.ContainsAny(msg, "\n\r\t") {
				t.Fatalf("core.Error message spans more than one line: %q", msg)
			}
		})
		t.Run("json refusal/"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatJSON, core.ErrorResult(tc.err)); err != nil {
				t.Fatalf("WriteResult(json) error = %v", err)
			}
			var env map[string]json.RawMessage
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("json output is not one JSON object: %v; output %q", err, buf.String())
			}
			assertJSONString(t, env, "detail", tc.err.Error())
		})
	}

	t.Run("unknown format printed anyway instead of refused", func(t *testing.T) {
		var buf bytes.Buffer
		r := core.Result[any]{Status: core.StatusOK, Outcome: core.Outcome("ok"), Detail: "done"}
		if err := core.WriteResult(&buf, core.Format("yaml"), r); err == nil {
			t.Fatalf("WriteResult(yaml) error = nil, want a refusal (fail-closed)")
		}
		if buf.Len() != 0 {
			t.Fatalf("WriteResult(yaml) wrote %q, want nothing", buf.String())
		}
	})

	t.Run("status words differ from the exit-code table of SPEC 5.3", func(t *testing.T) {
		want := map[core.Status]string{
			core.StatusOK: "ok", core.StatusError: "error", core.StatusUsage: "usage",
			core.StatusRed: "red", core.StatusNoChange: "no_change",
		}
		for s, w := range want {
			if string(s) != w {
				t.Errorf("status constant = %q, want %q", s, w)
			}
		}
	})
}

func keysOf(m map[string]json.RawMessage) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func assertJSONString(t *testing.T, env map[string]json.RawMessage, key, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(env[key], &got); err != nil {
		t.Fatalf("key %q is not a JSON string: %s", key, env[key])
	}
	if got != want {
		t.Fatalf("key %q = %q, want %q", key, got, want)
	}
}

func jsonEqual(t *testing.T, got, want any) bool {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	return bytes.Equal(g, w)
}

// TestOutcomeErrorLine catches an error that is not a core.Error, such as a wrapped OS error that
// names a state path, printing a raw LF, CR or tab through ErrorResult: the path fakes a second
// result line (#223).
func TestOutcomeErrorLine(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "LF in a state path fakes an ok line",
			err:  fmt.Errorf("owner-only check of %s: %v", "/state\nok: gate green", fs.ErrPermission),
			want: `error: owner-only check of /state\x0aok: gate green: permission denied` + "\n",
		},
		{
			name: "raw LF, CR and tab in a wrapped OS error",
			err:  &fs.PathError{Op: "open", Path: "/srv/state\r\nok: gate green\tdone", Err: fs.ErrPermission},
			want: `error: open /srv/state\x0d\x0aok: gate green\x09done: permission denied` + "\n",
		},
		{
			name: "a joined error spans two lines",
			err:  errors.Join(errors.New("owner key: write failed"), errors.New("remove tmp: busy")),
			want: `error: owner key: write failed\x0aremove tmp: busy` + "\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := core.ErrorResult(tc.err)
			if strings.ContainsAny(r.Detail, "\n\r\t") {
				t.Fatalf("ErrorResult detail spans more than one line: %q", r.Detail)
			}
			var buf bytes.Buffer
			if err := core.WriteResult(&buf, core.FormatText, r); err != nil {
				t.Fatalf("WriteResult(text) error = %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("text output = %q, want %q", got, tc.want)
			}
		})
	}
}
