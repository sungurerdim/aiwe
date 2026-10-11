package gates

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Result is the result word of a gate or of one step.
type Result string

// The result words of a gate and its steps: green, red, incomplete or not_measured (DR-10, DR-13).
const (
	ResultGreen       Result = "green"
	ResultRed         Result = "red"
	ResultIncomplete  Result = "incomplete"
	ResultNotMeasured Result = "not_measured"
)

const (
	// exitUnknown is the exit code of a step that never exited: it was not run, could not start or
	// was stopped. Unmeasured is never 0 (DR-13, DR-21).
	exitUnknown = -1
	// stepGrace is how long a step's leftover processes get to end before they are killed (DR-15).
	stepGrace = 2 * time.Second
	// testStepID is the step that runs the tests with the measured worker count (DR-7).
	testStepID = "test"
)

// StepReport is the result of one declared step. ExitCode is exitUnknown when the step never
// exited.
type StepReport struct {
	ID       string `json:"id"`
	Result   Result `json:"result"`
	ExitCode int    `json:"exitCode"`
	Detail   string `json:"detail,omitempty"`
}

// GateReport is the result of a gate: one report per declared step, in declared order. Partial
// reports that the run left a declared step out on purpose.
type GateReport struct {
	Result  Result       `json:"result"`
	Partial bool         `json:"partial"`
	Steps   []StepReport `json:"steps"`
}

// Runner runs the steps of a gate, one after another, each as a process tree (SPEC §5.20).
// Capacity sets the test step's worker count (DR-7); without one the test step is not_measured.
// A green or red test step prints the count with its inputs as its detail.
type Runner struct {
	Capacity core.Capacity
}

// Run runs the steps of def in dir. A non-empty only names the steps to run; every other step is
// reported not_measured and the gate is at best incomplete (DR-10, SPEC §7.8). Any red step makes
// the gate red; otherwise any step that is not green makes it incomplete. Run returns an error
// only when it cannot decide: an empty gate or an unknown step name in only.
func (r *Runner) Run(ctx context.Context, def Definition, dir string, only []string) (GateReport, error) {
	if len(def.Steps) == 0 {
		return GateReport{}, invalidGate("at least one step", "no step", nil)
	}
	if err := checkOnly(def, only); err != nil {
		return GateReport{}, err
	}
	report := GateReport{Steps: make([]StepReport, 0, len(def.Steps))}
	for _, step := range def.Steps {
		if len(only) > 0 && !slices.Contains(only, step.ID) {
			report.Partial = true
			report.Steps = append(report.Steps, unmeasured(step.ID, "left out of this run"))
			continue
		}
		report.Steps = append(report.Steps, r.runStep(ctx, step, dir))
	}
	report.Result = gateResult(report.Steps)
	return report, nil
}

// checkOnly refuses a step name that def does not declare, so a typo never runs nothing.
func checkOnly(def Definition, only []string) error {
	for _, id := range only {
		if !slices.ContainsFunc(def.Steps, func(s GateStep) bool { return s.ID == id }) {
			return &core.Error{Outcome: core.OutcomeUsage, Expected: "a step that gate.json declares",
				Found: fmt.Sprintf("step %q", id), Next: "name only steps listed in gate.json"}
		}
	}
	return nil
}

// runStep runs one step as a process tree and judges it by its declared exit code. A step that
// cannot start, is stopped before it exits, or declares an output expectation is not_measured.
func (r *Runner) runStep(ctx context.Context, step GateStep, dir string) StepReport {
	if err := ctx.Err(); err != nil {
		return unmeasured(step.ID, "not started: "+err.Error())
	}
	if step.Expect.OutputMatches != "" || step.Expect.OutputRejects != "" {
		return unmeasured(step.ID, "an output expectation cannot be checked: the runner does not capture output")
	}
	if step.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	argv, note, err := r.command(step)
	if err != nil {
		return unmeasured(step.ID, "not started: "+err.Error())
	}
	child, err := core.Start(ctx, core.ChildSpec{Argv: argv, Dir: dir})
	if err != nil {
		return unmeasured(step.ID, "could not start: "+err.Error())
	}
	waitErr := child.Wait()
	if err := child.StopTree(stepGrace); err != nil {
		return unmeasured(step.ID, "a leftover process could not be stopped: "+err.Error())
	}
	if err := ctx.Err(); err != nil {
		return unmeasured(step.ID, "stopped before it exited: "+err.Error())
	}
	code, ok := exitCode(waitErr)
	if !ok {
		return unmeasured(step.ID, "no exit code: "+waitErr.Error())
	}
	result := ResultRed
	if code == step.Expect.ExitCode {
		result = ResultGreen
	}
	return StepReport{ID: step.ID, Result: result, ExitCode: code, Detail: note}
}

// command returns the argv a step runs and the note its report carries. The test step runs with
// the worker count of DR-7, and the note prints the count with its inputs; every other step runs
// as declared. A test step with no chosen count cannot run: the count is never assumed (DR-13).
func (r *Runner) command(step GateStep) (argv []string, note string, err error) {
	if step.ID != testStepID {
		return step.Command, "", nil
	}
	if r.Capacity.Workers < 1 {
		return nil, "", errors.New("no worker count was chosen for the test step")
	}
	return withParallelism(step.Command, r.Capacity.Workers), r.Capacity.String(), nil
}

// exitCode returns the exit code behind the error of Wait; ok is false when the child did not
// exit normally.
func exitCode(waitErr error) (int, bool) {
	if waitErr == nil {
		return 0, true
	}
	var exited interface{ ExitCode() int }
	if !errors.As(waitErr, &exited) {
		return exitUnknown, false
	}
	code := exited.ExitCode()
	return code, code >= 0
}

// unmeasured is the report of a step that produced no result.
func unmeasured(id, detail string) StepReport {
	return StepReport{ID: id, Result: ResultNotMeasured, ExitCode: exitUnknown, Detail: detail}
}

// gateResult folds step results into the gate result: red beats incomplete beats green.
func gateResult(steps []StepReport) Result {
	result := ResultGreen
	for _, s := range steps {
		switch s.Result {
		case ResultRed:
			return ResultRed
		case ResultGreen:
		case ResultIncomplete, ResultNotMeasured:
			result = ResultIncomplete
		default:
			result = ResultIncomplete
		}
	}
	return result
}
