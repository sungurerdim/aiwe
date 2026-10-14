package ledger

import (
	"bytes"
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
