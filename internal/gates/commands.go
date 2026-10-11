package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The outcomes of a gate that does not hold; each is a row of core.Outcomes (BC-1).
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
// incomplete gate never exits 0 (BC-1, DR-10).
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

// engineOwner opens the engine owner under the resolved roots, with repo as the canonical
// repository it must keep out of reach (SPEC §6.4, §5.15). Every check runs in roots this owner
// makes, so a gate with no owner refuses: an unsafe or unresolved root keeps its named outcome.
func engineOwner(repo string) (*core.Owner, error) {
	roots, err := core.UserRoots()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ownerRefusal(fmt.Errorf("%w: home: %w", core.ErrRootUnsafe, err))
	}
	self, err := core.CurrentProcess()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	owner, err := core.OpenOwner(core.OwnedRoots{State: roots.State, Temp: roots.Temp, Cache: roots.Cache,
		Home: home, Canonical: []string{repo}}, self)
	if err != nil {
		return nil, ownerRefusal(err)
	}
	return owner, nil
}

// ownerRefusal is the refusal of a gate whose engine owner did not open. Its outcome is the one the
// cause names in the outcome table, and so is its next action (BC-1).
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

// readDefinition reads and parses gate.json at the repository root. A missing file is an error,
// never a green gate (DR-13).
func readDefinition(repo string) (Definition, error) {
	path, err := core.ResolvePath(repo, "gate.json")
	if err != nil {
		return Definition{}, err
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Definition{}, &core.Error{Outcome: core.OutcomeError, Expected: "a gate.json at the repository root",
			Found: fmt.Sprintf("no file at %q", path), Next: "add gate.json (SPEC §8.3), or pass the --repo that holds it",
			Err: err}
	case err != nil:
		return Definition{}, &core.Error{Outcome: core.OutcomeError, Expected: "a readable gate.json",
			Found: err.Error(), Next: "make gate.json readable, then run the gate again", Err: err}
	}
	return ParseDefinition(data)
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
	for _, s := range report.Steps {
		fmt.Fprintf(&b, "\n  %s: %s", s.ID, s.Result)
		if s.Detail != "" {
			fmt.Fprintf(&b, " (%s)", s.Detail)
		}
	}
	return b.String()
}
