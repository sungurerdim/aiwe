//go:build !windows

package core

import (
	"fmt"
	"os"
	"syscall"
)

// checkOwnerOnly refuses p, the state root or the owner key, when its mode gives group or others any
// access, or when the effective user does not own it. p is not followed, so a link, whose mode
// is 0777, is refused too. aiwe makes the state root 0o700 and the key 0o600.
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

// mkdirOwnerOnly makes the state root p, and any missing parent, with mode 0o700.
func mkdirOwnerOnly(p string) error { return os.MkdirAll(p, 0o700) }
