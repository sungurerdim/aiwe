package item

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

const (
	// bidiControls are the twelve runes of the Unicode property Bidi_Control (UAX #9): the arabic
	// letter mark, the two marks, the embeddings, the overrides, the isolates and their pops.
	bidiControls = "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
	// openerLen is the length of every comment opener the filter looks for: <! <? and </.
	openerLen = len("<!")
)

// ErrBidiControl is wrapped by the refusal of a text that holds a bidirectional control (UAX #9;
// Trojan Source, CVE-2021-42574; STD-22).
var ErrBidiControl = errors.New("text holds a bidirectional control")

// isBidiControl reports whether r is one of the twelve bidirectional controls.
func isBidiControl(r rune) bool { return strings.ContainsRune(bidiControls, r) }

// firstBidi returns the first bidirectional control of s, if s holds one.
func firstBidi(s string) (rune, bool) {
	i := strings.IndexFunc(s, isBidiControl)
	if i < 0 {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r, true
}

// FilterText is the intake filter of SPEC §8.4 (DR-20) for one untrusted text. It refuses a text
// that is not UTF-8 with core.ErrInvalidUTF8 before it removes anything, since removing a comment
// could join bytes into a rune that no scan saw. It removes every comment an HTML5 reader hides, and
// refuses a text that holds a bidirectional control. The control is looked for in the input,
// comments included, because a comment is removed here but not by every reader, and again in the
// output, so the filter never returns a control it refuses. A refused text returns no text. The
// caller names the field in its own refusal.
func FilterText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", core.ErrInvalidUTF8
	}
	out := stripComments(s)
	for _, text := range []string{s, out} {
		if r, ok := firstBidi(text); ok {
			return "", fmt.Errorf("%w: U+%04X", ErrBidiControl, r)
		}
	}
	return out, nil
}

// stripComments removes, in one pass, every comment of the HTML5 tokenizer (WHATWG HTML, 13.2.5):
// <!-- --> with its abrupt forms <!--> and <!---> and the close --!>, and the bogus comments that
// start with <? or with <! not followed by --, CDATA included, or with </ followed by a byte that is
// not an ASCII letter. A comment that never closes hides the rest of the text, so it is removed to
// the end. The opener is looked for at the end of out, so text that joins into a new comment once
// another is removed, such as "<<!-- x -->!-- y -->", loses that comment too: the result never
// holds an opener, and an HTML5 reader of the input would show that second comment as text.
func stripComments(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, s[i])
		if n := commentLen(out, s[i+1:]); n >= 0 {
			out = out[:len(out)-openerLen]
			i += n
		}
	}
	return string(out)
}

// commentLen returns how many bytes of rest belong to the comment that the last two bytes of out
// open, or -1 when they open none. A </ at the end of the text is shown as text.
func commentLen(out []byte, rest string) int {
	n := len(out)
	if n < openerLen || out[n-2] != '<' {
		return -1
	}
	switch out[n-1] {
	case '!':
		if body, ok := strings.CutPrefix(rest, "--"); ok {
			return len("--") + commentBodyLen(body)
		}
		return bogusLen(rest)
	case '?':
		return bogusLen(rest)
	case '/':
		if rest == "" || isASCIILetter(rest[0]) {
			return -1
		}
		return bogusLen(rest)
	}
	return -1
}

// commentBodyLen returns the length of a comment body after <!-- up to and with its close: a > or a
// -> right after the opener closes an empty comment, else the first -- followed by > or !> closes
// it, and a comment that never closes runs to the end.
func commentBodyLen(body string) int {
	switch {
	case strings.HasPrefix(body, ">"):
		return len(">")
	case strings.HasPrefix(body, "->"):
		return len("->")
	}
	for from := 0; ; {
		k := strings.Index(body[from:], "--")
		if k < 0 {
			return len(body)
		}
		end := from + k + len("--")
		switch {
		case strings.HasPrefix(body[end:], ">"):
			return end + len(">")
		case strings.HasPrefix(body[end:], "!>"):
			return end + len("!>")
		}
		from += k + 1
	}
}

// bogusLen returns the length of a bogus comment up to and with its first >, or to the end.
func bogusLen(rest string) int {
	if i := strings.IndexByte(rest, '>'); i >= 0 {
		return i + 1
	}
	return len(rest)
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
