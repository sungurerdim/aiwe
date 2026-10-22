// Package engine holds the flows of one run (SPEC §6.1, §7.3): the claim of a unit, the isolation
// of the run, the collection of its residue and the scope check of its diff. Every git call goes
// through core.Git, so no program a repository configures can run (DR-10, SPEC §5.21).
package engine

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/sungurerdim/aiwe/internal/adapters"
	"github.com/sungurerdim/aiwe/internal/core"
)

// The shapes of one run clone (SPEC §6.4, §6.5).
const (
	// runDirName and scratchDirName name the owned temp directories of one run: its own clone and
	// its own scratch, so nothing of the run outlives them (DR-11).
	runDirName     = "run-"
	scratchDirName = "scratch-"
	// cloneDirName is the run's clone inside its own directory.
	cloneDirName = "clone"
	// maxNameBytes bounds a plain name that becomes a directory name or a ref component.
	maxNameBytes = 64
)

// Clone is one run's isolation (DR-10, SPEC §6.5): its own fresh shallow clone of the canonical
// repository at the base commit, its own scratch directory as HOME and temp, and a secret-free
// environment. Only NewClone makes one.
type Clone struct {
	RunID     string   // the run id (SPEC §1.6)
	Base      string   // the commit the run starts from
	Dir       string   // the run's own clone
	Scratch   string   // the run's own scratch: its home, its profile and its temp variables point into it
	Env       []string // the environment of the run, without a secret of the parent
	Canonical string   // the canonical repository the clone was made from

	// owned are the run's own temp directories; only core.Owner removes them (DR-11).
	owned []core.OwnedDir
}

// CloneSpec names what a run clone is made from.
type CloneSpec struct {
	// Canonical is the canonical repository: the onboarded local path, or a bare clone in the cache
	// root (SPEC §6.5).
	Canonical string
	// Base is the commit the run starts from: the head of the working branch at claim (SPEC §7.3).
	// It has to be a commit the canonical repository advertises, so a shallow fetch can reach it.
	Base string
	// RunID is the run id, a ULID (SPEC §1.6).
	RunID string
}

// NewClone makes the isolation of one run: a fresh shallow clone of the canonical repository at the
// base commit, a scratch directory of the run's own, and an environment without the parent's
// secret-shaped variables (DR-10, SPEC §6.5). A failure removes what it made and returns the error,
// so a run never starts without its own clone (isolation_failed, SPEC §7.4).
func NewClone(ctx context.Context, owner *core.Owner, spec CloneSpec) (*Clone, error) {
	if owner == nil {
		return nil, errors.New("run clone: no owner")
	}
	if err := checkSpec(spec); err != nil {
		return nil, err
	}
	if err := checkForeignRepo(); err != nil {
		return nil, err
	}
	run, err := owner.CreateOwned(ctx, core.TempRoot, runDirName+spec.RunID)
	if err != nil {
		return nil, fmt.Errorf("run clone: %w", err)
	}
	clone := &Clone{
		RunID:     spec.RunID,
		Base:      spec.Base,
		Dir:       filepath.Join(run.Path(), cloneDirName),
		Canonical: spec.Canonical,
		owned:     []core.OwnedDir{run},
	}
	scratch, err := owner.CreateOwned(ctx, core.TempRoot, scratchDirName+spec.RunID)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("run scratch: %w", err), clone.remove(ctx, owner))
	}
	clone.owned = append(clone.owned, scratch)
	clone.Scratch = scratch.Path()
	clone.Env = runEnv(clone.Scratch)
	if err := clone.make(ctx, run.Path()); err != nil {
		return nil, errors.Join(err, clone.remove(ctx, owner))
	}
	return clone, nil
}

// BindRequest copies the run environment onto req. The shell tool of the run gets only that
// copy, so a command of the clone never sees the parent's repository pointers or profile
// (SPEC §6.5, DR-10).
func (c *Clone) BindRequest(req *adapters.RunRequest) {
	req.Env = append([]string{}, c.Env...)
}

// make clones the canonical repository into the run's own directory, fetches the base commit and
// checks it out, so the run starts at its base commit whatever the HEAD of the canonical repository
// is. into is the run's own directory, which holds the clone.
func (c *Clone) make(ctx context.Context, into string) error {
	source, err := fileURL(c.Canonical)
	if err != nil {
		return err
	}
	if _, err := core.Git(ctx, into, "clone", "--depth", "1", "--no-tags", "--no-checkout",
		"--config", "core.autocrlf=false", source, c.Dir); err != nil {
		return fmt.Errorf("clone %s: %w", c.Canonical, err)
	}
	if _, err := core.Git(ctx, c.Dir, "fetch", "--depth", "1", "--no-tags", "origin", c.Base); err != nil {
		return fmt.Errorf("fetch the base commit %s: %w", c.Base, err)
	}
	if _, err := core.Git(ctx, c.Dir, "checkout", "--detach", c.Base); err != nil {
		return fmt.Errorf("check out the base commit %s: %w", c.Base, err)
	}
	return nil
}

// remove removes the run's own temp directories, the clone and the scratch, with everything in
// them (SPEC §6.5): the run leaves nothing of its own behind. A directory that is no longer owned
// is not removed and its error is returned (DR-11).
func (c *Clone) remove(ctx context.Context, owner *core.Owner) error {
	var errs []error
	for _, dir := range c.owned {
		if err := owner.RemoveOwned(ctx, dir); err != nil {
			errs = append(errs, fmt.Errorf("remove the run directory %s: %w", dir.Path(), err))
		}
	}
	return errors.Join(errs...)
}

// checkSpec refuses a spec that cannot make an isolated run: a canonical repository that is not an
// absolute path, a base that is no commit id and a run id that is no plain name.
func checkSpec(spec CloneSpec) error {
	if !filepath.IsAbs(spec.Canonical) {
		return fmt.Errorf("run clone: canonical repository %q is not an absolute path", spec.Canonical)
	}
	if err := checkCommit(spec.Base); err != nil {
		return fmt.Errorf("run clone: %w", err)
	}
	if !plainName(spec.RunID) {
		return fmt.Errorf("run clone: run id %q: want 1 to %d letters, digits, '.', '_' or '-', "+
			"starting with a letter or digit and not ending in '.'", spec.RunID, maxNameBytes)
	}
	return nil
}

// plainName reports whether s is a plain name of at most maxNameBytes bytes: letters, digits, '.',
// '_' or '-', starting with a letter or a digit and not ending in '.'. Such a name is neither a
// path nor "." or "..", it is one component of a directory name and of a ref, and Windows cannot
// strip a trailing dot to make it name another entry (SPEC §5.15).
func plainName(s string) bool {
	if s == "" || len(s) > maxNameBytes || s[len(s)-1] == '.' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// fileURL returns the file URL of a local directory. The engine reads the canonical repository and
// its own run clone over Git's file protocol and nothing else (SPEC §5.21); the path is escaped, so
// a directory name with a space or another special byte stays one path (SPEC §6.3: no raw argv).
func fileURL(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("repository %q: want an absolute path", dir)
	}
	path := "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Clean(dir)), "/")
	return (&url.URL{Scheme: "file", Path: path}).String(), nil
}

// runEnv returns the environment of a run: the parent environment without its secret-shaped
// variables and without the variables that point a git call at a repository outside the run
// (DR-10, SPEC §6.5), with the home, the profile and the temp variables inside the run's own
// scratch. The run's whole profile is its own scratch: a Windows runtime reads USERPROFILE and the
// app-data directories in place of HOME, so a run whose environment kept the parent's values would
// write into the user's profile (DR-10, SPEC §6.7). The adapter of the run's source adds only its
// own key back.
func runEnv(scratch string) []string {
	env := core.CleanEnv(core.Environ(), nil)
	for _, name := range repoEnvNames() {
		env = dropEnv(env, name)
	}
	for _, name := range scratchEnvNames() {
		env = setEnv(env, name, scratch)
	}
	return env
}

// scratchEnvNames are the parent variables that point a run at a directory of the process that
// started it: the home directory, the Windows profile and its two app-data directories, and the temp
// directories. The run gets its own scratch in every one of them, so no runtime of the run writes
// into a directory of the user (DR-10, SPEC §6.5, §6.7).
func scratchEnvNames() []string {
	return []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR"}
}

// repoEnvNames are the parent variables that would point a run's git at another repository: a
// repository, a work tree, an index or an object store outside the clone, which a run that
// inherited them could change (DR-10, the done criterion of [sk-07]).
func repoEnvNames() []string {
	return []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"}
}

// ErrForeignRepoEnv reports a run whose own engine environment points a git call at a repository
// outside the run (DR-10, the done criterion of [sk-07]).
var ErrForeignRepoEnv = errors.New("the engine's own environment points a git call at another repository")

// foreignRepoName returns the name of the variable in the engine's own environment that points a git
// call at a repository outside the run, or "" when the environment holds none. The engine's own git
// calls run in that environment (core.Git, DR-10), so an inherited GIT_DIR, GIT_WORK_TREE or
// GIT_INDEX_FILE would aim the clone, the observed diff or the collection at the owner's checkout or
// at another repository, which no run may touch.
func foreignRepoName() string {
	for _, name := range repoEnvNames() {
		if core.LookupEnv(name) != "" {
			return name
		}
	}
	return ""
}

// checkForeignRepo refuses a run while the engine's own environment names such a repository. The
// engine cannot drop a variable from its own process, so it stops instead of running a git call that
// could change a repository outside the run (fail-closed, DR-11).
func checkForeignRepo() error {
	name := foreignRepoName()
	if name == "" {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrForeignRepoEnv, name)
}

// setEnv returns env with name set to value: every entry of that name, whatever its case, is
// dropped first, so the run sees one value, and the new entry is last.
func setEnv(env []string, name, value string) []string {
	return append(dropEnv(env, name), name+"="+value)
}

// dropEnv returns env without every entry named name, whatever its case.
func dropEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if n, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(n, name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
