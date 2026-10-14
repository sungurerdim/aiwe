package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoveOwnedNoEntryRemoved catches a refusal that claims a partial removal though no entry went (#243).
func TestRemoveOwnedNoEntryRemoved(t *testing.T) {
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d := removeTestCreate(t, o, TempRoot)
	keep := filepath.Join(d.Path(), "locked", "keep.txt")
	removeTestWrite(t, keep, "owned work\n")
	removeTestHold(t, o, keep)
	tries := 0
	o.hooks.removeCheck = func(string) { tries++ }
	o.hooks.mountCheck = func(*os.File, string) (bool, error) {
		if tries > 1 {
			return false, errors.New("statx: the system does not report a mount root (test)")
		}
		return false, nil
	}
	if err := o.RemoveOwned(t.Context(), d); err == nil || !errors.Is(err, ErrNotOwned) ||
		strings.Contains(err.Error(), "partial removal") || !removeTestExists(t, keep) {
		t.Errorf("RemoveOwned with every entry removal failing = %v (kept %v); want not_owned that never claims a partial removal", err, removeTestExists(t, keep))
	}
}

// TestRemoveOwnedPartialNested catches a refusal that says nothing removed though a failed removal of
// an entry took part of its tree first (#243): RemoveAll removes locked/a.txt, then fails on the held
// locked/held/keep.txt, and the next attempt refuses on its mount check.
func TestRemoveOwnedPartialNested(t *testing.T) {
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d := removeTestCreate(t, o, TempRoot)
	gone := filepath.Join(d.Path(), "locked", "a.txt")
	keep := filepath.Join(d.Path(), "locked", "held", "keep.txt")
	removeTestWrite(t, gone, "owned work, removable\n")
	removeTestWrite(t, keep, "owned work, held\n")
	removeTestHold(t, o, keep)
	if o.hooks.entryRemove != nil { // root ignores the mode: take a.txt as RemoveAll does, then fail
		o.hooks.entryRemove = func(sub *os.Root, name string) error {
			if err := sub.Remove(filepath.Join(name, "a.txt")); err != nil {
				return err
			}
			return errors.New("remove: the entry is busy (test)")
		}
	}
	tries := 0
	o.hooks.removeCheck = func(string) { tries++ }
	o.hooks.mountCheck = func(*os.File, string) (bool, error) {
		if tries > 1 {
			return false, errors.New("statx: the system does not report a mount root (test)")
		}
		return false, nil
	}
	err := o.RemoveOwned(t.Context(), d)
	if err == nil {
		t.Fatalf("RemoveOwned after a failed removal took %q = nil; want not_owned that names a partial removal", gone)
	}
	if removeTestExists(t, gone) || !removeTestExists(t, keep) {
		t.Fatalf("after RemoveOwned: %q kept %v, %q kept %v; want the first gone and the held one kept, or the case proves nothing",
			gone, removeTestExists(t, gone), keep, removeTestExists(t, keep))
	}
	if !errors.Is(err, ErrNotOwned) || !strings.Contains(err.Error(), "partial removal") {
		t.Errorf("RemoveOwned after a failed removal took %q = %v; want not_owned that names a partial removal", gone, err)
	}
}
