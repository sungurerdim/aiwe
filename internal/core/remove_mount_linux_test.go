package core

import (
	"context"
	"os/exec"
	"path/filepath"
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
	if out, err := exec.Command("mount", "--bind", src, mnt).CombinedOutput(); err != nil {
		t.Skipf("cannot bind mount %s on %s (needs root or CAP_SYS_ADMIN): %v %s", src, mnt, err, out)
	}
	t.Cleanup(func() {
		if err := exec.Command("umount", "-l", mnt).Run(); err != nil {
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
