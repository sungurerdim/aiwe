// Package engine holds the git calls of one integration in git.go: the tip the run branch names in
// the run clone and the push of that branch to the remote (SPEC §7.3 step 11, DR-14). Every call
// goes through core.Git, so no program a repository configures runs (DR-10, SPEC §5.21), and every
// ref is built from a name, so no name becomes an option of a call.
package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// branchRef returns the ref of branch inside a repository.
func branchRef(branch string) string { return "refs/heads/" + branch }

// branchCommit returns the commit the branch of dir names, or "" when the branch names none; a
// failure other than a ref that names no commit is returned. An integration reads the tip it is
// about to merge here, so it never trusts a tip a caller named alone.
func branchCommit(ctx context.Context, dir, branch string) (string, error) {
	res, err := core.Git(ctx, dir, "rev-parse", "--verify", "--quiet", branchRef(branch)+"^{commit}")
	if err != nil {
		if namesNoCommit(res) {
			return "", nil
		}
		return "", fmt.Errorf("read the branch %q of %s: %w", branch, dir, err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// namesNoCommit reports whether a failed call is git's answer that the ref names no commit:
// `rev-parse --verify --quiet` exits 1 and prints nothing for a ref it does not know. That is a
// measured absence and not a failure, but it never becomes a tip that passes a check on its own
// (DR-6): the caller refuses it.
func namesNoCommit(res core.GitResult) bool {
	return res.ExitCode == 1 && strings.TrimSpace(res.Stdout) == ""
}

// pushBranch pushes the branch of dir to remote under the same branch name (SPEC §7.3 step 11). The
// push carries no tag and no force flag: a remote branch that already moved is refused by git,
// never overwritten, and the refusal names its outcome, push_rejected (§7.4), with its cause. The
// refspec names one ref, so no other ref of the remote changes.
func pushBranch(ctx context.Context, dir, remote, branch string) error {
	target, err := pushTarget(remote)
	if err != nil {
		return err
	}
	refspec := branchRef(branch) + ":" + branchRef(branch)
	if _, err := core.Git(ctx, dir, "push", "--no-tags", target, refspec); err != nil {
		return &core.Error{Outcome: "push_rejected", Err: err,
			Expected: "a remote that accepts the run branch " + branch + " without a force",
			Found:    "the push of " + refspec + " was refused: " + err.Error(),
			Next:     "fetch the branch the remote moved, re-run the unit on its new base, then integrate the tip the auditor audited"}
	}
	return nil
}

// pushTarget returns the destination a push is given: the absolute path of a local remote becomes a
// file URL, so a path with a space or another special byte stays one destination (SPEC §5.21,
// §6.3), and a URL or a configured remote name is used as it is. A destination that names nothing
// or that git could read as an option is refused, so no remote name becomes an argument.
func pushTarget(remote string) (string, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return "", fmt.Errorf("remote %q: want a remote name, a URL or an absolute path", remote)
	}
	if !filepath.IsAbs(remote) {
		return remote, nil
	}
	return fileURL(remote)
}
