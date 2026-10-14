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
	Step gates.GateStep
	// Dir is the working directory of the step. It is not part of the key, so a fresh clone of the
	// same tree maps to the same key (SPEC §6.5, §7.8).
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
// run the step again. The key covers the step's argv, the digest of its declared inputs from git
// objects and the env fingerprint, so a stored result is never served for a changed tree; the
// step's id and the directory are not part of it, so a fresh clone of the same tree reuses the
// stored result and a caller labels it with the step it asked for (§6.5, §7.3 step 6). A red step
// returns its step, argv, exit code and output tail. The runner starts the step argv as a process
// tree, never through a shell. Verify returns an error only when the step does not exit or the key
// covers no tree.
func Verify(ctx context.Context, runner *gates.Runner, req VerifyRequest) (VerifyReport, error) {
	if req.Key.InputsDigest == "" {
		return VerifyReport{}, &core.Error{Outcome: core.OutcomeError,
			Expected: "a check key over the step's declared inputs", Found: "an empty InputsDigest",
			Next: "summarise the step's declared inputs from git objects, then run the verify again"}
	}
	key := gates.CheckKey(req.Key)
	if req.Cache == nil {
		return runOnce(ctx, runner, req)
	}
	stored, err := req.Cache.Once(key, func() (gates.CheckResult, error) {
		report, err := runOnce(ctx, runner, req)
		if err != nil {
			return gates.CheckResult{}, err
		}
		return gates.CheckResult{Key: key, StepID: report.Step, Result: report.Result,
			ExitCode: report.ExitCode, Tail: report.Tail}, nil
	})
	if err != nil {
		return VerifyReport{}, err
	}
	return newReport(req.Step.ID, req.Step.Command, stored.ExitCode, stored.Result, stored.Tail), nil
}

// runOnce refuses a runner that cannot run a check, then runs req once.
func runOnce(ctx context.Context, runner *gates.Runner, req VerifyRequest) (VerifyReport, error) {
	if runner == nil {
		return VerifyReport{}, &core.Error{Outcome: core.OutcomeError, Expected: "a gate runner",
			Found: "no runner", Next: "pass the runner that owns the check's roots"}
	}
	return runVerify(ctx, runner, req)
}

// runVerify runs req.Step once and returns its report. It runs on a copy of the runner whose output
// is the step's own buffer, so two verifies that share a runner — two different keys run at the
// same time (DR-3) — never write each other's output and never touch the runner's own field; the
// caller's writer still receives every line.
func runVerify(ctx context.Context, runner *gates.Runner, req VerifyRequest) (VerifyReport, error) {
	var buf strings.Builder
	own := *runner
	own.Output = &buf
	if prev := runner.Output; prev != nil {
		own.Output = io.MultiWriter(&buf, prev)
	}

	ran, err := own.Run(ctx, gates.Definition{Steps: []gates.GateStep{req.Step}}, req.Dir, nil)
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
