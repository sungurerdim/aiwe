// Package engine reproduces the findings of a security review itself: the reviewer never runs a
// reproduction, the engine runs each one in its own run clone of the unit's tip (SPEC §9.1, DR-8).
package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
)

// reproStepID is the id of the one step a reproduction runs as, so its output is bounded and
// labelled like a unit verify's (SPEC §7.8).
const reproStepID = "repro"

// ReproSpec is one finding to reproduce: the finding, the tip of the unit it affects, the run of
// the review that reported it, and the argv that finding names as its reproduction (SPEC §9.1).
type ReproSpec struct {
	Finding   string   // the path and line of the finding
	Tip       string   // the tip of the unit the finding affects
	ReviewRun string   // the run of the review that reported the finding; every reproduction needs it
	Command   []string // the argv the finding names as its reproduction
}

// ReproRun is one reproduction the engine ran: what it ran, where, with which exit code and the
// tail of its output, so a review is judged on a recorded run and never on the reviewer's word
// (SPEC §9.1, §7.3 step 6, DR-6).
type ReproRun struct {
	Finding  string   // the finding this run is the evidence of
	Tip      string   // the tip it ran at
	Command  []string // the argv the engine ran
	RunID    string   // the engine's own run, never the reviewing run
	Clone    string   // the engine's own scratch clone of the tip it ran in
	ExitCode int      // the exit code the reproduction itself exited with
	Tail     string   // the last lines of its output
}

// The refusals of Reproduce, which errors.Is sees (SPEC §5.3). Both are refused before the
// reproduction's command starts, so a refused finding leaves no run behind.
var (
	// ErrReproReviewerRun refuses a reproduction that would run in the clone of the reviewing run
	// itself: a reproduction the reviewer made is no evidence (DR-8).
	ErrReproReviewerRun = errors.New("reproduction refused: the clone belongs to the run of the review")
	// ErrReproOutsideTip refuses a reproduction that would run outside a clone of the tip the
	// finding affects: another commit holds no evidence of this finding (DR-6).
	ErrReproOutsideTip = errors.New("reproduction refused: the clone holds another commit than the finding's tip")
)

// Reproduce runs the reproduction of one finding in the engine's own run clone of the unit's tip
// and returns the recorded run (SPEC §9.1, §6.5, DR-8). The reviewer never runs it: a clone of the
// reviewing run and a clone of another commit are refused before the command starts, so a finding
// is never proved by a run the reviewer's own clone made outside the tip (DR-6). The command runs
// as the one step of a gate definition, so it gets the secret-free environment and the fresh roots
// of a check (§6.5, §7.8) and never runs through a shell (SPEC §5.20).
func Reproduce(ctx context.Context, runner *gates.Runner, clone *Clone, spec ReproSpec) (ReproRun, error) {
	if clone == nil {
		return ReproRun{}, noClone()
	}
	if err := checkRepro(clone, spec); err != nil {
		return ReproRun{}, err
	}
	step := gates.GateStep{ID: reproStepID, Command: slices.Clone(spec.Command), Required: true}
	report, err := runOnce(ctx, runner, VerifyRequest{Step: step, Dir: clone.Dir})
	if err != nil {
		return ReproRun{}, fmt.Errorf("reproduce the finding %s: %w", spec.Finding, err)
	}
	return ReproRun{
		Finding:  spec.Finding,
		Tip:      spec.Tip,
		Command:  slices.Clone(spec.Command),
		RunID:    clone.RunID,
		Clone:    clone.Dir,
		ExitCode: report.ExitCode,
		Tail:     report.Tail,
	}, nil
}

// noClone refuses a reproduction without a clone: the engine runs every reproduction in its own
// scratch clone of the tip, never in the repository of the review (SPEC §6.5, DR-10).
func noClone() error {
	return &core.Error{Outcome: core.OutcomeError, Expected: "the engine's own run clone of the tip",
		Found: "no clone", Next: "make the run clone with NewClone, then reproduce the finding in it"}
}

// checkRepro refuses a reproduction the engine may not run: one whose spec names no finding, one
// whose finding names no review run, one whose clone belongs to the run of the review, one whose
// clone is not at the finding's tip, and one whose finding names no argv. A reproduction without a
// finding or without its review run is refused, never run: it proves nothing and the engine could
// not tell its own clone from the reviewer's (fail closed, DR-8, DR-6).
func checkRepro(clone *Clone, spec ReproSpec) error {
	if spec.Finding == "" {
		return &core.Error{Outcome: core.OutcomeError,
			Expected: "the path and line of the finding to reproduce",
			Found:    "a reproduction that names no finding",
			Next:     "name the finding the reproduction belongs to, then reproduce it again"}
	}
	if spec.ReviewRun == "" {
		return &core.Error{Outcome: core.OutcomeError,
			Expected: "the run of the review that reported the finding",
			Found:    fmt.Sprintf("the finding %s names no review run", spec.Finding),
			Next:     "pass the run id of the review that reported the finding, then reproduce it again"}
	}
	if clone.RunID == spec.ReviewRun {
		return fmt.Errorf("%w: the run %s reported the finding", ErrReproReviewerRun, spec.ReviewRun)
	}
	if spec.Tip == "" || clone.Base != spec.Tip {
		return fmt.Errorf("%w: the clone holds %s, the finding's tip is %q",
			ErrReproOutsideTip, clone.Base, spec.Tip)
	}
	if len(spec.Command) == 0 || spec.Command[0] == "" {
		return &core.Error{Outcome: core.OutcomeError, Expected: "the argv the finding names as its reproduction",
			Found: fmt.Sprintf("the finding %s names no command", spec.Finding),
			Next:  "write the reproduction as a program and its arguments, then reproduce the finding again"}
	}
	return nil
}
