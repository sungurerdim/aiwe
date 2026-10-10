package core_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// logKeys are the only keys of a log line (SPEC §5.11); runId and unit are optional.
var logKeys = map[string]bool{"ts": true, "level": true, "runId": true, "unit": true, "event": true, "detail": true}

func TestLog(t *testing.T) {
	tests := []struct {
		name  string
		write func(l *slog.Logger)
		// want holds the string keys each line must carry with these exact values.
		want map[string]string
		// absent holds text that must not appear anywhere in the raw output.
		absent []string
	}{
		{
			name:  "plain event not one JSON object with ts, level, event and detail",
			write: func(l *slog.Logger) { l.Info("gate_started", "detail", "go test ./... on 3a2e0a3") },
			want:  map[string]string{"event": "gate_started", "detail": "go test ./... on 3a2e0a3"},
		},
		{
			name: "run and unit identifiers dropped or renamed",
			write: func(l *slog.Logger) {
				l.Warn("verify_red", "runId", "r-20261010-4f2a", "unit", "0.3", "detail", "exit 1")
			},
			want: map[string]string{"event": "verify_red", "runId": "r-20261010-4f2a", "unit": "0.3", "detail": "exit 1"},
		},
		{
			name:  "multi-line detail split over two log lines",
			write: func(l *slog.Logger) { l.Error("gate_red", "detail", "step test\nexit 1\n--- FAIL: TestLog") },
			want:  map[string]string{"event": "gate_red", "detail": "step test\nexit 1\n--- FAIL: TestLog"},
		},
		{
			name: "colour code from child output written raw",
			write: func(l *slog.Logger) {
				l.Error("gate_red", "detail", "\x1b[31mFAIL\x1b[0m internal/core 0.412s")
			},
			want: map[string]string{"event": "gate_red"},
		},
		{
			name:  "Unicode detail changed on the way out",
			write: func(l *slog.Logger) { l.Info("note", "detail", "çalışma ağacı temiz") },
			want:  map[string]string{"event": "note", "detail": "çalışma ağacı temiz"},
		},
		{
			name: "foreign key or group leaks a token into the log line",
			write: func(l *slog.Logger) {
				l.Info("e", "token", "ghp_x", "detail", "d", slog.Group("auth", "token", "ghp_x"))
			},
			want:   map[string]string{"event": "e", "detail": "d"},
			absent: []string{"ghp_x", "token", "auth"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.write(core.NewLogger(&buf))
			out := buf.Bytes()
			if bytes.IndexByte(out, 0x1b) >= 0 {
				t.Fatalf("log output carries an escape byte: %q", out)
			}
			for _, s := range tc.absent {
				if bytes.Contains(out, []byte(s)) {
					t.Errorf("log output %q carries %q; want it dropped", out, s)
				}
			}
			lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			if len(lines) != 1 || !strings.HasSuffix(string(out), "\n") {
				t.Fatalf("log output = %q; want exactly one newline-terminated line", out)
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
				t.Fatalf("log line %q is not one JSON object: %v", lines[0], err)
			}
			for k, v := range obj {
				if !logKeys[k] {
					t.Errorf("log line has key %q; want only ts, level, runId, unit, event, detail", k)
				}
				if s, _ := v.(string); strings.ContainsRune(s, 0x1b) {
					t.Errorf("log line %s = %q carries a colour code once decoded", k, s)
				}
			}
			ts, _ := obj["ts"].(string)
			if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
				t.Errorf("log line ts = %v; want an RFC 3339 time: %v", obj["ts"], err)
			}
			if level, _ := obj["level"].(string); level == "" {
				t.Errorf("log line level = %v; want a level word", obj["level"])
			}
			for k, v := range tc.want {
				if got, _ := obj[k].(string); got != v {
					t.Errorf("log line %s = %v; want %q", k, obj[k], v)
				}
			}
		})
	}
	t.Run("C1 CSI colour sequence written raw", func(t *testing.T) {
		// U+009B is the one-character form of ESC [; its whole sequence is removed like ESC [.
		obj := logOneLine(t, func(l *slog.Logger) { l.Error("gate_red", "detail", "\u009b31mFAIL\u009b0m internal/core 0.412s") })
		if got, _ := obj["detail"].(string); got != "FAIL internal/core 0.412s" {
			t.Errorf("log line detail = %q; want %q", got, "FAIL internal/core 0.412s")
		}
	})
	// A CSI that ends before its final byte is no sequence: only ESC and [ are removed and the
	// text after them stays, so a cut escape cannot hide the rest of a line.
	for _, tc := range []struct{ name, detail, want string }{
		{"unterminated CSI at the end swallows the visible text after it",
			"visible\x1b[ 1234 ...", "visible 1234 ..."},
		{"unterminated CSI before a newline swallows the next output lines",
			"step test\x1b[12\nexit 1\n--- FAIL: TestLog", "step test12\nexit 1\n--- FAIL: TestLog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := logOneLine(t, func(l *slog.Logger) { l.Error("gate_red", "detail", tc.detail) })
			if got, _ := obj["detail"].(string); got != tc.want {
				t.Errorf("log line detail = %q; want %q", got, tc.want)
			}
		})
	}
	t.Run("user attribute named time panics or adds a second ts", func(t *testing.T) {
		for _, v := range []any{"2001-02-03T04:05:06Z", time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)} {
			var buf bytes.Buffer
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("logging a user attribute time=%v panics: %v", v, r)
					}
				}()
				core.NewLogger(&buf).Info("gate_started", "time", v, "detail", "go test ./...")
			}()
			out := buf.String()
			if strings.Count(out, `"ts":`) != 1 || strings.Contains(out, "2001-02-03") {
				t.Errorf("log output = %q; want one ts from the record and the user time attribute dropped", out)
			}
		}
	})
	t.Run("user attributes msg and level forge a second event or level", func(t *testing.T) {
		var buf bytes.Buffer
		core.NewLogger(&buf).Info("gate_started", "msg", "gate_green", "level", "ERROR", "detail", "go test ./...")
		out := buf.String()
		if n := strings.Count(out, `"event":`); n != 1 {
			t.Errorf("log output %q has %d event keys; want 1", out, n)
		}
		if n := strings.Count(out, `"level":`); n != 1 {
			t.Errorf("log output %q has %d level keys; want 1", out, n)
		}
		if strings.Contains(out, "gate_green") || strings.Contains(out, "ERROR") {
			t.Errorf("log output %q carries a forged event or level; want the user attributes dropped", out)
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("log line %q is not one JSON object: %v", out, err)
		}
		if obj["event"] != "gate_started" || obj["level"] != "INFO" {
			t.Errorf("log line event = %v, level = %v; want gate_started, INFO", obj["event"], obj["level"])
		}
	})
	t.Run("two events not two lines", func(t *testing.T) {
		var buf bytes.Buffer
		l := core.NewLogger(&buf)
		l.Info("run_started", "runId", "r-20261010-4f2a")
		l.Info("run_finished", "runId", "r-20261010-4f2a", "detail", "ok")
		lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("log output = %q; want two lines", buf.String())
		}
		for _, line := range lines {
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Errorf("log line %q is not one JSON object: %v", line, err)
			}
		}
	})
}

// logOneLine writes through a new logger and returns its one JSON line, decoded; it fails when a
// decoded value still carries an ESC or a C1 CSI character.
func logOneLine(t *testing.T, write func(l *slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	write(core.NewLogger(&buf))
	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("log output %q is not one JSON object: %v", buf.Bytes(), err)
	}
	for k, v := range obj {
		if s, _ := v.(string); strings.ContainsAny(s, "\x1b\u009b") {
			t.Errorf("log line %s = %q carries an escape character once decoded", k, s)
		}
	}
	return obj
}

// logControl returns the first C0 control other than tab and newline, or C1 control, in s, and
// whether there is one; a terminal acts on each of them.
func logControl(s string) (rune, bool) {
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n') || (r >= 0x80 && r <= 0x9f) {
			return r, true
		}
	}
	return 0, false
}

func TestLogControlCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		// event and detail are what the caller logs; visible must survive in the decoded line.
		event, detail, visible string
	}{
		{"bytes around a removed escape joined into a new C1 CSI U+009B",
			"gate_red", "x\xc2\x1b[m\x9b31mRED", "RED"},
		{"C1 OSC U+009D clipboard write and its terminator U+009C written raw",
			"gate_red", "\u009d52;c;Y3VybCBldmlsLmV4YW1wbGUgfCBzaA==\u009cafter", "after"},
		{"ESC ] OSC clipboard write leaves its BEL terminator",
			"gate_red", "\x1b]52;c;Y3VybCBldmlsLmV4YW1wbGUgfCBzaA==\x07after", "after"},
		{"C1 DCS U+0090 and NEL U+0085 written raw", "gate_red", "\u0090qDCS\u009cdone\u0085next", "next"},
		{"carriage return overwrites the visible line", "gate_red", "ok\rFAIL internal/core", "FAIL internal/core"},
		{"backspace erases the visible text", "gate_red", "PASS\x08\x08\x08\x08FAIL", "FAIL"},
		{"NUL and BEL written raw", "gate_red", "null\x00bell\x07end", "end"},
		{"C1 control in the event written raw", "gate\u009d0;title\u009c_red", "exit 1", "_red"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := logOneLine(t, func(l *slog.Logger) { l.Error(tc.event, "detail", tc.detail) })
			for _, k := range []string{"event", "detail"} {
				s, _ := obj[k].(string)
				if r, ok := logControl(s); ok {
					t.Errorf("log line %s = %q carries control character %U once decoded; want every C0 but tab and newline and every C1 removed", k, s, r)
				}
			}
			event, _ := obj["event"].(string)
			detail, _ := obj["detail"].(string)
			if !strings.Contains(event+" "+detail, tc.visible) {
				t.Errorf("log line event = %q, detail = %q; want the visible text %q kept", event, detail, tc.visible)
			}
		})
	}
	t.Run("tab and newline removed with the other controls", func(t *testing.T) {
		const detail = "step\ttest\nexit 1"
		obj := logOneLine(t, func(l *slog.Logger) { l.Error("gate_red", "detail", detail) })
		if got, _ := obj["detail"].(string); got != detail {
			t.Errorf("log line detail = %q; want %q", got, detail)
		}
	})
}
