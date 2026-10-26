package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/gates"
)

// greenReport returns the report of a run that reached an audited tip: every §7.3 step ran and the
// audit approved the collected tip.
func greenReport() LifecycleReport {
	return LifecycleReport{
		RunID: "01J8Z3K7Q5W0H8N4P2R6T1V9Y0", Unit: lifecycleTestUnit,
		Steps: []StepReport{
			{Step: StepIntake, Outcome: OutcomeAccepted},
			{Step: StepClaim, Outcome: OutcomeClaimed},
			{Step: StepIsolate, Outcome: OutcomeIsolated},
			{Step: StepWrite, Outcome: OutcomeWritten},
			{Step: StepVerify, Outcome: OutcomeGreen},
			{Step: StepCollect, Outcome: OutcomeCollected},
			{Step: StepScope, Outcome: OutcomeInScope},
			{Step: StepAudit, Outcome: OutcomeApproved},
		},
		Tip:        lifecycleTestTip,
		AuditedTip: lifecycleTestTip,
		Outcome:    OutcomeApproved,
	}
}

// TestAccept covers the one record a run hands to integration (SPEC §7.3, DR-14): a unit reaches an
// audited tip or a named outcome, never a bare "done", and a missing step is incomplete.
func TestAccept(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		prepare     func(*LifecycleReport)
		wantTip     string
		wantOutcome StepOutcome
		wantErr     string
	}{
		{
			name:        "an audited tip is accepted",
			prepare:     func(*LifecycleReport) {},
			wantTip:     lifecycleTestTip,
			wantOutcome: OutcomeApproved,
		},
		{
			name: "a quarantined unit is a named outcome with no tip",
			prepare: func(rep *LifecycleReport) {
				rep.Outcome = OutcomeQuarantined
				rep.AuditedTip = ""
				rep.Steps = append(rep.Steps, StepReport{Step: StepFix, Outcome: OutcomeQuarantined})
			},
			wantOutcome: OutcomeQuarantined,
		},
		{
			name: "a report that skipped the scope check accepts no tip",
			prepare: func(rep *LifecycleReport) {
				rep.Steps = rep.Steps[:len(rep.Steps)-2]
			},
			wantErr: "incomplete",
		},
		{
			name: "an approval of a tip another commit replaced is unaudited",
			prepare: func(rep *LifecycleReport) {
				rep.AuditedTip = "01J8Z3K7Q5W0H8N4P2R6T1V9Z8"
			},
			wantErr: "unaudited_commits",
		},
		{
			name: "an approval that names no tip accepts nothing",
			prepare: func(rep *LifecycleReport) {
				rep.AuditedTip = ""
			},
			wantErr: "incomplete",
		},
		{
			name: "a report that names no outcome is refused, not accepted",
			prepare: func(rep *LifecycleReport) {
				rep.Outcome = ""
				rep.AuditedTip = ""
			},
			wantErr: "incomplete",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := greenReport()
			tc.prepare(&rep)
			acc, err := Accept(rep)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("accepted a report that must be refused with %s", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("refusal = %v, want it to name %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Accept: %v", err)
			}
			if acc.AuditedTip != tc.wantTip || acc.Outcome != tc.wantOutcome {
				t.Fatalf("accepted = %q/%q, want %q/%q", acc.AuditedTip, acc.Outcome, tc.wantTip, tc.wantOutcome)
			}
			if tc.wantTip == "" && acc.AuditedTip != "" {
				t.Fatalf("a stopped run accepted the tip %q", acc.AuditedTip)
			}
		})
	}
}

// assertSteps requires the report to carry exactly steps and outcomes, in order, each outcome named.
func assertSteps(t *testing.T, rep LifecycleReport, steps []string, outcomes []StepOutcome) {
	t.Helper()
	if len(rep.Steps) != len(steps) {
		t.Fatalf("report has %d steps, want %d: %+v", len(rep.Steps), len(steps), rep.Steps)
	}
	for i := range steps {
		if rep.Steps[i].Step != steps[i] || rep.Steps[i].Outcome != outcomes[i] {
			t.Fatalf("step %d = %s/%s, want %s/%s", i, rep.Steps[i].Step, rep.Steps[i].Outcome, steps[i], outcomes[i])
		}
	}
}

// laterSteps returns the steps after step in the §7.3 order.
func laterSteps(step string) []string {
	order := []string{StepIntake, StepClaim, StepIsolate, StepWrite, StepVerify, StepCollect, StepScope, StepAudit}
	for i, id := range order {
		if id == step {
			return order[i+1:]
		}
	}
	return nil
}

// hasStep reports whether the report carries the step.
func hasStep(rep LifecycleReport, step string) bool {
	for _, s := range rep.Steps {
		if s.Step == step {
			return true
		}
	}
	return false
}

// TestLifecycleReleasesWhatARunHolds covers DR-11 and SPEC §7.3 step 3: a run that stops after the
// claim releases it, and a run that stops after its clone exists removes the clone; a failed release
// is named in the report instead of lost, and does not change the outcome.
//
// rule: DR-11
func TestLifecycleReleasesWhatARunHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		prepare     func(*lifecycleFixture)
		wantOutcome StepOutcome
		wantRemoved int
	}{
		{"the clone cannot be made", func(f *lifecycleFixture) {
			f.isolator.err = errors.New("the clone could not be made")
		}, OutcomeIsolationFailed, 0},
		{"the diff leaves the file set", func(f *lifecycleFixture) {
			f.differ.paths = []string{"internal/engine/outside.go"}
		}, OutcomeScopeMismatch, 1},
		{"the verify stays red", func(f *lifecycleFixture) {
			f.verifier.results = []gates.Result{gates.ResultRed, gates.ResultRed, gates.ResultRed}
		}, OutcomeQuarantined, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLifecycleFixture(t, "medium")
			tc.prepare(f)
			rep, err := RunUnit(t.Context(), f.request())
			if err != nil {
				t.Fatalf("RunUnit: %v", err)
			}
			if rep.Outcome != tc.wantOutcome {
				t.Fatalf("report outcome = %q, want %q", rep.Outcome, tc.wantOutcome)
			}
			if want := strconv.Itoa(lifecycleTestUnit) + "/" + rep.RunID; len(f.claimer.released) != 1 ||
				f.claimer.released[0] != want {
				t.Fatalf("released claims = %v, want [%s]", f.claimer.released, want)
			}
			if len(f.isolator.removed) != tc.wantRemoved {
				t.Fatalf("removed %d clones, want %d", len(f.isolator.removed), tc.wantRemoved)
			}
		})
	}

	t.Run("a failed release is named in the report", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		f.differ.paths = []string{"internal/engine/outside.go"}
		f.claimer.releaseErr = errors.New("the claim table is not writable")
		f.isolator.removeErr = errors.New("the clone is locked")
		rep, err := RunUnit(t.Context(), f.request())
		if err != nil {
			t.Fatalf("RunUnit: %v", err)
		}
		if rep.Outcome != OutcomeScopeMismatch {
			t.Fatalf("report outcome = %q, want scope_mismatch", rep.Outcome)
		}
		all := strings.Join(rep.Leftover, "\n")
		if !strings.Contains(all, "not writable") || !strings.Contains(all, "is locked") {
			t.Fatalf("the report names %q, want the failed release and the failed removal", all)
		}
	})
}

// TestLifecycleReleasesAfterTheRunContextIsCancelled covers DR-11: the owner stops the run by
// cancelling its context, and the claim and the clone are still given back, because the release does
// not run under the cancelled context.
//
// rule: DR-11
func TestLifecycleReleasesAfterTheRunContextIsCancelled(t *testing.T) {
	t.Parallel()
	f := newLifecycleFixture(t, "medium")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.differ.paths = []string{"internal/engine/outside.go"}
	f.runner.report = func(DispatchRequest) DispatchReport {
		cancel()
		return roleReturn()
	}
	rep, err := RunUnit(ctx, f.request())
	if err != nil {
		t.Fatalf("RunUnit: %v", err)
	}
	if rep.Outcome != OutcomeScopeMismatch {
		t.Fatalf("report outcome = %q, want scope_mismatch", rep.Outcome)
	}
	if len(f.claimer.released) != 1 || len(f.isolator.removed) != 1 {
		t.Fatalf("released %d claims and removed %d clones, want 1 and 1",
			len(f.claimer.released), len(f.isolator.removed))
	}
	if f.claimer.releaseCtx != nil || f.isolator.removeCtx != nil {
		t.Fatalf("the release saw a cancelled context: claim %v, clone %v", f.claimer.releaseCtx, f.isolator.removeCtx)
	}
}
