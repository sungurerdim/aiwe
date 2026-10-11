package gates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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
// A green or red test step prints the count with its inputs as its detail. History holds each
// step's recorded runs, oldest first, and sets the test step's limit (SPEC §7.8); Recorded holds
// the recorded package durations that raise it (SPEC §5.13). Each step's output streams to Output
// while it runs (DR-38); a nil Output streams nowhere. Each step runs in fresh state, cache and
// temp roots and a secret-free environment (DR-35, SPEC §7.8): an owned directory of Owner under
// the temp root. A Runner with no Owner runs no step; each step is not_measured (SPEC §6.4).
type Runner struct {
	Capacity core.Capacity
	History  map[string][]StepRun
	Recorded []PackageDuration
	Output   io.Writer
	Owner    *core.Owner
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
		report.Steps = append(report.Steps, r.runStep(ctx, step, dir, def.Proof))
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

// runStep runs one step as a process tree in fresh roots, streams its output while it runs and
// judges it by its declared exit code and the roots it leaves (SPEC §7.8). A step that cannot
// start, is stopped before it exits, or declares an output expectation is not_measured. A test
// step that exits as declared is then judged by its package durations (SPEC §5.13).
func (r *Runner) runStep(ctx context.Context, step GateStep, dir string, proof *Proof) StepReport {
	if err := ctx.Err(); err != nil {
		return unmeasured(step.ID, "not started: "+err.Error())
	}
	if step.Expect.OutputMatches != "" || step.Expect.OutputRejects != "" {
		return unmeasured(step.ID, "an output expectation cannot be checked: the runner does not match output against it")
	}
	step, note, err := r.prepare(step, proof)
	if err != nil {
		return unmeasured(step.ID, "not started: "+err.Error())
	}
	stream, err := newStepStream(r.Output, step)
	if err != nil {
		return unmeasured(step.ID, "not started: "+err.Error())
	}
	roots, err := r.freshRoots(ctx)
	if err != nil {
		return unmeasured(step.ID, "not started: fresh roots: "+err.Error())
	}
	var out bytes.Buffer
	spec := core.ChildSpec{Dir: dir, Env: roots.env(), Stdout: io.MultiWriter(&out, stream), Stderr: stream}
	code, failed := execStep(ctx, step, spec)
	if failed != nil {
		return roots.close(ctx, *failed)
	}
	return roots.close(ctx, judgeStep(step, code, note, out.Bytes()))
}

// judgeStep judges a step that exited by its declared exit code; a test step that exits as
// declared is then judged by its package durations (SPEC §5.13).
func judgeStep(step GateStep, code int, note string, out []byte) StepReport {
	if code != step.Expect.ExitCode {
		return StepReport{ID: step.ID, Result: ResultRed, ExitCode: code, Detail: note}
	}
	if step.ID != testStepID {
		return StepReport{ID: step.ID, Result: ResultGreen, ExitCode: code, Detail: note}
	}
	return judgeDurations(step, code, note, out)
}

// prepare returns the step as it runs and the note its report carries. Every other step runs as
// declared. The test step runs with the worker count of DR-7 under the limit of SPEC §7.8 (its
// declared limit, else StepLimit), raised to 2 × the longest recorded package (SPEC §5.13); the
// note prints the count with its inputs. A test step with no chosen count cannot run: the count
// is never assumed (DR-13).
func (r *Runner) prepare(step GateStep, proof *Proof) (prepared GateStep, note string, err error) {
	if step.ID != testStepID {
		return step, "", nil
	}
	if r.Capacity.Workers < 1 {
		return step, "", errors.New("no worker count was chosen for the test step")
	}
	if step.TimeoutSeconds == 0 {
		step.TimeoutSeconds = ceilSeconds(StepLimit(baseProofStep(proof, step.ID), r.History[step.ID]))
	}
	return PrepareTestStep(step, r.Capacity.Workers, r.Recorded), r.Capacity.String(), nil
}

// execStep runs step as a process tree under its time limit, with the directory, environment and
// output of spec, and returns its exit code. A non-nil report is the not_measured result of a step
// that gave no exit code.
func execStep(ctx context.Context, step GateStep, spec core.ChildSpec) (int, *StepReport) {
	if step.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	fail := func(detail string) (int, *StepReport) {
		report := unmeasured(step.ID, detail)
		return exitUnknown, &report
	}
	spec.Argv = step.Command
	child, err := core.Start(ctx, spec)
	if err != nil {
		return fail("could not start: " + err.Error())
	}
	waitErr := child.Wait()
	if err := child.StopTree(stepGrace); err != nil {
		return fail("a leftover process could not be stopped: " + err.Error())
	}
	if err := ctx.Err(); err != nil {
		return fail("stopped before it exited: " + err.Error())
	}
	code, ok := exitCode(waitErr)
	if !ok {
		return fail("no exit code: " + waitErr.Error())
	}
	return code, nil
}

// judgeDurations judges a test step that exited as declared by the package durations its
// `go test -json` output reports, against the step's limit. Output that measured no package is
// not_measured, never green (DR-13).
func judgeDurations(step GateStep, code int, note string, out []byte) StepReport {
	limit := time.Duration(step.TimeoutSeconds) * time.Second
	current, err := ParsePackageDurations(out)
	if err != nil {
		return StepReport{ID: step.ID, Result: ResultNotMeasured, ExitCode: code, Detail: joinDetail(note, err.Error())}
	}
	result, detail := CheckPackageDurations(limit, current)
	return StepReport{ID: step.ID, Result: result, ExitCode: code, Detail: joinDetail(note, detail)}
}

// joinDetail joins the non-empty parts of a step detail.
func joinDetail(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), "; ")
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
