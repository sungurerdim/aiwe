package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/sungurerdim/aiwe/internal/core"
)

// EventName names one lifecycle event of an item (K45, SPEC §8.5).
type EventName string

// The 13 lifecycle events of SPEC §8.5. The finished check is the check record, not an event.
const (
	EventCreated      EventName = "created"
	EventReady        EventName = "ready"
	EventTaken        EventName = "taken"
	EventReturned     EventName = "returned"
	EventCheckQueued  EventName = "check_queued"
	EventCheckStarted EventName = "check_started"
	EventVerdict      EventName = "verdict"
	EventLanded       EventName = "landed"
	EventPushed       EventName = "pushed"
	EventMerged       EventName = "merged"
	EventDeployed     EventName = "deployed"
	EventClosed       EventName = "closed"
	EventReopened     EventName = "reopened"
)

// eventFieldKeys lists, in a fixed order, the keys of the fields an event carries beyond the common
// ones; a refusal names the first that breaks the schema.
func eventFieldKeys() []string {
	return []string{"checkKind", "key", "role", "verdict", "evidence", "commit", "files", "branch", "env", "result"}
}

// eventSpec is one row of eventSpecs: an event name and the fields that event carries.
type eventSpec struct {
	name   EventName
	fields []string
}

// eventSpecs is the one table of the closed set of events: each name with the fields that event
// carries and requires (SPEC §8.5). A field outside its row is refused, so an event never carries
// the field of another.
func eventSpecs() []eventSpec {
	return []eventSpec{
		{EventCreated, nil},
		{EventReady, nil},
		{EventTaken, nil},
		{EventReturned, nil},
		{EventCheckQueued, []string{"checkKind", "key"}},
		{EventCheckStarted, []string{"checkKind", "key"}},
		{EventVerdict, []string{"role", "verdict", "evidence"}},
		{EventLanded, []string{"commit", "files"}},
		{EventPushed, []string{"branch"}},
		{EventMerged, []string{"commit"}},
		{EventDeployed, []string{"env", "result"}},
		{EventClosed, nil},
		{EventReopened, nil},
	}
}

// fieldValues returns the closed value set of the field key, and nil for a field that has none
// (SPEC §8.5).
func fieldValues(key string) []string {
	switch key {
	case "verdict":
		return []string{"GREEN", "RED"}
	case "result":
		return []string{"green", "red", "rolled_back"}
	}
	return nil
}

// unknownName is how an actor part that cannot be named reads (DR-6, SPEC §8.5).
const unknownName = "unknown"

// Actor names who acted in an event: the role, the harness, the provider and model, and the session.
// A part that cannot be named reads unknown (SPEC §8.5).
type Actor struct {
	Role      string `json:"role"`
	Harness   string `json:"harness"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	SessionID string `json:"sessionId"`
}

// named returns a with every part that is empty read as unknown.
func (a Actor) named() Actor {
	for _, part := range []*string{&a.Role, &a.Harness, &a.Provider, &a.Model, &a.SessionID} {
		if *part == "" {
			*part = unknownName
		}
	}
	return a
}

// MarshalJSON writes the actor with every part that cannot be named as unknown, never as nothing.
func (a Actor) MarshalJSON() ([]byte, error) {
	type plain Actor
	return json.Marshal(plain(a.named()))
}

// genAIAttributes is the one mapping from the camelCase keys of the ledger to the OpenTelemetry GenAI
// attribute names (SPEC §8.5, §17). The attributes have status Development and may be renamed, so an
// export renames only here. role and durationMs have no attribute and keep their aiwe names. Each
// row is an aiwe key and its attribute name, in that order.
func genAIAttributes() []struct{ key, attribute string } {
	return []struct{ key, attribute string }{
		{"provider", "gen_ai.provider.name"},
		{"model", "gen_ai.request.model"},
		{"modelReported", "gen_ai.response.model"},
		{"harness", "gen_ai.agent.name"},
		{"sessionId", "gen_ai.conversation.id"},
		{"tokensIn", "gen_ai.usage.input_tokens"},
		{"tokensOut", "gen_ai.usage.output_tokens"},
		{"tokensCachedIn", "gen_ai.usage.cache_read.input_tokens"},
	}
}

// GenAIAttribute returns the OpenTelemetry GenAI attribute name of an aiwe key, and false for a key
// that has none (SPEC §8.5).
func GenAIAttribute(key string) (string, bool) {
	for _, row := range genAIAttributes() {
		if row.key == key {
			return row.attribute, true
		}
	}
	return "", false
}

// Event is one lifecycle event of an item: the unit is the item id and At its time, in UTC (K45,
// SPEC §8.5). Beyond the common fields each event carries only its own, listed in eventSpecs.
type Event struct {
	Header

	Name  EventName `json:"event"`
	Actor Actor     `json:"actor"`

	TokensIn   core.Measured[int64] `json:"tokensIn"`
	TokensOut  core.Measured[int64] `json:"tokensOut"`
	DurationMS core.Measured[int64] `json:"durationMs"`

	CheckKind string   `json:"checkKind,omitempty"`
	Key       string   `json:"key,omitempty"`
	Role      string   `json:"role,omitempty"`
	Verdict   string   `json:"verdict,omitempty"`
	Evidence  string   `json:"evidence,omitempty"`
	Commit    string   `json:"commit,omitempty"`
	Files     []string `json:"files,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Env       string   `json:"env,omitempty"`
	Result    string   `json:"result,omitempty"`
}

// isRecord marks an Event as a ledger record.
func (Event) isRecord() {}

// UnmarshalJSON reads an event and gives every actor part the line does not name the value unknown:
// no reader converts an older shape, and a field that is absent reads unknown (L32, DR-6).
func (e *Event) UnmarshalJSON(data []byte) error {
	type plain Event
	var p plain
	// A key the event does not hold is refused, here as well as in the reader: encoding/json hands
	// this method the raw object, so the reader's own decoder setting does not reach it (STD-11).
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return err
	}
	p.Actor = p.Actor.named()
	*e = Event(p)
	return nil
}

// field returns the value of the per-event field key, empty when the event holds none; the files read
// as one line each.
func (e Event) field(key string) string {
	switch key {
	case "checkKind":
		return e.CheckKind
	case "key":
		return e.Key
	case "role":
		return e.Role
	case "verdict":
		return e.Verdict
	case "evidence":
		return e.Evidence
	case "commit":
		return e.Commit
	case "files":
		return strings.Join(e.Files, "\n")
	case "branch":
		return e.Branch
	case "env":
		return e.Env
	case "result":
		return e.Result
	}
	return ""
}

// validate refuses an event that is not one record of the schema: a header that is not an event
// header, a time that is not in UTC, a name outside the 13, a field that its event does not carry or
// lacks, or personal data (SPEC §8.5).
func (e Event) validate() error {
	if err := e.Header.validate(KindEvent); err != nil {
		return err
	}
	if err := e.validateBinding(); err != nil {
		return err
	}
	if _, offset := e.At.Zone(); offset != 0 {
		return refuse("an at in UTC (RFC 3339, STD-36)", "an at with a UTC offset")
	}
	fields, err := eventFields(e.Name)
	if err != nil {
		return err
	}
	if err := e.validateFields(fields); err != nil {
		return err
	}
	return e.validatePersonalData()
}

// absentID is how the ledger writes an identity that is absent (SPEC §8.5).
const absentID = "none"

// validateBinding refuses an event of no item, since the ledger writes an absent unit as none, and a
// verdict that binds to no run.
func (e Event) validateBinding() error {
	switch {
	case namesNothing(e.Unit):
		return refuse("the id of the item the event belongs to", "a unit that names no item")
	case e.Name == EventVerdict && namesNothing(e.RunID):
		return refuse("the runId of the run the verdict judges", "a runId that names no run")
	}
	return nil
}

// namesNothing reports whether an id names nothing: blank once trimmed, or none or unknown in any
// case.
func namesNothing(id string) bool {
	id = strings.TrimSpace(id)
	return id == "" || strings.EqualFold(id, absentID) || strings.EqualFold(id, unknownName)
}

// eventFields returns the per-event fields of the event name, or the refusal of a name outside the
// closed set.
func eventFields(name EventName) ([]string, error) {
	specs := eventSpecs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.name == name {
			return spec.fields, nil
		}
		names = append(names, string(spec.name))
	}
	return nil, refuse("one of "+strings.Join(names, ", "), "an event name outside the set")
}

// validateFields refuses an event that lacks a field its name requires, carries one it does not, or
// holds a value outside the closed set of that field.
func (e Event) validateFields(fields []string) error {
	if slices.Contains(e.Files, "") {
		return refuse("a file path in every entry of files", "an empty entry")
	}
	for _, key := range eventFieldKeys() {
		value, needed, allowed := e.field(key), slices.Contains(fields, key), fieldValues(key)
		switch {
		case needed && value == "":
			return refuse(fmt.Sprintf("a %s on a %s event", key, e.Name), "none")
		case !needed && value != "":
			return refuse(fmt.Sprintf("no %s on a %s event", key, e.Name), fmt.Sprintf("a %s", key))
		case value != "" && allowed != nil && !slices.Contains(allowed, value):
			return refuse("a "+key+" of "+strings.Join(allowed, ", "), "a "+key+" outside the set")
		}
	}
	return nil
}

// pathChar is a character that continues a path or a URL. A slash, a drive or a UNC prefix after one
// of them is part of the longer path or URL; after any other character, or at the start, it opens an
// absolute path.
const pathChar = `\p{L}\p{N}\p{M}_.~%/\\-`

// absolutePath finds an absolute path in a text: a POSIX path, a Windows drive, a UNC path written
// with backslashes or slashes, or a file URL in any case, wherever it is not preceded by a path
// character. A colon opens a POSIX path or a drive (key:/home/x) but not //, so a URL such as
// https://example.com/x and a relative path such as a/b pass.
var absolutePath = regexp.MustCompile(`(?:^|[^` + pathChar + `])(?:/[\p{L}\p{N}_.~-]|[A-Za-z]:[\\/]|\\\\)` +
	`|(?:^|[^:` + pathChar + `])//[\p{L}\p{N}_.~-]` +
	`|(?:^|[^\p{L}\p{N}])(?i:file):/`)

// emailAddress finds an e-mail address in a text, also one whose local part or domain holds letters
// or combining marks outside ASCII; the top-level label opens with a letter, so a package version
// such as tools@v0.30.1 passes. It matches the text as mailText reads it.
var emailAddress = regexp.MustCompile(`[\p{L}\p{M}\p{N}._%+-]+@[\p{L}\p{M}0-9-]+(\.[\p{L}\p{M}0-9-]+)*\.\p{L}[\p{L}\p{M}]+`)

// mailLookalike returns the ASCII form of a character that reads as an at sign or a label dot of an
// address: the fullwidth and small at signs (U+FF20, U+FE6B), and the fullwidth, ideographic and
// halfwidth ideographic full stops (U+FF0E, U+3002, U+FF61) that IDNA reads as a dot. Any other
// character is returned as it is.
func mailLookalike(r rune) rune {
	switch r {
	case 0xFF20, 0xFE6B:
		return '@'
	case 0xFF0E, 0x3002, 0xFF61:
		return '.'
	}
	return r
}

// mailText returns text as the e-mail pattern reads it: without the invisible format characters
// (\p{Cf}), such as a zero-width space or a soft hyphen, and with the lookalikes of mailLookalike in
// their ASCII form. NFKC would need golang.org/x/text, which the module does not require, so the
// mapping is explicit.
func mailText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return mailLookalike(r)
	}, text)
}

// validatePersonalData refuses an event that holds an absolute path or an e-mail address in any text
// field (SPEC §8.5). The refusal names the kind of data and never repeats it.
func (e Event) validatePersonalData() error {
	texts := []string{e.RepoID, e.Goal, e.Unit, e.RunID,
		e.Actor.Role, e.Actor.Harness, e.Actor.Provider, e.Actor.Model, e.Actor.SessionID}
	for _, key := range eventFieldKeys() {
		texts = append(texts, e.field(key))
	}
	for _, text := range texts {
		switch {
		case absolutePath.MatchString(text):
			return refuse("a record with no personal data", "an absolute path")
		case emailAddress.MatchString(mailText(text)):
			return refuse("a record with no personal data", "an e-mail address")
		}
	}
	return nil
}
