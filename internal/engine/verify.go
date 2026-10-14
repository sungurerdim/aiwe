// Package engine runs one unit verify and returns its result to the fix round.
package engine

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
)

// outputTailLines is how many lines of a verify's output reach the fix round (SPEC §8.5).
const outputTailLines = 40

// VerifyRequest is one unit verify: the step to run, the directory it runs in, the key inputs
// and the cache that makes the same key run once (DR-8, SPEC §7.8).
type VerifyRequest struct {
	Step  gates.GateStep
	Dir   string
	Key   gates.KeyInput
	Cache *gates.Cache
}

// VerifyReport is what a fix round reads: the step, the argv, the exit code, the result word and
// the tail of the output (SPEC §7.3 step 6).
type VerifyReport struct {
	Step     string
	Command  []string
	ExitCode int
	Result   gates.Result
	Tail     string
}

// Verify runs req once per key. A key already in the cache returns the stored result and does not
// run the step again. A red step returns its step, argv, exit code and output tail (DR-8, SPEC
// §7.3 step 6, §7.8). The runner starts the step argv as a process tree, never through a shell.
// Verify returns an error only when the step does not exit.
func Verify(ctx context.Context, runner *gates.Runner, req VerifyRequest) (VerifyReport, error) {
	key := gates.CheckKey(req.Key)
	if req.Cache != nil {
		if stored, ok := req.Cache.Lookup(key); ok {
			return newReport(stored.StepID, req.Step.Command, stored.ExitCode, stored.Result, stored.Tail), nil
		}
	}
	if runner == nil {
		return VerifyReport{}, &core.Error{Outcome: core.OutcomeError, Expected: "a gate runner",
			Found: "no runner", Next: "pass the runner that owns the check's roots"}
	}
	report, err := runVerify(ctx, runner, req)
	if err != nil {
		return VerifyReport{}, err
	}
	if req.Cache != nil {
		req.Cache.Store(gates.CheckResult{Key: key, StepID: report.Step, Result: report.Result,
			ExitCode: report.ExitCode, Tail: report.Tail})
	}
	return report, nil
}

// runVerify runs req.Step once and returns its report. The runner's output writer is restored
// before runVerify returns.
func runVerify(ctx context.Context, runner *gates.Runner, req VerifyRequest) (VerifyReport, error) {
	var buf strings.Builder
	prev := runner.Output
	runner.Output = &buf
	if prev != nil {
		runner.Output = io.MultiWriter(&buf, prev)
	}
	defer func() { runner.Output = prev }()

	ran, err := runner.Run(ctx, gates.Definition{Steps: []gates.GateStep{req.Step}}, req.Dir, nil)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("verify: %w", err)
	}
	if len(ran.Steps) != 1 {
		return VerifyReport{}, &core.Error{Outcome: core.OutcomeError, Expected: "one step report",
			Found: fmt.Sprintf("%d step reports", len(ran.Steps)), Next: "run the verify as one step"}
	}
	step := ran.Steps[0]
	if step.Result == gates.ResultNotMeasured || step.ExitCode < 0 {
		found := step.Detail
		if found == "" {
			found = "no exit code"
		}
		return VerifyReport{}, &core.Error{Outcome: core.OutcomeError, Expected: "a verify that exits",
			Found: found, Next: "correct the verify command and run it again"}
	}
	return newReport(step.ID, req.Step.Command, step.ExitCode, step.Result, outputTail(buf.String(), step.ID)), nil
}

// newReport builds a report whose command slice is independent of the caller's slice.
func newReport(stepID string, command []string, code int, result gates.Result, tail string) VerifyReport {
	return VerifyReport{
		Step:     stepID,
		Command:  slices.Clone(command),
		ExitCode: code,
		Result:   result,
		Tail:     tail,
	}
}

// outputTail returns the last outputTailLines lines of the step's own output. The runner writes
// one header line before that output; the header is not part of the tail.
func outputTail(streamed, stepID string) string {
	streamed = strings.TrimPrefix(streamed, "--- step "+stepID+"\n")
	lines := strings.Split(streamed, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) > outputTailLines {
		lines = lines[len(lines)-outputTailLines:]
	}
	return strings.Join(lines, "\n")
}
