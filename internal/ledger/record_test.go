package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// ledgerTestTime is a fixed whole-second instant, so a JSON round trip is exact.
func ledgerTestTime() time.Time {
	return time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
}

// ledgerTestHeader returns the fields every ledger record carries (SPEC §8.5).
func ledgerTestHeader(kind Kind) Header {
	h := Header{FormatVersion: 1, Kind: kind, At: ledgerTestTime()}
	h.RepoID = "github.com/sungurerdim/" + core.Product
	h.Goal = "sk-06"
	h.Unit = "29"
	h.RunID = "01JC0R3N1T5BASE0000000000A"
	return h
}

// ledgerTestRun builds one fully measured run record with realistic values (SPEC §8.5).
func ledgerTestRun() Run {
	r := Run{Header: ledgerTestHeader(KindRun)}
	r.Role = "engineer"
	r.Source = "opencode"
	r.Adapter = "openai-chat"
	r.Model = "deepseek-v4.1-flash"
	r.ModelReported = "deepseek-v4.1-flash"
	r.Effort = "high"
	r.Tier = "base"
	r.Outcome = "ok"
	r.Gate = "green"
	r.Audit = "approved"
	r.TokensIn = core.Measured[int64]{Value: 18342, Known: true}
	r.TokensOut = core.Measured[int64]{Value: 2048, Known: true}
	r.CostUSD = core.Measured[float64]{Value: 4.73, Known: true}
	r.Tip = "9c2f1ab"
	return r
}

// ledgerTestCheck builds one check record with realistic values (SPEC §8.5).
func ledgerTestCheck() Check {
	c := Check{Header: ledgerTestHeader(KindCheck)}
	c.Key = "test:2f7c1a9b4e6d"
	c.StepID = "test"
	c.Commit = "9c2f1ab"
	c.DurationMS = core.Measured[int64]{Value: 41250, Known: true}
	return c
}

// appendLine appends rec and returns the raw ledger line and the record read back.
func appendLine(t *testing.T, rec Record) (string, Record) {
	t.Helper()
	var buf bytes.Buffer
	if err := Append(&buf, rec); err != nil {
		t.Fatalf("Append(%T) error = %v, want nil", rec, err)
	}
	raw := buf.String()
	if strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("Append(%T) wrote %q, want one line ending in a newline", rec, raw)
	}
	records, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read after Append(%T) error = %v, want nil", rec, err)
	}
	if len(records) != 1 {
		t.Fatalf("Read after Append(%T) returned %d records, want 1", rec, len(records))
	}
	return raw, records[0]
}

// TestAppendReadRun pins the Done criterion: an appended run reads back unchanged (SPEC §8.5).
func TestAppendReadRun(t *testing.T) {
	want := ledgerTestRun()
	raw, got := appendLine(t, want)
	run, ok := got.(Run)
	if !ok {
		t.Fatalf("Read returned %T, want Run", got)
	}
	if !run.At.Equal(want.At) {
		t.Errorf("run at = %v, want %v", run.At, want.At)
	}
	if run.FormatVersion != 1 || run.Kind != KindRun {
		t.Errorf("run kind = %q with formatVersion %d, want %q with 1", run.Kind, run.FormatVersion, KindRun)
	}
	if run.RepoID != want.RepoID || run.Goal != want.Goal || run.Unit != want.Unit || run.RunID != want.RunID {
		t.Errorf("run identity = %+v, want %+v", run.Header, want.Header)
	}
	if run.Role != want.Role || run.Source != want.Source || run.Adapter != want.Adapter ||
		run.Model != want.Model || run.ModelReported != want.ModelReported || run.Effort != want.Effort ||
		run.Tier != want.Tier || run.Outcome != want.Outcome || run.Gate != want.Gate ||
		run.Audit != want.Audit || run.Tip != want.Tip {
		t.Errorf("run fields = %+v, want %+v", run, want)
	}
	if run.TokensIn != want.TokensIn || run.TokensOut != want.TokensOut || run.CostUSD != want.CostUSD {
		t.Errorf("run measured values = %+v, want %+v", run, want)
	}
	if !strings.Contains(raw, `"kind":"run"`) || !strings.Contains(raw, `"formatVersion":1`) {
		t.Errorf("run line %s does not carry kind run and formatVersion 1", raw)
	}
}

// TestAppendReadCheck pins the Done criterion: an appended check reads back unchanged (SPEC §8.5).
func TestAppendReadCheck(t *testing.T) {
	want := ledgerTestCheck()
	raw, got := appendLine(t, want)
	check, ok := got.(Check)
	if !ok {
		t.Fatalf("Read returned %T, want Check", got)
	}
	if !check.At.Equal(want.At) {
		t.Errorf("check at = %v, want %v", check.At, want.At)
	}
	if check.FormatVersion != 1 || check.Kind != KindCheck {
		t.Errorf("check kind = %q with formatVersion %d, want %q with 1", check.Kind, check.FormatVersion, KindCheck)
	}
	if check.Key != want.Key || check.StepID != want.StepID || check.Commit != want.Commit ||
		check.ExitCode != want.ExitCode || check.Killed != want.Killed {
		t.Errorf("check fields = %+v, want %+v", check, want)
	}
	if check.DurationMS != want.DurationMS {
		t.Errorf("check duration = %+v, want %+v", check.DurationMS, want.DurationMS)
	}
	if !strings.Contains(raw, `"kind":"check"`) {
		t.Errorf("check line %s does not carry kind check", raw)
	}
}

// TestUnmeasuredRunReadsUnknown pins DR-6: an unmeasured run value reads unknown with its reason,
// never 0.
func TestUnmeasuredRunReadsUnknown(t *testing.T) {
	run := ledgerTestRun()
	run.TokensIn = core.Measured[int64]{}
	run.TokensOut = core.Measured[int64]{}
	run.CostUSD = core.Measured[float64]{}
	run.UnknownReason = "no_usage_data"
	raw, got := appendLine(t, run)
	for _, frag := range []string{`"tokensIn":"unknown"`, `"tokensOut":"unknown"`, `"costUsd":"unknown"`} {
		if !strings.Contains(raw, frag) {
			t.Errorf("run line %s does not hold %s", raw, frag)
		}
	}
	for _, frag := range []string{`"tokensIn":0`, `"tokensOut":0`, `"costUsd":0`} {
		if strings.Contains(raw, frag) {
			t.Errorf("run line %s holds %s; an unmeasured value is never 0", raw, frag)
		}
	}
	back := got.(Run)
	if back.TokensIn.Known || back.TokensOut.Known || back.CostUSD.Known {
		t.Errorf("run read back %+v; an unmeasured value reads unknown", back)
	}
	if back.UnknownReason != "no_usage_data" {
		t.Errorf("unknownReason = %q, want no_usage_data", back.UnknownReason)
	}
}

// TestUnmeasuredCheckReadsUnknown pins DR-6 for a check: an unmeasured duration reads unknown,
// never 0.
func TestUnmeasuredCheckReadsUnknown(t *testing.T) {
	check := ledgerTestCheck()
	check.DurationMS = core.Measured[int64]{}
	raw, got := appendLine(t, check)
	if !strings.Contains(raw, `"durationMs":"unknown"`) {
		t.Errorf("check line %s does not hold durationMs unknown", raw)
	}
	if strings.Contains(raw, `"durationMs":0`) {
		t.Errorf("check line %s holds durationMs 0; an unmeasured value is never 0", raw)
	}
	back := got.(Check)
	if back.DurationMS.Known {
		t.Errorf("check read back duration %+v; an unmeasured value reads unknown", back.DurationMS)
	}
}

// TestUnknownReasonRequired pins the To do: an unknown value carries a reason; a record that leaves
// one value unknown with no reason is refused (DR-6).
func TestUnknownReasonRequired(t *testing.T) {
	run := ledgerTestRun()
	run.CostUSD = core.Measured[float64]{}
	var buf bytes.Buffer
	if err := Append(&buf, run); err == nil {
		t.Fatalf("Append of an unknown cost with no unknownReason = nil error, want a refusal")
	}
}

// testRunKeys are the keys of the L67 test-run fields a check record adds (SPEC §8.5).
func testRunKeys() []string {
	return []string{"treeHash", "toolchain", "os", "baseSha", "baseReason", "selected", "slowest",
		"selectMs", "queueWaitMs", "counts", "redClass", "tailPath", "mapGaps"}
}

// knownField returns a measured Field holding v.
func knownField[T any](v T) Field[T] { return Field[T]{Value: v, Known: true} }

// ledgerTestRunFields sets every L67 test-run field of c to a measured value.
func ledgerTestRunFields(c Check) Check {
	c.TreeHash = knownField("4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	c.Toolchain = knownField("go1.27.3")
	c.OS = knownField("windows/amd64")
	c.BaseSha = knownField("9c2f1ab")
	c.BaseReason = knownField("merge_base")
	c.Selected = knownField([]Selected{{ID: "ledger.TestAppendReadCheck", Reason: "import"}})
	c.Slowest = knownField([]Timed{{ID: "ledger.TestAppendIsAppendOnly", DurationMS: 812}})
	c.SelectMS = core.Measured[int64]{Value: 37, Known: true}
	c.QueueWaitMS = core.Measured[int64]{Value: 0, Known: true}
	c.Counts = knownField(map[string]int64{"fail": 1, "pass": 41, "skip": 0})
	c.RedClass = knownField("real_bug")
	c.TailPath = knownField("tails/test-2f7c1a9b4e6d.txt")
	c.MapGaps = knownField([]string{"scripts/gen.py"})
	return c
}

// testRunFieldsOf returns the L67 test-run fields of c by key, so two records compare field by field.
func testRunFieldsOf(c Check) map[string]any {
	return map[string]any{"treeHash": c.TreeHash, "toolchain": c.Toolchain, "os": c.OS,
		"baseSha": c.BaseSha, "baseReason": c.BaseReason, "selected": c.Selected, "slowest": c.Slowest,
		"selectMs": c.SelectMS, "queueWaitMs": c.QueueWaitMS, "counts": c.Counts, "redClass": c.RedClass,
		"tailPath": c.TailPath, "mapGaps": c.MapGaps}
}

// TestCheckRecordCarriesTestRunFields pins L67: the check record carries the test-run fields, they
// round-trip, and a field that was not measured reads unknown, never 0 (DR-6, SPEC §8.5).
func TestCheckRecordCarriesTestRunFields(t *testing.T) {
	t.Run("every field round-trips and its key is written", func(t *testing.T) {
		// catches a field left off the struct, renamed on the wire or lost on the way back
		want := ledgerTestRunFields(ledgerTestCheck())
		raw, got := appendLine(t, want)
		check, ok := got.(Check)
		if !ok {
			t.Fatalf("Read returned %T, want Check", got)
		}
		back := testRunFieldsOf(check)
		for key, wantField := range testRunFieldsOf(want) {
			if !strings.Contains(raw, `"`+key+`":`) {
				t.Errorf("check line %s does not carry the key %s", raw, key)
			}
			if !reflect.DeepEqual(back[key], wantField) {
				t.Errorf("%s read back %+v, want %+v", key, back[key], wantField)
			}
		}
	})
	t.Run("an unmeasured field is written unknown and reads unknown", func(t *testing.T) {
		// catches a missing field written as "", 0, null or left out of the line
		raw, got := appendLine(t, ledgerTestCheck())
		for _, key := range testRunKeys() {
			if !strings.Contains(raw, `"`+key+`":"unknown"`) {
				t.Errorf("check line %s does not hold %s unknown", raw, key)
			}
		}
		if strings.Contains(raw, `"selectMs":0`) || strings.Contains(raw, `"queueWaitMs":0`) {
			t.Errorf("check line %s holds a 0 for an unmeasured time; it is never 0", raw)
		}
		if back := testRunFieldsOf(got.(Check)); !reflect.DeepEqual(back, testRunFieldsOf(ledgerTestCheck())) {
			t.Errorf("unmeasured fields read back %+v, want every one unknown", back)
		}
	})
	t.Run("a measured zero and an empty list stay measured", func(t *testing.T) {
		// catches an empty list or a 0 read back as unknown
		want := ledgerTestRunFields(ledgerTestCheck())
		want.MapGaps = knownField([]string{})
		want.Counts = knownField(map[string]int64{})
		raw, got := appendLine(t, want)
		for _, frag := range []string{`"queueWaitMs":0`, `"mapGaps":[]`, `"counts":{}`} {
			if !strings.Contains(raw, frag) {
				t.Errorf("check line %s does not hold %s", raw, frag)
			}
		}
		check := got.(Check)
		if !check.QueueWaitMS.Known || !check.MapGaps.Known || !check.Counts.Known {
			t.Errorf("measured 0 and empty lists read back %+v, want measured", testRunFieldsOf(check))
		}
	})
	t.Run("a measured list that is nil is refused", func(t *testing.T) {
		// catches a measured list written as null, which a reader could take for 0 or for nothing
		c := ledgerTestCheck()
		c.Selected = knownField([]Selected(nil))
		var buf bytes.Buffer
		if err := Append(&buf, c); err == nil || buf.Len() != 0 {
			t.Fatalf("Append of a nil measured list = %v with %d bytes written, want a refusal and none", err, buf.Len())
		}
	})
	t.Run("a line written before the fields reads with them unknown", func(t *testing.T) {
		// catches a reader that refuses every check line an older writer wrote
		raw, _ := appendLine(t, ledgerTestCheck())
		old := regexp.MustCompile(`,"(`+strings.Join(testRunKeys(), "|")+`)":"unknown"`).ReplaceAllString(raw, "")
		if strings.Contains(old, "treeHash") {
			t.Fatalf("test setup: line %s still carries a test-run key", old)
		}
		records, err := Read(strings.NewReader(old))
		if err != nil || len(records) != 1 {
			t.Fatalf("Read of a line without the test-run keys = %d records, %v, want 1 and nil", len(records), err)
		}
		if back := testRunFieldsOf(records[0].(Check)); !reflect.DeepEqual(back, testRunFieldsOf(ledgerTestCheck())) {
			t.Errorf("fields absent from the line read back %+v, want every one unknown", back)
		}
	})
}

// ownedDirLine returns an owned_dir line as internal/core writes it (SPEC §8.5): snake_case keys and
// no prev, so it is no link of the hash chain; n tells two lines apart.
func ownedDirLine(n int) string {
	return fmt.Sprintf(`{"format_version":1,"kind":"owned_dir","at":"2026-10-14T10:00:00Z","repo_id":"none",`+
		`"goal":"none","unit":"none","run_id":"none","root":"temp","path":"aiwe-%06d","owner":"o",`+
		`"pid":4242,"start":"2026-10-14T09:59:00Z","start_id":"none","removed_at":null,"hmac":"00"}`+"\n", n)
}

// recordLines returns the record lines of a ledger, owned_dir lines left out, and the prev of each.
func recordLines(t *testing.T, raw string) (lines, prevs []string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if strings.Contains(line, `"kind":"owned_dir"`) {
			continue
		}
		var h Header
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			t.Fatalf("ledger line %q: %v", line, err)
		}
		lines, prevs = append(lines, line), append(prevs, h.Prev)
	}
	return lines, prevs
}

// requireLinkedToFirst requires the ledger raw to hold two record lines whose second prev is the
// hash of the first (L59).
func requireLinkedToFirst(t *testing.T, raw string) {
	t.Helper()
	lines, prevs := recordLines(t, raw)
	if len(lines) != 2 {
		t.Fatalf("ledger holds %d record lines, want 2", len(lines))
	}
	want, err := hashLine([]byte(lines[0]))
	if err != nil || prevs[1] != want {
		t.Fatalf("second record prev = %s, want %s (hash error %v)", prevs[1], want, err)
	}
}

// TestReadPassesOverOwnedDirLines pins the chain over owned_dir lines: core writes them into the
// ledger without a prev, Read passes over them and Append links to the record before them (L59).
func TestReadPassesOverOwnedDirLines(t *testing.T) {
	first, _ := appendLine(t, ledgerTestCheck())
	t.Run("Read skips an owned_dir line between records", func(t *testing.T) {
		// catches a reader that refuses the kind owned_dir, which stops every read of the ledger
		var buf bytes.Buffer
		buf.WriteString(first + ownedDirLine(1))
		if err := Append(&buf, ledgerTestRun()); err != nil {
			t.Fatalf("Append after an owned_dir line error = %v, want nil", err)
		}
		records, err := Read(&buf)
		if err != nil || len(records) != 2 {
			t.Fatalf("Read = %d records, %v, want 2 and nil", len(records), err)
		}
	})
	t.Run("Append links to the record before the owned_dir lines", func(t *testing.T) {
		// catches a head read from the owned_dir line, which is no record
		var buf bytes.Buffer
		buf.WriteString(first + ownedDirLine(1) + ownedDirLine(2))
		if err := Append(&buf, ledgerTestRun()); err != nil {
			t.Fatalf("Append after owned_dir lines error = %v, want nil", err)
		}
		requireLinkedToFirst(t, buf.String())
	})
	t.Run("a ledger of owned_dir lines alone starts the chain at genesis", func(t *testing.T) {
		// catches an empty chain refused because its only lines are not records
		var buf bytes.Buffer
		buf.WriteString(ownedDirLine(1))
		if err := Append(&buf, ledgerTestRun()); err != nil {
			t.Fatalf("Append after owned_dir lines only error = %v, want nil", err)
		}
		if _, prevs := recordLines(t, buf.String()); len(prevs) != 1 || prevs[0] != genesis {
			t.Fatalf("first record prev = %v, want [%s]", prevs, genesis)
		}
	})
	t.Run("a file with owned_dir lines past one tail chunk keeps the chain", func(t *testing.T) {
		// catches a file tail that stops at its first chunk, which holds owned_dir lines alone
		path := filepath.Join(t.TempDir(), "ledger.jsonl")
		body := first
		for n := 0; len(body) < 3*tailChunk; n++ {
			body += ownedDirLine(n)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write ledger %s: %v", path, err)
		}
		appendFile(t, path, ledgerTestRun())
		whole, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read ledger %s: %v", path, err)
		}
		requireLinkedToFirst(t, string(whole))
	})
	t.Run("a line of another unknown kind is still refused", func(t *testing.T) {
		// catches a skip that is wider than owned_dir, which would drop a line silently
		stranger := strings.Replace(ownedDirLine(1), "owned_dir", "stranger", 1)
		if _, err := Read(strings.NewReader(first + stranger)); err == nil {
			t.Fatalf("Read over a line of kind stranger = nil error, want a refusal")
		}
	})
	t.Run("an owned_dir line that was never finished is a torn line", func(t *testing.T) {
		// catches a torn line passed over because its text names owned_dir
		torn := strings.TrimSuffix(ownedDirLine(1), "\n")
		if _, err := Read(strings.NewReader(first + torn)); !errors.Is(err, ErrTornLine) {
			t.Fatalf("Read over a torn owned_dir line error = %v, want ErrTornLine", err)
		}
	})
}
