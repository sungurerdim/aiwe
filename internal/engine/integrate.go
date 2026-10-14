// Package engine integrates one audited unit in integrate.go: it pushes the run branch, opens the
// one batch pull request, reads the CI result of the audited tip and merges that tip only while it
// still equals the tip the auditor audited under a green CI (SPEC §7.3 step 11, DR-14). A red or
// absent CI result stops the integration and names its outcome (§7.4). Push, pull request and merge
// belong to the engine, never to a run's agent (DR-13).
package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/tracker"
)

// maxTitleRunes is the §12.3 limit the whole pull request title is cut to, counted in runes.
const maxTitleRunes = 50

// AuditedUnit is one unit of a batch whose tip an auditor audited: the issue it closes, the head of
// its merge commit and the run whose verdict the tip carries (SPEC §7.3 step 11, DR-14).
type AuditedUnit struct {
	Issue  int    // the unit issue the merge closes
	Type   string // the Conventional Commits type of the audited commit
	Scope  string // its scope, written into the title
	Title  string // the issue title, written into the title
	RunID  string // the run that audited the tip, written into the Run-Id trailer
	Effect string // the measured effect, written into the Effect line
	Tip    string // the audited tip: the only commit that may be merged
}

// PullRequest is the one batch pull request of an integration (SPEC §7.3 step 11, §10.1).
type PullRequest struct {
	Repo  tracker.RepoID // the repository the pull request belongs to
	Base  string         // the branch it merges into
	Head  string         // the run branch it merges
	Title string         // the §7.3 step 11 title
	Body  string         // the evidence of the audited run
}

// MergeRequest is one merge of a batch pull request, pinned to the tip the auditor audited
// (SPEC §10.1). The merge method is the tracker's, never a field of this request: §10.1 pins it.
type MergeRequest struct {
	Repo    tracker.RepoID // the repository the pull request belongs to
	Number  int            // the pull request to merge
	SHA     string         // the tip the merge must land; another tip is no audited tip
	Message string         // the §12.3 message of the merge commit
}

// Integrator is the tracker port of one integration (SPEC §5.1, §10.1): the engine opens the batch
// pull request, reads the CI result of the audited tip and merges it. A tracker adapter implements
// it, and a fake stands in for it in the tests (SPEC §5.13).
type Integrator interface {
	core.Identified
	CreatePullRequest(ctx context.Context, pr PullRequest) (int, error)
	CIStatus(ctx context.Context, repo tracker.RepoID, sha string) (CIState, error)
	MergePullRequest(ctx context.Context, req MergeRequest) error
}

// IntegrateRequest is one integration: the run clone that holds the audited branch, the remote it is
// pushed to, and the unit whose tip the auditor audited.
type IntegrateRequest struct {
	Repo    tracker.RepoID // the repository the pull request belongs to
	Dir     string         // the run clone that holds the run branch
	Remote  string         // the remote the run branch is pushed to
	Branch  string         // the run branch
	Main    string         // the branch the pull request merges into
	Unit    AuditedUnit    // the audited unit this integration merges
	Tracker Integrator     // the tracker that opens and merges the pull request
}

// IntegrateReport is what one integration did: the batch pull request it opened and whether the
// audited tip reached the main branch (SPEC §7.3 step 11).
type IntegrateReport struct {
	PullRequest int  // the pull request the tracker opened for this integration
	Merged      bool // whether the audited, CI-green tip was merged
}

// Integrate takes one audited unit to a merged pull request: it refuses a request that names no
// tracker, a run branch that no longer equals the audited tip as unaudited_commits before it touches
// the remote, then pushes the run branch, opens the one batch pull request, reads the CI result of
// the audited tip and merges that tip only under a green result (SPEC §7.3 step 11, DR-14). A red CI
// result is refused as red and an absent one as ci_not_observed, and neither is merged (§7.4). A
// refusal of a tracker write, of the CI read or of the merge is returned with its cause.
func Integrate(ctx context.Context, req IntegrateRequest) (IntegrateReport, error) {
	tr := req.Tracker
	if tr == nil {
		return IntegrateReport{}, errors.New("integrate: no tracker")
	}
	if err := checkIntegrateRequest(req); err != nil {
		return IntegrateReport{}, err
	}
	if err := checkAuditedTip(ctx, req); err != nil {
		return IntegrateReport{}, err
	}
	if err := pushBranch(ctx, req.Dir, req.Remote, req.Branch); err != nil {
		return IntegrateReport{}, err
	}
	number, err := tr.CreatePullRequest(ctx, pullRequest(req))
	if err != nil {
		return IntegrateReport{}, fmt.Errorf("open the pull request of the run branch %q: %w", req.Branch, err)
	}
	if err := requireGreen(ctx, tr, req.Repo, req.Unit.Tip); err != nil {
		return IntegrateReport{PullRequest: number}, err
	}
	if err := tr.MergePullRequest(ctx, mergeRequest(req, number)); err != nil {
		return IntegrateReport{PullRequest: number}, fmt.Errorf("merge the audited tip %s: %w", req.Unit.Tip, err)
	}
	return IntegrateReport{PullRequest: number, Merged: true}, nil
}

// checkIntegrateRequest refuses a request that cannot integrate one unit: a run directory that is
// no absolute path, an environment that points a git call at another repository (DR-10), and a unit
// whose merge commit could not carry the §12.3 rules. Integrate refuses a missing tracker itself, in
// the flow that reads it, so the tracker here is known to be there.
func checkIntegrateRequest(req IntegrateRequest) error {
	if !filepath.IsAbs(req.Dir) {
		return fmt.Errorf("integrate: run directory %q: want an absolute path", req.Dir)
	}
	if err := checkForeignRepo(); err != nil {
		return err
	}
	return checkUnit(req.Unit)
}

// checkUnit refuses a unit no merge may be composed from: one without the run id of its audit
// (run_id_missing, §7.4) and one that names no issue, type, title or measured effect, which the
// §7.3 step 11 title and the §12.3 message are built from. The detail names the values it found, so
// the reader sees which one the unit left empty.
func checkUnit(u AuditedUnit) error {
	if u.RunID == "" {
		return &core.Error{Outcome: "run_id_missing",
			Expected: "the run id of the run that audited the unit", Found: "an empty run id",
			Next: "start the run through the engine, or pass the run id the command needs"}
	}
	if u.Issue <= 0 || u.Type == "" || u.Title == "" || u.Effect == "" {
		return &core.Error{Outcome: core.OutcomeError,
			Expected: "a unit that carries its issue number, its type, its title and its measured effect",
			Found:    fmt.Sprintf("issue %d, type %q, title %q, effect %q", u.Issue, u.Type, u.Title, u.Effect),
			Next:     "integrate the audited unit of the issue the run worked on"}
	}
	if err := checkCommit(u.Tip); err != nil {
		return fmt.Errorf("integrate: %w", err)
	}
	return nil
}

// checkAuditedTip refuses a run branch that no longer holds the tip the auditor audited, before the
// engine touches the remote: the refusal names unaudited_commits, so no push, no pull request and no
// merge happens for an unaudited tip (DR-14). The tip is read from the branch itself, never taken
// from the caller alone.
func checkAuditedTip(ctx context.Context, req IntegrateRequest) error {
	tip, err := branchCommit(ctx, req.Dir, req.Branch)
	if err != nil {
		return err
	}
	if tip == req.Unit.Tip {
		return nil
	}
	found := "the branch " + req.Branch + " names no commit"
	if tip != "" {
		found = "the branch " + req.Branch + " holds the tip " + tip
	}
	return &core.Error{Outcome: "unaudited_commits",
		Expected: "a run branch whose tip is the audited tip " + req.Unit.Tip,
		Found:    found,
		Next:     "audit the run branch again, then integrate the tip the auditor audited"}
}

// pullRequest returns the one batch pull request of one integration: the audited run branch into the
// main branch, with the §7.3 step 11 title and the evidence of the audited run in its body.
func pullRequest(req IntegrateRequest) PullRequest {
	return PullRequest{
		Repo:  req.Repo,
		Base:  req.Main,
		Head:  req.Branch,
		Title: integrateTitle(req.Unit),
		Body:  "Run-Id: " + req.Unit.RunID + "\nEffect: " + req.Unit.Effect,
	}
}

// mergeRequest returns the one merge of one integration: the batch pull request, pinned to the tip
// the auditor audited, so a merge of another tip is refused by the tracker (§10.1).
func mergeRequest(req IntegrateRequest, number int) MergeRequest {
	return MergeRequest{
		Repo:    req.Repo,
		Number:  number,
		SHA:     req.Unit.Tip,
		Message: mergeMessage(req.Repo, req.Unit),
	}
}

// mergeMessage returns the message of the merge commit of one unit: the §7.3 step 11 title as its
// header, then the trailer block a closing commit carries — the measured Effect line, the Closes
// trailer of the unit and the Run-Id of the run that audited the tip (SPEC §12.3, DR-14, L7).
func mergeMessage(repo tracker.RepoID, u AuditedUnit) string {
	var b strings.Builder
	b.WriteString(integrateTitle(u))
	b.WriteString("\n\n")
	b.WriteString("Effect: " + u.Effect + "\n")
	fmt.Fprintf(&b, "Closes %s/%s#%d\n", repo.Owner, repo.Name, u.Issue)
	b.WriteString("Run-Id: " + u.RunID + "\n")
	return b.String()
}

// integrateTitle returns the title of the pull request and the header of the merge commit of one
// unit: `<type>(<scope>): ` of the audited commit, then the issue title with a lower-case first
// letter, the whole cut to the §12.3 limit (SPEC §7.3 step 11).
func integrateTitle(u AuditedUnit) string {
	head := u.Type
	if u.Scope != "" {
		head += "(" + u.Scope + ")"
	}
	return cutRunes(head+": "+lowerFirst(u.Title), maxTitleRunes)
}

// lowerFirst returns s with its first letter in lower case, as the §7.3 step 11 title requires. Only
// ASCII letters are mapped, as the engine maps no other case (SPEC §5.14).
func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return string(s[0]-'A'+'a') + s[1:]
}

// cutRunes returns s cut to limit runes. The §12.3 limits count characters, not bytes.
func cutRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}
