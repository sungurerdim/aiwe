package core

import (
	"context"
	"errors"
	"fmt"
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

// removeCheckedHook, when set, runs right after removeOnce has checked name and before it opens it;
// a test uses it to swap name at that point.
var removeCheckedHook func(name string)

// removeTree removes name under rootPath through an os.Root, which never leaves rootPath. A name
// that is not a plain name, or that is not a directory, such as one swapped for a link, fails with
// ErrNotOwned at once. A failed attempt, such as a file another process holds open, is retried 5
// times, 200 ms apart, then fails with ErrResidueLocked (SPEC §6.4); the next sweep retries.
func removeTree(ctx context.Context, rootPath, name string) error {
	if !plainName(name, maxOwnedName) {
		return fmt.Errorf("%w: remove %q: not an owned directory name", ErrNotOwned, name)
	}
	rt, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	defer rt.Close()
	for attempt := 1; ; attempt++ {
		err = removeOnce(rt, name)
		if err == nil || errors.Is(err, ErrNotOwned) {
			return err
		}
		if attempt == removeAttempts {
			break
		}
		t := time.NewTimer(removeWait)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Join(fmt.Errorf("remove %q: %w", name, err), ctx.Err())
		case <-t.C:
		}
	}
	return fmt.Errorf("%w: remove %q: %d attempts %v apart: %w", ErrResidueLocked, name, removeAttempts, removeWait, err)
}

// removeOnce removes the entries of name, then its marker, then name itself, so a removal that
// stops part way leaves the marker and the next removal still finds the directory owned. name is
// checked once without following it, then opened once as a sub-root that must be the same
// directory; the entries are removed through that sub-root, so a name swapped after the check,
// such as for a link, is never followed and fails with ErrNotOwned.
func removeOnce(rt *os.Root, name string) error {
	fi, err := rt.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%w: %q is not a directory (a link is never followed)", ErrNotOwned, name)
	}
	if removeCheckedHook != nil {
		removeCheckedHook(name)
	}
	sub, err := openChecked(rt, name, fi)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := errors.Join(emptyOwned(sub, name, fi), sub.Close()); err != nil {
		return err
	}
	// Root.Remove never follows a link and never removes a directory that holds an entry.
	if err := sameEntry(rt, name, fi); err != nil {
		return err
	}
	return rt.Remove(name)
}

// openChecked opens name under rt once as a sub-root. A failed open fails with ErrNotOwned when
// name is no longer the directory fi describes.
func openChecked(rt *os.Root, name string, fi fs.FileInfo) (*os.Root, error) {
	sub, err := rt.OpenRoot(name)
	if err != nil {
		if serr := sameEntry(rt, name, fi); serr != nil {
			return nil, serr
		}
		return nil, err
	}
	return sub, nil
}

// emptyOwned removes every entry of the sub-root sub, the marker last. It first proves that sub is
// the directory fi describes, else it fails with ErrNotOwned and removes nothing.
func emptyOwned(sub *os.Root, name string, fi fs.FileInfo) error {
	d, err := sub.Open(".")
	if err != nil {
		return err
	}
	di, err := d.Stat()
	if err == nil && !os.SameFile(fi, di) {
		err = fmt.Errorf("%w: %q was swapped after the check (a link is never followed)", ErrNotOwned, name)
	}
	var names []string
	if err == nil {
		names, err = d.Readdirnames(-1)
	}
	if err := errors.Join(err, d.Close()); err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		if n != markerFile {
			errs = append(errs, sub.RemoveAll(n))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := sub.Remove(markerFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// sameEntry fails with ErrNotOwned when name under rt, read without following it, is no longer the
// directory fi describes. A name that no longer exists passes; the caller sees it as gone.
func sameEntry(rt *os.Root, name string, fi fs.FileInfo) error {
	now, err := rt.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if now.Mode().Type() != fs.ModeDir || !os.SameFile(fi, now) {
		return fmt.Errorf("%w: %q was swapped after the check (a link is never followed)", ErrNotOwned, name)
	}
	return nil
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
