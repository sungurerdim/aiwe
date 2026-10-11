//go:build unix

package core

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// heldDir is a directory under a root, held open by a handle opened without following a link, so
// its identity outlives a swap of its name.
type heldDir struct {
	parent *os.File // the root directory; name is removed relative to it
	f      *os.File
	name   string
}

// openHeldDir opens name under rt as a directory, without following a link; a name that is not a
// directory fails. The caller checks the identity of the handle.
func openHeldDir(rt *os.Root, name string) (*heldDir, error) {
	parent, err := rt.Open(".")
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.Join(&fs.PathError{Op: "openat", Path: name, Err: err}, parent.Close())
	}
	return &heldDir{parent: parent, f: os.NewFile(uintptr(fd), name), name: name}, nil
}

// rmdir removes name under its root only when name is a directory with no entries (unlinkat with
// AT_REMOVEDIR): a file or a link fails and is never removed.
func (h *heldDir) rmdir() error {
	if err := unix.Unlinkat(int(h.parent.Fd()), h.name, unix.AT_REMOVEDIR); err != nil {
		return &fs.PathError{Op: "rmdir", Path: h.name, Err: err}
	}
	return nil
}

// gone reports whether the held directory has no name left: its link count is 0.
func (h *heldDir) gone() (bool, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(h.f.Fd()), &st); err != nil {
		return false, &fs.PathError{Op: "fstat", Path: h.name, Err: err}
	}
	return st.Nlink == 0, nil
}

// close closes the held handle and the root handle.
func (h *heldDir) close() error {
	return errors.Join(h.f.Close(), h.parent.Close())
}
