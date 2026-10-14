package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// RunRef returns the ref of the canonical repository a run's branch is collected into (SPEC §6.5).
// The name holds the product name, which lives in core.Product alone, so no other file spells it
// (DR-17, check repo/source-rules).
func RunRef(runID string) string { return "refs/" + core.Product + "/runs/" + runID }

// Residue is what a run leaves behind: the run ref and its tip in the canonical repository, and the
// residue of the run's clone, the paths the run left uncommitted and the diff stat of its commits.
type Residue struct {
	RunID       string
	Ref         string
	Tip         string
	Uncommitted []string
	DiffStat    string
}

// Collect fetches the run branch from the run's clone into the canonical repository as the run ref,
// records the residue of the clone, and only then removes the clone and the run's scratch directory
// (SPEC §6.5). A fetch that fails keeps the clone: no commit of the run is removed with it, and the
// sweep collects the clone of a run that died (§6.5).
func Collect(ctx context.Context, owner *core.Owner, clone *Clone, branch string) (Residue, error) {
	if owner == nil || clone == nil {
		return Residue{}, errors.New("collect: no owner or run clone")
	}
	if err := checkForeignRepo(); err != nil {
		return Residue{}, err
	}
	if err := checkBranch(branch); err != nil {
		return Residue{}, err
	}
	residue, err := residueOf(ctx, clone, branch)
	if err != nil {
		return Residue{}, err
	}
	if err := fetchRun(ctx, clone, branch); err != nil {
		return Residue{}, err
	}
	return residue, clone.remove(ctx, owner)
}

// residueOf reads what the clone holds: the tip of the run branch, the paths the run left
// uncommitted, and the diff stat of the run's commits.
func residueOf(ctx context.Context, clone *Clone, branch string) (Residue, error) {
	tip, err := branchTip(ctx, clone, branch)
	if err != nil {
		return Residue{}, err
	}
	uncommitted, err := uncommittedPaths(ctx, clone)
	if err != nil {
		return Residue{}, err
	}
	stat, err := diffStat(ctx, clone, tip)
	if err != nil {
		return Residue{}, err
	}
	return Residue{
		RunID:       clone.RunID,
		Ref:         RunRef(clone.RunID),
		Tip:         tip,
		Uncommitted: uncommitted,
		DiffStat:    stat,
	}, nil
}

// branchTip returns the commit the run branch points at; a branch that names no commit names no
// run, so it is an error, never an empty tip (DR-6).
func branchTip(ctx context.Context, clone *Clone, branch string) (string, error) {
	res, err := core.Git(ctx, clone.Dir, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("collect: the run branch %s: %w", branch, err)
	}
	tip := strings.TrimSpace(res.Stdout)
	if tip == "" {
		return "", fmt.Errorf("collect: the run branch %s names no commit", branch)
	}
	return tip, nil
}

// uncommittedPaths returns the paths the run left uncommitted in its clone: the changes between the
// run's commit and the work tree, and every untracked path (SPEC §6.5). Git's `status` is not used:
// it reads the clone's own diff configuration, and a clone that configures an external diff or a
// textconv driver must never run one (SPEC §5.21).
func uncommittedPaths(ctx context.Context, clone *Clone) ([]string, error) {
	changed, err := core.Git(ctx, clone.Dir, "diff", "--name-only", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("collect: the changed paths of the clone: %w", err)
	}
	untracked, err := core.Git(ctx, clone.Dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("collect: the untracked paths of the clone: %w", err)
	}
	return append(pathLines(changed.Stdout), pathLines(untracked.Stdout)...), nil
}

// diffStat returns the diff stat of the run's commits, from the base commit to the run's tip as
// `<base>...<tip>`, the range DR-9 gives every engine diff, so the stat counts from the run's base
// commit and not between the two trees. Git runs with the repository's programs off, so the clone's
// own diff configuration runs nothing.
func diffStat(ctx context.Context, clone *Clone, tip string) (string, error) {
	res, err := core.Git(ctx, clone.Dir, "diff", "--stat", clone.Base+"..."+tip)
	if err != nil {
		return "", fmt.Errorf("collect: the diff stat of the run: %w", err)
	}
	return res.Stdout, nil
}

// fetchRun fetches the run branch from the clone into the canonical repository as the run ref
// (SPEC §6.5). Only that ref and the objects of the run are written: the refspec names one ref, and
// the owner's FETCH_HEAD is left alone, so the run writes nothing else in the owner's repository.
func fetchRun(ctx context.Context, clone *Clone, branch string) error {
	source, err := fileURL(clone.Dir)
	if err != nil {
		return err
	}
	refspec := "+refs/heads/" + branch + ":" + RunRef(clone.RunID)
	if _, err := core.Git(ctx, clone.Canonical, "fetch", "--no-tags", "--no-write-fetch-head",
		source, refspec); err != nil {
		return fmt.Errorf("collect: fetch the run branch %s: %w", branch, err)
	}
	return nil
}

// checkBranch refuses a branch name a run could not have made: the name becomes a ref and a
// refspec, so a name that git could read as an option or as a traversal is refused (fail-closed).
func checkBranch(branch string) error {
	if branch == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "/") ||
		strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") ||
		strings.HasSuffix(branch, ".lock") {
		return fmt.Errorf("collect: branch %q: want a branch name", branch)
	}
	for _, bad := range []string{"..", "//", "~", "^", ":", "?", "*", "[", "\\", " ", "@{"} {
		if strings.Contains(branch, bad) {
			return fmt.Errorf("collect: branch %q: want a branch name without %q", branch, bad)
		}
	}
	return nil
}
