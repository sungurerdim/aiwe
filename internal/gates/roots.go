package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// rootCell is one disk root of SPEC §6.4 as a check sees it: the suffix of its override variable
// and the directory that stands for it inside the check's fresh roots.
type rootCell struct{ suffix, dir string }

// rootCells are the state, cache and temp roots, in that order.
func rootCells() []rootCell {
	return []rootCell{{"HOME", "state"}, {"CACHE", "cache"}, {"TMP", "temp"}}
}

// rootVar returns the override variable of c, derived from the product constant.
func rootVar(c rootCell) string {
	return strings.ToUpper(core.Product) + "_" + c.suffix
}

// checkRoots is one fresh directory that holds a check's state, cache and temp roots (SPEC §7.8).
// With an owner it is an owned directory in the temp root, removed through the owner; without one
// it is a new directory in the OS temp directory, removed only while it is empty.
type checkRoots struct {
	owner *core.Owner
	owned core.OwnedDir
	base  string
}

// freshRoots makes the fresh roots of one check.
func (r *Runner) freshRoots(ctx context.Context) (*checkRoots, error) {
	roots := &checkRoots{owner: r.Owner}
	if r.Owner != nil {
		d, err := r.Owner.CreateOwned(ctx, core.TempRoot, "check")
		if err != nil {
			return nil, err
		}
		roots.owned, roots.base = d, d.Path()
	} else {
		base, err := os.MkdirTemp("", core.Product+"-check-")
		if err != nil {
			return nil, err
		}
		roots.base = base
	}
	for _, c := range rootCells() {
		if err := os.Mkdir(filepath.Join(roots.base, c.dir), 0o700); err != nil {
			return nil, errors.Join(err, roots.remove(ctx))
		}
	}
	return roots, nil
}

// env returns the secret-free check environment of core.CleanEnv (DR-35, SPEC §6.5) with each root
// override pointed at its directory in the fresh roots. A parent override never reaches the check.
func (c *checkRoots) env() []string {
	cells := rootCells()
	parent := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !isRootVar(name, cells) {
			parent = append(parent, kv)
		}
	}
	env := core.CleanEnv(parent, nil)
	for _, cell := range cells {
		env = append(env, rootVar(cell)+"="+filepath.Join(c.base, cell.dir))
	}
	return env
}

// isRootVar reports whether name is a root override variable; names compare without case, as
// Windows does.
func isRootVar(name string, cells []rootCell) bool {
	for _, c := range cells {
		if strings.EqualFold(name, rootVar(c)) {
			return true
		}
	}
	return false
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

// remove removes the fresh roots. Without an owner it removes only empty directories, so it never
// removes a tree (DR-25); an entry left inside keeps the directory and fails.
func (c *checkRoots) remove(ctx context.Context) error {
	if c.owner != nil {
		return c.owner.RemoveOwned(context.WithoutCancel(ctx), c.owned)
	}
	var errs []error
	for _, cell := range rootCells() {
		if err := os.Remove(filepath.Join(c.base, cell.dir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, os.Remove(c.base))...)
}

// close judges the fresh roots after the check and removes them. An entry left in them turns the
// report red (SPEC §7.8); roots that cannot be read or removed make a report that is not red
// not_measured, and the detail names them.
func (c *checkRoots) close(ctx context.Context, report StepReport) StepReport {
	left, err := c.leftovers()
	switch {
	case err != nil:
		report = notMeasuredUnlessRed(report, "its fresh roots could not be read: "+err.Error())
	case len(left) > 0:
		report.Result = ResultRed
		report.Detail = joinDetail(report.Detail, "left entries in its fresh roots: "+strings.Join(left, ", "))
	}
	if err := c.remove(ctx); err != nil {
		report = notMeasuredUnlessRed(report, fmt.Sprintf("its fresh roots %s were not removed: %v", c.base, err))
	}
	return report
}

// notMeasuredUnlessRed adds detail to report and turns a report that is not red not_measured.
func notMeasuredUnlessRed(report StepReport, detail string) StepReport {
	if report.Result != ResultRed {
		report.Result = ResultNotMeasured
	}
	report.Detail = joinDetail(report.Detail, detail)
	return report
}
