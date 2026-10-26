// Package engine accepts the one record a finished run hands to integration: the tip an auditor
// approved, or the named outcome that stopped the run before an audited tip existed (SPEC §7.3
// step 11, DR-14, DR-6). It also owns the outcome word of one step: every step says what it did, or
// which §7.4 outcome stopped it (DR-1).
package engine

import (
	"context"
	"strconv"

	"github.com/sungurerdim/aiwe/internal/core"
)

// StepOutcome names what one step decided: what it did when it continued, or the §7.4 outcome that
// stopped the run. A stop is never silent (DR-1).
type StepOutcome string

const (
	OutcomeAccepted          StepOutcome = "accepted"
	OutcomeClaimed           StepOutcome = "claimed"
	OutcomeIsolated          StepOutcome = "isolated"
	OutcomeWritten           StepOutcome = "written"
	OutcomeFixed             StepOutcome = "fixed"
	OutcomeGreen             StepOutcome = "green"
	OutcomeRed               StepOutcome = "red"
	OutcomeCollected         StepOutcome = "collected"
	OutcomeInScope           StepOutcome = "in_scope"
	OutcomeApproved          StepOutcome = "approved"
	OutcomeReturned          StepOutcome = "returned"
	OutcomeUnitInvalid       StepOutcome = "unit_invalid"
	OutcomeAlreadyClaimed    StepOutcome = "already_claimed"
	OutcomeIsolationFailed   StepOutcome = "isolation_failed"
	OutcomePromptTooLarge    StepOutcome = "prompt_too_large"
	OutcomeScopeMismatch     StepOutcome = "scope_mismatch"
	OutcomeUnverifiedFinding StepOutcome = "unverified_finding"
	OutcomeNoCapacity        StepOutcome = "no_capacity"
	OutcomeEnvironment       StepOutcome = "environment"
	OutcomeRoleUnresolved    StepOutcome = "role_unresolved"
	OutcomeQuarantined       StepOutcome = "quarantined"
)

// StepReport is one step and the outcome it ended with, with the cause of a stop.
type StepReport struct {
	Step    string
	Outcome StepOutcome
	Detail  string
}

// stopError stops the walk: the step appended its report and the run's named outcome.
type stopError struct {
	Outcome StepOutcome
	Detail  string
}

// Error renders the stop.
func (e stopError) Error() string { return string(e.Outcome) + ": " + e.Detail }

// ok records a step that continued.
func (l *lifecycleRun) ok(step string, outcome StepOutcome) {
	l.rep.Steps = append(l.rep.Steps, StepReport{Step: step, Outcome: outcome})
}

// stop records the step that stopped the run and returns the stop signal.
func (l *lifecycleRun) stop(step string, outcome StepOutcome, cause error) error {
	detail := string(outcome)
	if cause != nil {
		detail = cause.Error()
	}
	l.rep.Steps = append(l.rep.Steps, StepReport{Step: step, Outcome: outcome, Detail: detail})
	l.rep.Outcome = outcome
	return stopError{Outcome: outcome, Detail: detail}
}

// release gives back what a stopped run holds: its clone, then its claim (SPEC §7.3 step 3, DR-11).
// It runs without the caller's cancellation, so a cancelled run still lets go. A failure is named in
// the report's Leftover; the outcome the run reached stays.
func (l *lifecycleRun) release(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	if l.clone != nil {
		if err := l.req.Isolator.Remove(ctx, l.clone); err != nil {
			l.rep.Leftover = append(l.rep.Leftover, "clone "+l.clone.Dir+": "+err.Error())
		}
	}
	if l.claimed {
		if err := l.req.Claims.Release(ctx, l.req.Unit, l.req.RunID); err != nil {
			l.rep.Leftover = append(l.rep.Leftover, "claim of unit "+strconv.Itoa(l.req.Unit)+": "+err.Error())
		}
	}
}

// quarantine stops the run with the quarantined outcome after its last fix round (§7.4).
func (l *lifecycleRun) quarantine(detail string) error {
	l.rep.Steps = append(l.rep.Steps, StepReport{Step: StepFix, Outcome: OutcomeQuarantined, Detail: detail})
	l.rep.Outcome = OutcomeQuarantined
	return stopError{Outcome: OutcomeQuarantined, Detail: detail}
}

// Accepted is the one record of a finished run: the audited tip, or the outcome that stopped it.
type Accepted struct {
	RunID      string
	Unit       int
	Outcome    StepOutcome
	AuditedTip string
	FixRounds  int
}

// Accept reads the lifecycle report. A run that reached an audited tip accepts exactly that tip; a
// report that skipped a step is incomplete, an approval of another tip is unaudited_commits, and a
// stopped run accepts no tip and names its outcome (DR-6, DR-14).
func Accept(rep LifecycleReport) (Accepted, error) {
	acc := Accepted{RunID: rep.RunID, Unit: rep.Unit, Outcome: rep.Outcome, FixRounds: rep.FixRounds}
	if rep.Outcome == "" {
		return acc, incomplete("the report names no outcome")
	}
	if rep.Outcome != OutcomeApproved {
		return acc, nil
	}
	if rep.AuditedTip == "" {
		return acc, incomplete("the audit approved no tip")
	}
	if rep.AuditedTip != rep.Tip {
		return acc, &core.Error{Outcome: "unaudited_commits",
			Expected: "the collected tip " + rep.Tip,
			Found:    "an approval of " + rep.AuditedTip,
			Next:     "audit the run branch again, then integrate the tip the auditor audited"}
	}
	for _, step := range acceptanceSteps() {
		if !carriesStep(rep, step) {
			return acc, incomplete("the report skips the step " + step)
		}
	}
	acc.AuditedTip = rep.AuditedTip
	return acc, nil
}

// acceptanceSteps are the steps a report must carry before it accepts a tip (SPEC §7.3, DR-6).
func acceptanceSteps() []string {
	return []string{StepIntake, StepClaim, StepIsolate, StepWrite, StepVerify,
		StepCollect, StepScope, StepAudit}
}

// incomplete returns the DR-6 refusal: a skipped step makes the result incomplete, never green.
func incomplete(detail string) error {
	return &core.Error{Outcome: "incomplete", Expected: "every step of the run", Found: detail,
		Next: "run the missing step, then accept the unit again"}
}

// carriesStep reports whether the report carries the step.
func carriesStep(rep LifecycleReport, step string) bool {
	for _, report := range rep.Steps {
		if report.Step == step {
			return true
		}
	}
	return false
}
