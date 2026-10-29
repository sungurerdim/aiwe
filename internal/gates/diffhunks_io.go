package gates

import (
	"context"
	"fmt"
	"regexp"

	"github.com/sungurerdim/aiwe/internal/core"
)

// diffCommitID is the only form ReadDiffHunks takes for a base or a tip: a full or abbreviated
// commit id, never a branch, HEAD, a reflog entry or a ref counted from one.
var diffCommitID = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// ReadDiffHunks returns the added line ranges of `git diff --unified=0 <base>...<tip>` in repo
// (DR-9). The range is the run's base commit to its tip: a base or tip that is not a commit id
// (`HEAD`, `HEAD~n`, `main`, `HEAD@{1}`) is refused. Git runs through core.Git, so a program the
// repository configures never runs (DR-10); the hunks are Git's own output, never a made-up list.
func ReadDiffHunks(ctx context.Context, repo, base, tip string) (DiffHunks, error) {
	for _, id := range []string{base, tip} {
		if !diffCommitID.MatchString(id) {
			return nil, fmt.Errorf("diff hunks: %q is not a commit id", id)
		}
	}
	res, err := core.Git(ctx, repo, "diff", "--unified=0", "--no-color", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", base+"..."+tip, "--")
	if err != nil {
		return nil, fmt.Errorf("diff hunks %s...%s: %w", base, tip, err)
	}
	return ParseDiffHunks(res.Stdout), nil
}
