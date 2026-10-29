package gates

import (
	"fmt"
	"slices"

	"github.com/sungurerdim/aiwe/internal/ledger"
)

// findingKind is the kind of a CheckEntry that holds a lint, type or format finding.
const findingKind = "finding"

// CheckEntry is one entry of failures[] in a check record (L76, SPEC-contracts §8.5): a failing
// test read by a test parser (#608) or a lint, type or format finding read by a static reader
// (#609), in one shape. TestLocation is the file:line where the test failed, or the file:line:col
// of a finding; SourceLocation is the deepest product frame of a test, or the finding's own place.
// stored is the same entry in the form the ledger keeps (#702), converted when the entry is read.
type CheckEntry struct {
	Test           string       `json:"test,omitempty"`
	Tool           string       `json:"tool,omitempty"`
	TestLocation   string       `json:"testLocation"`
	SourceLocation string       `json:"sourceLocation,omitempty"`
	Kind           string       `json:"kind"`
	Rule           string       `json:"rule,omitempty"`
	Level          string       `json:"level,omitempty"`
	Message        string       `json:"message"`
	Fix            string       `json:"fix,omitempty"`
	Rerun          []string     `json:"rerun,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`

	stored ledger.Failure
}

// failureEntry is the entry of one failing test.
func failureEntry(f Failure) CheckEntry {
	return CheckEntry{Test: f.Test, TestLocation: f.TestLocation, SourceLocation: f.SourceLocation, Kind: f.Kind,
		Message: f.Message, Rerun: slices.Clone(f.Rerun), Attachments: slices.Clone(f.Attachments),
		stored: failureToLedger(f)}
}

// findingEntry is the entry of one static finding.
func findingEntry(s StaticFinding) CheckEntry {
	loc := findingLocation(s)
	return CheckEntry{Tool: s.Tool, TestLocation: loc, SourceLocation: loc, Kind: findingKind, Rule: s.Rule,
		Level: s.Level, Message: s.Message, Fix: s.Fix, stored: findingToLedger(s)}
}

// findingLocation is the place of a finding: file, file:line or file:line:col, as much as the tool
// gave.
func findingLocation(s StaticFinding) string {
	loc := s.File
	if s.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, s.Line)
		if s.Col > 0 {
			loc = fmt.Sprintf("%s:%d", loc, s.Col)
		}
	}
	return loc
}

// failureToLedger is the ledger entry of one failing test.
func failureToLedger(f Failure) ledger.Failure {
	out := ledger.Failure{Test: f.Test, Kind: f.Kind, Message: f.Message, TestLocation: f.TestLocation,
		SourceLocation: f.SourceLocation, Got: f.Got, Want: f.Want, Rerun: slices.Clone(f.Rerun)}
	for _, a := range f.Attachments {
		out.Attachments = append(out.Attachments, ledger.Attachment{Kind: a.Kind, Path: a.Path, Text: a.Text})
	}
	return out
}

// findingToLedger is the ledger entry of one static finding: its place is the location, its rule the
// kind and its fix hint part of the message. The tool and the level are not kept in the entry.
func findingToLedger(s StaticFinding) ledger.Failure {
	loc := findingLocation(s)
	msg := s.Message
	if s.Fix != "" {
		msg += " (fix: " + s.Fix + ")"
	}
	kind := s.Rule
	if kind == "" {
		kind = findingKind
	}
	return ledger.Failure{Kind: kind, Message: msg, TestLocation: loc, SourceLocation: loc}
}

// setLedgerFailures sets the failures of c to the ledger form of entries. A step whose output no
// parser read has no entries and leaves the field unknown: it did not parse a failure, which is not
// the same as having none (DR-6).
func setLedgerFailures(c *ledger.Check, entries []CheckEntry) {
	if len(entries) == 0 {
		return
	}
	stored := make([]ledger.Failure, 0, len(entries))
	for _, e := range entries {
		stored = append(stored, e.stored)
	}
	c.SetFailures(stored)
}
