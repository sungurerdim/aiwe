//go:build windows

package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rmdirTestOutcome fails t unless the result of a removal matches what happened to the approved
// dir: nil only when no path in paths holds it any more, ErrNotOwned only while one still does.
func rmdirTestOutcome(t *testing.T, err error, approved fs.FileInfo, paths ...string) {
	t.Helper()
	held := ""
	for _, p := range paths {
		if fi, lerr := os.Lstat(p); lerr == nil && os.SameFile(approved, fi) {
			held = p
		}
	}
	switch {
	case err == nil && held != "":
		t.Errorf("RemoveOwned = nil, but the approved dir survives at %s", held)
	case err != nil && !errors.Is(err, ErrNotOwned):
		t.Errorf("RemoveOwned = %v, want nil or %v", err, ErrNotOwned)
	case err != nil && held == "":
		t.Errorf("RemoveOwned = %v, but the approved dir is gone", err)
	}
}

// rmdirTestIdentity returns the file info of the dir at path read through a handle, so its file ID
// is read now and still names that dir after the name is swapped.
func rmdirTestIdentity(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the owned dir %s: %v", path, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close the owned dir %s: %v", path, err)
		}
	}()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat the owned dir %s: %v", path, err)
	}
	return fi
}

// rmdirTestDisposition sets the delete disposition of the open handle h to del.
func rmdirTestDisposition(h windows.Handle, del bool) error {
	b := del
	return windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&b)), 1)
}

// rmdirTestMarkPending opens path for delete, as another process may, and marks it delete-pending.
// It returns the handle, or an error when the open or the mark is refused.
func rmdirTestMarkPending(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	h, err := windows.CreateFile(p, windows.DELETE|windows.FILE_READ_ATTRIBUTES, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return 0, err
	}
	if err := rmdirTestDisposition(h, true); err != nil {
		return 0, errors.Join(err, windows.CloseHandle(h))
	}
	return h, nil
}

// TestRemoveOwnedDeletePendingBeforeRmdir catches a removal that reads delete-pending as proof
// that the approved dir is gone (#144). In hooks.rmdirChecked another handle marks the approved dir
// delete-pending, under its name ("same name") or after a rename that puts an unowned empty dir
// under the name ("moved"), and clears the mark after the removal returns. The removal must
// return nil only when the approved dir is gone, and must keep the unowned dir.
func TestRemoveOwnedDeletePendingBeforeRmdir(t *testing.T) {
	for _, mode := range []string{"same name", "moved"} {
		t.Run(mode, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			o := removeTestOpen(t, r)
			d := removeTestCreate(t, o, TempRoot)
			approved := rmdirTestIdentity(t, d.Path())
			moved := filepath.Join(o.temp, "moved-away")
			var pending windows.Handle
			ran, interposed := false, false
			o.hooks.rmdirChecked = func(string) {
				if ran {
					return
				}
				ran = true
				target := d.Path()
				if mode == "moved" {
					if os.Rename(d.Path(), moved) != nil {
						return // the held handle refuses the rename: nothing to mark
					}
					swapTestForeign(t, d.Path(), true)
					interposed, target = true, moved
				}
				if h, err := rmdirTestMarkPending(target); err == nil {
					pending = h
				}
			}

			err := o.RemoveOwned(context.Background(), d)
			if pending != 0 {
				if cerr := rmdirTestDisposition(pending, false); cerr != nil {
					t.Errorf("clear the delete-pending mark: %v", cerr)
				}
				if cerr := windows.CloseHandle(pending); cerr != nil {
					t.Errorf("close the delete-pending handle: %v", cerr)
				}
			}
			if !ran {
				t.Fatalf("hooks.rmdirChecked never ran for %s", d.Path())
			}
			rmdirTestOutcome(t, err, approved, d.Path(), moved)
			if interposed && !removeTestExists(t, d.Path()) {
				t.Errorf("unowned empty dir put under %s was removed", d.Path())
			}
		})
	}
}

// TestRemoveOwnedJunctionBeforeRmdir catches a removal by name that deletes a junction swapped in
// under the name (#144): in hooks.rmdirChecked the approved dir moves away and a junction to a dir
// with a file takes its name. The junction and the file behind it must stay, and the result must
// match what happened to the approved dir.
func TestRemoveOwnedJunctionBeforeRmdir(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatalf("make the victim dir %s: %v", victim, err)
	}
	removeTestWrite(t, filepath.Join(victim, "keep.txt"), "not owned")
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	approved := rmdirTestIdentity(t, d.Path())
	moved := filepath.Join(o.temp, "moved-away")
	ran, linked := false, false
	o.hooks.rmdirChecked = func(string) {
		if ran {
			return
		}
		ran = true
		if os.Rename(d.Path(), moved) != nil {
			return // the held handle refuses the rename: no junction can take the name
		}
		JunctionForTest(t, victim, d.Path())
		linked = true
	}

	err := o.RemoveOwned(context.Background(), d)
	if !ran {
		t.Fatalf("hooks.rmdirChecked never ran for %s", d.Path())
	}
	rmdirTestOutcome(t, err, approved, d.Path(), moved)
	if fi, lerr := os.Lstat(d.Path()); linked && (lerr != nil || fi.Mode().Type() == fs.ModeDir) {
		t.Errorf("junction put under %s: lstat %v, error %v; want it kept", d.Path(), fi, lerr)
	}
	if b, rerr := os.ReadFile(filepath.Join(victim, "keep.txt")); rerr != nil || string(b) != "not owned" {
		t.Errorf("file behind the junction: content %q, error %v; want it kept", b, rerr)
	}
}

// TestRemoveDirEntryParentCloseError catches a failed close of the parent handle that openHeldDir
// opens on Windows, lost before the caller of removeDirEntry (§5.15): the error is returned and
// the empty directory stays, so the next sweep retries it.
func TestRemoveDirEntryParentCloseError(t *testing.T) {
	root := t.TempDir()
	name := "run-20261012-0003"
	if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
		t.Fatal(err)
	}
	rt, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	fi, err := rt.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	h := removalHooks{closeFail: testFailClose("held parent")}
	if err := h.removeDirEntry(rt, name, fi); !errors.Is(err, errCloseFailed) || !errors.Is(err, errTestClose) {
		t.Errorf("removeDirEntry with a failing close of the held parent = %v, want %v from %v", err, errCloseFailed, errTestClose)
	}
	if !removeTestExists(t, filepath.Join(root, name)) {
		t.Errorf("empty dir %q removed although the close of its held parent failed", name)
	}
}
