package ledger

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// allEventNames lists the 13 lifecycle events of SPEC §8.5, in the order the SPEC names them.
func allEventNames() []EventName {
	return []EventName{
		EventCreated, EventReady, EventTaken, EventReturned, EventCheckQueued, EventCheckStarted,
		EventVerdict, EventLanded, EventPushed, EventMerged, EventDeployed, EventClosed, EventReopened,
	}
}

// ledgerTestActor returns a fully named actor with realistic values (SPEC §8.5).
func ledgerTestActor() Actor {
	return Actor{
		Role:      "engineer",
		Harness:   "opencode",
		Provider:  "deepseek",
		Model:     "deepseek-v4.1-flash",
		SessionID: "3f6c9d1e-7a42-4b58-9c0e-5d2a8b71f4c6",
	}
}

// ledgerTestEvent builds one valid event of the given name, with the fields SPEC §8.5 gives that event.
func ledgerTestEvent(name EventName) Event {
	e := Event{Header: ledgerTestHeader(KindEvent), Name: name, Actor: ledgerTestActor()}
	e.Unit = "503"
	switch name {
	case EventCreated, EventReady, EventTaken, EventReturned, EventClosed, EventReopened:
		// These events carry no field of their own (SPEC §8.5).
	case EventCheckQueued, EventCheckStarted:
		e.CheckKind = "verify"
		e.Key = "verify:2f7c1a9b4e6d"
	case EventVerdict:
		e.Role = "auditor"
		e.Verdict = "GREEN"
		e.Evidence = "go test ./internal/ledger/ exit 0 at 9c2f1ab"
	case EventLanded:
		e.Commit = "9c2f1ab"
		e.Files = []string{"internal/ledger/event.go", "internal/ledger/record.go"}
	case EventPushed:
		e.Branch = "unit/503-ledger-lifecycle-events"
	case EventMerged:
		e.Commit = "9c2f1ab"
	case EventDeployed:
		e.Env = "production"
		e.Result = "green"
	}
	return e
}

// eventLine marshals a valid event the way Append does and returns the raw line without its newline.
func eventLine(t *testing.T, e Event) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Append(&buf, e); err != nil {
		t.Fatalf("Append(%s event) error = %v, want nil", e.Name, err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// mustRefuse fails unless Append refuses rec and writes nothing.
func mustRefuse(t *testing.T, rec Record) {
	t.Helper()
	var buf bytes.Buffer
	if err := Append(&buf, rec); err == nil {
		t.Errorf("Append(%+v) = nil error, want a refusal", rec)
	}
	if buf.Len() != 0 {
		t.Errorf("Append(%+v) refused but wrote %q", rec, buf.String())
	}
}

// mustRefuseLine fails unless Read refuses the raw ledger line: a line that holds a broken event is
// never read as an event (SPEC §4).
func mustRefuseLine(t *testing.T, line string) {
	t.Helper()
	if records, err := Read(strings.NewReader(line + "\n")); err == nil {
		t.Errorf("Read(%s) = %+v, nil error, want a refusal", line, records)
	}
}

// replaceOnce returns line with old replaced by repl and fails when old is not in line, so a test
// never mutates nothing and passes for the wrong reason.
func replaceOnce(t *testing.T, line, old, repl string) string {
	t.Helper()
	if !strings.Contains(line, old) {
		t.Fatalf("line %s does not hold %s", line, old)
	}
	return strings.Replace(line, old, repl, 1)
}

// readEvent appends e and returns the event read back.
func readEvent(t *testing.T, e Event) Event {
	t.Helper()
	_, got := appendLine(t, e)
	back, ok := got.(Event)
	if !ok {
		t.Fatalf("Read returned %T, want Event", got)
	}
	return back
}

// TestAppendReadEvent guards against an event that is lost, reordered or changed on the way through
// the ledger: it must read back unchanged, next to the run and check records (SPEC §8.5).
func TestAppendReadEvent(t *testing.T) {
	t.Run("an event between a run and a check keeps its place and its fields", func(t *testing.T) {
		want := ledgerTestEvent(EventVerdict)
		var buf bytes.Buffer
		for _, rec := range []Record{ledgerTestRun(), want, ledgerTestCheck()} {
			if err := Append(&buf, rec); err != nil {
				t.Fatalf("Append(%T) error = %v, want nil", rec, err)
			}
		}
		raw := buf.String()
		records, err := Read(&buf)
		if err != nil {
			t.Fatalf("Read error = %v, want nil", err)
		}
		if len(records) != 3 {
			t.Fatalf("Read returned %d records, want 3", len(records))
		}
		if _, ok := records[0].(Run); !ok {
			t.Errorf("first record is %T, want Run", records[0])
		}
		if _, ok := records[2].(Check); !ok {
			t.Errorf("third record is %T, want Check", records[2])
		}
		got, ok := records[1].(Event)
		if !ok {
			t.Fatalf("second record is %T, want Event", records[1])
		}
		if !got.At.Equal(want.At) {
			t.Errorf("event at = %v, want %v", got.At, want.At)
		}
		got.At = want.At
		if !reflect.DeepEqual(got, want) {
			t.Errorf("event read back = %+v, want %+v", got, want)
		}
		if !strings.Contains(raw, `"kind":"event"`) || !strings.Contains(raw, `"event":"verdict"`) {
			t.Errorf("ledger %s does not carry kind event and event verdict", raw)
		}
	})
	for _, name := range allEventNames() {
		t.Run(string(name)+" reads back unchanged", func(t *testing.T) {
			want := ledgerTestEvent(name)
			got := readEvent(t, want)
			got.At = want.At
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s event read back = %+v, want %+v", name, got, want)
			}
		})
	}
}

// TestEventNamesClosed guards against a name outside the 13 of SPEC §8.5 entering the ledger, and
// against one of the 13 being refused.
func TestEventNamesClosed(t *testing.T) {
	for _, name := range allEventNames() {
		t.Run("accepts "+string(name), func(t *testing.T) {
			var buf bytes.Buffer
			if err := Append(&buf, ledgerTestEvent(name)); err != nil {
				t.Errorf("Append(%s event) error = %v, want nil", name, err)
			}
		})
	}
	for _, name := range []EventName{"check_finished", "started", "CREATED", "closed ", "", "merge"} {
		t.Run("refuses "+string(name), func(t *testing.T) {
			e := ledgerTestEvent(EventCreated)
			e.Name = name
			mustRefuse(t, e)
			mustRefuseLine(t, replaceOnce(t, eventLine(t, ledgerTestEvent(EventCreated)),
				`"event":"created"`, `"event":"`+string(name)+`"`))
		})
	}
	if len(allEventNames()) != 13 {
		t.Fatalf("the test lists %d events, want the 13 of SPEC §8.5", len(allEventNames()))
	}
}

// TestEventRefusesMissingUnit guards against an event that names no item: flow time cannot be read
// from an event of nothing (SPEC §8.5).
func TestEventRefusesMissingUnit(t *testing.T) {
	e := ledgerTestEvent(EventTaken)
	e.Unit = ""
	mustRefuse(t, e)
	mustRefuseLine(t, replaceOnce(t, eventLine(t, ledgerTestEvent(EventTaken)), `"unit":"503"`, `"unit":""`))
	// The ledger writes an absent unit as none; an event of none names no item either.
	noneUnit := ledgerTestEvent(EventTaken)
	noneUnit.Unit = "none"
	mustRefuse(t, noneUnit)
	mustRefuseLine(t, replaceOnce(t, eventLine(t, ledgerTestEvent(EventTaken)), `"unit":"503"`, `"unit":"none"`))
	// validateBinding compared only the exact string none: a blank unit, or none or unknown in
	// another case, names no item either.
	for _, unit := range []string{" ", "None", "NONE", "none ", "unknown", "Unknown"} {
		blank := ledgerTestEvent(EventTaken)
		blank.Unit = unit
		mustRefuse(t, blank)
	}
}

// TestEventRefusesNonUTCTime guards against a time that is not RFC 3339 in UTC (STD-36): an offset
// would make two events of one instant sort and group differently.
func TestEventRefusesNonUTCTime(t *testing.T) {
	plus2 := time.FixedZone("", 2*60*60)
	t.Run("a time with an offset is refused on write", func(t *testing.T) {
		e := ledgerTestEvent(EventTaken)
		e.At = ledgerTestTime().In(plus2)
		mustRefuse(t, e)
	})
	t.Run("a line with an offset is refused on read", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventTaken))
		mustRefuseLine(t, replaceOnce(t, line, `"at":"2026-10-14T10:00:00Z"`, `"at":"2026-10-14T12:00:00+02:00"`))
	})
	t.Run("a line that is not RFC 3339 is refused on read", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventTaken))
		mustRefuseLine(t, replaceOnce(t, line, `"at":"2026-10-14T10:00:00Z"`, `"at":"2026-10-14 10:00:00"`))
	})
	t.Run("a UTC time is written with the Z designator", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventTaken))
		if !strings.Contains(line, `"at":"2026-10-14T10:00:00Z"`) {
			t.Errorf("event line %s does not hold the instant as 2026-10-14T10:00:00Z", line)
		}
	})
}

// TestEventActorUnknown guards against an unnamed actor part being written as an empty string, which
// reads as a name; DR-6 and SPEC §8.5 want unknown.
func TestEventActorUnknown(t *testing.T) {
	parts := []struct {
		key   string
		clear func(*Actor)
		read  func(Actor) string
	}{
		{"role", func(a *Actor) { a.Role = "" }, func(a Actor) string { return a.Role }},
		{"harness", func(a *Actor) { a.Harness = "" }, func(a Actor) string { return a.Harness }},
		{"provider", func(a *Actor) { a.Provider = "" }, func(a Actor) string { return a.Provider }},
		{"model", func(a *Actor) { a.Model = "" }, func(a Actor) string { return a.Model }},
		{"sessionId", func(a *Actor) { a.SessionID = "" }, func(a Actor) string { return a.SessionID }},
	}
	for _, p := range parts {
		t.Run(p.key+" unnamed reads unknown", func(t *testing.T) {
			e := ledgerTestEvent(EventTaken)
			p.clear(&e.Actor)
			raw := eventLine(t, e)
			if !strings.Contains(raw, `"`+p.key+`":"unknown"`) {
				t.Errorf("event line %s does not hold %s unknown", raw, p.key)
			}
			if got := p.read(readEvent(t, e).Actor); got != "unknown" {
				t.Errorf("actor %s read back %q, want unknown", p.key, got)
			}
		})
	}
	t.Run("an actor of nothing reads unknown in every part", func(t *testing.T) {
		e := ledgerTestEvent(EventTaken)
		e.Actor = Actor{}
		want := Actor{Role: "unknown", Harness: "unknown", Provider: "unknown", Model: "unknown", SessionID: "unknown"}
		if got := readEvent(t, e).Actor; got != want {
			t.Errorf("empty actor read back %+v, want %+v", got, want)
		}
	})
	t.Run("a line with no actor reads unknown in every part", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventTaken))
		start := strings.Index(line, `"actor":{`)
		if start < 0 {
			t.Fatalf("event line %s holds no actor object", line)
		}
		end := start + strings.Index(line[start:], "}") + 1
		line = strings.Replace(line[:start]+line[end:], ",,", ",", 1)
		records, err := Read(strings.NewReader(line + "\n"))
		if err != nil {
			t.Fatalf("Read of a line with no actor error = %v, want nil", err)
		}
		want := Actor{Role: "unknown", Harness: "unknown", Provider: "unknown", Model: "unknown", SessionID: "unknown"}
		if got := records[0].(Event).Actor; got != want {
			t.Errorf("absent actor read back %+v, want %+v", got, want)
		}
	})
}

// TestActorAttributeMapping guards against the actor keys drifting from the OpenTelemetry GenAI
// names: SPEC §8.5 keeps the one mapping, so an export renames only there.
func TestActorAttributeMapping(t *testing.T) {
	want := map[string]string{
		"provider":       "gen_ai.provider.name",
		"model":          "gen_ai.request.model",
		"modelReported":  "gen_ai.response.model",
		"harness":        "gen_ai.agent.name",
		"sessionId":      "gen_ai.conversation.id",
		"tokensIn":       "gen_ai.usage.input_tokens",
		"tokensOut":      "gen_ai.usage.output_tokens",
		"tokensCachedIn": "gen_ai.usage.cache_read.input_tokens",
	}
	for key, attr := range want {
		got, ok := GenAIAttribute(key)
		if !ok || got != attr {
			t.Errorf("GenAIAttribute(%q) = %q, %v, want %q, true", key, got, ok, attr)
		}
	}
	for _, key := range []string{"role", "durationMs", "unit", ""} {
		if got, ok := GenAIAttribute(key); ok {
			t.Errorf("GenAIAttribute(%q) = %q, true; SPEC §8.5 gives it no attribute", key, got)
		}
	}
	t.Run("every actor key that has an attribute is a key the actor writes", func(t *testing.T) {
		raw, err := json.Marshal(ledgerTestActor())
		if err != nil {
			t.Fatalf("marshal actor: %v", err)
		}
		var keys map[string]string
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatalf("unmarshal actor %s: %v", raw, err)
		}
		for _, key := range []string{"provider", "model", "harness", "sessionId"} {
			if _, ok := keys[key]; !ok {
				t.Errorf("actor %s does not write the key %q that the mapping names", raw, key)
			}
		}
		if _, ok := keys["role"]; !ok {
			t.Errorf("actor %s does not write role", raw)
		}
	})
}
