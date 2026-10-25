package ledger

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestVerdictEventFields guards against a verdict that cannot be bound to a judging run or that
// carries a verdict word other than GREEN or RED (SPEC §8.5, F1).
func TestVerdictEventFields(t *testing.T) {
	for _, verdict := range []string{"GREEN", "RED"} {
		t.Run("accepts "+verdict, func(t *testing.T) {
			e := ledgerTestEvent(EventVerdict)
			e.Verdict = verdict
			back := readEvent(t, e)
			if back.Role != "auditor" || back.Verdict != verdict || back.Evidence != e.Evidence || back.RunID != e.RunID {
				t.Errorf("verdict event read back %+v, want role auditor, verdict %s, evidence and runId kept", back, verdict)
			}
		})
	}
	for _, verdict := range []string{"green", "Red", "PASS", "APPROVED", "none", ""} {
		t.Run("refuses verdict "+verdict, func(t *testing.T) {
			e := ledgerTestEvent(EventVerdict)
			e.Verdict = verdict
			mustRefuse(t, e)
		})
	}
	t.Run("refuses a verdict without role, evidence or runId", func(t *testing.T) {
		noRole := ledgerTestEvent(EventVerdict)
		noRole.Role = ""
		mustRefuse(t, noRole)
		noEvidence := ledgerTestEvent(EventVerdict)
		noEvidence.Evidence = ""
		mustRefuse(t, noEvidence)
		noRun := ledgerTestEvent(EventVerdict)
		noRun.RunID = ""
		mustRefuse(t, noRun)
		// The ledger writes an absent run as none; a verdict must bind to a run.
		noneRun := ledgerTestEvent(EventVerdict)
		noneRun.RunID = "none"
		mustRefuse(t, noneRun)
	})
	// validateBinding compared only the exact string none.
	for _, runID := range []string{" ", "None", "NONE", " none", "unknown", "Unknown"} {
		t.Run("refuses runId "+runID, func(t *testing.T) {
			e := ledgerTestEvent(EventVerdict)
			e.RunID = runID
			mustRefuse(t, e)
		})
	}
	t.Run("a line with a verdict word outside the two is refused on read", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventVerdict))
		mustRefuseLine(t, replaceOnce(t, line, `"verdict":"GREEN"`, `"verdict":"PASS"`))
	})
}

// TestLandedEventFields guards against a landing that names no commit or no files: the first landed
// event is the start of the DORA lead time (SPEC §8.5).
func TestLandedEventFields(t *testing.T) {
	want := ledgerTestEvent(EventLanded)
	back := readEvent(t, want)
	if back.Commit != want.Commit || !reflect.DeepEqual(back.Files, want.Files) {
		t.Errorf("landed event read back commit %q files %v, want %q %v", back.Commit, back.Files, want.Commit, want.Files)
	}
	noCommit := ledgerTestEvent(EventLanded)
	noCommit.Commit = ""
	mustRefuse(t, noCommit)
	noFiles := ledgerTestEvent(EventLanded)
	noFiles.Files = nil
	mustRefuse(t, noFiles)
	emptyFile := ledgerTestEvent(EventLanded)
	emptyFile.Files = []string{"internal/ledger/event.go", ""}
	mustRefuse(t, emptyFile)
}

// TestPushedEventFields guards against a push that names no branch, and against a field of another
// event riding on it.
func TestPushedEventFields(t *testing.T) {
	want := ledgerTestEvent(EventPushed)
	if back := readEvent(t, want); back.Branch != want.Branch {
		t.Errorf("pushed event read back branch %q, want %q", back.Branch, want.Branch)
	}
	noBranch := ledgerTestEvent(EventPushed)
	noBranch.Branch = ""
	mustRefuse(t, noBranch)
	foreign := ledgerTestEvent(EventPushed)
	foreign.Commit = "9c2f1ab"
	mustRefuse(t, foreign)
}

// TestMergedEventFields guards against a merge that names no commit: the merge time is the end of
// the lead time for a project that does not deploy (SPEC §8.5).
func TestMergedEventFields(t *testing.T) {
	want := ledgerTestEvent(EventMerged)
	if back := readEvent(t, want); back.Commit != want.Commit {
		t.Errorf("merged event read back commit %q, want %q", back.Commit, want.Commit)
	}
	noCommit := ledgerTestEvent(EventMerged)
	noCommit.Commit = ""
	mustRefuse(t, noCommit)
}

// TestDeployedEventFields guards against a deploy whose result is outside green, red and rolled_back,
// which the change fail rate and the recovery time would misread (SPEC §8.5).
func TestDeployedEventFields(t *testing.T) {
	for _, result := range []string{"green", "red", "rolled_back"} {
		t.Run("accepts "+result, func(t *testing.T) {
			e := ledgerTestEvent(EventDeployed)
			e.Result = result
			back := readEvent(t, e)
			if back.Env != "production" || back.Result != result {
				t.Errorf("deployed event read back env %q result %q, want production %q", back.Env, back.Result, result)
			}
		})
	}
	for _, result := range []string{"GREEN", "failed", "rolledback", "success", ""} {
		t.Run("refuses result "+result, func(t *testing.T) {
			e := ledgerTestEvent(EventDeployed)
			e.Result = result
			mustRefuse(t, e)
		})
	}
	noEnv := ledgerTestEvent(EventDeployed)
	noEnv.Env = ""
	mustRefuse(t, noEnv)
	line := eventLine(t, ledgerTestEvent(EventDeployed))
	mustRefuseLine(t, replaceOnce(t, line, `"result":"green"`, `"result":"success"`))
}

// TestCheckEventFields guards against a queued or started check that cannot be tied to its finished
// check record: both carry the checkKind and the cache key (SPEC §8.5).
func TestCheckEventFields(t *testing.T) {
	for _, name := range []EventName{EventCheckQueued, EventCheckStarted} {
		t.Run(string(name), func(t *testing.T) {
			want := ledgerTestEvent(name)
			back := readEvent(t, want)
			if back.CheckKind != want.CheckKind || back.Key != want.Key {
				t.Errorf("%s event read back checkKind %q key %q, want %q %q", name, back.CheckKind, back.Key, want.CheckKind, want.Key)
			}
			noKind := ledgerTestEvent(name)
			noKind.CheckKind = ""
			mustRefuse(t, noKind)
			noKey := ledgerTestEvent(name)
			noKey.Key = ""
			mustRefuse(t, noKey)
		})
	}
}

// TestEventMeasuredUnknown guards against DR-6: a tokensIn, tokensOut or durationMs that was not
// measured is written unknown, never 0, and a line that lacks the key reads unknown.
func TestEventMeasuredUnknown(t *testing.T) {
	t.Run("unmeasured values are written unknown and never 0", func(t *testing.T) {
		e := ledgerTestEvent(EventTaken)
		raw := eventLine(t, e)
		for _, frag := range []string{`"tokensIn":"unknown"`, `"tokensOut":"unknown"`, `"durationMs":"unknown"`} {
			if !strings.Contains(raw, frag) {
				t.Errorf("event line %s does not hold %s", raw, frag)
			}
		}
		for _, frag := range []string{`"tokensIn":0`, `"tokensOut":0`, `"durationMs":0`} {
			if strings.Contains(raw, frag) {
				t.Errorf("event line %s holds %s; an unmeasured value is never 0", raw, frag)
			}
		}
		back := readEvent(t, e)
		if back.TokensIn.Known || back.TokensOut.Known || back.DurationMS.Known {
			t.Errorf("event read back %+v; an unmeasured value reads unknown", back)
		}
	})
	t.Run("measured values read back", func(t *testing.T) {
		e := ledgerTestEvent(EventVerdict)
		e.TokensIn = core.Measured[int64]{Value: 18342, Known: true}
		e.TokensOut = core.Measured[int64]{Value: 2048, Known: true}
		e.DurationMS = core.Measured[int64]{Value: 41250, Known: true}
		back := readEvent(t, e)
		if back.TokensIn != e.TokensIn || back.TokensOut != e.TokensOut || back.DurationMS != e.DurationMS {
			t.Errorf("event read back %+v, want the measured values of %+v", back, e)
		}
	})
	t.Run("a line that lacks the keys reads unknown", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventTaken))
		for _, frag := range []string{`,"tokensIn":"unknown"`, `,"tokensOut":"unknown"`, `,"durationMs":"unknown"`} {
			line = replaceOnce(t, line, frag, "")
		}
		records, err := Read(strings.NewReader(line + "\n"))
		if err != nil {
			t.Fatalf("Read of a line with no measured keys error = %v, want nil", err)
		}
		back := records[0].(Event)
		if back.TokensIn.Known || back.TokensOut.Known || back.DurationMS.Known {
			t.Errorf("event read back %+v; an absent value reads unknown", back)
		}
	})
}

// TestEventRefusesPersonalData guards against a record that holds personal data: SPEC §8.5 allows no
// user name, e-mail, host name or absolute path in the ledger.
func TestEventRefusesPersonalData(t *testing.T) {
	refused := []struct {
		name  string
		build func(*Event)
	}{
		{"a POSIX path in the evidence", func(e *Event) { e.Evidence = "ran in /home/sungur/work/aiwe, exit 0" }},
		{"a Windows path in the evidence", func(e *Event) { e.Evidence = `exit 0 in C:\Users\Sungur\aiwe` }},
		{"a Windows path with a slash in the evidence", func(e *Event) { e.Evidence = "exit 0 in d:/GitHub/aiwe" }},
		{"a UNC path in the evidence", func(e *Event) { e.Evidence = `exit 0 in \\build-host\share\aiwe` }},
		{"a path that opens the evidence", func(e *Event) { e.Evidence = "/var/lib/aiwe/ledger.jsonl is intact" }},
		{"an e-mail address in the evidence", func(e *Event) { e.Evidence = "reviewed by sungur@example.com, exit 0" }},
		{"an e-mail address in the session id", func(e *Event) { e.Actor.SessionID = "sungur@example.com" }},
		{"an e-mail address in the model", func(e *Event) { e.Actor.Model = "ops@example.org" }},
		{"a file URL in the evidence", func(e *Event) { e.Evidence = "opened file:///home/u/x, exit 0" }},
		{"a path in brackets in the evidence", func(e *Event) { e.Evidence = "exit 0 [/home/u/x]" }},
		{"a path after a redirect in the evidence", func(e *Event) { e.Evidence = "exit 0 log>/home/u/x" }},
		{"a path after a less-than in the evidence", func(e *Event) { e.Evidence = "exit 0 log</home/u/x" }},
		{"a path after a pipe in the evidence", func(e *Event) { e.Evidence = "exit 0 log|/home/u/x" }},
		{"a Windows path in brackets in the evidence", func(e *Event) { e.Evidence = `exit 0 [C:\Users\x]` }},
		{"an e-mail address with a non-ASCII domain", func(e *Event) { e.Evidence = "reviewed by ali@örnek.com.tr" }},
		{"an e-mail address with a non-ASCII local part and domain", func(e *Event) { e.Evidence = "reviewed by barış@örnek.com.tr" }},
		{"an e-mail address with a non-ASCII local part", func(e *Event) { e.Evidence = "reviewed by çağrı@example.com" }},
		{"an upper-case file URL in the evidence", func(e *Event) { e.Evidence = "opened FILE:///home/u/x, exit 0" }},
		{"a path in backticks (path regex: prefix allow-list)", func(e *Event) { e.Evidence = "see `/home/alice/aiwe`" }},
		{"a Windows path in backticks (path regex: prefix allow-list)", func(e *Event) { e.Evidence = "see `C:\\Users\\alice`" }},
		{"a path in braces (path regex: prefix allow-list)", func(e *Event) { e.Evidence = "see {/home/alice}" }},
		{"a path after a no-break space (path regex: ASCII-only space)", func(e *Event) { e.Evidence = "in\u00a0/home/alice" }},
		{"a UNC path written with slashes (path regex: no // branch)", func(e *Event) { e.Evidence = "//host/share/alice" }},
		{"a path after a key and a colon", func(e *Event) { e.Evidence = "cwd:/home/alice" }},
		{"a drive path after a key and a colon", func(e *Event) { e.Evidence = `cwd:C:\Users\alice` }},
		{"an e-mail address in NFD (mail regex: no \\p{M})", func(e *Event) { e.Evidence = "jose\u0301@example.com" }},
		{"an e-mail address in Devanagari (mail regex: no \\p{M})", func(e *Event) { e.Evidence = "user@उदाहरण.भारत" }},
		{"an e-mail address with a zero-width space (mail regex: \\p{Cf} kept)", func(e *Event) { e.Evidence = "alice\u200b@example.com" }},
		{"an e-mail address with a fullwidth at sign (mail regex: no at-sign mapping)", func(e *Event) { e.Evidence = "alice\uff20example.com" }},
		{"an e-mail address with a small at sign (mail regex: no at-sign mapping)", func(e *Event) { e.Evidence = "alice\ufe6bexample.com" }},
		{"an absolute path in the goal", func(e *Event) { e.Goal = "/home/sungur/goals/sk-06" }},
		{"an absolute path in a landed file", func(e *Event) {
			e.Files = []string{"internal/ledger/event.go", "/home/sungur/aiwe/internal/ledger/record.go"}
		}},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			e := ledgerTestEvent(EventVerdict)
			if strings.Contains(tc.name, "landed file") {
				e = ledgerTestEvent(EventLanded)
			}
			tc.build(&e)
			mustRefuse(t, e)
		})
	}
	t.Run("refuses a branch that is an e-mail address", func(t *testing.T) {
		e := ledgerTestEvent(EventPushed)
		e.Branch = "sungur@example.com"
		mustRefuse(t, e)
	})
	t.Run("refuses a line that holds a path on read", func(t *testing.T) {
		line := eventLine(t, ledgerTestEvent(EventVerdict))
		mustRefuseLine(t, replaceOnce(t, line, `exit 0 at 9c2f1ab`, `exit 0 in /home/sungur/aiwe`))
	})
	t.Run("refusal does not echo the personal data", func(t *testing.T) {
		e := ledgerTestEvent(EventVerdict)
		e.Evidence = "reviewed by sungur@example.com"
		err := Append(&bytes.Buffer{}, e)
		if err == nil {
			t.Fatalf("Append of an e-mail address = nil error, want a refusal")
		}
		if strings.Contains(err.Error(), "sungur@example.com") {
			t.Errorf("refusal %q repeats the e-mail address it refuses", err)
		}
	})
	t.Run("a refusal of a value outside a set does not echo the value", func(t *testing.T) {
		verdict := ledgerTestEvent(EventVerdict)
		verdict.Verdict = "alice@example.com"
		name := ledgerTestEvent(EventCreated)
		name.Name = "alice@example.com"
		for _, e := range []Event{verdict, name} {
			err := Append(&bytes.Buffer{}, e)
			if err == nil || strings.Contains(err.Error(), "alice@example.com") {
				t.Errorf("Append(%s event) error = %v, want a refusal that names the field, not the value", e.Name, err)
			}
		}
	})
	accepted := []struct {
		name  string
		build func(*Event)
	}{
		{"a URL in the evidence", func(e *Event) { e.Evidence = "https://github.com/sungurerdim/aiwe/pull/512 is green" }},
		{"a relative path in the evidence", func(e *Event) { e.Evidence = "go test ./internal/ledger/ exit 0" }},
		{"a package version in the evidence", func(e *Event) { e.Evidence = "x/tools@v0.30.1 is pinned" }},
		{"a time of day in the evidence", func(e *Event) { e.Evidence = "finished at 10:00:00, exit 0" }},
		{"a URL with a path in the evidence", func(e *Event) { e.Evidence = "see https://example.com/x" }},
		{"a URL in backticks in the evidence", func(e *Event) { e.Evidence = "see `https://example.com/x`" }},
		{"a short relative path in the evidence", func(e *Event) { e.Evidence = "a/b is green" }},
		{"a relative path in backticks in the evidence", func(e *Event) { e.Evidence = "see `internal/ledger/event.go`" }},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			e := ledgerTestEvent(EventVerdict)
			tc.build(&e)
			var buf bytes.Buffer
			if err := Append(&buf, e); err != nil {
				t.Errorf("Append(%s) error = %v, want nil", tc.name, err)
			}
		})
	}
}
