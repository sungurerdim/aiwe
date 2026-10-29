package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/ledger"
)

// engineOwner opens the engine owner under the resolved roots, with repo as the canonical
// repository it must keep out of reach (SPEC §6.4, §5.15). Every check runs in roots this owner
// makes, so a gate with no owner refuses: an unsafe or unresolved root keeps its named outcome.
func engineOwner(repo string) (*core.Owner, error) {
	roots, err := core.UserRoots()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ownerRefusal(fmt.Errorf("%w: home: %w", core.ErrRootUnsafe, err))
	}
	self, err := core.CurrentProcess()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	owner, err := core.OpenOwner(core.OwnedRoots{State: roots.State, Temp: roots.Temp, Cache: roots.Cache,
		Home: home, Canonical: []string{repo}}, self)
	if err != nil {
		return nil, ownerRefusal(err)
	}
	return owner, nil
}

// readDefinition reads and parses gate.json at the repository root. A missing file is an error,
// never a green gate (DR-6). The read goes through an os.Root: a gate.json symlink or junction that
// leads out of repo is refused. A hardlink to a file outside repo, on the same volume, is read; the
// root cannot see it (SPEC §5.6).
func readDefinition(repo string) (Definition, error) {
	path, err := core.ResolvePath(repo, "gate.json")
	if err != nil {
		return Definition{}, err
	}
	data, err := readInRoot(repo, "gate.json")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Definition{}, &core.Error{Outcome: core.OutcomeError, Expected: "a gate.json at the repository root",
			Found: fmt.Sprintf("no file at %q", path), Next: "add gate.json (SPEC §8.3), or pass the --repo that holds it",
			Err: err}
	case err != nil:
		return Definition{}, &core.Error{Outcome: core.OutcomeError, Expected: "a readable gate.json inside the repository",
			Found: err.Error(), Next: "make gate.json a readable file inside --repo, then run the gate again", Err: err}
	}
	return ParseDefinition(data)
}

// maxGateDefinitionBytes caps gate.json; a larger file is refused unread (#199).
const maxGateDefinitionBytes = 1 << 20

// readInRoot reads the file name under dir; a name or link that leads out of dir, a non-regular
// file or a file over maxGateDefinitionBytes is an error that names the path.
func readInRoot(dir, name string) (data []byte, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err = core.ReadRegularCapped(root, name, maxGateDefinitionBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
	}
	return data, nil
}

const (
	// tailDirName is the directory of the state root that holds the tail files of check records.
	tailDirName = "tails"
	// recordLockWait bounds the wait for the ledger lock. It does not end with the gate's context,
	// so a stopped gate still writes the records of the steps it took up.
	recordLockWait = 30 * time.Second
)

// checkLedger is the record sink of a gate: a kept tail goes to a new file of the tail directory,
// then the record that names it is appended to the ledger of the state root under its lock (#592).
type checkLedger struct{ path, tails string }

// openCheckLedger opens the ledger of the state root for check records and makes its tail
// directory. An unresolved state root keeps its named outcome.
func openCheckLedger() (*checkLedger, error) {
	roots, err := core.UserRoots()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	tails := filepath.Join(roots.State, tailDirName)
	if err := os.MkdirAll(tails, 0o700); err != nil {
		return nil, &core.Error{Outcome: core.OutcomeError, Expected: "a tail directory in the state root",
			Found: err.Error(), Next: "make the state root writable, then run the gate again", Err: err}
	}
	return &checkLedger{path: filepath.Join(roots.State, core.LedgerFile), tails: tails}, nil
}

// WriteCheck writes rec: its tail, when it keeps one, to a new file whose path the record then
// names, and the record to the ledger. A tail that is not written leaves the record unwritten, so no
// record names a file that does not exist; a tail whose record the ledger refuses is removed, so no
// tail file outlives the record that would name it (DR-11).
func (l *checkLedger) WriteCheck(ctx context.Context, rec CheckRecord) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordLockWait)
	defer cancel()
	check := rec.Check
	if len(rec.Tail) == 0 {
		return ledger.AppendFile(ctx, l.path, check)
	}
	name, err := writeTail(l.tails, check.StepID, check.At, tailText(rec.Tail))
	if err != nil {
		return fmt.Errorf("check tail: %w", err)
	}
	check.TailPath = ledger.Field[string]{Value: filepath.Join(l.tails, name), Known: true}
	if err := ledger.AppendFile(ctx, l.path, check); err != nil {
		return errors.Join(err, removeInRoot(l.tails, name))
	}
	return nil
}

// tailNameTries bounds the names writeTail tries for one tail.
const tailNameTries = 100

// writeTail writes data to the first free tail name of step at at in dir and returns that name; a
// name another tail of the same step and instant holds is skipped.
func writeTail(dir, step string, at time.Time, data []byte) (string, error) {
	for n := range tailNameTries {
		name := tailName(step, at, n)
		err := writeNewFile(dir, name, data)
		if !errors.Is(err, fs.ErrExist) {
			return name, err
		}
	}
	return "", fmt.Errorf("the first %d tail names of step %q are taken", tailNameTries, step)
}

// removeInRoot removes the file name in dir, opened through dir so a link in the name cannot lead
// out of it (SPEC §5.6).
func removeInRoot(dir, name string) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return root.Remove(name)
}

// writeNewFile writes data to the new file name in dir. It opens through dir, so a link in the
// name cannot lead out of it, and refuses a name that already exists (SPEC §5.6).
func writeNewFile(dir, name string, data []byte) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = f.Write(data)
	return err
}

// MeasureFacts measures the facts every check record of a run in dir carries: the commit and tree
// it runs on, the toolchain probe's output, the OS and architecture, and the Go module path of
// dir/go.mod. A fact that cannot be measured stays empty, so its record reads it unknown (DR-6).
func MeasureFacts(ctx context.Context, dir string) RunFacts {
	ctx, cancel := context.WithTimeout(ctx, keyProbeLimit)
	defer cancel()
	facts := RunFacts{OS: runtime.GOOS + "/" + runtime.GOARCH}
	facts.Commit, facts.Tree = measureCommit(ctx, dir)
	if out, err := toolchainOutput(ctx, dir); err == nil {
		facts.Toolchain = toolchainText(out)
	}
	if data, err := readInRoot(dir, "go.mod"); err == nil {
		facts.Module = goModulePath(data)
	}
	return facts
}

// measureCommit returns HEAD and its tree when the work tree in dir matches HEAD. Otherwise the run
// tests work that is not committed: the commit is uncommitted and the tree is not measured, because
// it is not the tree of HEAD (SPEC-contracts §8.5).
func measureCommit(ctx context.Context, dir string) (commit, tree string) {
	res, err := core.Git(ctx, dir, "rev-parse", "HEAD", "HEAD^{tree}")
	if err != nil {
		return ledger.CommitUncommitted, ""
	}
	ids := strings.Fields(res.Stdout)
	clean, err := workTreeMatchesHead(ctx, dir, nil)
	if err != nil || !clean || len(ids) != 2 {
		return ledger.CommitUncommitted, ""
	}
	return ids[0], ids[1]
}

// readChecks returns the check records of the ledger in the state root that belong to repo (any repo
// when it is empty) and are not older than since. The ledger is opened through the state root, and a
// ledger that does not exist holds none.
func readChecks(state, repo string, since time.Time) (checks []ledger.Check, err error) {
	root, err := os.OpenRoot(state)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ledgerRefusal(err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	f, err := root.Open(core.LedgerFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ledgerRefusal(err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	records, err := ledger.Read(f)
	if err != nil {
		return nil, ledgerRefusal(err)
	}
	for _, rec := range records {
		if c, ok := rec.(ledger.Check); ok && (repo == "" || c.RepoID == repo) && !c.At.Before(since) {
			checks = append(checks, c)
		}
	}
	return checks, nil
}
