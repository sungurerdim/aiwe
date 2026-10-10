package core

import (
	"io"
	"log/slog"
	"strings"
	"time"
)

// NewLogger returns a logger that writes one JSON object per line to w with only the keys ts,
// level, runId, unit, event and detail (SPEC §5.11). Other keys and groups are dropped; colour
// and other escape sequences are removed from every value.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: logAttr}))
}

// logAttr maps one slog attribute onto the log line keys; a zero Attr drops it.
func logAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return slog.Attr{}
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.String("ts", a.Value.Time().UTC().Format(time.RFC3339Nano))
	case slog.LevelKey:
		return a
	case slog.MessageKey:
		return slog.String("event", stripEscapes(a.Value.String()))
	case "runId", "unit", "detail":
		return slog.String(a.Key, stripEscapes(a.Value.Resolve().String()))
	default:
		return slog.Attr{}
	}
}

// stripEscapes removes ESC and the escape sequence it starts: a CSI sequence up to its final
// byte, else the one byte after ESC.
func stripEscapes(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i < len(s) && s[i] == '[' {
			for i+1 < len(s) && (s[i+1] < 0x40 || s[i+1] > 0x7e) {
				i++
			}
			i++
		}
	}
	return b.String()
}
