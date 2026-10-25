package item

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

const (
	// commentOpen and commentClose delimit an HTML comment.
	commentOpen  = "<!--"
	commentClose = "-->"
)

// ErrBidiControl is wrapped by the refusal of a text that holds a bidirectional embedding, override
// or isolate control (UAX #9; Trojan Source, CVE-2021-42574; STD-22).
var ErrBidiControl = errors.New("text holds a bidirectional control")

// isBidiControl reports whether r is one of the nine controls U+202A to U+202E (embedding, override,
// pop) and U+2066 to U+2069 (isolates, pop).
func isBidiControl(r rune) bool {
	return r >= '\u202A' && r <= '\u202E' || r >= '\u2066' && r <= '\u2069'
}

// FilterText is the intake filter of SPEC §8.4 (DR-20) for one untrusted text: it refuses a text
// that holds a bidirectional control, and it removes every HTML comment. A comment that never closes
// hides the rest of the text from a reader, so it is removed to the end. The scan is one pass over
// the bytes, so text that joins into a new comment once another is removed, such as
// "<!<!-- x -->-- y -->", loses that comment too and the result never holds an opener. A refused
// text returns no text. The control is checked in the whole input, comments included, because a
// comment is removed here but not by every reader. The caller names the field in its own refusal.
func FilterText(s string) (string, error) {
	for _, r := range s {
		if isBidiControl(r) {
			return "", fmt.Errorf("%w: U+%04X", ErrBidiControl, r)
		}
	}
	return stripComments(s), nil
}

// stripComments removes every HTML comment in one pass. out never ends with an opener: when it
// does, the opener is cut from out and the input skips to the end of the comment.
func stripComments(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, s[i])
		if !bytes.HasSuffix(out, []byte(commentOpen)) {
			continue
		}
		out = out[:len(out)-len(commentOpen)]
		end := strings.Index(s[i+1:], commentClose)
		if end < 0 {
			break
		}
		i += end + len(commentClose)
	}
	return string(out)
}
