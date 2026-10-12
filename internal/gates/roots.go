package gates

import (
	"context"
	"fmt"
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

// checkRoots is one fresh directory that holds a check's state, cache and temp roots (SPEC §7.8):
// an owned directory in the temp root, removed through its owner (SPEC §6.4).
type checkRoots struct {
	owner *core.Owner
	owned core.OwnedDir
	base  string
}

// env returns the secret-free check environment of core.CleanEnv (DR-10, SPEC §6.5) with each root
// override pointed at its directory in the fresh roots. A parent override never reaches the check.
func (c *checkRoots) env() []string {
	cells := rootCells()
	environ := core.Environ()
	parent := make([]string, 0, len(environ))
	for _, kv := range environ {
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

// remove removes the fresh roots through their owner, the one owner of a recursive removal (DR-11).
func (c *checkRoots) remove(ctx context.Context) error {
	return c.owner.RemoveOwned(context.WithoutCancel(ctx), c.owned)
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
