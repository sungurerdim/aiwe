package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
)

// init records the working directory of a reproduction child. The engine starts this test binary
// with verifyTestEnv set and the record path, then the cwd file, as its arguments (see
// verify_test.go). This file is compiled before verify_test.go, so this init writes the directory
// the process was started in and returns; that helper then records the run and exits. A test that
// only reads the clone field the engine copied still passes when the child was started elsewhere.
func init() {
	if os.Getenv(verifyTestEnv()) != "1" || len(os.Args) < 3 {
		return
	}
	dir, err := os.Getwd()
	if err != nil {
		os.Exit(8)
	}
	if err := os.WriteFile(os.Args[2], []byte(dir), 0o600); err != nil {
		os.Exit(9)
	}
}

// reproTestSpec returns the reproduction the engine must run for the finding of reviewTestReview:
// this test binary, told by verifyTestEnv to record its run and exit 4 (see verify_test.go). tip is
// the commit the reproduction must run against.
func reproTestSpec(tip, record string) ReproSpec {
	return ReproSpec{
		Finding:   reviewTestFinding,
		Tip:       tip,
		ReviewRun: reviewTestRunID,
		Command:   append(verifyTestStep(record).Command, reproTestCwdFile(record)),
	}
}

// reproTestCwdFile is the file the reproduction child writes its working directory into.
func reproTestCwdFile(record string) string { return record + ".cwd" }

// reproTestTip writes the unit's tip on top of the fixture repository and returns its id.
func reproTestTip(t *testing.T, r engineTestRepo) string {
	t.Helper()
	engineTestWrite(t, filepath.Join(r.dir, engineTestReadme), "# readme\n\nthe unit's tip\n")
	engineTestGit(t, r.dir, "add", engineTestReadme)
	engineTestGit(t, r.dir, "commit", "-q", "-m", "feat(core): the tip the finding affects")
	return engineTestGit(t, r.dir, "rev-parse", "HEAD")
}

// reproTestRanDir returns the working directory the reproduction child recorded. A missing file
// means the child never ran, so the directory was not measured (DR-6).
func reproTestRanDir(t *testing.T, record string) string {
	t.Helper()
	data, err := os.ReadFile(reproTestCwdFile(record))
	if err != nil {
		t.Fatalf("catches a reproduction whose working directory was not recorded: %v", err)
	}
	dir := strings.TrimRight(string(data), "\r\n")
	if dir == "" {
		t.Fatal("catches a reproduction that recorded an empty working directory")
	}
	return dir
}

// reproTestNoRun fails when the reproduction command started: a refused reproduction leaves the
// record the helper writes uncreated (verify_test.go).
func reproTestNoRun(t *testing.T, record string) {
	t.Helper()
	if _, err := os.Stat(record); err == nil {
		t.Errorf("catches a refused reproduction that still ran its command")
	}
}

// TestReproRunsInTheEngineOwnScratchCloneOfTheTip proves the done criterion of #69 and its second To
// do: the engine runs a finding's reproduction itself, in its own fresh scratch clone of the tip,
// and records where it ran (SPEC §9.1, §6.5, DR-8, DR-10). It catches a reproduction the reviewer's
// run made itself and one run outside a scratch clone of the tip.
func TestReproRunsInTheEngineOwnScratchCloneOfTheTip(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])
	tip := reproTestTip(t, r)
	owner := engineTestOwner(t, roots)

	t.Run("the engine runs the reproduction in its own clone of the tip", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "ran.txt")
		clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: tip, RunID: engineTestRunID})
		if err != nil {
			t.Fatalf("NewClone: %v", err)
		}
		run, err := Reproduce(ctx, &gates.Runner{Owner: owner}, clone, reproTestSpec(tip, record))
		if err != nil {
			t.Fatalf("Reproduce: %v", err)
		}
		if got := verifyTestRuns(t, record); got != 1 {
			t.Fatalf("catches a reproduction the engine did not run: %d runs", got)
		}
		if run.RunID != engineTestRunID || run.RunID == reviewTestRunID {
			t.Errorf("Reproduce.RunID = %q, want the engine's own run %q", run.RunID, engineTestRunID)
		}
		if run.Finding != reviewTestFinding || run.Tip != tip {
			t.Errorf("Reproduce = %+v, want the finding %q at the tip %q", run, reviewTestFinding, tip)
		}
		if run.ExitCode != 4 || !strings.Contains(run.Tail, "verify detail tail") {
			t.Errorf("Reproduce = %+v, want the reproduction's own exit code and output tail", run)
		}
		if got := reproTestRanDir(t, record); got != run.Clone {
			t.Errorf("the reproduction ran in %q, want the engine's own scratch clone %q", got, run.Clone)
		}
		if run.Clone == "" || !engineTestUnder(roots.Temp, run.Clone) || run.Clone == r.dir {
			t.Errorf("Reproduce.Clone = %q, want its own scratch clone under the temp root", run.Clone)
		}
		head, err := core.Git(ctx, run.Clone, "rev-parse", "HEAD")
		if err != nil {
			t.Fatalf("read the head of the reproduction clone: %v", err)
		}
		if got := strings.TrimSpace(head.Stdout); got != tip {
			t.Errorf("the reproduction clone is at %q, want the unit's tip %q", got, tip)
		}
	})

	t.Run("a clone the reviewer's run made is refused", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "ran.txt")
		reviewerClone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: tip, RunID: reviewTestRunID})
		if err != nil {
			t.Fatalf("NewClone: %v", err)
		}
		_, err = Reproduce(ctx, &gates.Runner{Owner: owner}, reviewerClone, reproTestSpec(tip, record))
		if !errors.Is(err, ErrReproReviewerRun) {
			t.Fatalf("Reproduce in the reviewer's own clone = %v, want ErrReproReviewerRun", err)
		}
		reproTestNoRun(t, record)
	})

	t.Run("a finding without its review run is refused", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "ran.txt")
		clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: tip, RunID: engineTestRunID})
		if err != nil {
			t.Fatalf("NewClone: %v", err)
		}
		spec := reproTestSpec(tip, record)
		spec.ReviewRun = ""
		_, err = Reproduce(ctx, &gates.Runner{Owner: owner}, clone, spec)
		var refusal *core.Error
		if !errors.As(err, &refusal) {
			t.Fatalf("Reproduce without the review's run = %v, want a *core.Error that refuses it", err)
		}
		if refusal.Outcome != core.OutcomeError {
			t.Fatalf("Reproduce without the review's run outcome = %q, want %q", refusal.Outcome, core.OutcomeError)
		}
		reproTestNoRun(t, record)
	})

	t.Run("a clone of another commit is refused", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "ran.txt")
		baseClone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestRunID})
		if err != nil {
			t.Fatalf("NewClone: %v", err)
		}
		_, err = Reproduce(ctx, &gates.Runner{Owner: owner}, baseClone, reproTestSpec(tip, record))
		if !errors.Is(err, ErrReproOutsideTip) {
			t.Fatalf("Reproduce in a clone of the base = %v, want ErrReproOutsideTip", err)
		}
		reproTestNoRun(t, record)
	})

	t.Run("a spec that names no finding is refused", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "ran.txt")
		clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: tip, RunID: engineTestRunID})
		if err != nil {
			t.Fatalf("NewClone: %v", err)
		}
		spec := reproTestSpec(tip, record)
		spec.Finding = ""
		_, err = Reproduce(ctx, &gates.Runner{Owner: owner}, clone, spec)
		var refusal *core.Error
		if !errors.As(err, &refusal) || refusal.Outcome != core.OutcomeError {
			t.Fatalf("Reproduce without a finding = %v, want a *core.Error that refuses it", err)
		}
		reproTestNoRun(t, record)
	})

	t.Run("the engine's own recorded run is the evidence of the finding", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "ran.txt")
		clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: tip, RunID: engineTestRunID})
		if err != nil {
			t.Fatalf("NewClone: %v", err)
		}
		run, err := Reproduce(ctx, &gates.Runner{Owner: owner}, clone, reproTestSpec(tip, record))
		if err != nil {
			t.Fatalf("Reproduce: %v", err)
		}
		review := reviewTestReview()
		review.Findings[0].Reproduction = run.Command
		report, err := Review(review, []ReproRun{run}, tip)
		if err != nil {
			t.Fatalf("Review of the engine's own recorded run: %v", err)
		}
		if len(report.Findings) != 1 {
			t.Fatalf("Review judged %d findings, want 1", len(report.Findings))
		}
		got := report.Findings[0]
		if got.Result != gates.ResultRed || got.RunID != engineTestRunID || got.ExitCode != 4 {
			t.Errorf("catches a review the engine's own recorded run did not judge: %+v", got)
		}
		if !report.Blocks() {
			t.Errorf("catches a finding the engine's own run reproduced that does not block its unit")
		}
	})
}
