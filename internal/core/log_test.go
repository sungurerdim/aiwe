package core_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// logTestKeys are the only keys a log line may carry (SPEC §5.3).
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

// logTestStringer is a fmt.Stringer whose text reaches the log only through its String method or
// its JSON form.
type logTestStringer string

func (s logTestStringer) String() string { return string(s) }

// logTestPayload is a struct value logged through slog.Any.
type logTestPayload struct {
	Step string `json:"step"`
}

// logTestGroupValuer is a slog.LogValuer that resolves to a group holding a forged detail.
type logTestGroupValuer struct{}

func (logTestGroupValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("detail", "forged"))
}

// logTestWalk calls f on every string, key or value, inside the decoded JSON value v.
func logTestWalk(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []any:
		for _, e := range x {
			logTestWalk(e, f)
		}
	case map[string]any:
		for k, e := range x {
			f(k)
			logTestWalk(e, f)
		}
	}
}

func TestLogControlCharactersInValueKinds(t *testing.T) {
	const input = "x\u009b31mRED\x1b[0m tail"
	tests := []struct {
		name  string
		value any
	}{
		{name: "error value passes its controls", value: errors.New(input)},
		{name: "Stringer value passes its controls", value: logTestStringer(input)},
		{name: "struct value passes its controls", value: logTestPayload{Step: input}},
		{name: "string slice value passes its controls", value: []string{"step", input}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			core.NewLog(&buf, slog.LevelInfo).Error("gate_red", "detail", tc.value)
			lines := logTestLines(t, buf.Bytes())
			if len(lines) != 1 {
				t.Fatalf("one record wrote %d lines %q, want 1", len(lines), lines)
			}
			raw := lines[0]
			for _, r := range raw {
				if logTestForbidden(r) {
					t.Errorf("log line %q carries the raw control U+%04X", raw, r)
				}
			}
			kept := false
			logTestWalk(logTestDecode(t, raw), func(s string) {
				for _, r := range s {
					if logTestForbidden(r) {
						t.Errorf("log line %q decodes %q to the control U+%04X", raw, s, r)
					}
				}
				kept = kept || strings.Contains(s, "tail")
			})
			if !kept {
				t.Errorf("log line %q lost the printable text %q around the control", raw, "tail")
			}
		})
	}
}

// logTestTopKeys returns the top-level keys of the JSON object line in order, duplicates kept.
func logTestTopKeys(t *testing.T, line string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("log line %q does not open a JSON object: %v, %v", line, tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("log line %q: read key: %v", line, err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("log line %q: read value of %q: %v", line, tok, err)
		}
	}
	return keys
}

func TestLogKeys(t *testing.T) {
	const secret = "ghp_EXAMPLE0123456789abcdefEXAMPLE0123"
	tests := []struct {
		name   string
		write  func(l *slog.Logger)
		want   []string
		values map[string]string
	}{
		{name: "allowed key from the record written twice",
			write:  func(l *slog.Logger) { l.Info("unit_started", "unit", "98", "unit", "99", "detail", "clone ready") },
			want:   []string{"ts", "level", "event", "unit", "detail"},
			values: map[string]string{"unit": "98"}},
		{name: "allowed key from With and the record written twice",
			write: func(l *slog.Logger) {
				l.With("runId", "r-20261011-0001").Info("unit_started", "runId", "r-forged", "detail", "clone ready")
			},
			want:   []string{"ts", "level", "event", "runId", "detail"},
			values: map[string]string{"runId": "r-20261011-0001"}},
		{name: "record field after WithGroup reaches the line",
			write: func(l *slog.Logger) {
				l.With("detail", "clone ready").WithGroup("g").Info("unit_started", "unit", "98", "runId", "r-forged")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "With field after WithGroup reaches the line",
			write: func(l *slog.Logger) {
				l.With("detail", "clone ready").WithGroup("g").With("unit", "98").Info("unit_started")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "allowed key inside a slog.Group reaches the line",
			write: func(l *slog.Logger) {
				l.Info("unit_started", slog.Group("g", "unit", "98", "runId", "r-forged"), "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "slog.Group named after an allowed key reaches the line",
			write: func(l *slog.Logger) {
				l.Info("unit_started", slog.Group("unit", "detail", "forged"), "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "slog.Group from With named after an allowed key reaches the line",
			write: func(l *slog.Logger) {
				l.With(slog.Group("runId", "detail", "forged")).Info("unit_started", "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "LogValuer resolving to a group under an allowed key reaches the line",
			write: func(l *slog.Logger) {
				l.Info("unit_started", "unit", logTestGroupValuer{}, "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "LogValuer from With resolving to a group under an allowed key reaches the line",
			write: func(l *slog.Logger) {
				l.With("runId", logTestGroupValuer{}).Info("unit_started", "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "With fields lost from the line",
			write: func(l *slog.Logger) {
				l.With("runId", "r-20261011-0001", "unit", "98").Info("unit_started", "detail", "clone ready")
			},
			want:   []string{"ts", "level", "event", "runId", "unit", "detail"},
			values: map[string]string{"runId": "r-20261011-0001", "unit": "98"}},
		{name: "foreign key leaks its value",
			write: func(l *slog.Logger) { l.Info("unit_started", "unit", "98", "token", secret, "detail", "clone ready") },
			want:  []string{"ts", "level", "event", "unit", "detail"}},
		{name: "foreign key from With leaks its value",
			write: func(l *slog.Logger) { l.With("token", secret).Info("unit_started", "detail", "clone ready") },
			want:  []string{"ts", "level", "event", "detail"}},
		{name: "group leaks a nested value",
			write: func(l *slog.Logger) {
				l.Info("unit_started", slog.Group("g", "token", secret), "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "msg attribute forges a second event",
			write: func(l *slog.Logger) { l.Info("unit_started", "msg", "unit_closed", "detail", "clone ready") },
			want:  []string{"ts", "level", "event", "detail"}},
		{name: "time attribute forges a second ts",
			write: func(l *slog.Logger) {
				l.Info("unit_started", "time", "2020-01-01T00:00:00Z", "detail", "clone ready")
			},
			want: []string{"ts", "level", "event", "detail"}},
		{name: "level attribute forges a second level",
			write: func(l *slog.Logger) { l.Info("unit_started", "level", "ERROR", "detail", "clone ready") },
			want:  []string{"ts", "level", "event", "detail"}},
		{name: "C1 control in a key passes raw",
			write: func(l *slog.Logger) { l.Info("unit_started", "de\u009btail", "forged", "detail", "clone ready") },
			want:  []string{"ts", "level", "event", "detail"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.write(core.NewLog(&buf, slog.LevelInfo))
			lines := logTestLines(t, buf.Bytes())
			if len(lines) != 1 {
				t.Fatalf("one record wrote %d lines %q, want 1", len(lines), lines)
			}
			raw := lines[0]
			for _, r := range raw {
				if logTestForbidden(r) {
					t.Errorf("log line %q carries the raw control U+%04X", raw, r)
				}
			}
			if strings.Contains(raw, secret) {
				t.Errorf("log line %q carries the value of a key outside {ts, level, runId, unit, event, detail}", raw)
			}
			got := logTestTopKeys(t, raw)
			gotSet := map[string]int{}
			for _, k := range got {
				gotSet[k]++
			}
			if len(got) != len(tc.want) {
				t.Errorf("log line %q has keys %q, want exactly %q", raw, got, tc.want)
			}
			for _, k := range tc.want {
				if gotSet[k] != 1 {
					t.Errorf("log line %q has key %q %d times, want once (keys %q)", raw, k, gotSet[k], got)
				}
			}
			obj := logTestDecode(t, raw)
			if obj["event"] != "unit_started" || obj["level"] != "INFO" || obj["detail"] != "clone ready" {
				t.Errorf("event, level, detail = %v, %v, %v; want unit_started, INFO, clone ready",
					obj["event"], obj["level"], obj["detail"])
			}
			for k, v := range tc.values {
				if obj[k] != v {
					t.Errorf("log line %q has %s = %v, want the first value %q", raw, k, obj[k], v)
				}
			}
		})
	}
}

// TestConfigStateDirectory guards the config reader against reading a directory whose name ends
// in ".json" as a state file; reading it fails, so the whole config would refuse.
func TestConfigStateDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "x.json"), 0o700); err != nil {
		t.Fatalf("make directory x.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"logLevel": "error"}`), 0o600); err != nil {
		t.Fatalf("write state file settings.json: %v", err)
	}
	settings := []core.ConfigSetting{{Name: "logLevel", Env: strings.ToUpper(core.Product) + "_LOG_LEVEL", Default: "info"}}
	got, err := core.ResolveConfig(settings, nil, func(string) string { return "" }, dir)
	if err != nil {
		t.Fatalf("ResolveConfig with a directory x.json in the state root: %v, want it skipped", err)
	}
	if len(got) != 1 || got[0].Value != "error" || got[0].Origin != core.ConfigOriginState {
		t.Errorf("ResolveConfig = %+v, want logLevel = error from the state file", got)
	}
}
