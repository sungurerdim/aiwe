package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/tracker"
)

// The realistic values of the integration tests of [sk-12] (SPEC §7.3 step 11, DR-14).
const (
	integrateTestBranch = "unit/41-integrate"
	integrateTestOwner  = "acme"
	integrateTestName   = "widget"
	integrateTestIssue  = 41
)

// fakeIntegrator is the tracker the integration tests drive: it records every pull request and every
// merge it is asked for, and answers CIStatus with one state (SPEC §5.13: a fake stands in for the
// port, never for the behaviour the test measures).
type fakeIntegrator struct {
	state  CIState
	prs    []PullRequest
	merges []MergeRequest
	ciSHAs []string
}

func (f *fakeIntegrator) ID() string { return "fake" }

func (f *fakeIntegrator) CreatePullRequest(_ context.Context, pr PullRequest) (int, error) {
	f.prs = append(f.prs, pr)
	return len(f.prs), nil
}

func (f *fakeIntegrator) CIStatus(_ context.Context, _ tracker.RepoID, sha string) (CIState, error) {
	f.ciSHAs = append(f.ciSHAs, sha)
	return f.state, nil
}

func (f *fakeIntegrator) MergePullRequest(_ context.Context, req MergeRequest) error {
	f.merges = append(f.merges, req)
	return nil
}

// integrateTestRepo makes the fixture of one integration: a repository whose run branch holds one
// audited commit, and a bare origin the branch is pushed to. It returns the repository, the audited
// tip and the origin path.
func integrateTestRepo(t *testing.T) (engineTestRepo, string, string) {
	t.Helper()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])
	engineTestGit(t, r.dir, "checkout", "-q", "-b", integrateTestBranch)
	engineTestWrite(t, filepath.Join(r.dir, engineTestReadme), "# readme\n\nthe audited unit\n")
	engineTestGit(t, r.dir, "add", engineTestReadme)
	engineTestGit(t, r.dir, "commit", "-q", "-m", "feat(engine): the audited unit")
	tip := engineTestGit(t, r.dir, "rev-parse", "HEAD")
	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(origin, 0o700); err != nil {
		t.Fatal(err)
	}
	engineTestGit(t, origin, "init", "--bare", "-q")
	return r, tip, origin
}

// integrateTestRepoID is the repository the batch pull request belongs to.
func integrateTestRepoID() tracker.RepoID {
	return tracker.RepoID{Owner: integrateTestOwner, Name: integrateTestName}
}

// integrateTestUnit is the audited unit the run branch carries.
func integrateTestUnit(tip string) AuditedUnit {
	return AuditedUnit{
		Issue:  integrateTestIssue,
		Type:   "feat",
		Scope:  "engine",
		Title:  "integrate the audited unit",
		RunID:  engineTestRunID,
		Effect: "no merge → one audited unit merged",
		Tip:    tip,
	}
}

// integrateTestRequest builds the entry point call of one integration.
func integrateTestRequest(r engineTestRepo, origin string, unit AuditedUnit, tr Integrator) IntegrateRequest {
	return IntegrateRequest{
		Repo:    integrateTestRepoID(),
		Dir:     r.dir,
		Remote:  origin,
		Branch:  integrateTestBranch,
		Main:    "main",
		Unit:    unit,
		Tracker: tr,
	}
}

// integrateTestRef returns the commit a ref of repo names, or "" when it names none.
func integrateTestRef(t *testing.T, repo, ref string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "--no-pager", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// integrateTestOutcome returns the outcome the entry point refused with; a missing *core.Error is a
// finding of its own, so a refusal never passes by its text alone (SPEC §5.3).
func integrateTestOutcome(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("Integrate = nil error, want a refusal that names its outcome")
	}
	var e *core.Error
	if !errors.As(err, &e) {
		t.Fatalf("Integrate error %v, want a *core.Error that names its outcome", err)
	}
	return string(e.Outcome)
}

// TestIntegratePushesAndOpensOnePullRequest guards the To do of [sk-12] and SPEC §7.3 step 11: the
// engine pushes the run branch and opens one pull request per unit, and a green CI of the audited
// tip is the only merge. It catches an integrate that opens no pull request, opens more than one, or
// never pushes the audited tip.
func TestIntegratePushesAndOpensOnePullRequest(t *testing.T) {
	ctx := t.Context()
	r, tip, origin := integrateTestRepo(t)
	fake := &fakeIntegrator{state: CIGreen}

	report, err := Integrate(ctx, integrateTestRequest(r, origin, integrateTestUnit(tip), fake))
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if len(fake.prs) != 1 {
		t.Fatalf("the fake tracker recorded %d pull requests, want exactly 1", len(fake.prs))
	}
	if got := integrateTestRef(t, origin, "refs/heads/"+integrateTestBranch); got != tip {
		t.Errorf("the remote run branch = %q, want the pushed audited tip %q", got, tip)
	}
	if !report.Merged {
		t.Errorf("Integrate.Merged = false, want the audited, CI-green tip merged")
	}
}

// TestIntegrateMergesAuditedGreenTipWithTrailers guards the done criterion and DR-14: the audited,
// CI-green tip is squash-merged, and the merge commit carries the Effect line, the Closes trailer
// and the Run-Id trailer. It catches a merge that loses a trailer, merges another sha, or reads CI
// for a commit other than the audited tip.
func TestIntegrateMergesAuditedGreenTipWithTrailers(t *testing.T) {
	ctx := t.Context()
	r, tip, origin := integrateTestRepo(t)
	fake := &fakeIntegrator{state: CIGreen}
	unit := integrateTestUnit(tip)

	report, err := Integrate(ctx, integrateTestRequest(r, origin, unit, fake))
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if len(fake.merges) != 1 {
		t.Fatalf("the fake tracker recorded %d merges, want exactly 1", len(fake.merges))
	}
	merge := fake.merges[0]
	if merge.SHA != tip {
		t.Errorf("merged sha = %q, want the audited tip %q", merge.SHA, tip)
	}
	if merge.Number != report.PullRequest {
		t.Errorf("merged pull request = %d, want %d", merge.Number, report.PullRequest)
	}
	for _, want := range []string{
		"Run-Id: " + unit.RunID,
		fmt.Sprintf("Closes %s/%s#%d", integrateTestOwner, integrateTestName, integrateTestIssue),
		"Effect: " + unit.Effect,
	} {
		if !strings.Contains(merge.Message, want) {
			t.Errorf("the merge commit message %q does not carry %q", merge.Message, want)
		}
	}
	if len(fake.ciSHAs) != 1 || fake.ciSHAs[0] != tip {
		t.Errorf("CI was read for %v, want the audited tip %q", fake.ciSHAs, tip)
	}
}

// TestIntegrateRefusesTipMismatch guards the done criterion and DR-14: a run branch that no longer
// equals the audited tip is refused as unaudited_commits, before any push, pull request or merge. It
// catches an integrate that merges whatever the branch holds.
func TestIntegrateRefusesTipMismatch(t *testing.T) {
	ctx := t.Context()
	r, tip, origin := integrateTestRepo(t)
	engineTestWrite(t, filepath.Join(r.dir, engineTestReadme), "# readme\n\na later, unaudited change\n")
	engineTestGit(t, r.dir, "add", engineTestReadme)
	engineTestGit(t, r.dir, "commit", "-q", "-m", "fix(engine): an unaudited change")
	fake := &fakeIntegrator{state: CIGreen}

	_, err := Integrate(ctx, integrateTestRequest(r, origin, integrateTestUnit(tip), fake))
	if got := integrateTestOutcome(t, err); got != "unaudited_commits" {
		t.Errorf("Integrate outcome = %q, want unaudited_commits", got)
	}
	if len(fake.prs) != 0 {
		t.Errorf("the fake tracker recorded %d pull requests for an unaudited tip, want 0", len(fake.prs))
	}
	if len(fake.merges) != 0 {
		t.Errorf("the fake tracker recorded %d merges for an unaudited tip, want 0", len(fake.merges))
	}
	if got := integrateTestRef(t, origin, "refs/heads/"+integrateTestBranch); got != "" {
		t.Errorf("the unaudited run branch was pushed to %q, want no push", got)
	}
}

// TestIntegrateRefusesRedCI guards the To do of [sk-12]: a red CI result is not merged and names its
// outcome. It catches an integrate that merges under a red CI.
func TestIntegrateRefusesRedCI(t *testing.T) {
	ctx := t.Context()
	r, tip, origin := integrateTestRepo(t)
	fake := &fakeIntegrator{state: CIRed}

	_, err := Integrate(ctx, integrateTestRequest(r, origin, integrateTestUnit(tip), fake))
	if got := integrateTestOutcome(t, err); got != "red" {
		t.Errorf("Integrate outcome = %q, want red", got)
	}
	if len(fake.merges) != 0 {
		t.Errorf("the fake tracker recorded %d merges under a red CI, want 0", len(fake.merges))
	}
	if len(fake.prs) != 1 {
		t.Errorf("the fake tracker recorded %d pull requests, want the one whose CI was red", len(fake.prs))
	}
}

// TestIntegrateRefusesMissingCI guards the To do of [sk-12]: a missing CI result is not merged and
// names its outcome (ci_not_observed, SPEC §7.4). It catches an integrate that reads an absent CI
// status as green.
func TestIntegrateRefusesMissingCI(t *testing.T) {
	ctx := t.Context()
	r, tip, origin := integrateTestRepo(t)
	fake := &fakeIntegrator{state: CINone}

	_, err := Integrate(ctx, integrateTestRequest(r, origin, integrateTestUnit(tip), fake))
	if got := integrateTestOutcome(t, err); got != "ci_not_observed" {
		t.Errorf("Integrate outcome = %q, want ci_not_observed", got)
	}
	if len(fake.merges) != 0 {
		t.Errorf("the fake tracker recorded %d merges without a CI result, want 0", len(fake.merges))
	}
}

// TestIntegrateRefusesRejectedPush guards SPEC §7.3 step 11 and DR-14: a remote run branch that
// already moved — main moved under the batch — refuses the push, so the integration stops as
// push_rejected and never opens or merges a pull request, and the remote branch keeps the tip it
// had. It catches an integrate that force-pushes over the remote or merges without its own push.
func TestIntegrateRefusesRejectedPush(t *testing.T) {
	ctx := t.Context()
	r, tip, origin := integrateTestRepo(t)
	tree := engineTestGit(t, r.dir, "rev-parse", tip+"^{tree}")
	remoteTip := engineTestGit(t, r.dir, "commit-tree", tree, "-m", "fix(engine): the remote branch moved")
	engineTestGit(t, r.dir, "branch", "remote-moved", remoteTip)
	engineTestGit(t, r.dir, "push", "--no-tags", origin, "remote-moved:refs/heads/"+integrateTestBranch)
	fake := &fakeIntegrator{state: CIGreen}

	_, err := Integrate(ctx, integrateTestRequest(r, origin, integrateTestUnit(tip), fake))
	if got := integrateTestOutcome(t, err); got != "push_rejected" {
		t.Errorf("Integrate outcome = %q, want push_rejected", got)
	}
	if len(fake.prs) != 0 {
		t.Errorf("the fake tracker recorded %d pull requests for a refused push, want 0", len(fake.prs))
	}
	if len(fake.merges) != 0 {
		t.Errorf("the fake tracker recorded %d merges for a refused push, want 0", len(fake.merges))
	}
	if got := integrateTestRef(t, origin, "refs/heads/"+integrateTestBranch); got != remoteTip {
		t.Errorf("the remote run branch = %q, want the tip it already held %q: no force push", got, remoteTip)
	}
}
