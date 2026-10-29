package gates

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/ledger"
)

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

// readFailures reads the tail file of each red run that names one, inside tails, and collects the
// failing tests it holds. The tail is the only place a record holds test names until #702.
func readFailures(checks []ledger.Check, tails string) failures {
	f := failures{tests: map[string]*testFailure{}}
	for _, c := range checks {
		if !isRed(c) {
			continue
		}
		data, ok := readTail(c, tails)
		if !ok {
			f.unread++
			continue
		}
		f.read++
		f.addTail(data, c)
	}
	return f
}

// readTail returns the tail file c names, when it lies in tails.
func readTail(c ledger.Check, tails string) ([]byte, bool) {
	if !c.TailPath.Known || filepath.Dir(c.TailPath.Value) != tails {
		return nil, false
	}
	data, err := readInRoot(tails, filepath.Base(c.TailPath.Value))
	return data, err == nil
}
