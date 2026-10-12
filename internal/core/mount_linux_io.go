//go:build linux

package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// mountPointAt reports whether name under the open directory dir is a mount point, a bind mount on
// the same file system included: statx marks it as a mount root, or its mount differs from the one
// of dir. name is never followed. A kernel that does not report the mount-root attribute (before
// Linux 5.8) cannot rule a mount point out, so it fails with ErrNotOwned (#136).
func mountPointAt(dir *os.File, name string) (bool, error) {
	fd := int(dir.Fd())
	flags := unix.AT_SYMLINK_NOFOLLOW | unix.AT_NO_AUTOMOUNT
	var parent, st unix.Statx_t
	if err := unix.Statx(fd, "", flags|unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &parent); err != nil {
		return false, &fs.PathError{Op: "statx", Path: dir.Name(), Err: err}
	}
	p := filepath.Join(dir.Name(), name)
	if err := unix.Statx(fd, name, flags, unix.STATX_MNT_ID, &st); err != nil {
		return false, &fs.PathError{Op: "statx", Path: p, Err: err}
	}
	if st.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 {
		return false, fmt.Errorf("%w: %q: the system does not report a mount root, so a mount point is unknown; nothing removed",
			ErrNotOwned, p)
	}
	if st.Attributes&unix.STATX_ATTR_MOUNT_ROOT != 0 {
		return true, nil
	}
	return st.Mask&parent.Mask&unix.STATX_MNT_ID != 0 && st.Mnt_id != parent.Mnt_id, nil
}
