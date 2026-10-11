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
