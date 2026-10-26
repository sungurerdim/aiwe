package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
	"github.com/sungurerdim/aiwe/internal/roles"
	"github.com/sungurerdim/aiwe/internal/tracker"
	"github.com/sungurerdim/aiwe/internal/tracker/trackertest"
)

// lifecycleTestUnit is the number of the fixture unit, and lifecycleTestTip the tip its run
// collects from the run branch.
const (
	lifecycleTestUnit = 40
	lifecycleTestTip  = "01J8Z3K7Q5W0H8N4P2R6T1V9Z9"
)

// lifecycleTestFileSet is the file set of the fixture unit: two engine files of the lifecycle.
func lifecycleTestFileSet() []string {
	return []string{"internal/engine/lifecycle.go", "internal/engine/dispatch.go"}
}

// lifecycleTestBody is the §8.4 body of the fixture unit: the file set of Scope In, the verify line
// and the To do test item the intake parser requires.
func lifecycleTestBody(files []string) string {
	var b strings.Builder
	b.WriteString("## Problem\nSeverity: high · Affected: system\nThe engine has no lifecycle.\n\n")
	b.WriteString("## Scope\nIn:\n")
	for _, path := range files {
		b.WriteString("- " + path + "\n")
	}
	b.WriteString("\n## To do\n")
	b.WriteString("- The lifecycle reaches an audited tip — verify: go test ./internal/engine/... → exit 0\n")
	b.WriteString("\n## Verification\ngo test ./internal/engine/... → exit 0\n")
	return b.String()
}

// lifecycleTestRepo is the repository of the fixture unit.
func lifecycleTestRepo() tracker.RepoID {
	return tracker.RepoID{Owner: "sungurerdim", Name: core.Product}
}

// boundVerdict is the return of a fake auditor run: its VERDICT line binds the run that wrote it,
// as the §9.3 return format and DR-5 require.
func boundVerdict(req DispatchRequest, word string) DispatchReport {
	return DispatchReport{Outcome: "final_answer",
		Return: "VERDICT: " + word + " bound to RunID " + req.RunID}
}

// roleReturn is the return of a fake writing run.
func roleReturn() DispatchReport {
	return DispatchReport{Outcome: "final_answer", Return: "IMPLEMENTED: the change is on the run branch"}
}

// fakeClaimer is the claim port of a test: it records the unit and returns its configured error.
type fakeClaimer struct {
	err        error
	releaseErr error
	taken      []string
	released   []string
	releaseCtx error // the context error the release saw
}

// Release records the release and returns the configured error.
func (f *fakeClaimer) Release(ctx context.Context, unit int, runID string) error {
	f.releaseCtx = ctx.Err()
	f.released = append(f.released, strconv.Itoa(unit)+"/"+runID)
	return f.releaseErr
}

// Take records the claim and returns the configured error.
func (f *fakeClaimer) Take(_ context.Context, unit int, runID string) error {
	f.taken = append(f.taken, strconv.Itoa(unit)+"/"+runID)
	return f.err
}

// fakeIsolator is the isolation port of a test: it returns its configured error or a fake clone.
type fakeIsolator struct {
	err       error
	removeErr error
	spec      CloneSpec
	removed   []*Clone
	removeCtx error // the context error the removal saw
}

// Remove records the clone it was asked to remove and returns the configured error.
func (f *fakeIsolator) Remove(ctx context.Context, clone *Clone) error {
	f.removeCtx = ctx.Err()
	f.removed = append(f.removed, clone)
	return f.removeErr
}

// Isolate records the clone spec and returns the fake clone.
func (f *fakeIsolator) Isolate(_ context.Context, spec CloneSpec) (*Clone, error) {
	f.spec = spec
	if f.err != nil {
		return nil, f.err
	}
	return &Clone{RunID: spec.RunID, Base: spec.Base, Dir: "clone"}, nil
}

// fakeRunner is a fake provider adapter: it records each dispatched run and answers from report.
type fakeRunner struct {
	id     string
	err    error
	report func(DispatchRequest) DispatchReport
	runs   []DispatchRequest
}

// ID returns the source id the dispatcher routes by.
func (f *fakeRunner) ID() string { return f.id }

// Run records the request and answers with the configured report.
func (f *fakeRunner) Run(_ context.Context, req DispatchRequest) (DispatchReport, error) {
	if f.err != nil {
		return DispatchReport{}, f.err
	}
	f.runs = append(f.runs, req)
	if f.report == nil {
		return roleReturn(), nil
	}
	return f.report(req), nil
}

// fakeVerifier is the verify port of a test: it returns the configured results in order and green
// once the list is spent.
type fakeVerifier struct {
	results []gates.Result
	err     error
	dirs    []string
}

// Verify records the clone it ran in and returns the next configured result.
func (f *fakeVerifier) Verify(_ context.Context, dir string) (VerifyReport, error) {
	f.dirs = append(f.dirs, dir)
	if f.err != nil {
		return VerifyReport{}, f.err
	}
	result := gates.ResultGreen
	if len(f.results) > 0 {
		result = f.results[0]
		f.results = f.results[1:]
	}
	code := 0
	if result != gates.ResultGreen {
		code = 1
	}
	return VerifyReport{Step: "test", Command: []string{"go", "test", "./internal/engine/..."},
		ExitCode: code, Result: result, Tail: "--- FAIL: TestLifecycle"}, nil
}

// fakeCollector is the collect port of a test: it returns the configured tip.
type fakeCollector struct {
	tip string
	err error
}

// Collect returns the configured tip.
func (f *fakeCollector) Collect(context.Context, *Clone) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.tip, nil
}

// fakeDiffer is the diff port of a test: it returns the configured paths and stat.
type fakeDiffer struct {
	paths   []string
	stat    string
	err     error
	statErr error
}

// Diff returns the configured diff.
func (f *fakeDiffer) Diff(_ context.Context, base, tip string) (Diff, error) {
	if f.err != nil {
		return Diff{}, f.err
	}
	return Diff{Base: base, Tip: tip, Paths: f.paths}, nil
}

// Stat returns the configured diff stat.
func (f *fakeDiffer) Stat(context.Context, string, string) (string, error) {
	if f.statErr != nil {
		return "", f.statErr
	}
	if f.err != nil {
		return "", f.err
	}
	return f.stat, nil
}

// lifecycleFixture holds the fakes of one run and the request built over them.
type lifecycleFixture struct {
	body       string
	severity   string
	assignment Assignment
	claimer    *fakeClaimer
	isolator   *fakeIsolator
	runner     *fakeRunner
	verifier   *fakeVerifier
	collector  *fakeCollector
	differ     *fakeDiffer
	nextRun    int
}

// newLifecycleFixture returns a fixture whose run is green from end to end: one source serves the
// writing and the judging role, and the auditor answers GREEN bound to its own run.
func newLifecycleFixture(t *testing.T, severity string) *lifecycleFixture {
	t.Helper()
	one := []roles.Pin{{Source: "sub-a", Model: "model-a", Effort: "high"}}
	f := &lifecycleFixture{
		body:     lifecycleTestBody(lifecycleTestFileSet()),
		severity: severity,
		assignment: Assignment{
			roleEngineer:   one,
			roleTestAuthor: one,
			roleAuditor:    one,
		},
		claimer:   &fakeClaimer{},
		isolator:  &fakeIsolator{},
		verifier:  &fakeVerifier{},
		collector: &fakeCollector{tip: lifecycleTestTip},
		differ:    &fakeDiffer{paths: lifecycleTestFileSet()},
	}
	f.runner = &fakeRunner{id: "sub-a", report: func(req DispatchRequest) DispatchReport {
		if req.Role == roleAuditor {
			return boundVerdict(req, "GREEN")
		}
		return roleReturn()
	}}
	return f
}

// request returns the lifecycle request of the fixture, over its current body.
func (f *lifecycleFixture) request() LifecycleRequest {
	return LifecycleRequest{
		Repo: lifecycleTestRepo(), Unit: lifecycleTestUnit,
		RunID: "01J8Z3K7Q5W0H8N4P2R6T1V9Y0", Base: "527ba9aa3105452f9c96c53ededf59dc2b521416",
		Canonical: "canonical", Branch: "unit/40-acceptance", Severity: f.severity,
		Assignment: f.assignment,
		Tracker: trackertest.NewFake(trackertest.Fixture{Repo: lifecycleTestRepo(),
			Units: []tracker.Unit{{Number: lifecycleTestUnit, Title: "A unit moves from intake to audit",
				Body: f.body}}}),
		Claims: f.claimer, Isolator: f.isolator, Runs: NewDispatcher(f.runner),
		Verifier: f.verifier, Collector: f.collector, Differ: f.differ, NextRunID: f.newRunID,
	}
}

// newRunID returns the next run id of the fixture, so every role run is its own run.
func (f *lifecycleFixture) newRunID() string {
	f.nextRun++
	return "01J8Z3K7Q5W0H8N4P2R6T1V9Y" + strconv.Itoa(f.nextRun)
}

// TestLifecycle walks the §7.3 steps of one unit in order: intake, claim, isolate, write, verify,
// collect, scope check and audit. Every step names its outcome, and a step that stops the run names
// the §7.4 outcome that stopped it, so no wait and no stop is silent (DR-1).
//
// rule: DR-1
func TestLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("the eight steps run in order and each names its outcome", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		rep, err := RunUnit(t.Context(), f.request())
		if err != nil {
			t.Fatalf("RunUnit: %v", err)
		}
		assertSteps(t, rep, []string{StepIntake, StepClaim, StepIsolate, StepWrite,
			StepVerify, StepCollect, StepScope, StepAudit},
			[]StepOutcome{OutcomeAccepted, OutcomeClaimed, OutcomeIsolated, OutcomeWritten,
				OutcomeGreen, OutcomeCollected, OutcomeInScope, OutcomeApproved})
		if rep.Outcome != OutcomeApproved {
			t.Fatalf("report outcome = %q, want approved", rep.Outcome)
		}
		if rep.AuditedTip != lifecycleTestTip {
			t.Fatalf("audited tip = %q, want %q", rep.AuditedTip, lifecycleTestTip)
		}
		if len(rep.Runs) != 2 {
			t.Fatalf("the run dispatched %d role runs, want the writer and the auditor: %+v", len(rep.Runs), rep.Runs)
		}
		writer := rep.Runs[0]
		if writer.Role != roleEngineer {
			t.Fatalf("first run role = %q, want %q", writer.Role, roleEngineer)
		}
		assertSystemThenTask(t, writer.Prompt, "UNIT")
		card, ok := roles.Lookup(roleEngineer)
		if !ok {
			t.Fatal("the engineer card is not shipped")
		}
		if !strings.Contains(writer.Prompt, card.Body) {
			t.Fatal("the engineer prompt does not carry the card body")
		}
		audit := rep.Runs[1]
		if audit.Role != roleAuditor || audit.RunID == writer.RunID {
			t.Fatalf("audit run = %q/%q, want the auditor in its own run", audit.Role, audit.RunID)
		}
	})

	t.Run("a stop names its outcome and no later step runs", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			prepare     func(*lifecycleFixture)
			wantStep    string
			wantOutcome StepOutcome
		}{
			{
				name:        "the intake gates refuse the unit body",
				prepare:     func(f *lifecycleFixture) { f.body = "## Problem\nno unit standard\n" },
				wantStep:    StepIntake,
				wantOutcome: OutcomeUnitInvalid,
			},
			{
				name: "another run holds the claim of the unit",
				prepare: func(f *lifecycleFixture) {
					f.claimer.err = &UnitClaimedError{}
				},
				wantStep:    StepClaim,
				wantOutcome: OutcomeAlreadyClaimed,
			},
			{
				name:        "the run clone cannot be made",
				prepare:     func(f *lifecycleFixture) { f.isolator.err = errors.New("the clone could not be made") },
				wantStep:    StepIsolate,
				wantOutcome: OutcomeIsolationFailed,
			},
			{
				name:        "the diff leaves the declared file set",
				prepare:     func(f *lifecycleFixture) { f.differ.paths = []string{"internal/engine/outside.go"} },
				wantStep:    StepScope,
				wantOutcome: OutcomeScopeMismatch,
			},
			{
				name: "the verdict binds a run that did not write it",
				prepare: func(f *lifecycleFixture) {
					f.runner.report = func(req DispatchRequest) DispatchReport {
						if req.Role == roleAuditor {
							return DispatchReport{Outcome: "final_answer",
								Return: "VERDICT: GREEN bound to RunID another-run"}
						}
						return roleReturn()
					}
				},
				wantStep:    StepAudit,
				wantOutcome: OutcomeUnverifiedFinding,
			},
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
				last := rep.Steps[len(rep.Steps)-1]
				if last.Step != tc.wantStep || last.Outcome != tc.wantOutcome {
					t.Fatalf("last step = %s/%s, want %s/%s", last.Step, last.Outcome, tc.wantStep, tc.wantOutcome)
				}
				if last.Detail == "" {
					t.Fatalf("the stop of %s names no cause", last.Step)
				}
				if rep.Outcome != tc.wantOutcome {
					t.Fatalf("report outcome = %q, want %q", rep.Outcome, tc.wantOutcome)
				}
				for _, later := range laterSteps(tc.wantStep) {
					if hasStep(rep, later) {
						t.Fatalf("step %q ran after the run stopped at %q: %+v", later, tc.wantStep, rep.Steps)
					}
				}
			})
		}
	})

	t.Run("a request without its ports is refused", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		req := f.request()
		req.Tracker = nil
		if _, err := RunUnit(t.Context(), req); err == nil {
			t.Fatal("accepted a request with no tracker")
		}
		req = f.request()
		req.NextRunID = nil
		if _, err := RunUnit(t.Context(), req); err == nil {
			t.Fatal("accepted a request that can make no run id")
		}
	})
}

// TestLifecycleAuditsInAnIndependentRun proves the auditor runs in its own run and takes another
// provider when the assignment has one, else another model, else a new instance: its verdict counts
// only bound to that run (DR-5, SPEC §8.1 point 5).
//
// rule: DR-5
func TestLifecycleAuditsInAnIndependentRun(t *testing.T) {
	t.Parallel()

	t.Run("another provider is taken when the assignment has one", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		judge := &fakeRunner{id: "api-b", report: func(req DispatchRequest) DispatchReport {
			return boundVerdict(req, "GREEN")
		}}
		f.assignment[roleAuditor] = []roles.Pin{
			{Source: "sub-a", Model: "model-a", Effort: "high"},
			{Source: "api-b", Model: "model-b", Effort: "high"},
		}
		req := f.request()
		req.Runs = NewDispatcher(f.runner, judge)
		rep, err := RunUnit(t.Context(), req)
		if err != nil {
			t.Fatalf("RunUnit: %v", err)
		}
		if len(judge.runs) != 1 {
			t.Fatalf("the judge provider received %d runs, want 1", len(judge.runs))
		}
		audit := rep.Runs[len(rep.Runs)-1]
		if audit.Entry.Source != "api-b" || audit.Independence != IndependenceProvider {
			t.Fatalf("audit entry = %q/%q, want api-b/provider", audit.Entry.Source, audit.Independence)
		}
		if audit.RunID == rep.Runs[0].RunID {
			t.Fatalf("the auditor shared the writer's run %q", audit.RunID)
		}
		if !strings.Contains(audit.Prompt, "VERDICT: GREEN|RED bound to RunID "+audit.RunID) {
			t.Fatal("the auditor prompt does not bind its VERDICT to the auditor's own run id")
		}
	})

	t.Run("one source still works", func(t *testing.T) {
		t.Parallel()
		f := newLifecycleFixture(t, "medium")
		rep, err := RunUnit(t.Context(), f.request())
		if err != nil {
			t.Fatalf("RunUnit: %v", err)
		}
		if rep.Outcome != OutcomeApproved {
			t.Fatalf("report outcome = %q, want approved with one source", rep.Outcome)
		}
		audit := rep.Runs[len(rep.Runs)-1]
		if audit.Independence != IndependenceInstance {
			t.Fatalf("audit independence = %q, want instance", audit.Independence)
		}
	})
}

// TestLifecycleRiskyUnitRunsTheTestAuthorFirst proves L17 at the lifecycle: a risky unit dispatches the
// test-author before the engineer and the auditor last, and an ordinary unit never runs the test-author.
func TestLifecycleRiskyUnitRunsTheTestAuthorFirst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		severity string
		want     []string
	}{
		{"a high unit", "high", []string{roleTestAuthor, roleEngineer, roleAuditor}},
		{"a critical unit", "critical", []string{roleTestAuthor, roleEngineer, roleAuditor}},
		{"a medium unit", "medium", []string{roleEngineer, roleAuditor}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLifecycleFixture(t, tc.severity)
			rep, err := RunUnit(t.Context(), f.request())
			if err != nil {
				t.Fatalf("RunUnit: %v", err)
			}
			var got []string
			for _, run := range rep.Runs {
				got = append(got, run.Role)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("role runs = %v, want %v", got, tc.want)
			}
			if rep.Outcome != OutcomeApproved {
				t.Fatalf("report outcome = %q, want approved", rep.Outcome)
			}
		})
	}
}
