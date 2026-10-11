package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
)

// rmdirTestKinds are the entries a test puts under an owned name in the moment between the last
// handle check and the directory-only removal: an unowned file, an unowned empty dir, or nothing.
var rmdirTestKinds = []string{"file", "empty dir", "nothing"}

// rmdirSharingViolation is ERROR_SHARING_VIOLATION: on Windows the removal holds the owned dir
// open without delete sharing, so the system refuses to move it.
const rmdirSharingViolation = syscall.Errno(32)

// rmdirTestSwap arms rmdirCheckedHook for one removal of name under temp: the owned dir moves to
// moved, then an entry of kind takes its name. It returns a func that reports whether the hook ran
// and whether the system refused the move (Windows only), so no swap took place.
func rmdirTestSwap(t *testing.T, temp, name, moved, kind string) func() (bool, bool) {
	t.Helper()
	swapped, refused := false, false
	rmdirCheckedHook = func(n string) {
		if swapped || n != name {
			return
		}
		swapped = true
		if err := os.Rename(filepath.Join(temp, name), moved); err != nil {
			refused = runtime.GOOS == "windows" && errors.Is(err, rmdirSharingViolation)
			if !refused {
				t.Errorf("move the owned dir %s away: %v", name, err)
			}
			return
		}
		if kind != "nothing" {
			swapTestForeign(t, filepath.Join(temp, name), kind == "empty dir")
		}
	}
	t.Cleanup(func() { rmdirCheckedHook = nil })
	return func() (bool, bool) { return swapped, refused }
}

// rmdirTestCheckRemoved fails t unless a removal whose swap the system refused returned nil and
// left temp empty: the owned dir is gone and nothing else was made.
func rmdirTestCheckRemoved(t *testing.T, err error, temp string) {
	t.Helper()
	if err != nil {
		t.Errorf("removal after a refused swap = %v, want nil: the owned dir never left its name", err)
	}
	entries, rerr := os.ReadDir(temp)
	if rerr != nil {
		t.Fatalf("read the temp root %s: %v", temp, rerr)
	}
	for _, e := range entries {
		t.Errorf("temp root %s holds %q after the removal, want it empty", temp, e.Name())
	}
}

// rmdirTestCheckKept fails t unless the owned dir is still at moved, an unowned file under name
// still holds its content, and temp holds no entry but name and moved: the removal deleted no file
// and left no residue of its own (#144). An unowned empty dir under name may be removed: that is
// the accepted worst case of a removal by name.
func rmdirTestCheckKept(t *testing.T, temp, name, moved, kind string) {
	t.Helper()
	if !removeTestExists(t, moved) {
		t.Errorf("owned dir moved to %q is gone; the swap did not leave it elsewhere", moved)
	}
	if kind == "file" {
		b, err := os.ReadFile(filepath.Join(temp, name))
		if err != nil || string(b) != "not owned" {
			t.Errorf("unowned file %q put under the owned name: content %q, error %v; want it kept as %q", name, b, err, "not owned")
		}
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatalf("read the temp root %s: %v", temp, err)
	}
	for _, e := range entries {
		if !slices.Contains([]string{name, filepath.Base(moved)}, e.Name()) {
			t.Errorf("temp root %s holds %q after the removal, want only %q and %q", temp, e.Name(), name, filepath.Base(moved))
		}
	}
}

// TestRemoveOwnedSwapBeforeRmdir catches a last removal step that deletes whatever its name names
// after its handle check: a file put under the name is deleted, or the call reports success while
// the owned dir stays elsewhere (#144). The swap runs in rmdirCheckedHook, after the check of the
// held handle and before the directory-only removal. Each kind must fail with ErrNotOwned.
func TestRemoveOwnedSwapBeforeRmdir(t *testing.T) {
	for _, kind := range rmdirTestKinds {
		t.Run(kind, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			o := removeTestOpen(t, r)
			d := removeTestCreate(t, o, TempRoot)
			removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
			name, moved := filepath.Base(d.Path()), filepath.Join(o.temp, "moved-away")
			ran := rmdirTestSwap(t, o.temp, name, moved, kind)

			err := o.RemoveOwned(context.Background(), d)
			if ok, refused := ran(); !ok {
				t.Fatalf("rmdirCheckedHook never ran for %q", name)
			} else if refused {
				rmdirTestCheckRemoved(t, err, o.temp)
				return
			}
			if !errors.Is(err, ErrNotOwned) {
				t.Errorf("RemoveOwned after the swap = %v, want %v: the owned dir stays at %s", err, ErrNotOwned, moved)
			}
			rmdirTestCheckKept(t, o.temp, name, moved, kind)
		})
	}
}

// TestSweepSwapBeforeRmdir is TestRemoveOwnedSwapBeforeRmdir through Sweep: the swapped name of a
// dead owner must count as Refused with not_owned, never as Removed.
func TestSweepSwapBeforeRmdir(t *testing.T) {
	for _, kind := range rmdirTestKinds {
		t.Run(kind, func(t *testing.T) {
			r := sweepTestRoots(t)
			dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
			d := sweepTestCreate(t, dead, TempRoot)
			removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
			name, moved := filepath.Base(d.Path()), filepath.Join(dead.temp, "moved-away")
			ran := rmdirTestSwap(t, dead.temp, name, moved, kind)

			rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
			if ok, refused := ran(); !ok {
				t.Fatalf("rmdirCheckedHook never ran for %q", name)
			} else if refused {
				rmdirTestCheckRemoved(t, err, dead.temp)
				if rep.Removed != 1 || rep.Refused != 0 {
					t.Errorf("Sweep report = %+v, want Removed 1, Refused 0 after a refused swap", rep)
				}
				return
			}
			if !errors.Is(err, ErrNotOwned) {
				t.Errorf("Sweep after the swap = %v, want %v", err, ErrNotOwned)
			}
			if rep.Removed != 0 || rep.Refused != 1 {
				t.Errorf("Sweep report = %+v, want Removed 0, Refused 1 for the swapped name %q", rep, name)
			}
			rmdirTestCheckKept(t, dead.temp, name, moved, kind)
		})
	}
}

// TestSweepEmptyDirSwapBeforeRmdir is TestSweepSwapBeforeRmdir for an empty dir that fails the
// ownership check: it goes through the same directory-only removal, so a file put under its name
// is never deleted and a swapped name counts as Refused with not_owned (#144).
func TestSweepEmptyDirSwapBeforeRmdir(t *testing.T) {
	for _, kind := range rmdirTestKinds {
		t.Run(kind, func(t *testing.T) {
			r := sweepTestRoots(t)
			dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
			d := sweepTestCreate(t, dead, TempRoot)
			// A crash between Mkdir and the marker leaves an empty dir that fails the ownership check.
			if err := os.Remove(filepath.Join(d.Path(), markerFile)); err != nil {
				t.Fatalf("remove the marker of %s: %v", d.Path(), err)
			}
			name, moved := filepath.Base(d.Path()), filepath.Join(dead.temp, "moved-away")
			ran := rmdirTestSwap(t, dead.temp, name, moved, kind)

			rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
			if ok, refused := ran(); !ok {
				t.Fatalf("rmdirCheckedHook never ran for %q", name)
			} else if refused {
				rmdirTestCheckRemoved(t, err, dead.temp)
				if rep.Removed != 1 || rep.Refused != 0 {
					t.Errorf("Sweep report = %+v, want Removed 1, Refused 0 after a refused swap", rep)
				}
				return
			}
			if !errors.Is(err, ErrNotOwned) {
				t.Errorf("Sweep after the swap = %v, want %v", err, ErrNotOwned)
			}
			if rep.Removed != 0 || rep.Refused != 1 {
				t.Errorf("Sweep report = %+v, want Removed 0, Refused 1 for the swapped name %q", rep, name)
			}
			rmdirTestCheckKept(t, dead.temp, name, moved, kind)
		})
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
