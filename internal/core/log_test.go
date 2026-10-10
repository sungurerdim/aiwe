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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.write(core.NewLogger(&buf))
			out := buf.Bytes()
			if bytes.IndexByte(out, 0x1b) >= 0 {
				t.Fatalf("log output carries an escape byte: %q", out)
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
