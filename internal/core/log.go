package core

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// logKeys are the record keys a caller may set; ts, level and event come from the record itself
// (SPEC §5.11).
var logKeys = map[string]bool{"runId": true, "unit": true, "detail": true}

// NewLog returns the one logger (SPEC §5.11): one JSON object per line through slog's JSON handler,
// with the time under "ts" and the message under "event". A line carries only ts, level, event,
// runId, unit and detail, each once; every other key, a group and a forged built-in key are
// dropped. Every value is written as a string without C0 controls, DEL or C1 controls, so a line
// never carries a colour code or a line break.
func NewLog(w io.Writer, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: logReplaceAttr}
	return slog.New(&logHandler{inner: slog.NewJSONHandler(w, opts)})
}

// logReplaceAttr renames the built-in time and message keys.
func logReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			a.Key = "ts"
		case slog.MessageKey:
			a.Key = "event"
		}
	}
	return a
}

// logHandler passes only the allowed keys to the JSON handler, each once and as a clean string.
// Attributes added after a group would nest inside it, so a grouped handler drops them.
type logHandler struct {
	inner   slog.Handler
	attrs   []slog.Attr
	grouped bool
}

// Enabled reports whether the JSON handler writes records of level.
func (h *logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// WithAttrs returns a handler that adds attrs to every record.
func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.grouped {
		return h
	}
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &c
}

// WithGroup returns a handler that drops every attribute added from now on.
func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.grouped = true
	return &c
}

// Handle writes r with a clean event and only the first value of each allowed key. A group
// attribute is dropped whatever its name, so it cannot forge an allowed key's value.
func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, logStrip(r.Message), r.PC)
	seen := map[string]bool{}
	add := func(a slog.Attr) bool {
		v := a.Value.Resolve()
		if v.Kind() != slog.KindGroup && logKeys[a.Key] && !seen[a.Key] {
			seen[a.Key] = true
			out.AddAttrs(slog.String(a.Key, logStrip(v.String())))
		}
		return true
	}
	for _, a := range h.attrs {
		add(a)
	}
	if !h.grouped {
		r.Attrs(add)
	}
	return h.inner.Handle(ctx, out)
}

// logStrip returns s without C0 controls, DEL or C1 controls. strings.Map turns each invalid byte
// into U+FFFD, so no control rune can form from bytes around a stripped one.
func logStrip(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
