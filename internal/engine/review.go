// Package engine judges the findings of a security review against the reproductions the engine
// itself ran: a finding is a claim until the engine reproduced it (SPEC §9.1, DR-8).
package engine

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
)

// Finding is one finding of a security review (SPEC §9.1): the location of the defect, the
// reproduction the engine must run, and the severity the review claims. The claim is the review's;
// only the engine's own run of the reproduction is evidence.
type Finding struct {
	Location     string   // the path and line the finding names
	Reproduction []string // the argv the engine runs to reproduce it
	Severity     string   // the severity the review claims
}

// SecurityReview is one security-reviewer run: the threat model it states and the findings it
// reports (SPEC §9.1). The reviewer fixes nothing and runs no reproduction (DR-8).
type SecurityReview struct {
	RunID       string    // the run that reported the findings
	ThreatModel string    // its one-line threat model
	Findings    []Finding // the findings it reported
}

// Judgement is the engine's judgement of one finding (SPEC §9.1): the location of the defect, the
// reproduction the engine ran, the result of that run and the severity the review claims. The
// result word is the engine's, never the review's: red when the engine's own run reproduced the
// defect, green when its own run showed no defect, and not_measured when the engine holds no run of
// the finding at the tip (DR-6).
type Judgement struct {
	Location     string       // the path and line the finding names
	Reproduction []string     // the argv of the reproduction the engine ran
	Severity     string       // the severity the review claims
	Result       gates.Result // red, green or not_measured
	RunID        string       // the engine's own run; empty when no run was read
	ExitCode     int          // the exit code the engine's run exited with; exitUnknown when not measured
}

// exitUnknown is the exit code of a finding the engine did not measure: no reproduction ran, or
// the run never exited. Unmeasured is never 0 (DR-6). It is the same value a gate step records
// when it never exited.
const exitUnknown = -1

// ReviewReport is the judgement of one security review: one judgement per finding, bound to the
// reviewing run and to the tip the findings had to be reproduced at (SPEC §9.1, §9.3).
type ReviewReport struct {
	RunID    string      // the reviewing run this judgement belongs to
	Tip      string      // the tip the findings had to be reproduced at
	Findings []Judgement // one judgement per finding of the review
}

// Blocks reports whether the judgement holds the unit back (SPEC §9.1, DR-6): a finding the
// engine's own run reproduced (red) blocks, and a finding the engine did not measure (not_measured)
// blocks until its reproduction runs, so an unreproduced claim never lets a unit pass. A report
// that judges no finding blocks too: absence is not evidence. Only a review whose every finding the
// engine's own run showed green lets the unit pass.
func (r ReviewReport) Blocks() bool {
	if len(r.Findings) == 0 {
		return true
	}
	for _, finding := range r.Findings {
		if finding.Result != gates.ResultGreen {
			return true
		}
	}
	return false
}

// Review judges every finding of a security review by the reproductions the engine ran itself
// (SPEC §9.1, DR-8). A finding is red only when the engine's own run of the finding's own argv, at
// the unit's tip, in that run's own scratch clone, exits non-zero, so a finding blocks on evidence
// and never on the reviewer's claim. A finding the engine holds no such run of is not_measured
// (§9.1, DR-6), and a review that names no run, no tip or no finding, or a finding without a
// location, cannot be judged at all and is refused (SPEC §5.3).
func Review(review SecurityReview, repros []ReproRun, tip string) (ReviewReport, error) {
	if err := checkReview(review, tip); err != nil {
		return ReviewReport{}, err
	}
	report := ReviewReport{RunID: review.RunID, Tip: tip, Findings: make([]Judgement, 0, len(review.Findings))}
	for _, finding := range review.Findings {
		report.Findings = append(report.Findings, judge(finding, review.RunID, repros, tip))
	}
	return report, nil
}

// judge returns the engine's judgement of one finding: the finding's own reproduction, run by the
// engine itself at the tip, in that run's own clone, is the only evidence (SPEC §9.1, DR-8, DR-10).
// A run that exits non-zero reproduced the defect and is red; one that exits 0 showed no defect and
// is green; without such a run, or with one that never exited, the finding is not_measured and its
// exit code is exitUnknown, never 0 (DR-6).
func judge(finding Finding, reviewerRun string, repros []ReproRun, tip string) Judgement {
	judged := Judgement{
		Location:     finding.Location,
		Reproduction: slices.Clone(finding.Reproduction),
		Severity:     finding.Severity,
		Result:       gates.ResultNotMeasured,
		ExitCode:     exitUnknown,
	}
	run, ok := engineReproduction(finding, reviewerRun, repros, tip)
	if !ok || run.ExitCode < 0 {
		return judged
	}
	judged.RunID = run.RunID
	judged.ExitCode = run.ExitCode
	judged.Result = gates.ResultGreen
	if run.ExitCode != 0 {
		judged.Result = gates.ResultRed
	}
	return judged
}

// engineReproduction returns the reproduction of one finding that the engine ran itself: one of the
// finding's own argv, at the tip the finding affects, by a run other than the reviewing run, in the
// run's own clone of that tip. A reproduction the review made or in the review's clone, one of
// another commit or another command, one with no recorded clone, and a finding that names no
// reproduction, prove nothing (SPEC §9.1, DR-8, DR-10).
func engineReproduction(finding Finding, reviewerRun string, repros []ReproRun, tip string) (ReproRun, bool) {
	for _, run := range repros {
		if run.Finding != finding.Location || run.Tip != tip || !engineRun(run, reviewerRun) {
			continue
		}
		if sameCommand(finding.Reproduction, run.Command) {
			return run, true
		}
	}
	return ReproRun{}, false
}

// engineRun reports whether run is a reproduction of the engine itself and not of the review: its
// run is known, it is not the reviewing run, and its recorded clone is the clone of that run's own
// directory, the one NewClone makes under the engine's temp root as run-<run id>-<suffix> (SPEC
// §6.5, DR-8, DR-10). A reproduction whose run or clone is unknown, or whose clone is another
// directory, is no evidence either (DR-6).
func engineRun(run ReproRun, reviewerRun string) bool {
	if run.RunID == "" || run.RunID == reviewerRun || run.Clone == "" {
		return false
	}
	dir := filepath.Base(filepath.Dir(run.Clone))
	return filepath.Base(run.Clone) == cloneDirName && strings.HasPrefix(dir, runDirName+run.RunID+"-")
}

// sameCommand reports whether an engine-run reproduction carries the argv the finding names. A
// finding without a reproduction proves nothing: the engine runs the finding's own command, never
// one the review did not ask for (SPEC §9.1).
func sameCommand(want, got []string) bool {
	return len(want) > 0 && slices.Equal(want, got)
}

// checkReview refuses a review the engine cannot judge: one that names no reviewing run, no tip or
// no finding, and one whose finding names no location. The engine could not tell the review's own
// reproduction from its own, could not bind the judgement to a tip, or would judge nothing at all,
// which is not evidence (SPEC §5.3, DR-6).
func checkReview(review SecurityReview, tip string) error {
	switch {
	case review.RunID == "":
		return &core.Error{Outcome: core.OutcomeError, Expected: "the run id of the security review",
			Found: "a review without a run id",
			Next:  "record the run the review belongs to, then judge the review again"}
	case tip == "":
		return &core.Error{Outcome: core.OutcomeError, Expected: "the tip of the unit the findings affect",
			Found: "an empty tip", Next: "pass the tip the findings must be reproduced at, then judge the review again"}
	case len(review.Findings) == 0:
		return &core.Error{Outcome: core.OutcomeError, Expected: "at least one finding of the security review",
			Found: "a review without a finding", Next: "record each finding of the review, then judge the review again"}
	}
	for _, finding := range review.Findings {
		if finding.Location == "" {
			return &core.Error{Outcome: core.OutcomeError, Expected: "the location of every finding",
				Found: "a finding without a location",
				Next:  "name the path and line the finding attacks, then judge the review again"}
		}
	}
	return nil
}
