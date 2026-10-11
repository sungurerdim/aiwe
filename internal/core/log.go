package core

import (
	"io"
	"log/slog"
	"strings"
)

// NewLog returns the one logger (SPEC §5.11): one JSON object per line through slog's JSON handler,
// with the time under "ts" and the message under "event". Every string value loses its C0
// controls, DEL and C1 controls, so a line never carries a colour code or a line break.
func NewLog(w io.Writer, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: logReplaceAttr}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// logReplaceAttr renames the built-in time and message keys and strips controls from strings.
func logReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			a.Key = "ts"
		case slog.MessageKey:
			a.Key = "event"
		}
	}
	if a.Value.Kind() == slog.KindString {
		a.Value = slog.StringValue(logStrip(a.Value.String()))
	}
	return a
}

// logStrip returns s as valid UTF-8 without C0 controls, DEL or C1 controls. Invalid bytes become
// U+FFFD first, so no control rune can form from bytes around a stripped one.
func logStrip(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\uFFFD"))
}
