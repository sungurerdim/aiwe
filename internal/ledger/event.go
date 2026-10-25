package ledger

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

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
var eventFieldKeys = []string{"checkKind", "key", "role", "verdict", "evidence", "commit", "files", "branch", "env", "result"}

// eventSpecs is the one table of the closed set of events: each name with the fields that event
// carries and requires (SPEC §8.5). A field outside its row is refused, so an event never carries
// the field of another.
var eventSpecs = []struct {
	name   EventName
	fields []string
}{
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

// fieldValues holds the closed value set of the fields that have one (SPEC §8.5).
var fieldValues = map[string][]string{
	"verdict": {"GREEN", "RED"},
	"result":  {"green", "red", "rolled_back"},
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
// export renames only here. role and durationMs have no attribute and keep their aiwe names.
var genAIAttributes = map[string]string{
	"provider":       "gen_ai.provider.name",
	"model":          "gen_ai.request.model",
	"modelReported":  "gen_ai.response.model",
	"harness":        "gen_ai.agent.name",
	"sessionId":      "gen_ai.conversation.id",
	"tokensIn":       "gen_ai.usage.input_tokens",
	"tokensOut":      "gen_ai.usage.output_tokens",
	"tokensCachedIn": "gen_ai.usage.cache_read.input_tokens",
}

// GenAIAttribute returns the OpenTelemetry GenAI attribute name of an aiwe key, and false for a key
// that has none (SPEC §8.5).
func GenAIAttribute(key string) (string, bool) {
	name, ok := genAIAttributes[key]
	return name, ok
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
	if err := json.Unmarshal(data, &p); err != nil {
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

// eventFields returns the per-event fields of the event name, or the refusal of a name outside the
// closed set.
func eventFields(name EventName) ([]string, error) {
	names := make([]string, 0, len(eventSpecs))
	for _, spec := range eventSpecs {
		if spec.name == name {
			return spec.fields, nil
		}
		names = append(names, string(spec.name))
	}
	return nil, refuse("one of "+strings.Join(names, ", "), fmt.Sprintf("event %q", name))
}

// validateFields refuses an event that lacks a field its name requires, carries one it does not, or
// holds a value outside the closed set of that field.
func (e Event) validateFields(fields []string) error {
	if slices.Contains(e.Files, "") {
		return refuse("a file path in every entry of files", "an empty entry")
	}
	for _, key := range eventFieldKeys {
		value, needed := e.field(key), slices.Contains(fields, key)
		switch {
		case needed && value == "":
			return refuse(fmt.Sprintf("a %s on a %s event", key, e.Name), "none")
		case !needed && value != "":
			return refuse(fmt.Sprintf("no %s on a %s event", key, e.Name), fmt.Sprintf("a %s", key))
		case value != "" && fieldValues[key] != nil && !slices.Contains(fieldValues[key], value):
			return refuse("a "+key+" of "+strings.Join(fieldValues[key], ", "), fmt.Sprintf("%s %q", key, value))
		}
	}
	return nil
}

// absolutePath finds an absolute path in a text: POSIX, a Windows drive or UNC, at the start of the
// text or after a space or a delimiter, so a URL and a relative path pass.
var absolutePath = regexp.MustCompile(`(^|[\s"'(=:,;])(/[A-Za-z0-9_.~-]+|[A-Za-z]:[\\/]|\\\\)`)

// emailAddress finds an e-mail address in a text; the top-level label is alphabetic, so a package
// version such as tools@v0.30.1 passes.
var emailAddress = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

// validatePersonalData refuses an event that holds an absolute path or an e-mail address in any text
// field (SPEC §8.5). The refusal names the kind of data and never repeats it.
func (e Event) validatePersonalData() error {
	texts := []string{e.RepoID, e.Goal, e.Unit, e.RunID,
		e.Actor.Role, e.Actor.Harness, e.Actor.Provider, e.Actor.Model, e.Actor.SessionID}
	for _, key := range eventFieldKeys {
		texts = append(texts, e.field(key))
	}
	for _, text := range texts {
		switch {
		case absolutePath.MatchString(text):
			return refuse("a record with no personal data", "an absolute path")
		case emailAddress.MatchString(text):
			return refuse("a record with no personal data", "an e-mail address")
		}
	}
	return nil
}
