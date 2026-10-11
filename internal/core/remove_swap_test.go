package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCreateOwnedTrailingDot pins the narrowing of #109 (finding 14): an owner such as "run." is
// refused, since Windows strips the dot and the dir would name another entry, while an inner dot
// stays allowed. It catches a refusal whose message hides the reason: the error must name the
// trailing-dot rule, not only the allowed characters.
func TestCreateOwnedTrailingDot(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	for _, owner := range []string{"run.", "run-20261011-0005.."} {
		d, err := o.CreateOwned(context.Background(), TempRoot, owner)
		if err == nil {
			t.Errorf("CreateOwned(%q) = %+v, nil; want a refusal of the trailing dot", owner, d)
			continue
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "end") && !strings.Contains(msg, "trailing") {
			t.Errorf("CreateOwned(%q) error %q does not name the trailing-dot rule", owner, err)
		}
	}
	recs, err := o.records()
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("ledger holds %d records after refused owners, want 0: %+v", len(recs), recs)
	}
	if _, err := o.CreateOwned(context.Background(), TempRoot, "run.20261011"); err != nil {
		t.Errorf("CreateOwned(%q) with an inner dot: %v; want it allowed", "run.20261011", err)
	}
}

// TestRemoveOwnedSwapAfterCheck catches a removal that checks the owned name, then reopens it by
// name: a name swapped after the check for a link to another dir inside the root is followed, and
// that dir is emptied (#109, finding 8). The swap runs in removeCheckedHook, right after the check,
// so the test does not rely on a race.
//
// The link target is relative ("victim"), so it stays inside the temp root and an os.Root walk
// follows it. A junction cannot stand in: its target is always absolute, and os.Root treats every
// junction as an escape, so the swap would be refused for the wrong reason. Windows without the
// symlink privilege therefore skips; Unix always runs.
func TestRemoveOwnedSwapAfterCheck(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	victim := filepath.Join(o.temp, "victim")
	keep := filepath.Join(victim, "keep.txt")
	removeTestWrite(t, keep, "inside the temp root, not owned")
	probe := filepath.Join(o.temp, "link-probe")
	if err := os.Symlink("victim", probe); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("relative symlink %s -> victim: %v; a junction is absolute and os.Root never follows it", probe, err)
		}
		t.Fatalf("relative symlink %s -> victim: %v", probe, err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove link probe %s: %v", probe, err)
	}
	moved := filepath.Join(o.temp, "moved-away")
	swapped := false
	removeCheckedHook = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		if err := os.Rename(d.Path(), moved); err != nil {
			t.Errorf("move the owned dir away: %v", err)
			return
		}
		if err := os.Symlink("victim", d.Path()); err != nil {
			t.Errorf("relative symlink %s -> victim: %v", d.Path(), err)
		}
	}
	t.Cleanup(func() { removeCheckedHook = nil })

	err := o.RemoveOwned(context.Background(), d)
	if !swapped {
		t.Fatalf("removeCheckedHook never ran for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("RemoveOwned after the swap = %v, want %v", err, ErrNotOwned)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q in a dir the owner never made was removed through the swapped name", keep)
	}
}

// TestOwnerStateSymlink catches a Unix state path that is a symlink to another owner-only dir and
// that OpenOwner follows, writing the owner key at the target (#109, finding 13). It is the Unix
// twin of TestOwnerStateJunction.
func TestOwnerStateSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows state links are covered by TestOwnerStateJunction")
	}
	r := removeTestRoots(t, t.TempDir())
	target := filepath.Join(filepath.Dir(r.State), "elsewhere")
	if err := mkdirOwnerOnly(target); err != nil {
		t.Fatalf("make owner-only target %s: %v", target, err)
	}
	if err := os.Symlink(target, r.State); err != nil {
		t.Fatalf("symlink %s -> %s: %v", r.State, target, err)
	}
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	if o, err := OpenOwner(r, self); !errors.Is(err, ErrRootUnsafe) {
		t.Errorf("OpenOwner of state symlink %s -> %s: owner %t, error %v; want %v", r.State, target, o != nil, err, ErrRootUnsafe)
	}
	if removeTestExists(t, filepath.Join(target, ownerKeyFile)) {
		t.Errorf("OpenOwner wrote a key at the symlink target %s", target)
	}
}

// swapTestReplace moves the owned dir at owned aside to moved and moves a plain dir that the owner
// never made, holding keep.txt, into its place under the same name. It is a rename, not a link, so
// it runs on Windows without the symlink privilege.
func swapTestReplace(t *testing.T, owned, moved, foreign string) {
	t.Helper()
	if err := os.Rename(owned, moved); err != nil {
		t.Errorf("move the owned dir %s away: %v", owned, err)
		return
	}
	if err := os.Rename(foreign, owned); err != nil {
		t.Errorf("move the unowned dir %s to %s: %v", foreign, owned, err)
	}
}

// TestRemoveOwnedSwapAfterOwnerCheck catches a RemoveOwned that checks ownership by name, then
// removes by name: a plain dir moved under the owned name after the check is emptied though the
// owner never made it (#109). The swap runs in ownedCheckedHook, right after the ownership check
// and before the removal starts, so the test does not rely on a race.
func TestRemoveOwnedSwapAfterOwnerCheck(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	foreign := filepath.Join(o.temp, "not-ours")
	removeTestWrite(t, filepath.Join(foreign, "keep.txt"), "inside the temp root, not owned")
	keep := filepath.Join(d.Path(), "keep.txt")
	swapped := false
	ownedCheckedHook = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		swapTestReplace(t, d.Path(), filepath.Join(o.temp, "moved-away"), foreign)
	}
	t.Cleanup(func() { ownedCheckedHook = nil })

	err := o.RemoveOwned(context.Background(), d)
	if !swapped {
		t.Fatalf("ownedCheckedHook never ran for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("RemoveOwned after the swap = %v, want %v", err, ErrNotOwned)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q in a dir the owner never made was removed through the swapped name", keep)
	}
}

// TestSweepSwapAfterOwnerCheck catches a Sweep that checks every dead owner's dir first, then
// removes each by name: a plain dir moved under an owned name after the check is emptied and
// counted as Removed (#109). The other owned dir of the dead owner must still be removed.
func TestSweepSwapAfterOwnerCheck(t *testing.T) {
	ctx := context.Background()
	r := sweepTestRoots(t)
	dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	a := sweepTestCreate(t, dead, TempRoot)
	b := sweepTestCreate(t, dead, TempRoot)
	removeTestWrite(t, filepath.Join(b.Path(), "work.txt"), "owned work")
	foreign := filepath.Join(dead.temp, "not-ours")
	removeTestWrite(t, filepath.Join(foreign, "keep.txt"), "inside the temp root, not owned")
	keep := filepath.Join(b.Path(), "keep.txt")
	swapped := false
	ownedCheckedHook = func(name string) {
		if swapped || name != filepath.Base(b.Path()) {
			return
		}
		swapped = true
		swapTestReplace(t, b.Path(), filepath.Join(dead.temp, "moved-away"), foreign)
	}
	t.Cleanup(func() { ownedCheckedHook = nil })

	rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(ctx)
	if !swapped {
		t.Fatalf("ownedCheckedHook never ran for %q", filepath.Base(b.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("Sweep after the swap: %v, want %v for the swapped name", err, ErrNotOwned)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q in a dir the owner never made was swept through the swapped name", keep)
	}
	if removeTestExists(t, a.Path()) {
		t.Errorf("owned temp dir %q of a dead owner is left after the sweep", a.Path())
	}
	if rep.Removed != 1 {
		t.Errorf("Sweep report = %+v, want Removed 1: the swapped name %q must not count as removed", rep, filepath.Base(b.Path()))
	}
	if rep.Refused != 1 {
		t.Errorf("Sweep report = %+v, want Refused 1: the swapped name %q must count as refused", rep, filepath.Base(b.Path()))
	}
}

// TestRemoveOwnedSwapBeforeMarkerOpen catches an ownership check that reads the marker through a
// dir opened by name without proving it is the dir its Lstat saw (#109, finding 3). The swap runs
// in ownedMarkerHook, after the Lstat and before the open. The unowned dir holds a copy of the
// owned marker, so a missing marker cannot be the reason for the refusal.
func TestRemoveOwnedSwapBeforeMarkerOpen(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	marker, err := os.ReadFile(filepath.Join(d.Path(), markerFile))
	if err != nil {
		t.Fatalf("read the owned marker: %v", err)
	}
	foreign := filepath.Join(o.temp, "not-ours")
	removeTestWrite(t, filepath.Join(foreign, "keep.txt"), "inside the temp root, not owned")
	removeTestWrite(t, filepath.Join(foreign, markerFile), string(marker))
	keep := filepath.Join(d.Path(), "keep.txt")
	swapped := false
	ownedMarkerHook = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		swapTestReplace(t, d.Path(), filepath.Join(o.temp, "moved-away"), foreign)
	}
	t.Cleanup(func() { ownedMarkerHook = nil })

	err = o.RemoveOwned(context.Background(), d)
	if !swapped {
		t.Fatalf("ownedMarkerHook never ran for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("RemoveOwned after the swap before the marker open = %v, want %v", err, ErrNotOwned)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q in a dir the owner never made was removed through the swapped name", keep)
	}
}
