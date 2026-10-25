package item

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// textError is a refusal of the intake filter. It is a constant type, not an outcome of the CLI
// table: the caller that reports the refusal chooses the outcome.
type textError string

func (e textError) Error() string { return string(e) }

// ErrHiddenText is wrapped by the refusal of a text that can hide content from its reader: an HTML
// comment, declaration or processing-instruction opener, or a Unicode format control (SPEC-contracts
// §8.4; Trojan Source, CVE-2021-42574; STD-22).
const ErrHiddenText textError = "text can hide content"

// FilterText is the intake filter of SPEC-contracts §8.4 (DR-20) for one untrusted text. It refuses
// a text that is not UTF-8 with core.ErrInvalidUTF8, and a text that can hide content with
// ErrHiddenText: one that holds a Unicode format control (category Cf: bidirectional controls,
// zero-width characters, tag characters) or an opener a reader hides, <! or <? or a </ not followed
// by an ASCII letter. It removes nothing, so an accepted text comes back byte for byte and the owner
// rewrites a refused one. A refused text returns no text, and the refusal names a code point or a
// byte offset, never the text. The caller names the field in its own refusal.
func FilterText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", core.ErrInvalidUTF8
	}
	if i := strings.IndexFunc(s, isFormatControl); i >= 0 {
		r, _ := utf8.DecodeRuneInString(s[i:])
		return "", fmt.Errorf("%w: format control U+%04X", ErrHiddenText, r)
	}
	if i := hidingOpener(s); i >= 0 {
		return "", fmt.Errorf("%w: a comment, declaration or processing-instruction opener at byte %d", ErrHiddenText, i)
	}
	return s, nil
}

func isFormatControl(r rune) bool { return unicode.Is(unicode.Cf, r) }

// hidingOpener returns the byte offset of the first < that opens text a reader hides, or -1.
func hidingOpener(s string) int {
	for i := 0; ; {
		k := strings.IndexByte(s[i:], '<')
		if k < 0 {
			return -1
		}
		i += k + 1
		if opensHidden(s[i:]) {
			return i - 1
		}
	}
}

// opensHidden reports whether rest, the text after a <, makes that < an opener a reader hides: <!
// and <? always, </ unless an ASCII letter follows, as in an end tag.
func opensHidden(rest string) bool {
	if rest == "" {
		return false
	}
	switch rest[0] {
	case '!', '?':
		return true
	case '/':
		return len(rest) == 1 || !isASCIILetter(rest[1])
	}
	return false
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
