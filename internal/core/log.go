package core

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// NewLogger returns a logger that writes one JSON object per line to w with only the keys ts,
// level, runId, unit, event and detail (SPEC §5.11). Other keys and groups are dropped, so a
// caller's time, msg or level attribute cannot forge the record's own; escape sequences and every
// control character but tab and newline are removed from every value.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(lineHandler{next: slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: logAttr})})
}

// lineHandler passes only the caller attributes a log line may carry to next.
type lineHandler struct {
	next slog.Handler
}

func (h lineHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h lineHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if lineAttrKey(a.Key) {
			out.AddAttrs(a)
		}
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h lineHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	kept := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if lineAttrKey(a.Key) {
			kept = append(kept, a)
		}
	}
	return lineHandler{next: h.next.WithAttrs(kept)}
}

func (h lineHandler) WithGroup(name string) slog.Handler {
	return lineHandler{next: h.next.WithGroup(name)}
}

// lineAttrKey reports whether a caller attribute key is one a log line carries.
func lineAttrKey(key string) bool {
	return key == "runId" || key == "unit" || key == "detail"
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
		return slog.String("event", stripControls(a.Value.String()))
	case "runId", "unit", "detail":
		return slog.String(a.Key, stripControls(a.Value.Resolve().String()))
	default:
		return slog.Attr{}
	}
}

// stripControls removes the escape sequences ESC and the C1 CSI character U+009B start, then every
// C0 control but tab and newline, DEL and every C1 control. It works rune by rune and writes a
// byte that is not UTF-8 as U+FFFD, so the bytes that remain cannot join into a new control.
func stripControls(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == '\t' || r == '\n' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeLen returns the byte length of the escape sequence that starts s, or 0 when none does:
// a CSI sequence (ESC [ or U+009B) up to its final byte, else ESC and the one character after it.
func escapeLen(s string) int {
	switch {
	case strings.HasPrefix(s, "\x1b["):
		return csiLen(s, len("\x1b["))
	case strings.HasPrefix(s, "\u009b"):
		return csiLen(s, len("\u009b"))
	case strings.HasPrefix(s, "\x1b"):
		_, size := utf8.DecodeRuneInString(s[1:])
		return 1 + size
	default:
		return 0
	}
}

// csiLen returns the length of the CSI sequence whose introducer is the first intro bytes of s:
// parameter and intermediate bytes up to and with the final byte. A sequence that ends or meets
// another byte before its final byte is no sequence; only its introducer is removed, so a cut
// escape cannot hide the text after it.
func csiLen(s string, intro int) int {
	for i := intro; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 0x40 && c <= 0x7e:
			return i + 1
		case c < 0x20 || c > 0x3f:
			return intro
		}
	}
	return intro
}
