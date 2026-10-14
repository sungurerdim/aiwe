// Package engine reads the CI result of the audited tip in ci.go (SPEC §7.3 step 11). The result
// decides whether a batch may merge: only a measured green result of the tip itself lets the merge
// happen, so a red or absent result names its outcome and stops the integration (§7.4, DR-14).
package engine

import (
	"context"
	"fmt"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/tracker"
)

// CIState is the CI result of one commit as the tracker reports it (SPEC §10.1): a measured green
// or red result, or none when no run of that commit reported one.
type CIState string

// The states of one CI result.
const (
	// CIGreen is a CI run that passed for the commit.
	CIGreen CIState = "green"
	// CIRed is a CI run that failed for the commit.
	CIRed CIState = "red"
	// CINone is no CI result for the commit.
	CINone CIState = "none"
)

// requireGreen reads the CI result of sha from the tracker and refuses everything but green: a red
// result is refused as red, and an absent one — CINone, or a state the tracker reports that the
// engine does not know — as ci_not_observed (SPEC §7.4), so a missing result is never read as green
// (DR-6). The read asks for the audited tip alone, so the green result of another commit never
// stands in for it (DR-14).
func requireGreen(ctx context.Context, tr Integrator, repo tracker.RepoID, sha string) error {
	state, err := tr.CIStatus(ctx, repo, sha)
	if err != nil {
		return fmt.Errorf("read the CI result of %s: %w", sha, err)
	}
	if state == CIGreen {
		return nil
	}
	if state == CIRed {
		return &core.Error{Outcome: "red",
			Expected: "a green CI result of the audited tip " + sha,
			Found:    "the red CI result of " + sha,
			Next:     "fix the red CI run of the batch, then integrate the unit again"}
	}
	return &core.Error{Outcome: "ci_not_observed",
		Expected: "a green CI result of the audited tip " + sha,
		Found:    fmt.Sprintf("no CI result: the tracker reports the state %q", string(state)),
		Next:     "wait for the CI run of the batch and read its result, then integrate the unit again"}
}
