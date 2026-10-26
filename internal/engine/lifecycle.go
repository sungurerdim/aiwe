// Package engine runs one unit through the steps of SPEC §7.3: intake, claim, isolate, write, verify,
// collect, scope check and audit. Each step names its outcome, the auditor runs in its own run
// (DR-5), and at most two fix rounds run before the unit is quarantined (SPEC §7.4).
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/sungurerdim/aiwe/internal/gates"
	"github.com/sungurerdim/aiwe/internal/roles"
	"github.com/sungurerdim/aiwe/internal/tracker"
)

// The steps of SPEC §7.3 that one run walks, and the fix loop between them.
const (
	StepIntake  = "intake"
	StepClaim   = "claim"
	StepIsolate = "isolate"
	StepWrite   = "write"
	StepVerify  = "verify"
	StepCollect = "collect"
	StepScope   = "scope"
	StepAudit   = "audit"
	StepFix     = "fix"
)

// RunRecord is one role run the lifecycle dispatched: the prompt contract it received, the
// assignment entry it took and, for an auditor, its independence from the writing run.
type RunRecord struct {
	RunID        string
	Role         string
	Prompt       string
	Bytes        int
	Entry        roles.Pin
	Independence Independence
}

// LifecycleReport is what one unit run did: its steps, its role runs, the collected and audited tips
// and the outcome it reached.
type LifecycleReport struct {
	RunID      string
	Unit       int
	Steps      []StepReport
	Runs       []RunRecord
	Tip        string
	AuditedTip string
	FixRounds  int
	Outcome    StepOutcome
	// Leftover names what a stopped run could not release: its claim or its clone (DR-11).
	Leftover []string
}

// Claimer holds the one open run of a unit (SPEC §7.3 step 2, §6.6).
type Claimer interface {
	Take(ctx context.Context, unit int, runID string) error
	// Release drops the claim runID holds; a stopped run lets go of its unit (SPEC §7.3 step 3, DR-11).
	Release(ctx context.Context, unit int, runID string) error
}

// Isolator makes the run's own clone and scratch (SPEC §7.3 step 3, DR-10).
type Isolator interface {
	Isolate(ctx context.Context, spec CloneSpec) (*Clone, error)
	// Remove removes the run's own clone and scratch through core's owned removal (DR-11).
	Remove(ctx context.Context, clone *Clone) error
}

// Verifier runs the unit verify and returns its result (SPEC §7.3 step 6, DR-8).
type Verifier interface {
	Verify(ctx context.Context, dir string) (VerifyReport, error)
}

// Collector turns the run branch into a run ref and returns its tip (SPEC §7.3 step 7).
type Collector interface {
	Collect(ctx context.Context, clone *Clone) (string, error)
}

// Differ observes the run's diff and its stat from the base commit (SPEC §7.3 steps 8 and 10).
type Differ interface {
	Diff(ctx context.Context, base, tip string) (Diff, error)
	Stat(ctx context.Context, base, tip string) (string, error)
}

// LifecycleRequest is one unit run: the unit issue, the run it takes and the ports each step needs.
type LifecycleRequest struct {
	Repo       tracker.RepoID
	Unit       int
	RunID      string
	Base       string
	Canonical  string
	Branch     string
	Severity   string
	Criteria   []string
	Assignment Assignment
	Tracker    tracker.Tracker
	Claims     Claimer
	Isolator   Isolator
	Runs       *Dispatcher
	Verifier   Verifier
	Collector  Collector
	Differ     Differ
	NextRunID  func() string
}

// RunUnit runs one unit from intake to an audited tip, or stops with the named outcome of the step
// that stopped it. It returns an error only for a request it cannot run: a stopped run is a decided
// outcome in the report, never an error the caller must decode (SPEC §7.4, §5.3).
func RunUnit(ctx context.Context, req LifecycleRequest) (LifecycleReport, error) {
	if err := req.check(); err != nil {
		return LifecycleReport{}, err
	}
	run := &lifecycleRun{req: req, rep: LifecycleReport{RunID: req.RunID, Unit: req.Unit}}
	err := run.walk(ctx)
	if err != nil {
		run.release(ctx)
	}
	var stop stopError
	if errors.As(err, &stop) || err == nil {
		return run.rep, nil
	}
	return run.rep, err
}

// check refuses a request a step could not run, so a missing port is refused before intake (STD-11).
func (r LifecycleRequest) check() error {
	for _, port := range []struct {
		name string
		ok   bool
	}{
		{"tracker", r.Tracker != nil}, {"claim table", r.Claims != nil}, {"isolator", r.Isolator != nil},
		{"dispatcher", r.Runs != nil}, {"verifier", r.Verifier != nil}, {"collector", r.Collector != nil},
		{"diff", r.Differ != nil}, {"run id source", r.NextRunID != nil},
	} {
		if !port.ok {
			return fmt.Errorf("lifecycle: the request carries no %s", port.name)
		}
	}
	if r.Unit <= 0 || r.RunID == "" || r.Base == "" || r.Branch == "" {
		return fmt.Errorf("lifecycle: unit %d, run %q, base %q, branch %q: want a unit, a run, a base and a branch",
			r.Unit, r.RunID, r.Base, r.Branch)
	}
	return nil
}

// lifecycleRun is the state one RunUnit threads through its steps.
type lifecycleRun struct {
	req    LifecycleRequest
	rep    LifecycleReport
	unit   Unit
	body   string
	clone  *Clone
	writer roles.Pin
	verify VerifyReport
	// claimed is true once this run holds the claim of its unit, so a stop releases only its own.
	claimed bool
	// statNote says why the last fix round carries no diff stat.
	statNote string
}

// walk runs the steps in the §7.3 order and stops at the first one that cannot continue.
func (l *lifecycleRun) walk(ctx context.Context) error {
	for _, step := range []func(context.Context) error{l.intake, l.claim, l.isolate, l.write, l.reach} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// intake reads the unit issue through the tracker port and runs the intake gates (step 1).
func (l *lifecycleRun) intake(ctx context.Context) error {
	units, err := l.req.Tracker.ReadUnits(ctx, l.req.Repo)
	if err != nil {
		return l.stop(StepIntake, OutcomeEnvironment, err)
	}
	for _, issue := range units {
		if issue.Number != l.req.Unit {
			continue
		}
		unit, err := ParseUnit(issue.Body)
		if err != nil {
			return l.stop(StepIntake, OutcomeUnitInvalid, err)
		}
		l.unit, l.body = unit, issue.Body
		l.ok(StepIntake, OutcomeAccepted)
		return nil
	}
	return l.stop(StepIntake, OutcomeUnitInvalid, fmt.Errorf("the tracker returned no unit %d", l.req.Unit))
}

// claim takes the one open run of the unit (step 2).
func (l *lifecycleRun) claim(ctx context.Context) error {
	err := l.req.Claims.Take(ctx, l.req.Unit, l.req.RunID)
	if err == nil {
		l.claimed = true
		l.ok(StepClaim, OutcomeClaimed)
		return nil
	}
	var held *UnitClaimedError
	if errors.As(err, &held) {
		return l.stop(StepClaim, OutcomeAlreadyClaimed, err)
	}
	return l.stop(StepClaim, OutcomeEnvironment, err)
}

// isolate makes the run's own clone (step 3).
func (l *lifecycleRun) isolate(ctx context.Context) error {
	clone, err := l.req.Isolator.Isolate(ctx,
		CloneSpec{Canonical: l.req.Canonical, Base: l.req.Base, RunID: l.req.RunID})
	if err != nil {
		return l.stop(StepIsolate, OutcomeIsolationFailed, err)
	}
	if clone == nil || clone.Dir == "" {
		return l.stop(StepIsolate, OutcomeIsolationFailed, errors.New("the isolator returned no clone"))
	}
	l.clone = clone
	l.ok(StepIsolate, OutcomeIsolated)
	return nil
}

// write dispatches the writing roles in their §9.1 order: the test-author first on a risky unit.
func (l *lifecycleRun) write(ctx context.Context) error {
	for _, role := range ResolveRoles(l.unit.Files, l.req.Severity) {
		if role == roleAuditor {
			break
		}
		run, err := l.runRole(ctx, StepWrite, role, nil, l.body, "")
		if err != nil {
			return err
		}
		l.writer = run.record.Entry
	}
	l.ok(StepWrite, OutcomeWritten)
	return nil
}

// reach runs verify, collect, scope check and audit, and the fix rounds between them (steps 6-10).
func (l *lifecycleRun) reach(ctx context.Context) error {
	for fixRounds := 0; ; {
		green, err := l.verifyStep(ctx)
		if err != nil {
			return err
		}
		if !green {
			if err := l.redPath(ctx, fixRounds); err != nil {
				return err
			}
			fixRounds++
			l.rep.FixRounds = fixRounds
			continue
		}
		done, err := l.greenPath(ctx, fixRounds)
		if err != nil || done {
			return err
		}
		fixRounds++
		l.rep.FixRounds = fixRounds
	}
}

// redPath runs one fix round for a red verify, or quarantines the unit when the rounds are spent.
func (l *lifecycleRun) redPath(ctx context.Context, fixRounds int) error {
	if !FixRoundAllowed(fixRounds) {
		return l.quarantine("the verify stayed red after two fix rounds")
	}
	if err := l.collect(ctx); err != nil {
		return err
	}
	return l.fixRound(ctx, verifyDelta(l.verify, l.stat(ctx)))
}

// greenPath runs collect, scope check and audit for a green verify and reports whether the run is
// done; a red audit runs one fix round and reports false (steps 7-10).
func (l *lifecycleRun) greenPath(ctx context.Context, fixRounds int) (bool, error) {
	if err := l.collect(ctx); err != nil {
		return false, err
	}
	if err := l.scopeCheck(ctx); err != nil {
		return false, err
	}
	verdict, judgement, previous, err := l.audit(ctx)
	if err != nil {
		return false, err
	}
	if verdict == VerdictGreen {
		l.rep.AuditedTip, l.rep.Outcome = l.rep.Tip, OutcomeApproved
		return true, nil
	}
	if !FixRoundAllowed(fixRounds) {
		return false, l.quarantine("the audit stayed red after two fix rounds")
	}
	return false, l.fixRound(ctx, auditDelta(judgement, previous, l.stat(ctx)))
}

// verifyStep runs the unit verify (step 6) and reports whether it is green.
func (l *lifecycleRun) verifyStep(ctx context.Context) (bool, error) {
	report, err := l.req.Verifier.Verify(ctx, l.clone.Dir)
	if err != nil {
		return false, l.stop(StepVerify, OutcomeEnvironment, err)
	}
	l.verify = report
	if report.Result == gates.ResultGreen {
		l.ok(StepVerify, OutcomeGreen)
		return true, nil
	}
	l.ok(StepVerify, OutcomeRed)
	return false, nil
}

// collect turns the run branch into a run ref and keeps its tip (step 7).
func (l *lifecycleRun) collect(ctx context.Context) error {
	tip, err := l.req.Collector.Collect(ctx, l.clone)
	if err != nil {
		return l.stop(StepCollect, OutcomeEnvironment, err)
	}
	if tip == "" {
		return l.stop(StepCollect, OutcomeEnvironment, errors.New("the run branch holds no commit"))
	}
	l.rep.Tip = tip
	l.ok(StepCollect, OutcomeCollected)
	return nil
}

// scopeCheck compares the observed diff with the declared file set (step 8, DR-9).
func (l *lifecycleRun) scopeCheck(ctx context.Context) error {
	diff, err := l.req.Differ.Diff(ctx, l.req.Base, l.rep.Tip)
	if err != nil {
		return l.stop(StepScope, OutcomeEnvironment, err)
	}
	if err := CheckScope(l.unit.Files, diff); err != nil {
		return l.stop(StepScope, OutcomeScopeMismatch, err)
	}
	l.ok(StepScope, OutcomeInScope)
	return nil
}

// audit dispatches the auditor in its own run, independent of the writer (step 9, DR-5), and reads
// the verdict bound to that run.
func (l *lifecycleRun) audit(ctx context.Context) (Verdict, roles.Judgement, string, error) {
	entry, independence, err := PickAuditor(l.req.Assignment[roleAuditor], l.writer)
	if err != nil {
		return "", roles.Judgement{}, "", l.stop(StepAudit, OutcomeNoCapacity, err)
	}
	run, err := l.runRole(ctx, StepAudit, roleAuditor, &entry, l.body, "")
	if err != nil {
		return "", roles.Judgement{}, "", err
	}
	l.rep.Runs[len(l.rep.Runs)-1].Independence = independence
	verdict, judgement, err := ReadVerdict(run.record.RunID, run.report.Return, l.req.Criteria)
	if err != nil {
		return "", roles.Judgement{}, "", l.stop(StepAudit, OutcomeUnverifiedFinding, err)
	}
	if verdict == VerdictRed && !judgement.FixRound {
		return "", roles.Judgement{}, "", l.stop(StepAudit, OutcomeUnverifiedFinding,
			errors.New("the RED verdict cites no done criterion and no rule"))
	}
	if verdict == VerdictGreen {
		l.ok(StepAudit, OutcomeApproved)
	} else {
		l.ok(StepAudit, OutcomeReturned)
	}
	return verdict, judgement, run.report.Return, nil
}

// fixRound sends one engineer run whose task is the FIX delta only (step 10).
func (l *lifecycleRun) fixRound(ctx context.Context, delta FixDelta) error {
	entry, err := FirstEntry(l.req.Assignment, roleEngineer)
	if err != nil {
		return l.stop(StepFix, OutcomeNoCapacity, err)
	}
	if _, err := l.runRole(ctx, StepFix, roleEngineer, &entry, "", delta.Text()); err != nil {
		return err
	}
	l.writer = entry
	l.rep.Steps = append(l.rep.Steps, StepReport{Step: StepFix, Outcome: OutcomeFixed, Detail: l.statNote})
	return nil
}

// stat returns the diff stat for a fix round; an unreadable stat leaves the delta without it instead
// of failing a run that can still be fixed, and the fix step names why (SPEC §7.3 step 10).
func (l *lifecycleRun) stat(ctx context.Context) string {
	stat, err := l.req.Differ.Stat(ctx, l.req.Base, l.rep.Tip)
	l.statNote = ""
	if err != nil {
		l.statNote = "the fix task carries no diff stat: " + err.Error()
		return ""
	}
	return stat
}
