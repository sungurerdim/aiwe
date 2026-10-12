//go:build unix && !linux

package core

import (
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// mountPointAt reports whether name under the open directory dir sits on another device than dir,
// read without following name: the mark of a mount point (#136). A mount of the same device, such
// as a nullfs mount, is not seen.
func mountPointAt(dir *os.File, name string) (bool, error) {
	fd := int(dir.Fd())
	var parent, st unix.Stat_t
	if err := unix.Fstat(fd, &parent); err != nil {
		return false, &fs.PathError{Op: "fstat", Path: dir.Name(), Err: err}
	}
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, &fs.PathError{Op: "fstatat", Path: filepath.Join(dir.Name(), name), Err: err}
	}
	return st.Dev != parent.Dev, nil
}
