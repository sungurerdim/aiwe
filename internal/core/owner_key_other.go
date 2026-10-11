//go:build !windows

package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ownerKeyOpenFlag opens the owner key without blocking, so a FIFO under its name cannot hang the
// open; checkOwnerOnlyFile then refuses it on the handle, as it is not a plain file.
const ownerKeyOpenFlag = os.O_RDONLY | syscall.O_NONBLOCK

// checkOwnerOnly refuses p, the temp or cache root, when it is neither a plain file nor a
// directory, when its mode gives group or others any access, or when the effective user does not
// own it. p is not followed, so a link is refused. aiwe makes each root 0o700 and the key 0o600;
// the state root and the key are checked on their open handles (checkOwnerOnlyFile).
func checkOwnerOnly(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, p, err)
	}
	return checkOwnerOnlyInfo(p, fi)
}

// checkOwnerOnlyFile is checkOwnerOnly on the open file f, named p: the mode and owner are read
// from the handle (fstat), so they belong to the file f reads, whatever p names later.
func checkOwnerOnlyFile(f *os.File, p string) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, p, err)
	}
	return checkOwnerOnlyInfo(p, fi)
}

// checkOwnerOnlyInfo checks fi, the file info of p; only a plain file or directory passes. A FIFO,
// device or socket is refused. A link is refused from Lstat (checkOwnerOnly); from a handle
// (checkOwnerOnlyFile) the open already followed the link, so its target is checked.
func checkOwnerOnlyInfo(p string, fi fs.FileInfo) error {
	if t := fi.Mode().Type(); t != 0 && t != fs.ModeDir {
		return fmt.Errorf("%w: %q is a %v, not a plain file or directory", ErrRootUnsafe, p, t)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %q has mode %v; want no access for group or others"+openRootFix, ErrRootUnsafe, p, fi.Mode(),
			p, Product)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%w: %q is not owned by the effective user %d"+foreignRootFix, ErrRootUnsafe, p, os.Geteuid(),
			p, Product)
	}
	return nil
}

// mkdirOwnerOnly makes the state, temp or cache root p, and any missing parent, with mode 0o700;
// a p that exists keeps its mode, and checkOwnerOnly refuses it when it is open. A p that is a link
// fails with ErrRootUnsafe, the same as on Windows; nothing is written at its target.
func mkdirOwnerOnly(p string) error {
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return err
		}
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
