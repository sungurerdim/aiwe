package ledger

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// failureTestEntries returns n failures named t000, t001 and so on, each a test failure that holds
// every field of the entry.
func failureTestEntries(n int) []Failure {
	out := make([]Failure, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t%03d", i)
		out = append(out, Failure{Test: id, Kind: "assertion", Message: "got 1, want 2",
			TestLocation: "internal/x/x_test.go:12", SourceLocation: "internal/x/x.go:7", Got: "1", Want: "2",
			Rerun:       []string{core.Product, "check", "verify", "--test", id},
			Attachments: []Attachment{{Kind: "trace", Path: "traces/a.zip"}, {Kind: "log", Text: "boom"}}})
	}
	return out
}

// TestCheckRecordRoundTripsFailures pins the failure record of L76: a check record with two failures
// reads back the same entries in the same order, one with every field and one with only the fields a
// static finding holds.
func TestCheckRecordRoundTripsFailures(t *testing.T) {
	want := []Failure{failureTestEntries(1)[0],
		{Kind: "errcheck", Message: "Error return value is not checked (fix: handle it)",
			TestLocation: "internal/x/x.go:7:2", SourceLocation: "internal/x/x.go:7:2"}}
	check := ledgerTestCheck()
	check.SetFailures(want)
	raw, got := appendLine(t, check)
	back, ok := got.(Check)
	if !ok {
		t.Fatalf("Read returned %T, want Check", got)
	}
	if !back.Failures.Known || !reflect.DeepEqual(back.Failures.Value, want) {
		t.Errorf("catches failures that change on the way through the ledger:\n got %+v\nwant %+v\nline %s",
			back.Failures, want, raw)
	}
	if !back.FailuresDropped.Known || back.FailuresDropped.Value != 0 {
		t.Errorf("catches a record with all its failures that does not read failuresDropped 0: %+v", back.FailuresDropped)
	}
	for _, key := range []string{"test", "kind", "message", "testLocation", "sourceLocation", "got", "want", "rerun", "attachments"} {
		if !strings.Contains(raw, `"`+key+`":`) {
			t.Errorf("catches an entry that does not write %q: %s", key, raw)
		}
	}
}

// TestFailuresUnknownWhenNotParsed pins DR-6 for failures: a record that has no failures key, and a
// record that never set the field, read unknown and never an empty list.
func TestFailuresUnknownWhenNotParsed(t *testing.T) {
	raw, got := appendLine(t, ledgerTestCheck())
	for _, frag := range []string{`"failures":"unknown"`, `"failuresDropped":"unknown"`} {
		if !strings.Contains(raw, frag) {
			t.Errorf("catches a check that did not parse a failure and does not write %s: %s", frag, raw)
		}
	}
	if back := got.(Check); back.Failures.Known || back.FailuresDropped.Known {
		t.Errorf("catches an unparsed check that reads known: %+v", back)
	}
	legacy := strings.NewReplacer(`,"failures":"unknown"`, "", `,"failuresDropped":"unknown"`, "").Replace(raw)
	if strings.Contains(legacy, "failures") {
		t.Fatalf("the test did not remove the keys: %s", legacy)
	}
	records, err := Read(strings.NewReader(legacy))
	if err != nil || len(records) != 1 {
		t.Fatalf("catches a record written before the field that Read refuses: %v, %d records", err, len(records))
	}
	if back := records[0].(Check); back.Failures.Known || back.Failures.Value != nil {
		t.Errorf("catches a record without the key that reads an empty list: %+v", back.Failures)
	}
}

// TestGreenStepWritesEmptyKnownList pins that a measured empty list is [] and never null: a check
// with no failure holds a known empty list and a dropped count of 0.
func TestGreenStepWritesEmptyKnownList(t *testing.T) {
	for _, in := range [][]Failure{nil, {}} {
		check := ledgerTestCheck()
		check.SetFailures(in)
		raw, got := appendLine(t, check)
		for _, frag := range []string{`"failures":[]`, `"failuresDropped":0`} {
			if !strings.Contains(raw, frag) {
				t.Errorf("catches a green record that does not hold %s: %s", frag, raw)
			}
		}
		if strings.Contains(raw, "null") {
			t.Errorf("catches a null in a green record: %s", raw)
		}
		back := got.(Check)
		if !back.Failures.Known || back.Failures.Value == nil || len(back.Failures.Value) != 0 {
			t.Errorf("catches a green record that does not read a known empty list: %+v", back.Failures)
		}
	}
}

// TestFailuresCappedAtHundredWithDroppedCount pins the cap: a record keeps the first 100 failures in
// order and counts the rest, and 3 failures drop none.
func TestFailuresCappedAtHundredWithDroppedCount(t *testing.T) {
	check := ledgerTestCheck()
	all := failureTestEntries(130)
	check.SetFailures(all)
	_, got := appendLine(t, check)
	back := got.(Check)
	if !reflect.DeepEqual(back.Failures.Value, all[:100]) {
		t.Errorf("catches a cap that does not keep the first 100 failures in order: %d entries", len(back.Failures.Value))
	}
	if !back.FailuresDropped.Known || back.FailuresDropped.Value != 30 {
		t.Errorf("catches 130 failures that do not count 30 dropped: %+v", back.FailuresDropped)
	}
	few := ledgerTestCheck()
	few.SetFailures(failureTestEntries(3))
	_, got = appendLine(t, few)
	back = got.(Check)
	if len(back.Failures.Value) != 3 || !back.FailuresDropped.Known || back.FailuresDropped.Value != 0 {
		t.Errorf("catches 3 failures that do not read failuresDropped 0: %+v, %+v", back.Failures, back.FailuresDropped)
	}
}

// TestCheckRefusesBadFailures pins the validation of an entry: a kind is required, the list may not
// pass the cap, and the dropped count is not negative or known where the list is unknown.
func TestCheckRefusesBadFailures(t *testing.T) {
	over := ledgerTestCheck()
	over.Failures = Field[[]Failure]{Value: failureTestEntries(maxFailures + 1), Known: true}
	noKind := ledgerTestCheck()
	noKind.SetFailures([]Failure{{Message: "m"}})
	negative := ledgerTestCheck()
	negative.SetFailures(nil)
	negative.FailuresDropped.Value = -1
	orphan := ledgerTestCheck()
	orphan.FailuresDropped.Known = true
	for name, check := range map[string]Check{"101 entries": over, "no kind": noKind, "negative count": negative,
		"count without a list": orphan} {
		if err := Append(new(bytes.Buffer), check); err == nil {
			t.Errorf("catches a record with %s that Append writes", name)
		}
	}
}

// TestLedgerFailureHasNoGatesImport pins DR-17: package ledger imports neither gates nor engine, so
// package gates can import it.
func TestLedgerFailureHasNoGatesImport(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source file of the package: %v", err)
	}
	for _, file := range files {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasSuffix(path, "/internal/gates") || strings.HasSuffix(path, "/internal/engine") {
				t.Errorf("catches %s that imports %s, a cycle with gates", file, path)
			}
		}
	}
}
