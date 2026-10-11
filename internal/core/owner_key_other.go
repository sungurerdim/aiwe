//go:build !windows

package core

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkOwnerOnly refuses p, the state, temp or cache root or the owner key, when its mode gives
// group or others any access, or when the effective user does not own it. p is not followed, so a
// link, whose mode is 0777, is refused too. OpenOwner passes the real path of a temp or cache
// root, so a root that is a link is checked at its target. aiwe makes each root 0o700 and the
// key 0o600.
func checkOwnerOnly(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, p, err)
	}
	if fi.Mode().Perm()&0o077 != 0 || fi.Mode().Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q has mode %v; want no access for group or others", ErrRootUnsafe, p, fi.Mode())
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%w: %q is not owned by the effective user %d", ErrRootUnsafe, p, os.Geteuid())
	}
	return nil
}

// mkdirOwnerOnly makes the root p, and any missing parent, with mode 0o700. OpenOwner passes it
// the state root always and a temp or cache root only when missing. A p that is a link fails with
// ErrRootUnsafe, the same as on Windows; nothing is written at its target. So a state root that
// is a link is refused, and a temp or cache root that is a link is checked at its target.
func mkdirOwnerOnly(p string) error {
	if err := os.MkdirAll(p, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%w: %q is not a plain directory (a link is never followed)", ErrRootUnsafe, p)
	}
	return nil
}
