package core

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoveOwnedBindMount catches a removal that descends into a bind mount inside an owned dir
// and deletes the data on the mounted file system (#136). The mounted keep.txt must survive, and
// RemoveOwned must refuse or stop at the mount point. It skips without mount privilege.
func TestRemoveOwnedBindMount(t *testing.T) {
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d := removeTestCreate(t, o, TempRoot)
	src := t.TempDir()
	keep := filepath.Join(src, "keep.txt")
	removeTestWrite(t, keep, "mounted data, not owned")
	mnt := filepath.Join(d.Path(), "mnt")
	removeTestMkdir(t, mnt)
	if out, err := exec.CommandContext(t.Context(), "mount", "--bind", src, mnt).CombinedOutput(); err != nil {
		t.Skipf("cannot bind mount %s on %s (needs root or CAP_SYS_ADMIN): %v %s", src, mnt, err, out)
	}
	t.Cleanup(func() {
		if err := exec.CommandContext(context.WithoutCancel(t.Context()), "umount", "-l", mnt).Run(); err != nil {
			t.Logf("umount %s: %v", mnt, err)
		}
	})

	err := o.RemoveOwned(context.Background(), d)
	if !removeTestExists(t, keep) {
		t.Errorf("RemoveOwned over a bind mount deleted the mounted %s (err %v); want it kept", keep, err)
	}
	if err == nil {
		t.Errorf("RemoveOwned over a bind mount at %s = nil; want a refusal that names the mount point", mnt)
	}
}

// TestRemoveOwnedPartialThenMount catches a retry that says "nothing removed" after an earlier
// attempt removed entries (#136). The first attempt removes a/; then z/ appears, bind mounted,
// before the last rmdir, so that attempt fails and the next one refuses the mount point. The
// refusal must name a partial removal and the residue, and keep the mounted data.
func TestRemoveOwnedPartialThenMount(t *testing.T) {
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d := removeTestCreate(t, o, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "a", "data.txt"), "owned data\n")
	src := t.TempDir()
	keep := filepath.Join(src, "keep.txt")
	removeTestWrite(t, keep, "mounted data, not owned")
	probe := filepath.Join(t.TempDir(), "probe")
	removeTestMkdir(t, probe)
	if out, err := exec.Command("mount", "--bind", src, probe).CombinedOutput(); err != nil {
		t.Skipf("cannot bind mount %s on %s (needs root or CAP_SYS_ADMIN): %v %s", src, probe, err, out)
	}
	if err := exec.Command("umount", probe).Run(); err != nil {
		t.Fatalf("umount %s: %v", probe, err)
	}
	z := filepath.Join(d.Path(), "z")
	mounted := false
	o.hooks.lastChecked = func(string) {
		if mounted {
			return
		}
		mounted = true
		removeTestMkdir(t, z)
		if out, err := exec.Command("mount", "--bind", src, z).CombinedOutput(); err != nil {
			t.Fatalf("bind mount %s on %s: %v %s", src, z, err, out)
		}
		t.Cleanup(func() {
			if err := exec.Command("umount", "-l", z).Run(); err != nil {
				t.Logf("umount %s: %v", z, err)
			}
		})
	}

	err := o.RemoveOwned(context.Background(), d)
	if !mounted {
		t.Fatalf("hooks.lastChecked never ran for %s (err %v)", d.Path(), err)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("RemoveOwned deleted the mounted %s (err %v); want it kept", keep, err)
	}
	if err == nil {
		t.Fatalf("RemoveOwned after a partial attempt = nil; want not_owned naming a partial removal and the residue %s", d.Path())
	}
	if !errors.Is(err, ErrNotOwned) || strings.Contains(err.Error(), "nothing removed") ||
		!strings.Contains(err.Error(), "partial removal") || !strings.Contains(err.Error(), d.Path()) {
		t.Errorf("RemoveOwned after a partial attempt = %v; want not_owned naming a partial removal and the residue %s",
			err, d.Path())
	}
}
