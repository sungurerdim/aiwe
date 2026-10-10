package core_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// logTestKeys are the only keys a log line may carry (SPEC §5.11).
func logTestKeys() map[string]bool {
	return map[string]bool{"ts": true, "level": true, "runId": true, "unit": true, "event": true, "detail": true}
}

// logTestLines splits the output into lines and fails unless it ends with a newline.
func logTestLines(t *testing.T, out []byte) []string {
	t.Helper()
	if len(out) == 0 {
		t.Fatal("the log wrote nothing")
	}
	if out[len(out)-1] != '\n' {
		t.Fatalf("log output %q does not end with a newline", out)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

// logTestDecode decodes line as exactly one JSON object and fails on anything else.
func logTestDecode(t *testing.T, line string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("log line %q is not a JSON object: %v", line, err)
	}
	if dec.More() {
		t.Fatalf("log line %q holds more than one JSON value", line)
	}
	return obj
}

// logTestForbidden reports whether r is a C0 control, DEL or a C1 control.
func logTestForbidden(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func TestLog(t *testing.T) {
	t.Run("log line that is not one JSON object with the fixed keys", func(t *testing.T) {
		tests := []struct {
			name      string
			write     func(l *slog.Logger)
			wantLevel string
			wantEvent string
			want      map[string]string
		}{
			{
				name: "unit event with run id loses its keys",
				write: func(l *slog.Logger) {
					l.Info("unit_started", "runId", "r-20261011-0001", "unit", "98", "detail", "clone ready")
				},
				wantLevel: "INFO", wantEvent: "unit_started",
				want: map[string]string{"runId": "r-20261011-0001", "unit": "98", "detail": "clone ready"},
			},
			{
				name: "event without run id or unit gains other keys",
				write: func(l *slog.Logger) {
					l.Warn("root_swept", "detail", "3 leftovers removed")
				},
				wantLevel: "WARN", wantEvent: "root_swept",
				want: map[string]string{"detail": "3 leftovers removed"},
			},
			{
				name: "multi-line detail splits the record over lines",
				write: func(l *slog.Logger) {
					l.Error("gate_red", "unit", "98", "detail", "step test failed\n--- FAIL: TestConfig\nexit 1")
				},
				wantLevel: "ERROR", wantEvent: "gate_red",
				want: map[string]string{"unit": "98"},
			},
			{
				name: "Unicode detail is rewritten",
				write: func(l *slog.Logger) {
					l.Info("unit_closed", "detail", "Türkçe ğüş — 日本語 ✓")
				},
				wantLevel: "INFO", wantEvent: "unit_closed",
				want: map[string]string{"detail": "Türkçe ğüş — 日本語 ✓"},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var buf bytes.Buffer
				tc.write(core.NewLog(&buf, slog.LevelInfo))
				lines := logTestLines(t, buf.Bytes())
				if len(lines) != 1 {
					t.Fatalf("one record wrote %d lines %q, want 1", len(lines), lines)
				}
				obj := logTestDecode(t, lines[0])
				allowed := logTestKeys()
				for k := range obj {
					if !allowed[k] {
						t.Errorf("log line %q carries key %q outside {ts, level, runId, unit, event, detail}", lines[0], k)
					}
				}
				ts, _ := obj["ts"].(string)
				if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
					t.Errorf("ts = %v, want an RFC 3339 time: %v", obj["ts"], err)
				}
				if obj["level"] != tc.wantLevel || obj["event"] != tc.wantEvent {
					t.Errorf("level, event = %v, %v; want %q, %q", obj["level"], obj["event"], tc.wantLevel, tc.wantEvent)
				}
				for k, v := range tc.want {
					if obj[k] != v {
						t.Errorf("%s = %v, want %q", k, obj[k], v)
					}
				}
			})
		}
	})

	t.Run("several records share a line", func(t *testing.T) {
		var buf bytes.Buffer
		l := core.NewLog(&buf, slog.LevelInfo)
		l.Info("unit_started", "unit", "98", "detail", "clone ready")
		l.Info("verify_started", "unit", "98", "detail", "go test ./internal/core/")
		l.Info("verify_finished", "unit", "98", "detail", "exit 0")
		lines := logTestLines(t, buf.Bytes())
		if len(lines) != 3 {
			t.Fatalf("three records wrote %d lines %q, want 3", len(lines), lines)
		}
		for i, want := range []string{"unit_started", "verify_started", "verify_finished"} {
			if got := logTestDecode(t, lines[i])["event"]; got != want {
				t.Errorf("line %d event = %v, want %q", i, got, want)
			}
		}
	})

	t.Run("record below the level written", func(t *testing.T) {
		var buf bytes.Buffer
		core.NewLog(&buf, slog.LevelInfo).Debug("cache_probe", "detail", "hit")
		if buf.Len() != 0 {
			t.Errorf("a debug record at level info wrote %q, want nothing", buf.String())
		}
	})

	t.Run("colour code in a log line", func(t *testing.T) {
		var buf bytes.Buffer
		l := core.NewLog(&buf, slog.LevelDebug)
		l.Debug("cache_probe", "detail", "hit")
		l.Info("unit_started", "detail", "clone ready")
		l.Warn("root_swept", "detail", "3 leftovers removed")
		l.Error("gate_red", "detail", "step test failed")
		if bytes.IndexByte(buf.Bytes(), 0x1b) >= 0 {
			t.Errorf("log output %q carries an ESC byte (a colour code)", buf.String())
		}
	})
}

func TestLogControlCharacters(t *testing.T) {
	tests := []struct {
		name  string
		input string
		keep  string
	}{
		{name: "C1 CSI U+009B passes", input: "build \u009b31mred\u009b0m done", keep: "done"},
		{name: "C1 ST U+009C passes", input: "title\u009c set", keep: "set"},
		{name: "C1 OSC U+009D passes", input: "\u009d8;;https://example.com/x\u009c link", keep: "link"},
		{name: "ESC colour sequence passes", input: "\x1b[31mfailed\x1b[0m", keep: "failed"},
		{name: "ESC formed by joining after one strip pass", input: "\x1b\x1b[[31mwarn", keep: "warn"},
		{name: "U+009B formed by joining bytes around a stripped ESC", input: "a\xc2\x1b\x9b31m tail", keep: "tail"},
		{name: "U+009D formed by joining bytes around a stripped NUL", input: "b\xc2\x00\x9d0;x\x07 tail", keep: "tail"},
		{name: "raw C1 byte without a lead byte passes", input: "c\x9b31m tail", keep: "tail"},
		{name: "BEL, NUL, backspace or DEL passes", input: "ok\x07\x00\x08\x7f end", keep: "end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			core.NewLog(&buf, slog.LevelInfo).Info("unit_"+tc.input,
				"runId", "r-"+tc.input, "unit", tc.input, "detail", tc.input)
			lines := logTestLines(t, buf.Bytes())
			if len(lines) != 1 {
				t.Fatalf("one record wrote %d lines %q, want 1", len(lines), lines)
			}
			raw := lines[0]
			if !utf8.ValidString(raw) {
				t.Errorf("log line %q is not valid UTF-8: raw bytes can form a control on the reader", raw)
			}
			for _, r := range raw {
				if logTestForbidden(r) {
					t.Errorf("log line %q carries the raw control U+%04X", raw, r)
				}
			}
			obj := logTestDecode(t, raw)
			for _, k := range []string{"event", "runId", "unit", "detail"} {
				v, _ := obj[k].(string)
				for _, r := range v {
					if logTestForbidden(r) {
						t.Errorf("%s = %q decodes to the control U+%04X", k, v, r)
					}
				}
				if !strings.Contains(v, tc.keep) {
					t.Errorf("%s = %q lost the printable text %q around the control", k, v, tc.keep)
				}
			}
		})
	}
}
