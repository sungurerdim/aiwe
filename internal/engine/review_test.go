package engine

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
	"github.com/sungurerdim/aiwe/internal/stacks"
)

// The realistic values of the security-review tests (SPEC §9.1, DR-8, DR-15).
const (
	reviewTestRunID    = "01J8Z5R2W6Q1K7YV3M0F2T9C4E" // the security-reviewer run that reported the finding
	reviewTestReproRun = "01J8Z5R2W6Q1K7YV3M0F2T9C4F" // the engine's own reproduction run
	reviewTestTip      = "3f6a1c0b9e8d7c6b5a4938271605f4e3d2c1b0a9"
	reviewTestBase     = "0c5b4a39281706f5e4d3c2b1a097685746352413"
	reviewTestFinding  = "internal/core/env.go:27"
	reviewTestReproCmd = "go test -run TestCleanEnvSecretsDropped ./internal/core/"
	// reviewTestReproDir is the shape NewClone makes for a run: run-<run id>-<random suffix>
	// (core.CreateOwned appends the suffix), with the clone inside it (SPEC §6.5).
	reviewTestReproDir = runDirName + reviewTestReproRun + "-2f0c9d1a4b6e"
	reviewTestRunDir   = runDirName + reviewTestRunID + "-7b3e5a9c1d2f"
)

// reviewTestReview returns the security-reviewer run the tests decide on: one finding whose
// reproduction only the engine may run (SPEC §9.1).
func reviewTestReview() SecurityReview {
	return SecurityReview{
		RunID:       reviewTestRunID,
		ThreatModel: "the change lets an attacker read a credential the base kept out of the child",
		Findings: []Finding{{
			Location:     reviewTestFinding,
			Reproduction: strings.Fields(reviewTestReproCmd),
			Severity:     "high",
		}},
	}
}

// reviewTestRepro returns the engine's own reproduction of the finding, from its own scratch clone
// of the unit's tip. The reproduction fails while the defect exists, so it exits 1 and the engine
// read a defect (SPEC §9.1).
func reviewTestRepro() ReproRun {
	return ReproRun{
		Finding:  reviewTestFinding,
		Tip:      reviewTestTip,
		Command:  strings.Fields(reviewTestReproCmd),
		RunID:    reviewTestReproRun,
		Clone:    filepath.Join(reviewTestReproDir, cloneDirName),
		ExitCode: 1,
		Tail:     "--- FAIL: TestCleanEnvSecretsDropped\nFAIL",
	}
}

// reviewTestJudged returns the one judgement of a report; a report that judges another number of
// findings is a finding of its own.
func reviewTestJudged(t *testing.T, report ReviewReport) Judgement {
	t.Helper()
	if len(report.Findings) != 1 {
		t.Fatalf("Review judged %d findings, want 1", len(report.Findings))
	}
	return report.Findings[0]
}

// reviewTestRefused fails unless the engine refused the input with a *core.Error that names its
// outcome and its next action; a missing error, or one without them, is a finding of its own
// (SPEC §5.3).
func reviewTestRefused(t *testing.T, err error) {
	t.Helper()
	var refusal *core.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("Review error = %v, want a *core.Error that refuses the input", err)
	}
	if refusal.Outcome != core.OutcomeError {
		t.Errorf("Review refusal outcome = %q, want %q", refusal.Outcome, core.OutcomeError)
	}
	if refusal.Next == "" {
		t.Errorf("Review refusal carries no next action: %+v", refusal)
	}
}

// TestReviewJudgesEveryFindingByTheEngineOwnRun proves the done criterion of #69 and its first To
// do: every security finding that affects a unit is decided by a reproduction the engine itself ran
// in a scratch clone of the tip, so a finding without one is not_measured and never lets the unit
// pass (SPEC §9.1, DR-6, DR-8, DR-10). It catches a review that accepts a finding the engine never
// reproduced, one it proved with a run that exited 0, and one that passes a unit on absence.
func TestReviewJudgesEveryFindingByTheEngineOwnRun(t *testing.T) {
	review := reviewTestReview()

	t.Run("the engine's own reproduction of the tip shows the defect", func(t *testing.T) {
		report, err := Review(review, []ReproRun{reviewTestRepro()}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if report.RunID != reviewTestRunID || report.Tip != reviewTestTip {
			t.Errorf("ReviewReport = %+v, want the reviewing run and the tip", report)
		}
		got := reviewTestJudged(t, report)
		if got.Result != gates.ResultRed || got.RunID != reviewTestReproRun || got.ExitCode != 1 {
			t.Errorf("Review judgement = %+v, want the red engine run %q", got, reviewTestReproRun)
		}
		if got.Location != reviewTestFinding || got.Severity != "high" ||
			!slices.Equal(got.Reproduction, strings.Fields(reviewTestReproCmd)) {
			t.Errorf("Review judgement = %+v, want the finding, its reproduction and its severity", got)
		}
		if !report.Blocks() {
			t.Errorf("catches a reproduced finding that does not block its unit")
		}
	})

	t.Run("a reproduction that exits 0 showed no defect and is green", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.ExitCode = 0
		repro.Tail = "ok"
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultGreen {
			t.Errorf("Review judgement = %+v, want green: the engine's run showed no defect", got)
		}
		if report.Blocks() {
			t.Errorf("catches a green finding that blocks its unit")
		}
	})

	t.Run("a finding the engine never ran is not_measured and blocks", func(t *testing.T) {
		report, err := Review(review, nil, reviewTestTip)
		if err != nil {
			t.Fatalf("Review judged an unreproduced finding as %v, want the not_measured judgement of SPEC §9.1", err)
		}
		got := reviewTestJudged(t, report)
		if got.Result != gates.ResultNotMeasured || got.RunID != "" || got.ExitCode != exitUnknown {
			t.Errorf("Review judgement = %+v, want not_measured with exit %d: an unreproduced finding is never exit 0", got, exitUnknown)
		}
		if !report.Blocks() {
			t.Errorf("catches an unreproduced finding that lets its unit pass (DR-6)")
		}
	})

	t.Run("a reproduction that never exited is not measured", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.ExitCode = exitUnknown
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		got := reviewTestJudged(t, report)
		if got.Result != gates.ResultNotMeasured || got.ExitCode != exitUnknown {
			t.Errorf("Review judgement = %+v, want not_measured with exit %d: a run that never exited is no evidence", got, exitUnknown)
		}
	})

	t.Run("a reproduction the reviewer ran itself is not measured", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.RunID = reviewTestRunID
		repro.Clone = filepath.Join(reviewTestRunDir, cloneDirName)
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultNotMeasured {
			t.Errorf("Review judgement = %+v, want not_measured: the reviewer's run is no evidence", got)
		}
	})

	t.Run("a reproduction of another commit is not measured", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.Tip = reviewTestBase
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultNotMeasured {
			t.Errorf("Review judgement = %+v, want not_measured: another commit holds no evidence", got)
		}
	})

	t.Run("a reproduction with no recorded clone is not measured", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.Clone = ""
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultNotMeasured {
			t.Errorf("Review judgement = %+v, want not_measured: a run with no clone is no evidence", got)
		}
	})

	t.Run("a reproduction in a directory that is not the run's own is not measured", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.Clone = filepath.Join(scratchDirName+reviewTestReproRun, cloneDirName)
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultNotMeasured {
			t.Errorf("Review judgement = %+v, want not_measured: the run's own clone is the evidence", got)
		}
	})

	t.Run("a reproduction of another command is not measured", func(t *testing.T) {
		repro := reviewTestRepro()
		repro.Command = []string{"go", "test", "./internal/gates/"}
		report, err := Review(review, []ReproRun{repro}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultNotMeasured {
			t.Errorf("Review judgement = %+v, want not_measured: the engine ran another command", got)
		}
	})

	t.Run("a finding that names no argv is not measured", func(t *testing.T) {
		review := reviewTestReview()
		review.Findings[0].Reproduction = nil
		report, err := Review(review, []ReproRun{reviewTestRepro()}, reviewTestTip)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if got := reviewTestJudged(t, report); got.Result != gates.ResultNotMeasured {
			t.Errorf("Review judgement = %+v, want not_measured: the finding names no reproduction", got)
		}
	})

	t.Run("a review without a finding is refused", func(t *testing.T) {
		review := reviewTestReview()
		review.Findings = nil
		_, err := Review(review, []ReproRun{reviewTestRepro()}, reviewTestTip)
		reviewTestRefused(t, err)
	})

	t.Run("a finding without a location is refused", func(t *testing.T) {
		review := reviewTestReview()
		review.Findings[0].Location = ""
		_, err := Review(review, []ReproRun{reviewTestRepro()}, reviewTestTip)
		reviewTestRefused(t, err)
	})

	t.Run("a review without a run id is refused", func(t *testing.T) {
		review := reviewTestReview()
		review.RunID = ""
		_, err := Review(review, []ReproRun{reviewTestRepro()}, reviewTestTip)
		reviewTestRefused(t, err)
	})

	t.Run("a review without the tip is refused", func(t *testing.T) {
		_, err := Review(reviewTestReview(), []ReproRun{reviewTestRepro()}, "")
		reviewTestRefused(t, err)
	})
}

// TestDetectTheUnitThatNeedsTheSecurityReview proves the engine's own side of the role table of
// SPEC §8.2 and internal/stacks/profiles.go (DR-15): a unit whose diff holds a security segment or
// one of the stack's security paths needs the security review whose findings this unit's engine
// judges, and a path outside the table does not (SPEC §7.3 step 4, §9.1). It catches a segment or
// stack pattern the engine stopped matching, a match that catches an ordinary path, and an unknown
// stack decided instead of refused (DR-6).
func TestDetectTheUnitThatNeedsTheSecurityReview(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		paths   []string
		needs   bool
	}{
		{name: "a credential segment needs the review", profile: "go",
			paths: []string{"internal/core/env.go", "internal/auth/session.go"}, needs: true},
		{name: "a segment that only contains a credential word needs the review", profile: "go",
			paths: []string{"web/token-store/token.go"}, needs: true},
		{name: "a segment whose case differs still needs the review", profile: "go",
			paths: []string{"internal/Session/store.go"}, needs: true},
		{name: "the stack's own security path needs the review", profile: "go",
			paths: []string{"internal/api/user_handler.go"}, needs: true},
		{name: "a path outside both tables needs no review", profile: "go",
			paths: []string{"internal/core/env.go", "docs/readme.md"}, needs: false},
		{name: "a unit without a path needs no review", profile: "go", paths: nil, needs: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NeedsSecurityReview(tc.profile, tc.paths)
			if err != nil {
				t.Fatalf("NeedsSecurityReview(%q, %v): %v", tc.profile, tc.paths, err)
			}
			if got != tc.needs {
				t.Errorf("NeedsSecurityReview(%q, %v) = %v, want %v", tc.profile, tc.paths, got, tc.needs)
			}
		})
	}

	t.Run("an unknown stack is refused", func(t *testing.T) {
		_, err := NeedsSecurityReview("rust", []string{"src/auth.rs"})
		reviewTestRefused(t, err)
	})

	t.Run("a stack pattern the engine cannot read takes the review", func(t *testing.T) {
		profile := stacks.Profile{ID: "unreadable", SecurityPaths: []string{"["}}
		if !securityPath(profile, "internal/core/env.go") {
			t.Errorf("securityPath = false, want true: a pattern the engine cannot read decides nothing (DR-6)")
		}
	})
}

// TestProfileDerivesTheGoStackAndItsSecurityTable covers internal/stacks/profiles.go, the row the
// engine reads for the security review of a unit (SPEC §5.13): the Go profile detects the stack,
// derives its three gate steps, and the tables of SPEC §8.2 name the credential segments and the
// stack's own input entry point that add the security-reviewer role (DR-15). It catches a row that
// stops detecting the stack, a derived step that loses its argv or its output expectation, a
// segment table that stops matching a credential path or starts matching a document, and a
// profile lookup that answers for a stack the table does not hold.
func TestProfileDerivesTheGoStackAndItsSecurityTable(t *testing.T) {
	profile, ok := stacks.ProfileByID("go")
	if !ok {
		t.Fatalf("ProfileByID(go) = no profile, want the Go row of SPEC §8.2")
	}
	if !slices.Equal(profile.Detect, []string{"go.mod"}) {
		t.Errorf("go Detect = %v, want go.mod", profile.Detect)
	}
	if !slices.Equal(profile.Restore, []string{"go", "mod", "download"}) {
		t.Errorf("go Restore = %v, want go mod download", profile.Restore)
	}
	steps := map[string]struct {
		command       []string
		outputRejects string
	}{
		"vet":    {command: []string{"go", "vet", "./..."}},
		"format": {command: []string{"gofmt", "-l", "."}, outputRejects: `\S`},
		"test":   {command: []string{"go", "test", "./..."}},
	}
	for _, step := range profile.Gate {
		want, known := steps[step.ID]
		if !known {
			t.Errorf("the go profile derives the unknown gate step %q", step.ID)
			continue
		}
		if !slices.Equal(step.Command, want.command) {
			t.Errorf("the %s step = %v, want %v", step.ID, step.Command, want.command)
		}
		if step.OutputRejects != want.outputRejects {
			t.Errorf("the %s step OutputRejects = %q, want %q", step.ID, step.OutputRejects, want.outputRejects)
		}
		delete(steps, step.ID)
	}
	for id := range steps {
		t.Errorf("the go profile derives no %q gate step", id)
	}
	if !slices.Contains(profile.SecurityPaths, "*handler*.go") {
		t.Errorf("go SecurityPaths = %v, want *handler*.go", profile.SecurityPaths)
	}
	segments := stacks.SecuritySegments()
	for _, want := range []string{"auth", "login", "session", "token", "secret", "credential",
		"password", "crypto", "permission", "oauth", "webhook"} {
		if !slices.Contains(segments, want) {
			t.Errorf("SecuritySegments() = %v, want the segment %q", segments, want)
		}
	}
	if slices.Contains(segments, "docs") {
		t.Errorf("SecuritySegments() = %v, want only the credential segments", segments)
	}
	if _, ok := stacks.ProfileByID("rust"); ok {
		t.Errorf("ProfileByID(rust) = a profile, want an unknown stack")
	}
}
