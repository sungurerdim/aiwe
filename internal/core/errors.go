package core

import (
	"fmt"
	"strings"
)

// Error is a condition that carries data (SPEC §5.3). It renders the message format of SPEC §5.10;
// an empty Next takes the next action of its outcome row.
type Error struct {
	Outcome  Outcome
	Expected string
	Found    string
	Next     string
	Err      error
}

// Error renders "<outcome>: expected <x>; found <y>; next: <one action>". The message is one line
// (SPEC §5.10): each LF, CR and tab in a field is written as \xNN, so a value cannot fake a second
// result line (#200).
func (e *Error) Error() string {
	next := e.Next
	if next == "" {
		next = outcomeRow(e.Outcome).Next
	}
	return fmt.Sprintf("%s: expected %s; found %s; next: %s", e.Outcome, lineEscape(e.Expected),
		lineEscape(e.Found), lineEscape(next))
}

// lineEscape writes each LF, CR and tab in s as \xNN, so s stays on one line and cannot fake a
// second result line (SPEC §5.10, #200).
func lineEscape(s string) string {
	return strings.NewReplacer("\n", `\x0a`, "\r", `\x0d`, "\t", `\x09`).Replace(s)
}

// Unwrap returns the cause, so errors.Is and errors.As see through e.
func (e *Error) Unwrap() error {
	return e.Err
}
