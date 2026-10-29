package gates

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/ledger"
)

// entryTestKeys returns the sorted JSON keys of an entry.
func entryTestKeys(t *testing.T, e ledger.Failure) []string {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("encode the entry: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode the entry: %v", err)
	}
	return slices.Sorted(maps.Keys(m))
}

// TestStaticFindingSharesTheEntryShape pins L76: a golangci finding and a test failure convert to
// entries of one type that both hold the location, the kind and the message; a finding holds its
// location as file:line:col, its rule as the kind and its fix hint in the message.
func TestStaticFindingSharesTheEntryShape(t *testing.T) {
	findings, err := ParseGolangciSARIF([]byte(golangciSARIF))
	if err != nil || len(findings) == 0 {
		t.Fatalf("the golangci fixture reads no finding: %v", err)
	}
	fail := Failure{Test: "TestX", TestLocation: "internal/x/x_test.go:12", SourceLocation: "internal/x/x.go:7",
		Kind: FailureAssertion, Message: "got 1, want 2", Got: "1", Want: "2", Rerun: RerunCommand("TestX"),
		Attachments: []Attachment{{Kind: "log", Text: "boom"}}}
	fe := failureToLedger(fail)
	if fe.Test != "TestX" || fe.Kind != FailureAssertion || fe.TestLocation != fail.TestLocation ||
		fe.SourceLocation != fail.SourceLocation || fe.Got != "1" || fe.Want != "2" ||
		!slices.Equal(fe.Rerun, fail.Rerun) || len(fe.Attachments) != 1 || fe.Attachments[0].Text != "boom" {
		t.Errorf("catches a test failure that loses a field in its entry: %+v", fe)
	}
	se := findingToLedger(findings[0])
	if se.TestLocation != "internal/gates/runner.go:42:7" || se.SourceLocation != se.TestLocation {
		t.Errorf("catches a finding whose location is not file:line:col: %+v", se)
	}
	if se.Kind != "errcheck" {
		t.Errorf("catches a finding whose kind is not its rule: %q", se.Kind)
	}
	if !strings.HasPrefix(se.Message, findings[0].Message) || findings[0].Fix == "" ||
		!strings.Contains(se.Message, findings[0].Fix) {
		t.Errorf("catches a finding whose message or fix hint is lost: %q", se.Message)
	}
	if reflect.TypeOf(fe) != reflect.TypeOf(se) {
		t.Errorf("catches two entry types: %T and %T", fe, se)
	}
	for _, key := range []string{"testLocation", "kind", "message"} {
		if !slices.Contains(entryTestKeys(t, fe), key) || !slices.Contains(entryTestKeys(t, se), key) {
			t.Errorf("catches an entry of one source that lacks %q", key)
		}
	}
}

// TestCheckRecordPersistsFailuresInLedger pins the three states of a step record: a green step holds a
// known empty list, a red step whose output a parser read holds its entries, and a red step no parser
// read leaves the list unknown.
func TestCheckRecordPersistsFailuresInLedger(t *testing.T) {
	green := recordTestRun(t, recordTestStep(t, testStepID, "", 0))
	if len(green) != 1 || !green[0].Check.Failures.Known || green[0].Check.Failures.Value == nil ||
		len(green[0].Check.Failures.Value) != 0 || !green[0].Check.FailuresDropped.Known {
		t.Errorf("catches a green step whose record holds no known empty list: %+v", green)
	}
	red := recordTestRun(t, recordTestStep(t, testStepID, recordTestFailJSON, 1))
	if len(red) != 1 || !red[0].Check.Failures.Known || len(red[0].Check.Failures.Value) != 1 ||
		red[0].Check.Failures.Value[0].TestLocation != "internal/x/x_test.go:12" {
		t.Errorf("catches a red step whose parsed failure is not in the ledger record: %+v", red)
	}
	raw := recordTestRun(t, recordTestStep(t, testStepID, "connection refused\n", 1))
	if len(raw) != 1 || raw[0].Check.Failures.Known || raw[0].Check.FailuresDropped.Known {
		t.Errorf("catches a red step no parser read whose failures are not unknown: %+v", raw)
	}
}
