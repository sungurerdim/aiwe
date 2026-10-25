package item

import (
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

const (
	// minFence is the shortest run of backticks or tildes that opens a fenced code block.
	minFence = 3
	// maxFenceIndent is the most spaces a fence line may start with; four make an indented line.
	maxFenceIndent = 3
)

// textError is a refusal of the intake filter. It is a constant type, not an outcome of the CLI
// table: the caller that reports the refusal chooses the outcome.
type textError string

func (e textError) Error() string { return string(e) }

// ErrBidiControl is wrapped by the refusal of a text that holds a bidirectional control (UAX #9;
// Trojan Source, CVE-2021-42574; STD-22).
const ErrBidiControl textError = "text holds a bidirectional control"

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
// could join bytes into a rune that no scan saw. It refuses a text that holds a bidirectional
// control, looked for in the whole input, comments included, because a comment is removed here but
// not by every reader. It then removes every comment a reader does not see. The output is not
// scanned again: every removed range starts at an ASCII < and ends after an ASCII > or at the end,
// and the rest is copied whole, so the output of valid UTF-8 holds only runes of the input. A
// refused text returns no text. The caller names the field in its own refusal.
func FilterText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", core.ErrInvalidUTF8
	}
	if r, ok := firstBidi(s); ok {
		return "", fmt.Errorf("%w: U+%04X", ErrBidiControl, r)
	}
	return stripComments(s), nil
}

// stripComments removes, in one pass, every comment of the HTML5 tokenizer (WHATWG HTML, 13.2.5):
// <!-- --> with its abrupt forms <!--> and <!---> and the close --!>, and the bogus comments that
// start with <? or with <! not followed by --, CDATA included, or with </ followed by a byte that is
// not an ASCII letter. A comment that never closes hides the rest of the text, so it is removed to
// the end. Markdown code (see literalLen) is copied whole, since a reader sees its delimiters as
// text; whichever of a comment and a code span starts first wins, as in CommonMark. The opener is
// looked for at the end of out, so text that joins into a new comment once another is removed,
// such as "<<!-- x -->!-- y -->", loses that comment too: outside Markdown code the result never
// holds an opener, and an HTML5 reader of the input would show that second comment as text.
func stripComments(s string) string {
	out := make([]byte, 0, len(s))
	var spans spanIndex
	for i := 0; i < len(s); i++ {
		if n := literalLen(s, i, &spans); n > 0 {
			out = append(out, s[i:i+n]...)
			i += n - 1
			continue
		}
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

// literalLen returns the length of the Markdown code at s[i] that a reader sees as literal text
// (CommonMark 0.31), or 0: a fenced code block that opens at the start of a line, a code span, an
// unmatched backtick run, or a backslash before a backtick or a backslash. A code span stays on one
// line here; one that runs over a line end loses its comments, and indented code, lists and block
// quotes are not read.
func literalLen(s string, i int, spans *spanIndex) int {
	if i == 0 || s[i-1] == '\n' {
		if n := fenceLen(s[i:]); n > 0 {
			return n
		}
	}
	switch {
	case s[i] == '\\' && i+1 < len(s) && (s[i+1] == '`' || s[i+1] == '\\'):
		return 2 // the backslash and the character it escapes, one byte each
	case s[i] == '`':
		return spans.spanLen(s, i)
	}
	return 0
}

// fenceLen returns the length of the fenced code block whose opening fence is the first line of s,
// up to the end of its closing fence, or to the end of s when it never closes; 0 when that line is
// no opening fence.
func fenceLen(s string) int {
	line, _, _ := strings.Cut(s, "\n")
	c, n, info := fenceRun(line)
	if n < minFence || c == '`' && strings.Contains(info, "`") {
		return 0
	}
	for at := len(line); at < len(s); {
		next, _, _ := strings.Cut(s[at+1:], "\n")
		at += 1 + len(next)
		if d, m, rest := fenceRun(next); d == c && m >= n && strings.Trim(rest, " \t\r") == "" {
			return at
		}
	}
	return len(s)
}

// fenceRun returns the fence character, the length of its run at the start of line after at most
// maxFenceIndent spaces, and the rest of the line; 0, 0 and "" when the line starts no run.
func fenceRun(line string) (byte, int, string) {
	body := strings.TrimLeft(line, " ")
	if len(line)-len(body) > maxFenceIndent || body == "" || body[0] != '`' && body[0] != '~' {
		return 0, 0, ""
	}
	rest := strings.TrimLeft(body, body[:1])
	return body[0], len(body) - len(rest), rest
}

// spanIndex holds, for the line the filter is on, where the last backtick run of each length
// starts, so a run with no closer is known at once and the scan stays linear.
type spanIndex struct {
	end  int         // the end of that line
	last map[int]int // run length to the start of the last run of that length on the line
}

// spanLen returns the length of the code span that the backtick run at s[i] opens: up to the end of
// the next run of the same length on the line. A run with no such closer is text, so its length is
// returned and none of its backticks opens a span.
func (x *spanIndex) spanLen(s string, i int) int {
	if x.last == nil || i > x.end {
		x.read(s, i)
	}
	n := runLen(s[i:x.end])
	if x.last[n] <= i {
		return n
	}
	for j := i + n; j < x.end; {
		k := strings.IndexByte(s[j:x.end], '`')
		if k < 0 {
			break
		}
		m := runLen(s[j+k : x.end])
		if m == n {
			return j + k + m - i
		}
		j += k + m
	}
	return n
}

// read indexes the backtick runs of the line that holds s[i].
func (x *spanIndex) read(s string, i int) {
	start := strings.LastIndexByte(s[:i], '\n') + 1
	x.end = len(s)
	if k := strings.IndexByte(s[i:], '\n'); k >= 0 {
		x.end = i + k
	}
	x.last = map[int]int{}
	for j := start; j < x.end; {
		k := strings.IndexByte(s[j:x.end], '`')
		if k < 0 {
			return
		}
		m := runLen(s[j+k : x.end])
		x.last[m] = j + k
		j += k + m
	}
}

// runLen returns the length of the backtick run at the start of s.
func runLen(s string) int { return len(s) - len(strings.TrimLeft(s, "`")) }
