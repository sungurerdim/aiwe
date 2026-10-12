package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// SweepReport counts the registered temp directories one sweep found.
type SweepReport struct {
	Removed int // temp directories of a dead owner, removed
	Live    int // temp directories of a running owner, kept
	Refused int // temp directories of a dead owner that fail the ownership checks and are not empty, kept
	Failed  int // temp directories of a dead owner not removed cleanly: a close failed and its error is returned
}

// ownedTemp is an existing registered directory under the temp root and whether its owner runs.
type ownedTemp struct {
	name string
	live bool
}

// Sweep removes every owned temp directory whose owner process is dead (DR-36); a pid that runs
// under another start time is a reused pid, so its owner is dead too (U13), except a ProcessID
// built by hand on Linux: it has no exact start identity, so its owner counts as alive while its
// pid runs (DR-25). A registered directory of a dead owner that fails the ownership checks is
// removed only when it is an empty directory, such as one left by a crash before its marker; any
// other is kept and counted as Refused. Each directory is checked right before its removal, and a
// file is never removed: a name swapped after the check is counted as Refused and its not_owned
// error is returned; only an empty directory put under it may go (removeDirEntry). Cache
// directories are never touched.
func (o *Owner) Sweep(ctx context.Context) (SweepReport, error) {
	var rep SweepReport
	recs, err := o.records()
	if err != nil {
		return rep, err
	}
	dirs, err := o.ownedTempsOf(ctx, recs)
	if err != nil {
		return rep, err
	}
	var errs []error
	for _, d := range dirs {
		if d.live {
			rep.Live++
			continue
		}
		removed, err := o.sweepDead(ctx, d.name, recs)
		switch {
		case errors.Is(err, errCloseFailed):
			rep.Failed++
		case removed:
			rep.Removed++
		case err == nil, errors.Is(err, ErrNotOwned):
			rep.Refused++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return rep, errors.Join(errs...)
}

// sweepDead checks the temp directory name of a dead owner, then removes it, and reports whether
// it was removed. A directory that fails the check is removed only when it is empty, with no
// error. An approved directory is removed only while name is still the directory approved, else
// it fails with ErrNotOwned and nothing is removed. A close that fails during the check is returned
// and the directory is kept.
func (o *Owner) sweepDead(ctx context.Context, name string, recs []ownedRecord) (bool, error) {
	e, err := o.checkOwned(filepath.Join(o.temp, name), recs)
	if errors.Is(err, errCloseFailed) {
		return false, err
	}
	if err != nil {
		return o.hooks.removeEmptyDir(o.temp, name)
	}
	hookAt(o.hooks.ownedChecked, name)
	if err := o.hooks.removeTree(ctx, e.root, e.name, e.fi); err != nil {
		return false, err
	}
	return true, nil
}

// removeEmptyDir removes name under rootPath when it is a directory, not a link, with no entries; it
// reports whether name was removed. It works through an os.Root, which never leaves rootPath, and
// removes only the directory its check saw (removeDirEntry); a file never goes. A name swapped after
// the check fails with ErrNotOwned, and a failed close is returned (closeChecked) with false; any
// other failure, such as an entry in the directory, keeps it.
func (h removalHooks) removeEmptyDir(rootPath, name string) (removed bool, err error) {
	rt, err := os.OpenRoot(rootPath)
	if err != nil {
		return false, nil
	}
	defer func() {
		if cerr := h.closeChecked("sweep root", rt.Close); cerr != nil {
			removed, err = false, errors.Join(err, cerr)
		}
	}()
	fi, err := rt.Lstat(name)
	if err != nil || fi.Mode().Type() != fs.ModeDir {
		return false, nil
	}
	hookAt(h.emptyChecked, name)
	err = h.removeDirEntry(rt, name, fi)
	if errors.Is(err, ErrNotOwned) || errors.Is(err, errCloseFailed) {
		return false, err
	}
	return err == nil, nil
}

// removalHooks are the points of a removal where a test acts: each func, when set, runs at its
// point; all are nil outside tests. An Owner holds them (SPEC §5.4).
type removalHooks struct {
	ownedChecked func(name string)       // after the ownership check, before the removal starts
	ownedMarker  func(name string)       // after the ownership Lstat, before the marker open
	emptyChecked func(name string)       // after removeEmptyDir checked name, before its removal
	removeCheck  func(name string)       // after removeOnce checked name, before it opens it
	lastChecked  func(name string)       // after removeOnce checked the emptied name the last time
	rmdirChecked func(name string)       // after removeDirEntry checked the held handle
	closeFail    func(what string) error // an error to add at the close named what
}

// hookAt runs the hook f with name when f is set.
func hookAt(f func(name string), name string) {
	if f != nil {
		f(name)
	}
}

// removeDirEntry removes name under rt only when it is the empty directory fi describes. It holds
// name open by a handle that never follows a link, checks that handle against fi, then removes the
// directory through a directory-only call, so a file is never deleted. On Windows the call goes
// through the held handle, which no one can rename or delete while it is held (remove_windows.go).
// On Unix it goes by name (remove_unix.go), so a name swapped between the check and the call stays
// possible: a file under name is kept, an empty directory under name may go (the accepted worst
// case), and the held handle shows whether the approved directory went. When it did not, the call
// fails with ErrNotOwned. A directory that still holds an entry is kept.
func (h removalHooks) removeDirEntry(rt *os.Root, name string, fi fs.FileInfo) (err error) {
	if filepath.Base(name) != name || !filepath.IsLocal(name) {
		return fmt.Errorf("%w: remove %q: not a single name under the root", ErrNotOwned, name)
	}
	held, err := h.openHeldDir(rt, name)
	if err != nil {
		return errors.Join(sameEntry(rt, name, fi), err)
	}
	defer func() { err = errors.Join(err, h.closeChecked("held directory", held.close)) }()
	hi, err := held.f.Stat()
	if err != nil {
		return err
	}
	if hi.Mode().Type() != fs.ModeDir || !os.SameFile(fi, hi) {
		return fmt.Errorf("%w: %q was swapped after the check (a link is never followed)", ErrNotOwned, name)
	}
	hookAt(h.rmdirChecked, name)
	rmErr := held.rmdir()
	gone, err := held.gone()
	switch {
	case err != nil:
		return errors.Join(rmErr, err)
	case gone:
		return nil
	case rmErr == nil, errors.Is(rmErr, fs.ErrNotExist):
		return fmt.Errorf("%w: %q no longer names the approved directory, which stays elsewhere", ErrNotOwned, name)
	}
	return errors.Join(sameEntry(rt, name, fi), rmErr)
}

// errCloseFailed marks a failed close on the removal path; such a failure is returned, never retried.
var errCloseFailed = errors.New("close failed")

// closeChecked runs c, the close named what on the removal path, and returns its error marked with
// errCloseFailed.
func (h removalHooks) closeChecked(what string, c func() error) error {
	err := c()
	if h.closeFail != nil {
		err = errors.Join(err, h.closeFail(what))
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", errCloseFailed, what, err)
}

// removeTree removes name under rootPath through an os.Root, which never leaves rootPath. A name
// that is not a plain name, or that is not a directory, such as one swapped for a link, fails with
// ErrNotOwned at once. approved is the file info the ownership check approved, read through a
// handle; a nil approved fails with ErrNotOwned and removes nothing. Every attempt removes only
// that directory: a name that names another one fails with ErrNotOwned at once. A
// failed attempt, such as a file another process holds open, is retried 5 times, 200 ms apart,
// then fails with ErrResidueLocked (SPEC §6.4); the next sweep retries. A failed close is returned at
// once, not retried.
func (h removalHooks) removeTree(ctx context.Context, rootPath, name string, approved fs.FileInfo) (err error) {
	if !plainName(name, maxOwnedName) {
		return fmt.Errorf("%w: remove %q: not an owned directory name", ErrNotOwned, name)
	}
	if approved == nil {
		return fmt.Errorf("%w: remove %q: no directory approved by the ownership check", ErrNotOwned, name)
	}
	rt, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	defer func() { err = errors.Join(err, h.closeChecked("removal root", rt.Close)) }()
	for attempt := 1; ; attempt++ {
		err = h.removeOnce(rt, name, approved)
		if err == nil || errors.Is(err, ErrNotOwned) || errors.Is(err, errCloseFailed) {
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
// checked once without following it and must be the directory want describes, then opened once as
// a sub-root that must be the same directory; the entries are removed through that sub-root, so a
// name swapped after the check, such as for a link, is never followed and fails with ErrNotOwned.
// name itself goes only while it is still that directory (removeDirEntry).
func (h removalHooks) removeOnce(rt *os.Root, name string, want fs.FileInfo) error {
	fi, err := approvedEntry(rt, name, want)
	if fi == nil || err != nil {
		return err
	}
	hookAt(h.removeCheck, name)
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
	if err := sameEntry(rt, name, fi); err != nil {
		return err
	}
	hookAt(h.lastChecked, name)
	return h.removeDirEntry(rt, name, fi)
}

// approvedEntry returns the file info of name under rt, read without following it. It returns nil
// and no error when name no longer exists. It fails with ErrNotOwned when name is not a directory,
// or is not the directory want describes.
func approvedEntry(rt *os.Root, name string, want fs.FileInfo) (fs.FileInfo, error) {
	fi, err := rt.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return nil, fmt.Errorf("%w: %q is not a directory (a link is never followed)", ErrNotOwned, name)
	}
	if !os.SameFile(want, fi) {
		return nil, fmt.Errorf("%w: %q is not the directory the ownership check approved", ErrNotOwned, name)
	}
	return fi, nil
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
			errs = append(errs, removeEntryTree(sub, n))
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
// names, with the liveness of the owner the record names.
func (o *Owner) ownedTemps(ctx context.Context) ([]ownedTemp, error) {
	recs, err := o.records()
	if err != nil {
		return nil, err
	}
	return o.ownedTempsOf(ctx, recs)
}

// ownedTempsOf is ownedTemps over the records recs.
func (o *Owner) ownedTempsOf(ctx context.Context, recs []ownedRecord) ([]ownedTemp, error) {
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
		owner := ProcessID{PID: r.PID, Start: r.Start}
		if r.StartID != noneField {
			owner.id = r.StartID
		}
		out = append(out, ownedTemp{name: r.Path, live: ownerAlive(owner)})
	}
	return out, nil
}
