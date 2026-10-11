// Command repocheck runs this repository's own checks and its doctor (SPEC §12.2); process and
// file IO go through internal/repocheck's doctor_io.go.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// The per-check results of a report and the outcomes this command adds (SPEC §5.3).
const (
	resultGreen       = "green"
	resultRed         = "red"
	resultError       = "error"
	resultNotMeasured = "not_measured"

	outcomeRed        core.Outcome = "red"
	outcomeIncomplete core.Outcome = "incomplete"

	// productFile is the one file that may hold the product name (DR-25).
	productFile = "internal/core/platform.go"
)

func main() {
	os.Exit(core.RunCLI(context.Background(), commands(), os.Args[1:], os.Stdout))
}

// check is one repository check: its name and how it finds the broken rules of a repository.
type check struct {
	name string
	run  func(ctx context.Context, repo string) ([]repocheck.Finding, error)
}

// checks returns the checks `all` runs, in order (SPEC §12.2).
func checks() []check {
	return []check{
		{"rules", func(_ context.Context, repo string) ([]repocheck.Finding, error) { return repocheck.Rules(repo) }},
		{"size", runSize},
		{"boundaries", runBoundaries},
		{"source-rules", runSourceRules},
		{"deps", runDeps},
	}
}

// unmeasured are the checks of §12.2 whose unit has not landed; `all` reports them not_measured.
func unmeasured() []string { return []string{"ci-shape"} }

// commands returns the one command list of this binary (SPEC §5.7).
func commands() []core.Command {
	params := []string{"repo", "format"}
	cmds := []core.Command{
		{Module: "repocheck", Object: "doctor", Params: params,
			Purpose: "name each missing prerequisite with its install command", Run: runDoctor},
		{Module: "repocheck", Object: "all", Params: params,
			Purpose: "run every repository check and report each result", Run: runAll},
	}
	for _, c := range checks() {
		cmds = append(cmds, core.Command{Module: "repocheck", Object: c.name, Params: params,
			Purpose: "run the repository check " + c.name, Run: runOne(c)})
	}
	return cmds
}

// checkResult is one check of a report.
type checkResult struct {
	Check    string              `json:"check"`
	Result   string              `json:"result"`
	Findings []repocheck.Finding `json:"findings,omitempty"`
	Detail   string              `json:"detail,omitempty"`
}

// report is the data of a check command.
type report struct {
	Checks []checkResult `json:"checks"`
}

// repoParam returns --repo; an absent or empty --repo is a usage error.
func repoParam(in core.Input) (string, error) {
	if r, ok := in.Params["repo"]; ok && r != "" {
		return r, nil
	}
	return "", &core.Error{Outcome: core.OutcomeUsage, Expected: "--repo", Found: "no --repo",
		Next: "pass --repo <path>, for example --repo ."}
}

// runOne returns the Run function of the single check c.
func runOne(c check) func(context.Context, core.Input) (core.Result[any], error) {
	return func(ctx context.Context, in core.Input) (core.Result[any], error) {
		repo, err := repoParam(in)
		if err != nil {
			return core.Result[any]{}, err
		}
		return summarize([]checkResult{runCheck(ctx, c, repo)}), nil
	}
}

// runAll runs every check, then reports each check whose unit has not landed as not_measured.
func runAll(ctx context.Context, in core.Input) (core.Result[any], error) {
	repo, err := repoParam(in)
	if err != nil {
		return core.Result[any]{}, err
	}
	var results []checkResult
	for _, c := range checks() {
		results = append(results, runCheck(ctx, c, repo))
	}
	for _, name := range unmeasured() {
		results = append(results, checkResult{Check: name, Result: resultNotMeasured,
			Detail: "the check has not landed yet"})
	}
	return summarize(results), nil
}

// runCheck runs c on repo; an error is the result error, never green (DR-13).
func runCheck(ctx context.Context, c check, repo string) checkResult {
	found, err := c.run(ctx, repo)
	switch {
	case err != nil:
		return checkResult{Check: c.name, Result: resultError, Detail: err.Error()}
	case len(found) > 0:
		return checkResult{Check: c.name, Result: resultRed, Findings: found}
	}
	return checkResult{Check: c.name, Result: resultGreen}
}

// summarize maps the results to one envelope: all green is ok, any red is red, and anything else
// is incomplete; only ok exits 0.
func summarize(results []checkResult) core.Result[any] {
	res := core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: report{Checks: results}}
	var lines []string
	red, other := false, false
	for _, r := range results {
		lines = append(lines, describe(r))
		red = red || r.Result == resultRed
		other = other || (r.Result != resultGreen && r.Result != resultRed)
	}
	switch {
	case red:
		res.Status, res.Outcome = core.StatusRed, outcomeRed
	case other:
		res.Status, res.Outcome = core.StatusError, outcomeIncomplete
	}
	res.Detail = strings.Join(lines, "\n")
	return res
}

// describe renders one check result for the text output.
func describe(r checkResult) string {
	line := r.Check + ": " + r.Result
	if r.Detail != "" {
		line += " (" + r.Detail + ")"
	}
	for _, f := range r.Findings {
		line += fmt.Sprintf("\n  %s %s:%d", f.Rule, f.Path, f.Line)
	}
	return line
}

// runSize checks the size of every Go file of repo (SPEC §5.9).
func runSize(_ context.Context, repo string) ([]repocheck.Finding, error) {
	files, err := repocheck.GoFiles(repo)
	if err != nil {
		return nil, err
	}
	return repocheck.Size(files), nil
}

// runSourceRules checks every Go file of repo against the source rules (DR-25, SPEC §5.6).
func runSourceRules(_ context.Context, repo string) ([]repocheck.Finding, error) {
	files, err := repocheck.GoFiles(repo)
	if err != nil {
		return nil, err
	}
	cfg := repocheck.SourceRulesConfig{Product: core.Product, ProductFile: productFile}
	var found []repocheck.Finding
	for _, f := range repocheck.SourceRules(cfg, files) {
		found = append(found, repocheck.Finding{Rule: f.Rule, Path: f.Path, Line: f.Line})
	}
	return found, nil
}

// runBoundaries compares the imports of repo with the module table (DR-24, SPEC §6.1).
func runBoundaries(ctx context.Context, repo string) ([]repocheck.Finding, error) {
	out, err := repocheck.RunGo(ctx, repo, "list", "-deps", "-json", "./...")
	if err != nil {
		return nil, err
	}
	edges := map[string][]string{}
	for _, m := range core.Modules() {
		edges[m.Package] = m.Imports
	}
	return repocheck.Boundaries(edges, out)
}

// runDeps checks the modules linked into the binaries of repo against the allow-list (DR-41).
func runDeps(ctx context.Context, repo string) ([]repocheck.Finding, error) {
	out, err := repocheck.RunGo(ctx, repo, "list", "-deps", "-json", "./cmd/...")
	if err != nil {
		return nil, err
	}
	return repocheck.Deps(out)
}

// runDoctor reports each prerequisite; a missing one makes the result red (SPEC §12.2).
func runDoctor(ctx context.Context, in core.Input) (core.Result[any], error) {
	repo, err := repoParam(in)
	if err != nil {
		return core.Result[any]{}, err
	}
	prereqs, err := repocheck.Doctor(repocheck.MachineDoctorEnv(ctx, repo))
	if err != nil {
		return core.Result[any]{}, err
	}
	res := core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: prereqs}
	var lines []string
	for _, p := range prereqs {
		if p.Present {
			lines = append(lines, p.Name+": present")
			continue
		}
		lines = append(lines, p.Name+": missing; install: "+p.Install)
		res.Status, res.Outcome = core.StatusRed, outcomeRed
	}
	res.Detail = strings.Join(lines, "\n")
	return res, nil
}
