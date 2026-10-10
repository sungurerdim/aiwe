package core

import "fmt"

// Assert panics when cond is false. It is the one place for a programmer error (SPEC §5.3); a
// condition the caller can meet at run time is an error value, never an assertion.
func Assert(cond bool, format string, args ...any) {
	if !cond {
		panic(fmt.Sprintf("assertion failed: "+format, args...))
	}
}
