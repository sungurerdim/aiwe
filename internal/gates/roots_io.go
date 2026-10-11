package gates

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/sungurerdim/aiwe/internal/core"
)

// freshRoots makes the fresh roots of one check. A Runner with no owner refuses: a check never
// runs in roots no owner made.
func (r *Runner) freshRoots(ctx context.Context) (*checkRoots, error) {
	if r.Owner == nil {
		return nil, &core.Error{Outcome: core.OutcomeError, Expected: "an engine owner of the check's roots",
			Found: "a runner with no owner", Next: "open the engine owner under the resolved roots, then run the gate again"}
	}
	d, err := r.Owner.CreateOwned(ctx, core.TempRoot, "check")
	if err != nil {
		return nil, err
	}
	roots := &checkRoots{owner: r.Owner, owned: d, base: d.Path()}
	for _, c := range rootCells() {
		if err := os.Mkdir(filepath.Join(roots.base, c.dir), 0o700); err != nil {
			return nil, errors.Join(err, roots.remove(ctx))
		}
	}
	return roots, nil
}

// leftovers returns each entry the check left in its roots, as "<root>/<name>".
func (c *checkRoots) leftovers() ([]string, error) {
	var left []string
	for _, cell := range rootCells() {
		entries, err := os.ReadDir(filepath.Join(c.base, cell.dir))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			left = append(left, cell.dir+"/"+e.Name())
		}
	}
	return left, nil
}
