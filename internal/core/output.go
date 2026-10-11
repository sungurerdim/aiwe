package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Format is the output format of the result envelope.
type Format string

// The output formats every command accepts through the format parameter.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// envelope is the JSON shape of a result: exactly these four keys (SPEC §5.3).
type envelope struct {
	Status  Status  `json:"status"`
	Outcome Outcome `json:"outcome"`
	Data    any     `json:"data"`
	Detail  string  `json:"detail"`
}

// WriteResult writes r to w in format f. An unknown format is refused and nothing is written.
func WriteResult(w io.Writer, f Format, r Result[any]) error {
	var buf bytes.Buffer
	switch f {
	case FormatJSON:
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(envelope(r)); err != nil {
			return fmt.Errorf("encode result envelope: %w", err)
		}
	case FormatText:
		fmt.Fprintf(&buf, "%s\n", textEscape(textLine(r)))
	default:
		return fmt.Errorf("unknown output format %q: want %q or %q", f, FormatText, FormatJSON)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write result envelope: %w", err)
	}
	return nil
}

// textLine renders r as "<status>: <detail>". A refusal's detail already starts with
// "<outcome>: " (SPEC §5.10); when that prefix is the status word, the line names it once.
func textLine(r Result[any]) string {
	prefix := string(r.Status) + ": "
	if strings.HasPrefix(r.Detail, prefix) {
		return r.Detail
	}
	return prefix + r.Detail
}

// textEscape writes each terminal control in s as a visible escape, so a path or value from the file
// system or the environment cannot drive the terminal (#200). It escapes the logStrip set except
// newline and tab (C0, DEL, C1) and the bidi embeddings, overrides and isolates. Each invalid UTF-8
// byte is written as \xNN. Every other rune, in any script, passes unchanged.
func textEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}
