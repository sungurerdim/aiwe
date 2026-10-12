package gates

import (
	"context"
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The outcomes of a gate that does not hold; each is a row of core.Outcomes.
const (
	outcomeRed        core.Outcome = "red"
	outcomeIncomplete core.Outcome = "incomplete"
)

// Commands returns the gates commands of the one command registry (SPEC §5.7).
func Commands() []core.Command {
	return []core.Command{
		{Module: "gates", Object: "gates", Verb: "gate", Params: []string{"repo", "step", "format"},
			Purpose: "run the gate that <repo>/gate.json declares and report each step", Run: runGate},
	}
}

// runGate runs the gate of <repo>/gate.json, or only the steps --step names, and maps the gate
// result to one outcome: green is ok, red is red, anything else is incomplete. A red or
// incomplete gate never exits 0 (DR-6).
func runGate(ctx context.Context, in core.Input) (core.Result[any], error) {
	repo, ok := in.Params["repo"]
	if !ok {
		return core.Result[any]{}, &core.Error{Outcome: core.OutcomeUsage, Expected: "--repo",
			Found: "no --repo", Next: "pass --repo <path>, for example --repo ."}
	}
	only, err := stepNames(in.Params)
	if err != nil {
		return core.Result[any]{}, err
	}
	def, err := readDefinition(repo)
	if err != nil {
		return core.Result[any]{}, err
	}
	// One gate run starts no other job on this path, and no admission count exists yet: jobs is 1.
	capacity := core.CapacityFor(core.MeasureCores(), 1)
	capacity.JobsFrom = "single-run"
	owner, err := engineOwner(repo)
	if err != nil {
		return core.Result[any]{}, err
	}
	r := Runner{Capacity: capacity, Output: in.Stream, Owner: owner}
	report, err := r.Run(ctx, def, repo, only)
	if err != nil {
		return core.Result[any]{}, err
	}
	return gateOutcome(report), nil
}

// ownerRefusal is the refusal of a gate whose engine owner did not open. Its outcome is the one the
// cause names in the outcome table, and so is its next action.
func ownerRefusal(err error) error {
	outcome := core.ErrorResult(err).Outcome
	return &core.Error{Outcome: outcome, Expected: "an engine owner over the resolved state, cache and temp roots",
		Found: err.Error(), Err: err}
}

// stepNames splits the --step value into step ids; an empty id is a usage error. No --step runs
// every step.
func stepNames(params map[string]string) ([]string, error) {
	value, ok := params["step"]
	if !ok {
		return nil, nil
	}
	ids := strings.Split(value, ",")
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, &core.Error{Outcome: core.OutcomeUsage, Expected: "comma-separated step ids",
				Found: fmt.Sprintf("%q", value), Next: "pass --step a,b with no empty id"}
		}
	}
	return ids, nil
}

// gateOutcome maps a gate report to its result: the status and outcome come from the outcome table.
func gateOutcome(report GateReport) core.Result[any] {
	res := core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: report}
	switch report.Result {
	case ResultGreen:
	case ResultRed:
		res.Status, res.Outcome = core.StatusRed, outcomeRed
	case ResultIncomplete, ResultNotMeasured:
		res.Status, res.Outcome = core.StatusError, outcomeIncomplete
	default:
		res.Status, res.Outcome = core.StatusError, outcomeIncomplete
	}
	res.Detail = gateDetail(report)
	return res
}

// gateDetail is one line per step: the id, its result and, when it has one, its detail.
func gateDetail(report GateReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gate %s", report.Result)
	if report.Partial {
		b.WriteString(" (partial)")
	}
	if report.Detail != "" {
		fmt.Fprintf(&b, " (%s)", report.Detail)
	}
	for _, s := range report.Steps {
		fmt.Fprintf(&b, "\n  %s: %s", s.ID, s.Result)
		if s.Detail != "" {
			fmt.Fprintf(&b, " (%s)", s.Detail)
		}
	}
	return b.String()
}
