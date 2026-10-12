package core

import (
	"errors"
	"fmt"
	"strings"
)

// The outcomes the core itself names; every other outcome is a row of Outcomes.
const (
	OutcomeOK    Outcome = "ok"
	OutcomeError Outcome = "error"
	OutcomeUsage Outcome = "usage"
)

// OutcomeRow is one row of the outcome table: the name, the status word it carries and the one
// next action a reader takes. Err is the sentinel that maps to the row, or nil.
type OutcomeRow struct {
	Outcome Outcome
	Status  Status
	Next    string
	Err     error
}

// Outcomes returns the one outcome table (SPEC §5.3, §5.5): every outcome used in code is a row.
func Outcomes() []OutcomeRow {
	return []OutcomeRow{
		{Outcome: OutcomeOK, Status: StatusOK, Next: "read the data of the result"},
		{Outcome: OutcomeError, Status: StatusError,
			Next: "fix the condition the detail names, then run the command again"},
		{Outcome: OutcomeUsage, Status: StatusUsage, Next: "run " + Product + " --help and correct the call"},
		{Outcome: "red", Status: StatusRed, Next: "fix the failing step, then run the gate again"},
		{Outcome: "incomplete", Status: StatusError,
			Next: "run every gate step, and fix each step the report marks not_measured, then run the gate again"},
		{Outcome: "param_duplicate", Status: StatusError, Err: ErrParamDuplicate,
			Next: "remove the second definition of the parameter from the dictionary"},
		{Outcome: "param_undeclared", Status: StatusError, Err: ErrParamUndeclared,
			Next: "define the parameter and its meaning in the dictionary, or remove it from the command"},
		{Outcome: "invalid_utf8", Status: StatusError, Err: ErrInvalidUTF8,
			Next: "re-save the file as UTF-8; the detail names the first invalid byte"},
		{Outcome: "root_unresolved", Status: StatusError, Err: ErrRootUnresolved,
			Next: "set the named root override to an absolute local directory, or unset it"},
		{Outcome: "not_owned", Status: StatusError, Err: ErrNotOwned,
			Next: "leave the directory in place: its ledger record does not prove this process made it"},
		{Outcome: "root_unsafe", Status: StatusError, Err: ErrRootUnsafe,
			Next: "make the root the detail names an absolute directory apart from the repository, not a link, owned by " +
				"the user that runs " + Product + " and open to no other user"},
		{Outcome: "residue_locked", Status: StatusError, Err: ErrResidueLocked,
			Next: "close the process that holds a file in the directory the detail names; the next sweep retries"},
		{Outcome: "footprint_exceeded", Status: StatusError, Err: ErrFootprintExceeded,
			Next: "free space under the footprint the detail names, or raise its ceiling"},
		{Outcome: "config_invalid", Status: StatusError, Err: ErrConfigInvalid,
			Next: "correct the state file the detail names to one JSON object of string values"},
	}
}

// ownerOnlyNext returns the next step that fixes the refusal of p, open to another user or, when
// foreign, owned by one (SPEC §5.10, §5.15). A directory is never named for delete: it may be the
// home, a repository or a file-system root. A file, such as the owner key, is deleted first, so a
// key another user could read is replaced rather than kept.
func ownerOnlyNext(p string, dir, foreign bool) string {
	switch {
	case dir && foreign:
		return fmt.Sprintf("; next: give its ownership to the user that runs %s, or set the root to a directory that user owns", Product)
	case dir:
		return fmt.Sprintf("; next: remove the access of every other user from %q", p)
	case foreign:
		return fmt.Sprintf("; next: delete %q, or give its ownership to the user that runs %s", p, Product)
	}
	return fmt.Sprintf("; next: delete %q so the next %s run makes it owner-only, or remove the access of every other user from it", p, Product)
}

// exitCodeRow maps one status word to its process exit code.
type exitCodeRow struct {
	Status Status
	Code   int
}

// exitCodes returns the status word → exit code table of SPEC §5.3.
func exitCodes() []exitCodeRow {
	return []exitCodeRow{
		{Status: StatusOK, Code: 0},
		{Status: StatusError, Code: 1},
		{Status: StatusUsage, Code: 2},
		{Status: StatusRed, Code: 3},
		{Status: StatusNoChange, Code: 4},
	}
}

// ExitCode returns the process exit code of s; an unknown status exits 1, as an error (fail-closed).
func (s Status) ExitCode() int {
	for _, r := range exitCodes() {
		if r.Status == s {
			return r.Code
		}
	}
	return 1
}

// outcomeRow returns the row named o, or the error row when no row has that name (fail-closed).
func outcomeRow(o Outcome) OutcomeRow {
	var fallback OutcomeRow
	for _, r := range Outcomes() {
		if r.Outcome == o {
			return r
		}
		if r.Outcome == OutcomeError {
			fallback = r
		}
	}
	Assert(fallback.Outcome == OutcomeError, "the outcome table has no %q row", OutcomeError)
	return fallback
}

// classify returns the outcome row of err: a *Error by its outcome, a sentinel by errors.Is, and
// anything else as an error. An error means the command could not decide (SPEC §5.3), so it never
// maps to a decided status: ok, red or no_change.
func classify(err error) OutcomeRow {
	var e *Error
	if errors.As(err, &e) {
		if r := outcomeRow(e.Outcome); r.Status == StatusError || r.Status == StatusUsage {
			return r
		}
		return outcomeRow(OutcomeError)
	}
	for _, r := range Outcomes() {
		if r.Err != nil && errors.Is(err, r.Err) {
			return r
		}
	}
	return outcomeRow(OutcomeError)
}

// ErrorResult maps err to its result: the outcome, status and exit code come from the outcome table.
// The detail is one line: an error that is not a *Error, such as a wrapped OS error that names a
// path, has each LF, CR and tab escaped (#223). A wrapped *Error's "<outcome>: " moves to the start
// of the detail, so the text envelope names the outcome once (SPEC §5.10).
func ErrorResult(err error) Result[any] {
	r := classify(err)
	return Result[any]{Status: r.Status, Outcome: r.Outcome, Detail: lineEscape(outcomeFirst(err))}
}

// outcomeFirst returns the message of err with the "<outcome>: " of its first *Error moved to the
// front; the context the callers added stays, in order.
func outcomeFirst(err error) string {
	msg := err.Error()
	var e *Error
	if !errors.As(err, &e) {
		return msg
	}
	inner := e.Error()
	i := strings.LastIndex(msg, inner)
	if i <= 0 {
		return msg
	}
	prefix := string(e.Outcome) + ": "
	return prefix + msg[:i] + strings.TrimPrefix(inner, prefix)
}
