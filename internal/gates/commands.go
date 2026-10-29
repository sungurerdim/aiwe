package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/ledger"
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
		{Module: "gates", Object: "report", Verb: "tests", Params: []string{"repo", "since", "format"},
			Purpose: "print the test report of the check records of a period", Run: runReportTests},
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
	records, err := openCheckLedger()
	if err != nil {
		return core.Result[any]{}, err
	}
	r := Runner{Capacity: capacity, Output: in.Stream, Owner: owner, Records: records, Facts: MeasureFacts(ctx, repo)}
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

// maxSinceUnits bounds the count of a --since value, so its duration cannot overflow.
const maxSinceUnits = 36500

// runReportTests reads the check records of the ledger, keeps those of --repo (every repo when it is
// absent) that are not older than --since (all when it is absent), and prints the report. It writes
// no file (L68).
func runReportTests(_ context.Context, in core.Input) (core.Result[any], error) {
	since, err := parseSince(in.Params["since"], time.Now())
	if err != nil {
		return core.Result[any]{}, err
	}
	roots, err := core.UserRoots()
	if err != nil {
		return core.Result[any]{}, ownerRefusal(err)
	}
	checks, err := readChecks(filepath.Join(roots.State, core.LedgerFile), in.Params["repo"], since)
	if err != nil {
		return core.Result[any]{}, err
	}
	report := buildTestReport(checks, filepath.Join(roots.State, tailDirName), in.Params["since"])
	return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: report, Detail: report.text()}, nil
}

// parseSince returns the start of the period value names: Nd or Nh before now, or a date
// (2006-01-02) at midnight UTC. An empty value is no start: the zero time.
func parseSince(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if day, err := time.Parse(time.DateOnly, value); err == nil {
		return day, nil
	}
	unit := map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour}
	count, err := strconv.Atoi(value[:max(0, len(value)-1)])
	step, known := unit[value[max(0, len(value)-1):]]
	if err != nil || !known || count <= 0 || count > maxSinceUnits {
		return time.Time{}, &core.Error{Outcome: core.OutcomeUsage, Expected: "--since as Nd, Nh or a date such as 2026-10-22",
			Found: fmt.Sprintf("%q", value), Next: "pass --since 7d"}
	}
	return now.Add(-time.Duration(count) * step), nil
}

// readChecks returns the check records of the ledger at path that belong to repo (any repo when it
// is empty) and are not older than since. A ledger that does not exist holds none.
func readChecks(path, repo string, since time.Time) (checks []ledger.Check, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ledgerRefusal(err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	records, err := ledger.Read(f)
	if err != nil {
		return nil, ledgerRefusal(err)
	}
	for _, rec := range records {
		if c, ok := rec.(ledger.Check); ok && (repo == "" || c.RepoID == repo) && !c.At.Before(since) {
			checks = append(checks, c)
		}
	}
	return checks, nil
}

// ledgerRefusal is the refusal of a ledger that cannot be read: the report never guesses from a
// part of it.
func ledgerRefusal(err error) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: "a readable ledger in the state root", Found: err.Error(),
		Next: "repair or move the ledger file, then ask for the report again", Err: err}
}
