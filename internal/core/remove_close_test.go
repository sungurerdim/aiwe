package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestOwnedCauseWrapped catches a refusal that drops its cause (§5.15): a root or an owned
// directory that cannot be read fails with its sentinel, and errors.Is must still find the file
// system error under it, so a caller can tell a missing path from another failure.
func TestOwnedCauseWrapped(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "temp")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "missing")
	_, realDirErr := realDir(namedPath{"home", missing})
	_, missingRootErr := (&Owner{}).checkOwnedContent(missing, "run-20261012-0001", ownedRecord{})
	_, missingNameErr := (&Owner{}).checkOwnedContent(dir, "run-20261012-0001", ownedRecord{})
	for _, c := range []struct {
		name     string
		err      error
		sentinel error
	}{
		{"realDir of a missing home", realDirErr, ErrRootUnsafe},
		{"checkRootSafe of a missing root", checkRootSafe(namedPath{"temp root", missing}, nil), ErrRootUnsafe},
		{"checkAncestors of a missing home", checkAncestors(di, namedPath{"temp root", dir}, namedPath{"home", missing}), ErrRootUnsafe},
		{"checkOwnedContent under a missing root", missingRootErr, ErrNotOwned},
		{"checkOwnedContent of a missing name", missingNameErr, ErrNotOwned},
	} {
		if !errors.Is(c.err, c.sentinel) || !errors.Is(c.err, fs.ErrNotExist) {
			t.Errorf("%s = %v; want %v that wraps %v", c.name, c.err, c.sentinel, fs.ErrNotExist)
		}
	}
}

// errTestClose is the error closeFailHook adds at the close a test names.
var errTestClose = errors.New("close failed (test)")

// testFailClose makes the close named what on the removal path fail with errTestClose until the
// test ends.
func testFailClose(t *testing.T, what string) {
	t.Helper()
	closeFailHook = func(w string) error {
		if w == what {
			return errTestClose
		}
		return nil
	}
	t.Cleanup(func() { closeFailHook = nil })
}

// TestOwnedRemoveCloseError catches a failed close on the removal path lost before the caller
// (§5.15). A close that fails during the ownership check refuses the removal and keeps the
// directory; one that fails after the removal is reported with the directory gone.
func TestOwnedRemoveCloseError(t *testing.T) {
	for _, c := range []struct {
		what string
		kept bool
	}{{"owned root", true}, {"owned directory", true}, {"removal root", false}, {"held directory", false}} {
		t.Run(c.what, func(t *testing.T) {
			o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
			d := removeTestCreate(t, o, TempRoot)
			testFailClose(t, c.what)
			if err := o.RemoveOwned(t.Context(), d); !errors.Is(err, errTestClose) {
				t.Errorf("RemoveOwned with a failing close of the %s = %v, want %v", c.what, err, errTestClose)
			}
			if got := removeTestExists(t, d.Path()); got != c.kept {
				t.Errorf("after a failing close of the %s, %q exists = %v, want %v", c.what, d.Path(), got, c.kept)
			}
		})
	}
}

// TestRemoveDirEntryCloseError catches a failed close of the held directory lost before the caller
// of removeDirEntry (§5.15); the empty directory still goes.
func TestRemoveDirEntryCloseError(t *testing.T) {
	root := t.TempDir()
	name := "run-20261012-0002"
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
	testFailClose(t, "held directory")
	if err := removeDirEntry(rt, name, fi); !errors.Is(err, errTestClose) {
		t.Errorf("removeDirEntry with a failing close of the held directory = %v, want %v", err, errTestClose)
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty dir %q left after removeDirEntry: %v", name, err)
	}
}

// TestSweepRemovalCloseError catches a failed close on the sweep path lost before the caller of
// Sweep, or miscounted in its report (§5.15, DR-13). A close that fails while the directory of a dead
// owner is checked or removed, whether it passed the ownership check or is an empty orphan, must be
// returned by Sweep. The report follows one rule: a directory whose close failed is never counted
// Removed, even when it is gone; it is counted once, as Refused. A close that fails during the
// ownership check keeps the directory.
func TestSweepRemovalCloseError(t *testing.T) {
	for _, c := range []struct {
		what  string
		owned bool
		kept  bool
	}{
		{"owned root", true, true},
		{"owned directory", true, true},
		{"held directory", true, false},
		{"removal root", true, false},
		{"held directory", false, false},
		{"sweep root", false, false},
	} {
		t.Run(fmt.Sprintf("%s, owned %v", c.what, c.owned), func(t *testing.T) {
			r := sweepTestRoots(t)
			d := sweepTestCreate(t, sweepTestOpen(t, r, sweepTestDeadProcess(t)), TempRoot)
			if !c.owned {
				if err := os.Remove(filepath.Join(d.Path(), markerFile)); err != nil {
					t.Fatal(err)
				}
			}
			testFailClose(t, c.what)
			rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(t.Context())
			if !errors.Is(err, errTestClose) {
				t.Errorf("Sweep with a failing close of the %s = %+v, %v; want %v", c.what, rep, err, errTestClose)
			}
			if want := (SweepReport{Refused: 1}); rep != want {
				t.Errorf("Sweep report with a failing close of the %s = %+v, want %+v", c.what, rep, want)
			}
			if got := sweepTestExists(t, d.Path()); got != c.kept {
				t.Errorf("after a failing close of the %s, %q exists = %v, want %v", c.what, d.Path(), got, c.kept)
			}
		})
	}
}
