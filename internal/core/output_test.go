package core_test

import (
	"bytes"
	"encoding/json"
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
