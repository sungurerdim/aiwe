package core

import (
	"errors"
	"unicode/utf8"
)

// ErrInvalidUTF8 is returned for input that is not valid UTF-8 (SPEC §5.14, DR-6).
var ErrInvalidUTF8 = errors.New("invalid_utf8")

// ReadText returns b as text, byte for byte, when it is valid UTF-8 (DR-18). It never normalises,
// case-maps, strips a byte order mark or rewrites line ends, and it reads no locale. Invalid input
// returns "" and ErrInvalidUTF8.
func ReadText(b []byte) (string, error) {
	if !utf8.Valid(b) {
		return "", ErrInvalidUTF8
	}
	return string(b), nil
}
