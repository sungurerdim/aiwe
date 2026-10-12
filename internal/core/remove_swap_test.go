package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
// that dir is emptied (#109, finding 8). The swap runs in hooks.removeCheck, right after the check,
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
	o.hooks.removeCheck = func(name string) {
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

	err := o.RemoveOwned(context.Background(), d)
	if !swapped {
		t.Fatalf("hooks.removeCheck never ran for %q", filepath.Base(d.Path()))
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
// owner never made it (#109). The swap runs in hooks.ownedChecked, right after the ownership check
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
	o.hooks.ownedChecked = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		swapTestReplace(t, d.Path(), filepath.Join(o.temp, "moved-away"), foreign)
	}

	err := o.RemoveOwned(context.Background(), d)
	if !swapped {
		t.Fatalf("hooks.ownedChecked never ran for %q", filepath.Base(d.Path()))
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
	sw := sweepTestOpen(t, r, sweepTestSelf(t))
	sw.hooks.ownedChecked = func(name string) {
		if swapped || name != filepath.Base(b.Path()) {
			return
		}
		swapped = true
		swapTestReplace(t, b.Path(), filepath.Join(dead.temp, "moved-away"), foreign)
	}

	rep, err := sw.Sweep(ctx)
	if !swapped {
		t.Fatalf("hooks.ownedChecked never ran for %q", filepath.Base(b.Path()))
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
// in hooks.ownedMarker, after the Lstat and before the open. The unowned dir holds a copy of the
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
	o.hooks.ownedMarker = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		swapTestReplace(t, d.Path(), filepath.Join(o.temp, "moved-away"), foreign)
	}

	err = o.RemoveOwned(context.Background(), d)
	if !swapped {
		t.Fatalf("hooks.ownedMarker never ran for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("RemoveOwned after the swap before the marker open = %v, want %v", err, ErrNotOwned)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q in a dir the owner never made was removed through the swapped name", keep)
	}
}

// TestOwnerCheckMarkerOfSwappedName catches a checkMarkerOf that reads a marker through the dir it
// opened by name without proving that dir is the one its caller's Lstat saw (#109, finding 3).
// TestRemoveOwnedSwapBeforeMarkerOpen cannot pin this: the checks that follow checkMarkerOf refuse
// that swap too, yet they would pass a swap followed by a swap back. So the test calls
// checkMarkerOf itself, with the info read before the swap. The dir now under the name holds a
// copy of the owned marker, and a control call shows that it passes under its own info, so only
// the identity check can refuse.
func TestOwnerCheckMarkerOfSwappedName(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	name := filepath.Base(d.Path())
	marker, err := os.ReadFile(filepath.Join(d.Path(), markerFile))
	if err != nil {
		t.Fatalf("read the owned marker: %v", err)
	}
	recs, err := o.records()
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	rec, ok := findRecord(recs, "temp", name)
	if !ok {
		t.Fatalf("ledger has no owned_dir record for %q", name)
	}
	foreign := filepath.Join(o.temp, "not-ours")
	removeTestWrite(t, filepath.Join(foreign, "keep.txt"), "inside the temp root, not owned")
	removeTestWrite(t, filepath.Join(foreign, markerFile), string(marker))
	rt, err := os.OpenRoot(o.temp)
	if err != nil {
		t.Fatalf("open root %s: %v", o.temp, err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Errorf("close root %s: %v", o.temp, err)
		}
	}()

	before := swapTestLstat(t, o.temp, name)
	swapTestReplace(t, d.Path(), filepath.Join(o.temp, "moved-away"), foreign)
	after := swapTestLstat(t, o.temp, name)
	if err := o.checkMarkerOf(rt, o.temp, name, after, rec); err != nil {
		t.Fatalf("checkMarkerOf of the dir now under %q = %v; its marker copy must pass under its own info", name, err)
	}
	err = o.checkMarkerOf(rt, o.temp, name, before, rec)
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("checkMarkerOf(%q) with the info read before the swap = %v, want %v", name, err, ErrNotOwned)
	}
}

// TestRemoveTreeEscape catches an owned name that leaves its root, or was swapped for a link out
// of it after the check, reported as residue_locked after 5 retries instead of not_owned at once.
// Each name comes with the info of a real dir, so the refusal must come from the name check or the
// link check, not from the refusal of a nil approval (TestRemoveTreeNilApproved).
func TestRemoveTreeEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "temp")
	outside := filepath.Join(base, "outside")
	keep := filepath.Join(outside, "keep.txt")
	removeTestMkdir(t, root)
	removeTestWrite(t, keep, "outside the temp root")
	// The ownership check approved a real dir under this name; then the name became a link out of
	// the root. The dir moves away rather than going, so no new entry can take over its identity.
	swapped := "run-20261011-0004-0123456789abcdef"
	removeTestMkdir(t, filepath.Join(root, swapped))
	cases := []struct {
		name     string
		approved os.FileInfo
	}{
		{filepath.Join("..", "outside"), swapTestLstat(t, base, "outside")},
		{swapped, swapTestLstat(t, root, swapped)},
	}
	if err := os.Rename(filepath.Join(root, swapped), filepath.Join(base, "moved-away")); err != nil {
		t.Fatalf("move the approved dir away: %v", err)
	}
	removeTestLink(t, outside, filepath.Join(root, swapped))

	for _, tc := range cases {
		begin := time.Now()
		err := removalHooks{}.removeTree(context.Background(), root, tc.name, tc.approved)
		took := time.Since(begin)
		if !errors.Is(err, ErrNotOwned) || errors.Is(err, ErrResidueLocked) {
			t.Errorf("removeTree(%q) = %v, want not_owned, not residue_locked", tc.name, err)
		}
		if took >= removeWait {
			t.Errorf("removeTree(%q) took %v, want no retry of an escape (under %v)", tc.name, took, removeWait)
		}
		if !removeTestExists(t, keep) {
			t.Fatalf("file %q outside the root was removed through %q", keep, tc.name)
		}
	}
}

// swapTestForeign puts an entry the owner never made at path: a file, or an empty dir when dir is true.
func swapTestForeign(t *testing.T, path string, dir bool) {
	t.Helper()
	if dir {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Errorf("make the unowned empty dir %s: %v", path, err)
		}
		return
	}
	if err := os.WriteFile(path, []byte("not owned"), 0o644); err != nil {
		t.Errorf("write the unowned file %s: %v", path, err)
	}
}

// TestSweepEmptyDirSwappedForFile catches the sweep of an empty unowned dir that removes by name
// after its type check: a file put under the name after the check is deleted and counted as
// Removed (#144). Root.Remove also deletes files, so only a check right before the removal keeps
// it. The swap runs in hooks.emptyChecked, after the check and before the removal.
func TestSweepEmptyDirSwappedForFile(t *testing.T) {
	r := sweepTestRoots(t)
	dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	d := sweepTestCreate(t, dead, TempRoot)
	// A crash between Mkdir and the marker leaves an empty dir that fails the ownership check.
	if err := os.Remove(filepath.Join(d.Path(), markerFile)); err != nil {
		t.Fatalf("remove the marker of %s: %v", d.Path(), err)
	}
	moved := filepath.Join(dead.temp, "moved-away")
	swapped := false
	sw := sweepTestOpen(t, r, sweepTestSelf(t))
	sw.hooks.emptyChecked = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		if err := os.Rename(d.Path(), moved); err != nil {
			t.Errorf("move the empty dir %s away: %v", d.Path(), err)
			return
		}
		swapTestForeign(t, d.Path(), false)
	}

	rep, err := sw.Sweep(context.Background())
	if !swapped {
		t.Fatalf("hooks.emptyChecked never ran for %q", filepath.Base(d.Path()))
	}
	if err != nil && !errors.Is(err, ErrNotOwned) {
		t.Errorf("Sweep after the swap: %v, want nil or %v", err, ErrNotOwned)
	}
	if !removeTestExists(t, d.Path()) {
		t.Errorf("unowned file %q put under the empty dir name was deleted by the sweep", d.Path())
	}
	if rep.Removed != 0 || rep.Refused != 1 {
		t.Errorf("Sweep report = %+v, want Removed 0, Refused 1: the file under %q is not an empty dir", rep, filepath.Base(d.Path()))
	}
}

// TestRemoveOwnedSwapBeforeLastRemove catches a removal that empties the owned dir, checks its
// name once more, then removes by name: an unowned file or empty dir put under the name after
// that check is deleted, and the call reports success while the owned dir stays elsewhere
// (#144). The swap runs in hooks.lastChecked, after the last check and before the removal.
func TestRemoveOwnedSwapBeforeLastRemove(t *testing.T) {
	for _, tc := range []struct {
		kind string
		dir  bool
	}{{"file", false}, {"empty dir", true}} {
		t.Run(tc.kind, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			o := removeTestOpen(t, r)
			d := removeTestCreate(t, o, TempRoot)
			removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
			moved := filepath.Join(o.temp, "moved-away")
			swapped := false
			o.hooks.lastChecked = func(name string) {
				if swapped || name != filepath.Base(d.Path()) {
					return
				}
				swapped = true
				if err := os.Rename(d.Path(), moved); err != nil {
					t.Errorf("move the owned dir %s away: %v", d.Path(), err)
					return
				}
				swapTestForeign(t, d.Path(), tc.dir)
			}

			err := o.RemoveOwned(context.Background(), d)
			if !swapped {
				t.Fatalf("hooks.lastChecked never ran for %q", filepath.Base(d.Path()))
			}
			if !errors.Is(err, ErrNotOwned) {
				t.Errorf("RemoveOwned after the swap = %v, want %v: the owned dir stays at %s", err, ErrNotOwned, moved)
			}
			if !removeTestExists(t, d.Path()) {
				t.Errorf("unowned %s %q put under the owned name was deleted", tc.kind, d.Path())
			}
			if !removeTestExists(t, moved) {
				t.Errorf("owned dir moved to %q is gone; the swap did not leave it elsewhere", moved)
			}
		})
	}
}

// TestSweepSwapBeforeLastRemove catches a Sweep that counts a swapped name as Removed: the owned
// dir of a dead owner is moved aside after the last check, an unowned empty dir takes its name,
// and that dir is deleted in its place (#144). The name must count as Refused with not_owned.
func TestSweepSwapBeforeLastRemove(t *testing.T) {
	r := sweepTestRoots(t)
	dead := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	d := sweepTestCreate(t, dead, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	swapped := false
	sw := sweepTestOpen(t, r, sweepTestSelf(t))
	sw.hooks.lastChecked = func(name string) {
		if swapped || name != filepath.Base(d.Path()) {
			return
		}
		swapped = true
		if err := os.Rename(d.Path(), filepath.Join(dead.temp, "moved-away")); err != nil {
			t.Errorf("move the owned dir %s away: %v", d.Path(), err)
			return
		}
		swapTestForeign(t, d.Path(), true)
	}

	rep, err := sw.Sweep(context.Background())
	if !swapped {
		t.Fatalf("hooks.lastChecked never ran for %q", filepath.Base(d.Path()))
	}
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("Sweep after the swap: %v, want %v for the swapped name", err, ErrNotOwned)
	}
	if !removeTestExists(t, d.Path()) {
		t.Errorf("unowned empty dir %q put under the owned name was swept", d.Path())
	}
	if rep.Removed != 0 || rep.Refused != 1 {
		t.Errorf("Sweep report = %+v, want Removed 0, Refused 1 for the swapped name %q", rep, filepath.Base(d.Path()))
	}
}

// TestRemoveOwnedBindMount catches mounted data deleted through a bind mount in an owned dir (#136).
func TestRemoveOwnedBindMount(t *testing.T) {
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d, src := removeTestCreate(t, o, TempRoot), t.TempDir()
	keep, mnt := filepath.Join(src, "keep.txt"), filepath.Join(d.Path(), "mnt")
	removeTestWrite(t, keep, "mounted data, not owned")
	removeTestMkdir(t, mnt)
	if out, err := exec.Command("mount", "--bind", src, mnt).CombinedOutput(); err != nil {
		t.Skipf("cannot bind mount %s on %s (needs Linux and CAP_SYS_ADMIN): %v %s", src, mnt, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", "-l", mnt).Run() })
	if err := o.RemoveOwned(context.Background(), d); err == nil || !removeTestExists(t, keep) {
		t.Errorf("RemoveOwned over a bind mount = %v, %s kept %v; want a refusal that keeps it", err, keep, removeTestExists(t, keep))
	}
}
