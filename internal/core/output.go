package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
		if err := enc.Encode(envelope{Status: r.Status, Outcome: r.Outcome, Data: r.Data, Detail: r.Detail}); err != nil {
			return fmt.Errorf("encode result envelope: %w", err)
		}
	case FormatText:
		fmt.Fprintf(&buf, "%s: %s\n", r.Status, r.Detail)
	default:
		return fmt.Errorf("unknown output format %q: want %q or %q", f, FormatText, FormatJSON)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write result envelope: %w", err)
	}
	return nil
}
