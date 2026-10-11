package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// asideTestFindContent reports whether a regular file under root holds exactly content. Links are
// never followed.
func asideTestFindContent(t *testing.T, root, content string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || found || !e.Type().IsRegular() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && string(b) == content {
			found = true
		}
		return rerr
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// asideTestInterpose arms the hooks for one removal of name under temp: after the last check, the
// owned dir moves to moved and an unowned file holding "swapped" takes its name; the removal then
// moves that file aside, sees it is not the approved dir, and moves it back. Right before the move
// back, an unowned file holding "interposed" takes the free name. It returns a func that reports
// whether both hooks ran.
func asideTestInterpose(t *testing.T, temp, name, moved string) func() bool {
	t.Helper()
	swapped, interposed := false, false
	removeLastCheckedHook = func(n string) {
		if swapped || n != name {
			return
		}
		swapped = true
		if err := os.Rename(filepath.Join(temp, name), moved); err != nil {
			t.Errorf("move the owned dir %s away: %v", name, err)
			return
		}
		removeTestWrite(t, filepath.Join(temp, name), "swapped")
	}
	moveBackCheckedHook = func(n string) {
		if interposed || n != name {
			return
		}
		interposed = true
		removeTestWrite(t, filepath.Join(temp, name), "interposed")
	}
	t.Cleanup(func() {
		removeLastCheckedHook = nil
		moveBackCheckedHook = nil
	})
	return func() bool { return swapped && interposed }
}

// asideTestCheckInterposed fails t unless the file under path still holds "interposed" and the
// swapped file still exists somewhere under temp: the move back replaced nothing and lost nothing.
func asideTestCheckInterposed(t *testing.T, temp, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read the interposed file %s: %v", path, err)
	} else if string(b) != "interposed" {
		t.Errorf("file put under the free name %s holds %q, want %q: the move back replaced it", path, b, "interposed")
	}
	if !asideTestFindContent(t, temp, "swapped") {
		t.Errorf("the swapped file is gone from %s: the move back lost it", temp)
	}
}

// TestRemoveOwnedMoveBackKeepsInterposedFile catches a move back that checks that the name is free,
// then renames onto it: Root.Rename replaces an existing file on every OS, so a file put under the
// free name between the check and the rename is overwritten and lost (#144). The interposition
// runs in moveBackCheckedHook, after the free-name check and before the move back.
func TestRemoveOwnedMoveBackKeepsInterposedFile(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	ran := asideTestInterpose(t, o.temp, filepath.Base(d.Path()), filepath.Join(o.temp, "moved-away"))

	err := o.RemoveOwned(context.Background(), d)
	if !ran() {
		t.Fatalf("removeLastCheckedHook and moveBackCheckedHook did not both run for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("RemoveOwned after the swap = %v, want %v", err, ErrNotOwned)
	}
	asideTestCheckInterposed(t, o.temp, d.Path())
}

// TestSweepMoveBackKeepsInterposedFile is TestRemoveOwnedMoveBackKeepsInterposedFile through Sweep:
// the dir of a dead owner is swapped, the move back meets a file interposed under the free name,
// and that file must survive while the name counts as Refused with not_owned, never as Removed.
func TestSweepMoveBackKeepsInterposedFile(t *testing.T) {
	r := sweepTestRoots(t)
	dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	d := sweepTestCreate(t, dead, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	ran := asideTestInterpose(t, dead.temp, filepath.Base(d.Path()), filepath.Join(dead.temp, "moved-away"))

	rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
	if !ran() {
		t.Fatalf("removeLastCheckedHook and moveBackCheckedHook did not both run for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("Sweep after the swap = %v, want %v", err, ErrNotOwned)
	}
	if rep.Removed != 0 || rep.Refused != 1 {
		t.Errorf("Sweep report = %+v, want Removed 0, Refused 1", rep)
	}
	asideTestCheckInterposed(t, dead.temp, d.Path())
}

// TestSweepRemovesCrashedRemoveAside catches a sweep that never sees the directory a removal moves
// its target into before deleting it: a process that dies between that move and the delete leaves
// .remove-<hex> under the temp root with no live owner, and no later sweep removes it (DR-36,
// SPEC §6.4 "temp: 0 directories without a live owner"; "the sweeper covers crashes"). The crash
// state is built as removeDirEntry leaves it: the emptied owned dir of a dead owner, moved under a
// new .remove-<hex> dir; its owned_dir record still names the old path.
func TestSweepRemovesCrashedRemoveAside(t *testing.T) {
	r := sweepTestRoots(t)
	dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	d := sweepTestCreate(t, dead, TempRoot)
	if err := os.Remove(filepath.Join(d.Path(), markerFile)); err != nil {
		t.Fatalf("remove the marker of %s: %v", d.Path(), err)
	}
	suffix := make([]byte, dirSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	aside := filepath.Join(dead.temp, ".remove-"+hex.EncodeToString(suffix))
	if err := os.Mkdir(aside, 0o700); err != nil {
		t.Fatalf("make the aside dir %s: %v", aside, err)
	}
	if err := os.Rename(d.Path(), filepath.Join(aside, filepath.Base(d.Path()))); err != nil {
		t.Fatalf("move %s aside: %v", d.Path(), err)
	}

	_, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
	if err != nil {
		t.Errorf("Sweep: %v", err)
	}
	if sweepTestExists(t, aside) {
		t.Errorf("Sweep left the crashed removal's aside dir %s: a temp dir without a live owner stays forever", aside)
	}
}

// TestSweepEmptyDirSwapReturnsNotOwned catches a sweep of an empty unowned dir that drops the
// error of a swap after its check: Sweep must return the not_owned error of the swapped name, and
// errors.Is must find ErrNotOwned in it, so the wrapping (%w) is fixed (#144). The swap runs in
// removeEmptyCheckedHook: the empty dir moves away and an unowned file takes its name.
func TestSweepEmptyDirSwapReturnsNotOwned(t *testing.T) {
	r := sweepTestRoots(t)
	dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	d := sweepTestCreate(t, dead, TempRoot)
	// A crash between Mkdir and the marker leaves an empty dir that fails the ownership check.
	if err := os.Remove(filepath.Join(d.Path(), markerFile)); err != nil {
		t.Fatalf("remove the marker of %s: %v", d.Path(), err)
	}
	swapped := false
	removeEmptyCheckedHook = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		if err := os.Rename(d.Path(), filepath.Join(dead.temp, "moved-away")); err != nil {
			t.Errorf("move the empty dir %s away: %v", d.Path(), err)
			return
		}
		swapTestForeign(t, d.Path(), false)
	}
	t.Cleanup(func() { removeEmptyCheckedHook = nil })

	rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
	if !swapped {
		t.Fatalf("removeEmptyCheckedHook never ran for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("Sweep after the swap = %v, want an error that wraps %v", err, ErrNotOwned)
	}
	if rep.Removed != 0 || rep.Refused != 1 {
		t.Errorf("Sweep report = %+v, want Removed 0, Refused 1", rep)
	}
	if !removeTestExists(t, d.Path()) {
		t.Errorf("unowned file %q put under the empty dir name was deleted by the sweep", d.Path())
	}
}
