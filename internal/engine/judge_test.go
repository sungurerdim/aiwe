package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/gates"
	"github.com/sungurerdim/aiwe/internal/roles"
)

// TestReadVerdict covers the §9.3 verdict: it counts only bound to the run that wrote it (DR-5),
// and a RED that cites a criterion or a DR opens one fix round (K8).
func TestReadVerdict(t *testing.T) {
	t.Parallel()
	const runID = "01J8Z3K7Q5W0H8N4P2R6T1V9Y7"
	tests := []struct {
		name    string
		text    string
		want    Verdict
		wantFix bool
		wantErr bool
	}{
		{
			name: "a GREEN verdict bound to its run",
			text: "VERDICT: GREEN bound to RunID " + runID,
			want: VerdictGreen,
		},
		{
			name: "a one-line GREEN return ending in CRLF is approved, not read as empty",
			text: "VERDICT: GREEN bound to RunID " + runID + "\r\n",
			want: VerdictGreen,
		},
		{
			name: "a RED verdict that cites a DR opens a fix round",
			text: "VERDICT: RED bound to RunID " + runID +
				"\nfinding: internal/engine/lifecycle.go:42 · DR-12 · a check was removed · a regression may land · must",
			want: VerdictRed, wantFix: true,
		},
		{
			name: "a RED verdict that cites a done criterion opens a fix round",
			text: "VERDICT: RED bound to RunID " + runID + "\nThe done criterion a-done-criterion fails.",
			want: VerdictRed, wantFix: true,
		},
		{
			name:    "a verdict bound to another run is refused",
			text:    "VERDICT: GREEN bound to RunID another-run",
			wantErr: true,
		},
		{
			name:    "a verdict that binds no run is refused",
			text:    "VERDICT: RED",
			wantErr: true,
		},
		{
			name:    "a return with no verdict is refused",
			text:    "IMPLEMENTED: the change is on the run branch",
			wantErr: true,
		},
		{
			name:    "two verdict lines are refused",
			text:    "VERDICT: GREEN bound to RunID " + runID + "\nVERDICT: RED bound to RunID " + runID,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict, judgement, err := ReadVerdict(runID, tc.text, []string{"a-done-criterion"})
			if tc.wantErr {
				if err == nil {
					t.Fatal("accepted a return that carries no verdict of its own run")
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadVerdict: %v", err)
			}
			if verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", verdict, tc.want)
			}
			if judgement.FixRound != tc.wantFix {
				t.Fatalf("fix round = %v, want %v", judgement.FixRound, tc.wantFix)
			}
		})
	}

	t.Run("the refusal names both runs", func(t *testing.T) {
		t.Parallel()
		_, _, err := ReadVerdict(runID, "VERDICT: GREEN bound to RunID another-run", nil)
		var unbound *UnboundVerdictError
		if !errors.As(err, &unbound) {
			t.Fatalf("refusal is %T, want *UnboundVerdictError", err)
		}
		if unbound.RunID != runID || unbound.BoundTo != "another-run" {
			t.Fatalf("refusal names %q/%q, want %q/%q", unbound.RunID, unbound.BoundTo, runID, "another-run")
		}
	})
}

// TestFixDeltaText covers the §7.3 step 10 fix task: the failing items, the diff stat, the RED
// findings with path:line and the previous return cut to the §9.3 limit, and the unit text never.
func TestFixDeltaText(t *testing.T) {
	t.Parallel()
	delta := FixDelta{
		Failing:  []string{"the verify test exited 1"},
		DiffStat: "internal/engine/lifecycle.go | 12 ++++++------",
		Findings: []roles.Finding{{
			Location: "internal/engine/lifecycle.go:42", Rule: "DR-12",
			Evidence: "a check was removed", Impact: "a regression may land", Class: "must",
		}},
		Previous: strings.Repeat("the previous return line\n", 30),
	}
	text := delta.Text()
	for _, want := range []string{"the verify test exited 1", "12 ++++++------",
		"internal/engine/lifecycle.go:42", "DR-12"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fix task does not carry %q:\n%s", want, text)
		}
	}
	const marker = "Previous return:\n"
	at := strings.Index(text, marker)
	if at < 0 {
		t.Fatalf("fix task carries no previous return:\n%s", text)
	}
	lines := strings.Split(strings.TrimSpace(text[at+len(marker):]), "\n")
	if len(lines) > fixReturnLines {
		t.Fatalf("the fix task carries %d lines of the previous return, want at most %d", len(lines), fixReturnLines)
	}
}

// TestFixRoundAllowed covers the §7.3 step 10 ceiling: two fix rounds at most, then quarantined.
func TestFixRoundAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		done int
		want bool
	}{
		{0, true}, {1, true}, {2, false}, {3, false},
	}
	for _, tc := range tests {
		if got := FixRoundAllowed(tc.done); got != tc.want {
			t.Fatalf("FixRoundAllowed(%d) = %v, want %v", tc.done, got, tc.want)
		}
	}
}

// TestLifecycleFixesAtMostTwiceThenQuarantines covers §7.3 step 10 and §7.4: a red verify or a red
// audit opens a fix round, at most two, and a unit that stays red is quarantined, never green.
func TestLifecycleFixesAtMostTwiceThenQuarantines(t *testing.T) {
	t.Parallel()

	t.Run("an audit that stays red is quarantined after two fix rounds", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		f.runner.report = func(req DispatchRequest) DispatchReport {
			if req.Role == roleAuditor {
				return DispatchReport{Outcome: "final_answer",
					Return: "VERDICT: RED bound to RunID " + req.RunID +
						"\nfinding: internal/engine/lifecycle.go:42 · DR-12 · a check was removed · a regression may land · must"}
			}
			return roleReturn()
		}
		rep, err := RunUnit(t.Context(), f.request())
		if err != nil {
			t.Fatalf("RunUnit: %v", err)
		}
		if rep.Outcome != OutcomeQuarantined || rep.FixRounds != maxFixRounds {
			t.Fatalf("report = %q with %d fix rounds, want quarantined with %d", rep.Outcome, rep.FixRounds, maxFixRounds)
		}
		if got := countRuns(rep, roleEngineer); got != 1+maxFixRounds {
			t.Fatalf("the engineer ran %d times, want the write and %d fix rounds", got, maxFixRounds)
		}
		if got := countRuns(rep, roleAuditor); got != 1+maxFixRounds {
			t.Fatalf("the auditor ran %d times, want one audit per round", got)
		}
		last := rep.Steps[len(rep.Steps)-1]
		if last.Step != StepFix || last.Outcome != OutcomeQuarantined {
			t.Fatalf("last step = %s/%s, want fix/quarantined", last.Step, last.Outcome)
		}
	})

	t.Run("a verify that stays red is quarantined after two fix rounds", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		f.verifier.results = []gates.Result{gates.ResultRed, gates.ResultRed, gates.ResultRed}
		rep, err := RunUnit(t.Context(), f.request())
		if err != nil {
			t.Fatalf("RunUnit: %v", err)
		}
		if rep.Outcome != OutcomeQuarantined || rep.FixRounds != maxFixRounds {
			t.Fatalf("report = %q with %d fix rounds, want quarantined with %d", rep.Outcome, rep.FixRounds, maxFixRounds)
		}
		if got := countRuns(rep, roleEngineer); got != 1+maxFixRounds {
			t.Fatalf("the engineer ran %d times, want the write and %d fix rounds", got, maxFixRounds)
		}
		if len(f.verifier.dirs) != 1+maxFixRounds {
			t.Fatalf("the verify ran %d times, want one per round", len(f.verifier.dirs))
		}
	})
}

// TestFixRoundTaskIsTheDeltaOnly covers §7.3 step 10: a fix round carries the FIX delta of the red
// record only, behind the same card, RULES and SCOPE prefix the writer's prompt had, so the
// prefix cache hits and the fix task is not the unit again.
func TestFixRoundTaskIsTheDeltaOnly(t *testing.T) {
	t.Parallel()
	f := newLifecycleFixture(t, "medium")
	audits := 0
	f.runner.report = func(req DispatchRequest) DispatchReport {
		if req.Role == roleAuditor {
			audits++
			if audits == 1 {
				return DispatchReport{Outcome: "final_answer",
					Return: "VERDICT: RED bound to RunID " + req.RunID +
						"\nfinding: internal/engine/lifecycle.go:42 · DR-12 · a check was removed · a regression may land · must"}
			}
			return boundVerdict(req, "GREEN")
		}
		return roleReturn()
	}
	f.differ.stat = "internal/engine/lifecycle.go | 12 ++++++------"
	rep, err := RunUnit(t.Context(), f.request())
	if err != nil {
		t.Fatalf("RunUnit: %v", err)
	}
	if rep.Outcome != OutcomeApproved || rep.FixRounds != 1 {
		t.Fatalf("report = %q with %d fix rounds, want approved after one fix round", rep.Outcome, rep.FixRounds)
	}
	if len(rep.Runs) != 4 {
		t.Fatalf("the run dispatched %d role runs, want write, audit, fix and audit: %+v", len(rep.Runs), rep.Runs)
	}
	writer, fixRun := rep.Runs[0], rep.Runs[2]
	if fixRun.Role != roleEngineer {
		t.Fatalf("fix run role = %q, want %q", fixRun.Role, roleEngineer)
	}
	if headingIndex(fixRun.Prompt, "FIX") < 0 {
		t.Fatal("the fix prompt carries no FIX section")
	}
	if strings.Contains(fixRun.Prompt, "The engine has no lifecycle.") {
		t.Fatal("the fix prompt carries the unit text instead of the delta only")
	}
	if !strings.Contains(fixRun.Prompt, "internal/engine/lifecycle.go:42") ||
		!strings.Contains(fixRun.Prompt, "12 ++++++------") {
		t.Fatalf("the fix prompt does not carry the red findings and the diff stat:\n%s", fixRun.Prompt)
	}
	if taskPrefix(writer.Prompt) != taskPrefix(fixRun.Prompt) {
		t.Fatal("the fix prompt does not keep the writer's card, RULES and SCOPE prefix")
	}
}

// countRuns returns how many dispatched runs took the role.
func countRuns(rep LifecycleReport, role string) int {
	n := 0
	for _, run := range rep.Runs {
		if run.Role == role {
			n++
		}
	}
	return n
}

// TestVerifyFixTaskLabelsTheVerifyOutput covers SPEC §7.3 step 10: the output tail of a red verify is
// sent as the verify output, never labelled as the previous return of an earlier run.
func TestVerifyFixTaskLabelsTheVerifyOutput(t *testing.T) {
	t.Parallel()
	report := VerifyReport{Step: "test", ExitCode: 1, Tail: "--- FAIL: TestLifecycle"}
	text := verifyDelta(report, "internal/engine/lifecycle.go | 2 +-").Text()
	if !strings.Contains(text, "Verify output:\n--- FAIL: TestLifecycle") {
		t.Fatalf("the fix task does not label the verify output:\n%s", text)
	}
	if strings.Contains(text, "Previous return:") {
		t.Fatalf("the fix task calls the verify output a previous return:\n%s", text)
	}
}

// TestFixRoundNamesAnUnreadableDiffStat covers the fix round without its diff stat: the run goes on,
// and the fix step says why the delta carries no stat instead of dropping the error.
func TestFixRoundNamesAnUnreadableDiffStat(t *testing.T) {
	t.Parallel()
	f := newLifecycleFixture(t, "medium")
	f.verifier.results = []gates.Result{gates.ResultRed}
	f.differ.statErr = errors.New("git diff --stat failed")
	rep, err := RunUnit(t.Context(), f.request())
	if err != nil {
		t.Fatalf("RunUnit: %v", err)
	}
	if rep.Outcome != OutcomeApproved {
		t.Fatalf("report outcome = %q, want approved after the fix round", rep.Outcome)
	}
	for _, step := range rep.Steps {
		if step.Step == StepFix {
			if !strings.Contains(step.Detail, "git diff --stat failed") {
				t.Fatalf("fix step detail = %q, want the stat error named", step.Detail)
			}
			return
		}
	}
	t.Fatalf("the run ran no fix step: %+v", rep.Steps)
}

// FuzzParseVerdict catches a reader of the untrusted auditor text that panics, or that accepts a
// return that is not exactly one GREEN or RED VERDICT line (STD-19, DR-5).
func FuzzParseVerdict(f *testing.F) {
	f.Add("VERDICT: GREEN bound to RunID r1")
	f.Add("VERDICT: RED\r\nfinding: a.go:1 · DR-1 · e · i · must")
	f.Add("VERDICT: GREEN\nVERDICT: RED")
	f.Add("VERDICT: GREEN bound to RunID ")
	f.Add("")
	f.Fuzz(func(t *testing.T, text string) {
		verdict, bound, err := parseVerdict(text)
		if err != nil {
			return
		}
		if verdict != VerdictGreen && verdict != VerdictRed {
			t.Fatalf("parseVerdict(%q) accepted the word %q", text, verdict)
		}
		if bound != strings.TrimSpace(bound) {
			t.Fatalf("parseVerdict(%q) bound %q with surrounding space", text, bound)
		}
		lines := 0
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "VERDICT:") {
				lines++
			}
		}
		if lines != 1 {
			t.Fatalf("parseVerdict(%q) accepted %d VERDICT lines, want 1", text, lines)
		}
		_, _, _ = ReadVerdict(bound, text, nil)
	})
}
