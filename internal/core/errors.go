package core

import "fmt"

// Error is a condition that carries data (SPEC §5.3). It renders the message format of SPEC §5.10;
// an empty Next takes the next action of its outcome row.
type Error struct {
	Outcome  Outcome
	Expected string
	Found    string
	Next     string
	Err      error
}

// Error renders "<outcome>: expected <x>; found <y>; next: <one action>".
func (e *Error) Error() string {
	next := e.Next
	if next == "" {
		next = outcomeRow(e.Outcome).Next
	}
	return fmt.Sprintf("%s: expected %s; found %s; next: %s", e.Outcome, e.Expected, e.Found, next)
}

// Unwrap returns the cause, so errors.Is and errors.As see through e.
func (e *Error) Unwrap() error {
	return e.Err
}
