// Package item holds the work item record of SPEC-contracts §8.4: its Go type, the value sets of
// type and status, the schema version and the stable id. Reading and writing item files, validation
// of a whole record and the item commands are other units.
package item

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// SchemaVersion is the version v of the record format; work/schema.json carries the same number.
const SchemaVersion = 1

// Type is the kind of an item, its place in the breakdown (SPEC §8.4).
type Type string

// The 8 values of type.
const (
	TypeGoal        Type = "goal"
	TypeEpic        Type = "epic"
	TypeStory       Type = "story"
	TypeTask        Type = "task"
	TypeBug         Type = "bug"
	TypeSpike       Type = "spike"
	TypeChore       Type = "chore"
	TypeOwnerAction Type = "owner-action"
)

// Status is where an item stands in its life (SPEC §8.4).
type Status string

// The 6 values of status.
const (
	StatusOpen      Status = "open"
	StatusActive    Status = "active"
	StatusWaiting   Status = "waiting"
	StatusDeferred  Status = "deferred"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
)

// Types returns the value set of type, in the order of the specification.
func Types() []Type {
	return []Type{TypeGoal, TypeEpic, TypeStory, TypeTask, TypeBug, TypeSpike, TypeChore, TypeOwnerAction}
}

// Statuses returns the value set of status, in the order of the specification.
func Statuses() []Status {
	return []Status{StatusOpen, StatusActive, StatusWaiting, StatusDeferred, StatusDone, StatusCancelled}
}

// UnmarshalJSON refuses a value outside the set of Types.
func (t *Type) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("item type: %w", err)
	}
	if !slices.Contains(Types(), Type(s)) {
		return fmt.Errorf("item type %q is not one of %v", s, Types())
	}
	*t = Type(s)
	return nil
}

// UnmarshalJSON refuses a value outside the set of Statuses.
func (s *Status) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("item status: %w", err)
	}
	if !slices.Contains(Statuses(), Status(v)) {
		return fmt.Errorf("item status %q is not one of %v", v, Statuses())
	}
	*s = Status(v)
	return nil
}

// Record is one work item, one JSON object per file with camelCase keys (SPEC §8.4, STD-5). It has
// no time field: when an item was created, made ready, taken, closed or reopened is an event of the
// ledger, the one source of every time. A date the author sets as an input, such as Defer.Until, is
// not such a record. The field order follows the table of §8.4.
type Record struct {
	V      int    `json:"v"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   Type   `json:"type"`
	Status Status `json:"status"`

	// The work-report fields (K44).
	Repo    string `json:"repo"`
	Outcome string `json:"outcome"`
	Flow    string `json:"flow"`
	Quality string `json:"quality"`

	// Derived by the engine; the author never writes them.
	Priority string     `json:"priority"`
	Severity string     `json:"severity"`
	Key      string     `json:"key,omitempty"`
	Rules    []string   `json:"rules,omitempty"`
	Review   ReviewNeed `json:"review"`

	// Hierarchy and dependency.
	Parent       string   `json:"parent,omitempty"`
	Area         string   `json:"area"`
	AreasRelated []string `json:"areasRelated,omitempty"`
	BlockedBy    []string `json:"blockedBy,omitempty"`
	Related      []string `json:"related,omitempty"`

	Urgency string      `json:"urgency,omitempty"`
	Tags    []string    `json:"tags,omitempty"`
	Writers []string    `json:"writers,omitempty"`
	Reviews []ReviewRow `json:"reviews,omitempty"`

	// The definition of ready.
	Problem  *Problem  `json:"problem,omitempty"`
	Need     *Need     `json:"need,omitempty"`
	Scope    *Scope    `json:"scope,omitempty"`
	Estimate *Estimate `json:"estimate,omitempty"`
	Done     []Done    `json:"done,omitempty"`

	Steps     []Step     `json:"steps,omitempty"`
	Red       *Red       `json:"red,omitempty"`
	Distinct  []Distinct `json:"distinct,omitempty"`
	Legacy    bool       `json:"legacy,omitempty"`
	Context   string     `json:"context,omitempty"`
	Timebox   *Timebox   `json:"timebox,omitempty"`
	Decisions []string   `json:"decisions,omitempty"`

	// Required while the status asks for them.
	Defer   *Defer   `json:"defer,omitempty"`
	Waiting *Waiting `json:"waiting,omitempty"`
	Cancel  *Cancel  `json:"cancel,omitempty"`
	Handoff *Handoff `json:"handoff,omitempty"`
	Close   *Close   `json:"close,omitempty"`
}

// ReviewNeed is review.required: the roles of SPEC-contracts §9.5 that must judge the item.
type ReviewNeed struct {
	Required []string `json:"required,omitempty"`
}

// ReviewRow is one row of reviews, a read-only view of a verdict event of the ledger.
type ReviewRow struct {
	Role     string `json:"role"`
	Verdict  string `json:"verdict"`
	RunID    string `json:"runId"`
	Session  string `json:"session"`
	Evidence string `json:"evidence"`
}

// Cause is problem.cause.
type Cause struct {
	State string `json:"state"`
	Text  string `json:"text"`
}

// Problem is the problem of an item.
type Problem struct {
	Symptom  string   `json:"symptom"`
	Expected string   `json:"expected"`
	Cause    Cause    `json:"cause"`
	Impact   string   `json:"impact"`
	Affected []string `json:"affected"`
	Class    string   `json:"class"`
}

// Alternative is one entry of need.alternatives.
type Alternative struct {
	What   string   `json:"what"`
	Files  []string `json:"files"`
	WhyNot string   `json:"whyNot"`
}

// RiskNotAdded is one entry of need.risksNotAdded.
type RiskNotAdded struct {
	Risk       string `json:"risk"`
	Likelihood string `json:"likelihood"`
	Complexity string `json:"complexity"`
}

// Need is the justification of an item.
type Need struct {
	ProblemEvidence string         `json:"problemEvidence"`
	DeleteOption    string         `json:"deleteOption"`
	Existing        string         `json:"existing"`
	SmallestChange  string         `json:"smallestChange"`
	Alternatives    []Alternative  `json:"alternatives"`
	IfNotDone       string         `json:"ifNotDone"`
	RisksNotAdded   []RiskNotAdded `json:"risksNotAdded"`
}

// Scope is the planned file scope: a list of paths or globs for each part.
type Scope struct {
	In          []string `json:"in"`
	New         []string `json:"new"`
	Out         []string `json:"out"`
	Constraints []string `json:"constraints"`
}

// Estimate is the planned size.
type Estimate struct {
	Files int `json:"files"`
	Lines int `json:"lines"`
}

// Done is one done criterion: a check with its expectation, or a judgement by a named role.
type Done struct {
	ID        string `json:"id"`
	Check     string `json:"check,omitempty"`
	Expect    string `json:"expect,omitempty"`
	Judgement string `json:"judgement,omitempty"`
	Text      string `json:"text,omitempty"`
}

// Step is one entry of steps.
type Step struct {
	Do string `json:"do"`
	At string `json:"at"`
}

// Red is the red proof of a test commit on its base.
type Red struct {
	Check string   `json:"check"`
	Base  string   `json:"base"`
	Items []string `json:"items"`
}

// Distinct records why two flagged items are not duplicates.
type Distinct struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Timebox is hours or sessions.
type Timebox struct {
	Hours    int `json:"hours,omitempty"`
	Sessions int `json:"sessions,omitempty"`
}

// Defer is required while the status is deferred: a date input (Until) or another item (After).
type Defer struct {
	Until  string `json:"until,omitempty"`
	After  string `json:"after,omitempty"`
	Reason string `json:"reason"`
}

// Waiting is required while the status is waiting.
type Waiting struct {
	On             string   `json:"on"`
	QuestionFile   string   `json:"questionFile"`
	Options        []string `json:"options"`
	Recommendation string   `json:"recommendation"`
}

// Cancel is required while the status is cancelled.
type Cancel struct {
	Reason       string `json:"reason"`
	SupersededBy string `json:"supersededBy,omitempty"`
}

// Handoff is the one running note while the status is active.
type Handoff struct {
	State string `json:"state"`
	Next  string `json:"next"`
}

// Effect is close.effect: the measured value, else "not measured".
type Effect struct {
	Line   string `json:"line"`
	What   string `json:"what"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Close is required while the status is done.
type Close struct {
	Commit           string `json:"commit,omitempty"`
	VerifyOutputPath string `json:"verifyOutputPath"`
	Effect           Effect `json:"effect"`
}

// Decode reads one record from JSON. It refuses a value of type or status outside its set and any
// key that Record does not name, so a record that carries a time field such as createdAt is refused:
// every time is a ledger event. It checks nothing else; validation of a record is another unit.
func Decode(data []byte) (Record, error) {
	var r Record
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Record{}, fmt.Errorf("item record: %w", err)
	}
	// dec.More is false before a } or ], so only io.EOF proves nothing follows the object.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Record{}, errors.New("item record: data after the JSON object")
	}
	return r, nil
}
