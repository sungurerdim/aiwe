package core

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// SweepReport counts the registered temp directories one sweep found.
type SweepReport struct {
	Removed int // temp directories of a dead owner, removed
	Live    int // temp directories of a running owner, kept
	Refused int // temp directories of a dead owner that fail the ownership checks and are not empty, kept
}

// ownedTemp is an existing registered directory under the temp root: whether its owner runs and
// whether it passes the ownership checks of RemoveOwned.
type ownedTemp struct {
	name        string
	live, owned bool
}

// Sweep removes every owned temp directory whose owner process is dead (DR-36); a pid that runs
// under another start time is a reused pid, so its owner is dead too (U13), except a ProcessID
// built by hand on Linux: it has no exact start identity, so its owner counts as alive while its
// pid runs (DR-25). A registered directory of a dead owner that fails the ownership checks is
// removed only when it is an empty directory, such as one left by a crash before its marker; any
// other is kept and counted as Refused. Cache directories are never touched.
func (o *Owner) Sweep(ctx context.Context) (SweepReport, error) {
	var rep SweepReport
	dirs, err := o.ownedTemps(ctx)
	if err != nil {
		return rep, err
	}
	var errs []error
	for _, d := range dirs {
		switch {
		case d.live:
			rep.Live++
		case d.owned:
			if err := removeTree(ctx, o.temp, d.name); err != nil {
				errs = append(errs, err)
				continue
			}
			rep.Removed++
		case removeEmptyDir(o.temp, d.name):
			rep.Removed++
		default:
			rep.Refused++
		}
	}
	return rep, errors.Join(errs...)
}

// removeEmptyDir removes name under rootPath when it is a directory, not a link, with no entries; it
// reports whether name was removed. It works through an os.Root, which never leaves rootPath, and
// Root.Remove never removes a directory that holds an entry.
func removeEmptyDir(rootPath, name string) bool {
	rt, err := os.OpenRoot(rootPath)
	if err != nil {
		return false
	}
	defer rt.Close()
	fi, err := rt.Lstat(name)
	if err != nil || fi.Mode().Type() != fs.ModeDir {
		return false
	}
	return rt.Remove(name) == nil
}

// ownedTemps returns each existing directory under the temp root that a valid owned_dir record
// names, with the liveness of the owner the record names and whether it passes the ownership
// checks of RemoveOwned.
func (o *Owner) ownedTemps(ctx context.Context) ([]ownedTemp, error) {
	recs, err := o.records()
	if err != nil {
		return nil, err
	}
	var out []ownedTemp
	seen := make(map[string]bool)
	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.Root != "temp" || seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		p := filepath.Join(o.temp, r.Path)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		_, _, cerr := o.checkOwned(p, recs)
		owner := ProcessID{PID: r.PID, Start: r.Start}
		if r.StartID != noneField {
			owner.id = r.StartID
		}
		out = append(out, ownedTemp{name: r.Path, live: ownerAlive(owner), owned: cerr == nil})
	}
	return out, nil
}

// ownerAlive reports whether p still runs: the pid runs and its start matches. A p with an exact
// start identity is compared by startID; any other by the OS start time within startTolerance, which
// on Linux has no limit: a p built by hand counts as alive while its pid runs. When the start cannot
// be read, the owner counts as alive, so nothing is removed (fail-closed).
func ownerAlive(p ProcessID) bool {
	if p.id != "" {
		id, running, err := startID(p.PID)
		return err != nil || running && id == p.id
	}
	start, running, err := processStart(p.PID)
	if err != nil {
		return true
	}
	if !running {
		return false
	}
	d := start.Sub(p.Start)
	if d < 0 {
		d = -d
	}
	return d <= startTolerance()
}

// startTolerance is how far the OS start time of a p without an exact start identity may lie from
// p.Start. On Windows and macOS the start is a stored value, read the same way at every call, so the
// tolerance absorbs rounding only. Elsewhere it is read from /proc and moves with the wall clock, so
// it is the widest Duration: a p built by hand whose pid runs always counts as a live owner and
// nothing is removed (fail-closed, DR-25). On Linux CurrentProcess sets the exact identity, so the
// tolerance applies only to a p built by hand.
func startTolerance() time.Duration {
	switch runtime.GOOS {
	case "windows", "darwin":
		return time.Second
	}
	return time.Duration(math.MaxInt64)
}
