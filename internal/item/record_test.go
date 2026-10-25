package item_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/item"
)

// fullRecord returns a record with every field of SPEC §8.4 filled with a realistic value, so a round
// trip proves that no field is lost, renamed or reordered into another.
func fullRecord() item.Record {
	return item.Record{
		V:        item.SchemaVersion,
		ID:       "W-7K3F9Q",
		Title:    "Item records get a stable id and one versioned schema",
		Type:     item.TypeStory,
		Status:   item.StatusActive,
		Repo:     "github.com/sungurerdim/aiwe",
		Outcome:  "Every work item keeps one name for its whole life.",
		Flow:     "feature",
		Quality:  "maintainability",
		Priority: "P4",
		Severity: "high",
		Key:      "u1-1-item-records-get-a-stable-id",
		Rules:    []string{"DR-20@608076a3", "STD-5@274cc471"},
		Review:   item.ReviewNeed{Required: []string{"auditor", "security-reviewer"}},

		Parent:       "W-2M8D4T",
		Area:         "work/store",
		AreasRelated: []string{"core/modules"},
		BlockedBy:    []string{"W-5N1R6C"},
		Related:      []string{"W-9B7X0E"},

		Urgency: "M-Skeleton",
		Tags:    []string{"schema", "store"},
		Writers: []string{"sess-0194f3"},
		Reviews: []item.ReviewRow{{
			Role: "auditor", Verdict: "GREEN", RunID: "01JC0R3N1T5BASE0000000000A",
			Session: "sess-0194a7", Evidence: "evidence/u454-audit.md",
		}},

		Problem: &item.Problem{
			Symptom:  "The repository has no internal/item package, so a work item has no Go type.",
			Expected: "A record has one Go type and one versioned schema file.",
			Cause:    item.Cause{State: "known", Text: "The value sets of type, status and v are not in the code yet."},
			Impact:   "Every other unit of the goal reads or writes a record.",
			Affected: []string{"owner", "developer"},
			Class:    "must",
		},
		Need: &item.Need{
			ProblemEvidence: "gh issue view 454",
			DeleteOption:    "Delete nothing: the record type does not exist.",
			Existing:        "SPEC section 8.4 defines the record in prose only.",
			SmallestChange:  "One record file, one id file and one schema file.",
			Alternatives: []item.Alternative{{
				What: "Generate the Go type from the schema", Files: []string{"work/schema.json"},
				WhyNot: "Adds a generator for one type.",
			}},
			IfNotDone:     "No unit of the goal can start.",
			RisksNotAdded: []item.RiskNotAdded{{Risk: "Id collision", Likelihood: "low", Complexity: "medium"}},
		},
		Scope: &item.Scope{
			In:          []string{"internal/item/record.go", "internal/item/id.go", "work/schema.json"},
			New:         []string{"internal/item/record.go"},
			Out:         []string{"the file store and the item commands"},
			Constraints: []string{"The tests come first."},
		},
		Estimate: &item.Estimate{Files: 4, Lines: 320},
		Done: []item.Done{
			{ID: "an id is W- plus 6 Crockford base32 characters", Check: "go test ./internal/item/ -run TestIDFormat",
				Expect: "exit 0"},
			{ID: "the schema reads clearly", Judgement: "doc-writer", Text: "A new reader finds every field."},
		},

		Steps:     []item.Step{{Do: "define the Record type", At: "internal/item/record.go"}},
		Red:       &item.Red{Check: "go test ./internal/item/", Base: "45f760b", Items: []string{"TestIDFormat"}},
		Distinct:  []item.Distinct{{ID: "W-3H6J2V", Reason: "That item covers the store, not the record."}},
		Legacy:    true,
		Context:   "Split of the earlier unit of this number.",
		Timebox:   &item.Timebox{Hours: 4, Sessions: 2},
		Decisions: []string{"K45"},

		Defer:   &item.Defer{Until: "2026-11-01", After: "W-5N1R6C", Reason: "Waits for the store."},
		Waiting: &item.Waiting{On: "owner", QuestionFile: "questions/u454.md", Options: []string{"keep v at 1"}, Recommendation: "keep v at 1"},
		Cancel:  &item.Cancel{Reason: "Superseded", SupersededBy: "W-4C0P8S"},
		Handoff: &item.Handoff{State: "tests written", Next: "write record.go"},
		Close: &item.Close{
			Commit:           "9c2f1ab",
			VerifyOutputPath: "evidence/u454-verify.txt",
			Effect: item.Effect{
				Line: "Every work item now keeps one name.", What: "id collisions in 1000 draws",
				Before: "not measured", After: "0",
			},
		},
	}
}

// recordJSON marshals r with encoding/json, the one encoder of a record (STD-5).
func recordJSON(t *testing.T, r item.Record) []byte {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return data
}

// withKey returns the JSON of r with one top-level key set to value; a nil value deletes the key.
func withKey(t *testing.T, r item.Record, key string, value any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(recordJSON(t, r), &m); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if value == nil {
		delete(m, key)
	} else {
		m[key] = value
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal map: %v", err)
	}
	return data
}

// TestRecordRefusesUnknownType proves type holds exactly the 8 values of SPEC §8.4 and Decode refuses
// any other. It guards against a type set that grows silently, a case-insensitive match, and a
// spelling from a nearby vocabulary (the Flow Framework `feature`, an underscore for the hyphen).
func TestRecordRefusesUnknownType(t *testing.T) {
	t.Parallel()
	want := []item.Type{"goal", "epic", "story", "task", "bug", "spike", "chore", "owner-action"}
	if got := item.Types(); !slices.Equal(got, want) {
		t.Fatalf("Types() = %v, want exactly %v", got, want)
	}
	for _, typ := range want {
		t.Run("accepts "+string(typ), func(t *testing.T) {
			t.Parallel()
			got, err := item.Decode(withKey(t, fullRecord(), "type", string(typ)))
			if err != nil {
				t.Fatalf("Decode with type %q: %v", typ, err)
			}
			if got.Type != typ {
				t.Fatalf("Type = %q, want %q", got.Type, typ)
			}
		})
	}
	refused := []struct {
		name  string
		value any
	}{
		{name: "a Flow Framework value", value: "feature"},
		{name: "an upper-case spelling", value: "Goal"},
		{name: "an underscore for the hyphen", value: "owner_action"},
		{name: "a plural", value: "tasks"},
		{name: "an unlisted kind", value: "initiative"},
		{name: "the empty string", value: ""},
		{name: "a number", value: 3},
		{name: "null", value: json.RawMessage("null")},
	}
	for _, tt := range refused {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := item.Decode(withKey(t, fullRecord(), "type", tt.value)); err == nil {
				t.Fatalf("Decode accepted type %v as %q, want an error", tt.value, got.Type)
			}
		})
	}
}

// TestRecordRefusesUnknownStatus proves status holds exactly the 6 values of SPEC §8.4 and Decode
// refuses any other. It guards against a status borrowed from a tracker (`closed`, `in-progress`,
// `blocked`) or from the British spelling of `cancelled`.
func TestRecordRefusesUnknownStatus(t *testing.T) {
	t.Parallel()
	want := []item.Status{"open", "active", "waiting", "deferred", "done", "cancelled"}
	if got := item.Statuses(); !slices.Equal(got, want) {
		t.Fatalf("Statuses() = %v, want exactly %v", got, want)
	}
	for _, st := range want {
		t.Run("accepts "+string(st), func(t *testing.T) {
			t.Parallel()
			got, err := item.Decode(withKey(t, fullRecord(), "status", string(st)))
			if err != nil {
				t.Fatalf("Decode with status %q: %v", st, err)
			}
			if got.Status != st {
				t.Fatalf("Status = %q, want %q", got.Status, st)
			}
		})
	}
	refused := []struct {
		name  string
		value any
	}{
		{name: "a tracker value", value: "closed"},
		{name: "a hyphenated progress value", value: "in-progress"},
		{name: "a blocked value", value: "blocked"},
		{name: "the American spelling", value: "canceled"},
		{name: "an upper-case spelling", value: "Done"},
		{name: "the empty string", value: ""},
		{name: "a boolean", value: true},
		{name: "null", value: json.RawMessage("null")},
	}
	for _, tt := range refused {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := item.Decode(withKey(t, fullRecord(), "status", tt.value)); err == nil {
				t.Fatalf("Decode accepted status %v as %q, want an error", tt.value, got.Status)
			}
		})
	}
}

// camelKey is the shape of every JSON key of a record: lower camelCase, ASCII (SPEC §8.4).
var camelKey = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// collectKeys appends every object key under v, so a snake_case or capitalised key anywhere is found.
func collectKeys(v any, keys *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			*keys = append(*keys, k)
			collectKeys(child, keys)
		}
	case []any:
		for _, child := range x {
			collectKeys(child, keys)
		}
	}
}

// TestRecordJSONRoundTrip proves a record written as JSON with camelCase keys and the version v reads
// back unchanged. It guards against a lost or renamed field (a missing tag writes the Go name), a
// snake_case key, a version that is not written, and a field that changes in the round trip.
func TestRecordJSONRoundTrip(t *testing.T) {
	t.Parallel()
	want := fullRecord()
	data := recordJSON(t, want)

	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("written record is not JSON: %v", err)
	}
	if v, ok := generic["v"].(float64); !ok || v != item.SchemaVersion {
		t.Fatalf("written v = %v, want %d", generic["v"], item.SchemaVersion)
	}
	var keys []string
	collectKeys(generic, &keys)
	for _, k := range keys {
		if !camelKey.MatchString(k) {
			t.Errorf("key %q is not camelCase", k)
		}
	}
	for _, k := range []string{"areasRelated", "blockedBy", "runId", "questionFile", "supersededBy",
		"verifyOutputPath", "problemEvidence", "smallestChange", "ifNotDone", "risksNotAdded", "whyNot"} {
		if !slices.Contains(keys, k) {
			t.Errorf("the written record has no key %q", k)
		}
	}

	got, err := item.Decode(data)
	if err != nil {
		t.Fatalf("Decode of a written record: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record changed in the round trip\n got: %+v\nwant: %+v", got, want)
	}

	t.Run("a minimal record reads back unchanged and writes no empty optional key", func(t *testing.T) {
		t.Parallel()
		minimal := item.Record{
			V: item.SchemaVersion, ID: "W-7K3F9Q", Title: "Stable id", Type: item.TypeTask,
			Status: item.StatusOpen, Area: "work/store",
		}
		raw := recordJSON(t, minimal)
		back, err := item.Decode(raw)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !reflect.DeepEqual(back, minimal) {
			t.Fatalf("minimal record changed: got %+v, want %+v", back, minimal)
		}
		for _, k := range []string{`"problem"`, `"close"`, `"defer"`, `"reviews"`, `"legacy"`} {
			if strings.Contains(string(raw), k) {
				t.Errorf("minimal record wrote the optional key %s: %s", k, raw)
			}
		}
	})
}

// TestRecordRefusesTimeField proves a record that carries a time field is refused, because every time
// is a ledger event (SPEC §8.4, §8.5). It guards against a stored timestamp that could disagree with
// the ledger, and against a Record type that grows a time.Time field.
func TestRecordRefusesTimeField(t *testing.T) {
	t.Parallel()
	if _, err := item.Decode(recordJSON(t, fullRecord())); err != nil {
		t.Fatalf("Decode of a record with no time field: %v", err)
	}
	tests := []struct {
		name string
		key  string
	}{
		{name: "created", key: "createdAt"},
		{name: "updated", key: "updatedAt"},
		{name: "made ready", key: "readyAt"},
		{name: "taken", key: "takenAt"},
		{name: "closed", key: "closedAt"},
		{name: "reopened", key: "reopenedAt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := item.Decode(withKey(t, fullRecord(), tt.key, "2026-10-25T09:30:00Z"))
			if err == nil {
				t.Fatalf("Decode accepted a record with %s, want an error", tt.key)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not name the field %s", err, tt.key)
			}
		})
	}
	t.Run("a time field inside a nested object", func(t *testing.T) {
		t.Parallel()
		closeObj := map[string]any{"commit": "9c2f1ab", "closedAt": "2026-10-25T09:30:00Z"}
		if _, err := item.Decode(withKey(t, fullRecord(), "close", closeObj)); err == nil {
			t.Fatal("Decode accepted close.closedAt, want an error")
		}
	})
	t.Run("the Record type holds no time.Time", func(t *testing.T) {
		t.Parallel()
		var walk func(reflect.Type, string)
		walk = func(typ reflect.Type, path string) {
			for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
				typ = typ.Elem()
			}
			if typ == reflect.TypeFor[time.Time]() {
				t.Errorf("%s is a time.Time; a time belongs to the ledger", path)
			}
			if typ.Kind() != reflect.Struct {
				return
			}
			for i := range typ.NumField() {
				walk(typ.Field(i).Type, path+"."+typ.Field(i).Name)
			}
		}
		walk(reflect.TypeFor[item.Record](), "Record")
	})
}

// TestRecordRefusesTrailingData proves Decode reads exactly one JSON object and refuses any byte
// after it but white space, so a damaged or concatenated item file is never read as a valid record.
func TestRecordRefusesTrailingData(t *testing.T) {
	t.Parallel()
	whole := recordJSON(t, fullRecord())
	if _, err := item.Decode(append(slices.Clone(whole), '\n')); err != nil {
		t.Fatalf("Decode of a record with a trailing newline: %v", err)
	}
	tests := []struct {
		name  string
		after string
	}{
		{name: "a closing brace, which dec.More reports as no more data", after: "}"},
		{name: "a closing bracket, which dec.More reports as no more data", after: "]"},
		{name: "brackets then garbage after the object", after: "]]]garbage"},
		{name: "garbage after the object", after: " garbage"},
		{name: "a second object after the first", after: `{"v":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := append(slices.Clone(whole), tt.after...)
			if _, err := item.Decode(data); err == nil {
				t.Fatalf("Decode accepted a record followed by %q, want an error", tt.after)
			}
		})
	}
}
