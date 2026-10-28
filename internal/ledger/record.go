// Package ledger holds the append-only ledger of SPEC §8.5: one JSONL file whose lines are run and
// check records, each written once and never rewritten.
package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// formatVersion is the version of the ledger record schema; every record carries it, and a record
// of another version is refused, so a reader never reads a newer line as one of its own (SPEC §8).
const formatVersion = 1

// refuse returns the refusal of a record that breaks the ledger schema: what the schema expects and
// what the record holds (SPEC §5.3). A refused record is never written, and a line that holds one is
// never read as a record (SPEC §4).
func refuse(expected, found string) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found}
}

// Kind names the type of one ledger record (SPEC §8.5).
type Kind string

// The record kinds this package writes and reads (SPEC §8.5).
const (
	KindRun   Kind = "run"
	KindCheck Kind = "check"
)

// KindEvent is the kind of one lifecycle event of an item (K45, SPEC §8.5).
const KindEvent Kind = "event"

// Header holds the fields every ledger record carries: the schema version, the kind of the record,
// its instant, the repository, goal, unit and run it belongs to, and prev, the link to the record
// before it in the hash chain (L59, SPEC §8.5).
type Header struct {
	FormatVersion int       `json:"formatVersion"`
	Kind          Kind      `json:"kind"`
	At            time.Time `json:"at"`
	RepoID        string    `json:"repoId"`
	Goal          string    `json:"goal"`
	Unit          string    `json:"unit"`
	RunID         string    `json:"runId"`
	Prev          string    `json:"prev"`
}

// validate refuses a header that is not the header of the record kind it sits in: a version this
// package does not read, a kind that differs from the record, a missing instant, an empty identity,
// which the schema writes as a value such as none, never as nothing, or a prev that is not a link of
// the hash chain (SPEC §8.5).
func (h Header) validate(kind Kind) error {
	switch {
	case h.FormatVersion != formatVersion:
		return refuse(fmt.Sprintf("formatVersion %d", formatVersion),
			fmt.Sprintf("formatVersion %d", h.FormatVersion))
	case h.Kind != kind:
		return refuse(fmt.Sprintf("kind %q", kind), fmt.Sprintf("kind %q", h.Kind))
	case h.At.IsZero():
		return refuse("the instant of the record", "an empty at")
	case h.RepoID == "" || h.Goal == "" || h.Unit == "" || h.RunID == "":
		return refuse("a repoId, goal, unit and runId, each written as none when absent",
			fmt.Sprintf("repoId %q, goal %q, unit %q, runId %q", h.RepoID, h.Goal, h.Unit, h.RunID))
	case !isHash(h.Prev):
		return refuse("a prev of 64 lowercase hex digits, the hash of the record before",
			"a prev in another form")
	}
	return nil
}

// link sets prev, the link of the record to the one before it. Only Append sets it, so a record that
// arrives with a prev of its own is refused (L59, SPEC §8.5); the refusal does not repeat the value.
func (h *Header) link(prev string) error {
	if h.Prev != "" {
		return refuse("a record whose prev Append sets", "a record that arrives with a prev of its own")
	}
	h.Prev = prev
	return nil
}

// Run is one run record: the role that ran, the source and model it ran on, the usage it measured
// and how it ended (SPEC §8.5).
type Run struct {
	Header

	TokensIn  core.Measured[int64]   `json:"tokensIn"`
	TokensOut core.Measured[int64]   `json:"tokensOut"`
	CostUSD   core.Measured[float64] `json:"costUsd"`

	Role          string `json:"role"`
	Source        string `json:"source"`
	Adapter       string `json:"adapter"`
	Model         string `json:"model"`
	ModelReported string `json:"modelReported"`
	Effort        string `json:"effort"`
	Tier          string `json:"tier"`
	Outcome       string `json:"outcome"`
	Gate          string `json:"gate"`
	Audit         string `json:"audit"`
	Tip           string `json:"tip"`
	UnknownReason string `json:"unknownReason"`
}

// isRecord marks a Run as a ledger record.
func (Run) isRecord() {}

// validate refuses a run that is not one record of the schema: a header that is not a run header, or
// a value left unknown without the reason SPEC §8.5 names for it (DR-6).
func (r Run) validate() error {
	if err := r.Header.validate(KindRun); err != nil {
		return err
	}
	switch {
	case r.unknownValues() == 0:
		return nil
	case r.UnknownReason == "":
		return refuse("a reason for every value that was not measured",
			"an unknown value with no unknownReason")
	case !isUnknownReason(r.UnknownReason):
		return refuse("one of no_price, no_usage_data and subscription",
			fmt.Sprintf("unknownReason %q", r.UnknownReason))
	}
	return nil
}

// unknownValues returns how many of the measured values of r were never measured.
func (r Run) unknownValues() int {
	unknown := 0
	for _, known := range []bool{r.TokensIn.Known, r.TokensOut.Known, r.CostUSD.Known} {
		if !known {
			unknown++
		}
	}
	return unknown
}

// isUnknownReason reports whether reason is one of the reasons SPEC §8.5 names for a value that was
// never measured (DR-6).
func isUnknownReason(reason string) bool {
	switch reason {
	case "no_price", "no_usage_data", "subscription":
		return true
	}
	return false
}

// unknownJSON is the JSON form of a field that was not measured (DR-6).
const unknownJSON = `"unknown"`

// Field is a text, list or table of a record that is either measured or unknown (DR-6, SPEC §8.5),
// as core.Measured is for a number: the zero Field is unknown, written "unknown" and never as an
// empty value or 0. A measured list or table is never nil, because null reads as nothing.
type Field[T any] struct {
	Value T
	Known bool
}

// MarshalJSON writes a known value as JSON and an unknown one as "unknown"; a known value that
// encodes as null is refused.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if !f.Known {
		return []byte(unknownJSON), nil
	}
	data, err := json.Marshal(f.Value)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, fmt.Errorf("measured field: expected a value that is not null, found %s", data)
	}
	return data, nil
}

// UnmarshalJSON reads "unknown" as an unknown field and any other JSON as a known value; null is
// refused (fail closed, SPEC §4), never read as unknown or as empty.
func (f *Field[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte(unknownJSON)) {
		*f = Field[T]{}
		return nil
	}
	if bytes.Equal(data, []byte("null")) {
		return fmt.Errorf("measured field: expected a value or %s, found null", unknownJSON)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("measured field: %w", err)
	}
	*f = Field[T]{Value: v, Known: true}
	return nil
}

// Selected is one test a check ran and the reason it was selected: import, coverage, declared,
// stale, infra or toolchain (L67, SPEC-contracts §8.5).
type Selected struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Timed is one test of a check and how long it took (L67).
type Timed struct {
	ID         string `json:"id"`
	DurationMS int64  `json:"durationMs"`
}

// Check is one check record: a gate step run against one commit, with the duration and exit code it
// measured, and the test-run fields of L67 (SPEC §8.5). A test-run field that was not measured reads
// "unknown", never 0 (DR-6); a record written before the fields reads them unknown.
type Check struct {
	Header

	DurationMS core.Measured[int64] `json:"durationMs"`
	ExitCode   core.Measured[int64] `json:"exitCode"`
	SelectMS   core.Measured[int64] `json:"selectMs"`

	Key    string `json:"key"`
	StepID string `json:"stepId"`
	Commit string `json:"commit"`
	Killed bool   `json:"killed"`

	TreeHash   Field[string]           `json:"treeHash"`
	Toolchain  Field[string]           `json:"toolchain"`
	OS         Field[string]           `json:"os"`
	BaseSha    Field[string]           `json:"baseSha"`
	BaseReason Field[string]           `json:"baseReason"`
	Selected   Field[[]Selected]       `json:"selected"`
	Slowest    Field[[]Timed]          `json:"slowest"`
	Counts     Field[map[string]int64] `json:"counts"`
	RedClass   Field[string]           `json:"redClass"`
	TailPath   Field[string]           `json:"tailPath"`
	MapGaps    Field[[]string]         `json:"mapGaps"`

	// QueueWaitMS is the time spent waiting, per resource name (L63, SPEC-contracts §8.5); a resource
	// whose wait was not measured reads unknown, and so does a record that names no resource.
	QueueWaitMS Field[map[string]core.Measured[int64]] `json:"queueWaitMs"`
}

// isRecord marks a Check as a ledger record.
func (Check) isRecord() {}

// CommitUncommitted is the commit of a check that ran on work not yet committed (SPEC-contracts §8.5).
const CommitUncommitted = "uncommitted"

// validate refuses a check that is not one record of the schema: a header that is not a check
// header, or an empty commit (a sha or CommitUncommitted, DR-6). A check that never ran leaves its
// duration and exit code unknown, so unlike a run it names no reason for them (SPEC §8.5).
func (c Check) validate() error {
	if err := c.Header.validate(KindCheck); err != nil {
		return err
	}
	if c.Commit == "" {
		return refuse(fmt.Sprintf("a commit sha or %q", CommitUncommitted), "an empty commit")
	}
	return nil
}

// Record is one record of the ledger: a Run or a Check (SPEC §8.5). The unexported method keeps the
// set closed, so no other type enters the ledger.
type Record interface {
	// isRecord marks a type as a ledger record.
	isRecord()
}

// validateRecord reports why rec may not enter the ledger, or nil when it may; a type that is not a
// ledger record is refused (SPEC §4).
func validateRecord(rec Record) error {
	switch r := rec.(type) {
	case Run:
		return r.validate()
	case Check:
		return r.validate()
	case Event:
		return r.validate()
	}
	return refuse("a run, check or event record", fmt.Sprintf("%T", rec))
}

// linked returns rec with its prev set to prev, the link to the record before it (L59, SPEC §8.5).
// A type outside the closed set of records is refused, and so is a record that arrives with a prev of
// its own.
func linked(rec Record, prev string) (Record, error) {
	switch r := rec.(type) {
	case Run:
		err := r.link(prev)
		return r, err
	case Check:
		err := r.link(prev)
		return r, err
	case Event:
		err := r.link(prev)
		return r, err
	}
	return nil, refuse("a run, check or event record", fmt.Sprintf("%T", rec))
}
