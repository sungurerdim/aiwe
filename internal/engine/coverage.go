package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Diff is the observed change of one run: the commit range Git compared and the paths between the
// two commits, in Git's own order.
type Diff struct {
	Base  string
	Tip   string
	Paths []string
}

// ScopeViolation reports the paths a run changed outside its declared file set; such a run ends as
// scope_mismatch, the outcome DR-9 gives the comparison of the observed diff with the file set.
type ScopeViolation struct {
	Outside []string
}

// Error names the paths the run changed outside its declared set.
func (e *ScopeViolation) Error() string {
	return fmt.Sprintf("scope_mismatch: the run changed %d path(s) outside its declared file set: %s",
		len(e.Outside), strings.Join(e.Outside, ", "))
}

// ObserveDiff returns the paths Git reports between base and tip in repo (DR-9). The range is
// `base...tip`, exactly as DR-9 requires: only the run's own change is observed, also when the tip
// does not descend from the base. The observed set is Git's own list of the changes, never a list
// the engine made up, and Git runs with the repository's programs off, so an external diff or a
// textconv driver the repository configures never runs (SPEC §5.21). A path is reported as Git
// prints it, quoting included.
func ObserveDiff(ctx context.Context, repo, base, tip string) (Diff, error) {
	if err := checkCommit(base); err != nil {
		return Diff{}, fmt.Errorf("observed diff: %w", err)
	}
	if err := checkCommit(tip); err != nil {
		return Diff{}, fmt.Errorf("observed diff: %w", err)
	}
	if err := checkForeignRepo(); err != nil {
		return Diff{}, err
	}
	res, err := core.Git(ctx, repo, "diff", "--name-only", base+"..."+tip)
	if err != nil {
		return Diff{}, fmt.Errorf("observed diff %s...%s: %w", base, tip, err)
	}
	return Diff{Base: base, Tip: tip, Paths: pathLines(res.Stdout)}, nil
}

// CheckScope compares the observed diff with the declared file set (DR-9, Q25): a path outside the
// set is a violation, in the order Git reported it, and the run ends as scope_mismatch. The test
// file of a declared file and the module files are part of the set; every other path, a lock file an
// agent wrote included, is not.
func CheckScope(declared []string, diff Diff) error {
	allowed := declaredSet(declared)
	var outside []string
	for _, path := range diff.Paths {
		if !allowed[path] {
			outside = append(outside, path)
		}
	}
	if len(outside) == 0 {
		return nil
	}
	return &ScopeViolation{Outside: outside}
}

// declaredSet returns the paths the scope check allows: each declared path, the test file of each
// declared Go file, and the files of the module itself, which carry no behaviour.
func declaredSet(declared []string) map[string]bool {
	allowed := make(map[string]bool, len(declared)+3)
	for _, path := range declared {
		if path == "" {
			continue
		}
		allowed[path] = true
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			allowed[strings.TrimSuffix(path, ".go")+"_test.go"] = true
		}
	}
	for _, path := range moduleFiles() {
		allowed[path] = true
	}
	return allowed
}

// moduleFiles are the files of the module itself: every unit may change them, and they carry no
// behaviour of their own.
func moduleFiles() []string { return []string{"go.mod", "go.sum"} }

// checkCommit refuses an empty commit id and one that git could read as an option, since the id is
// an argument of every diff of a run.
func checkCommit(id string) error {
	if id == "" || strings.HasPrefix(id, "-") {
		return fmt.Errorf("commit id %q: want a commit id", id)
	}
	return nil
}

// pathLines returns the paths of one git path list, one path per line. A path with a byte git
// quotes, such as a newline or a non-ASCII byte, is returned quoted, so it compares unequal with a
// declared path and fails the scope check closed.
func pathLines(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}
