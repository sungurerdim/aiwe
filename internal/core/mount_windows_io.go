//go:build windows

package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// volumeTarget starts the target of a volume mount point; a directory junction carries the same
// reparse tag with a path target instead.
const volumeTarget = `\??\Volume{`

// mountPointAt reports whether name under the open directory dir is a volume mount point: a
// mount-point reparse tag whose target starts with volumeTarget (#136). A directory junction is
// not one; checkLinks keeps its target inside the root. name is opened relative to the handle of
// dir without following it, and the tag and the target are read from that handle.
func mountPointAt(dir *os.File, name string) (mount bool, err error) {
	p := filepath.Join(dir.Name(), name)
	h, err := openEntryAt(dir, name)
	if err != nil {
		return false, &fs.PathError{Op: "open", Path: p, Err: err}
	}
	defer func() {
		if cerr := windows.CloseHandle(h); cerr != nil {
			mount, err = false, errors.Join(err, &fs.PathError{Op: "close", Path: p, Err: cerr})
		}
	}()
	var info struct{ attrs, tag uint32 } // FILE_ATTRIBUTE_TAG_INFO
	err = windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)))
	if err != nil {
		return false, &fs.PathError{Op: "read reparse tag", Path: p, Err: err}
	}
	if info.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || info.tag != windows.IO_REPARSE_TAG_MOUNT_POINT {
		return false, nil
	}
	const size = windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE
	buf := make([]byte, size)
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &buf[0], size, &n, nil); err != nil {
		return false, &fs.PathError{Op: "read reparse point", Path: p, Err: err}
	}
	target, err := mountPointTarget(buf[:n])
	if err != nil {
		return false, &fs.PathError{Op: "read reparse point", Path: p, Err: err}
	}
	return len(target) >= len(volumeTarget) && strings.EqualFold(target[:len(volumeTarget)], volumeTarget), nil
}

// openEntryAt opens name relative to the handle of dir, without following it, for reading its
// attributes; any sharing is allowed, so an open file of another process does not block it.
func openEntryAt(dir *os.File, name string) (windows.Handle, error) {
	objName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(dir.Fd()),
		ObjectName:    objName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	err = windows.NtCreateFile(&h, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &oa, &windows.IO_STATUS_BLOCK{},
		nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	var st windows.NTStatus
	if errors.As(err, &st) {
		return 0, st.Errno()
	}
	return h, err
}

// mountPointTarget returns the substitute name of a mount-point REPARSE_DATA_BUFFER: an 8-byte
// header, then the offset and length in bytes of the name, both relative to the path buffer at 16.
// A buffer too short for the name it declares fails.
func mountPointTarget(b []byte) (string, error) {
	const pathBuffer = 16
	if len(b) < pathBuffer {
		return "", fmt.Errorf("reparse data of %d bytes is shorter than its %d-byte header", len(b), pathBuffer)
	}
	off := pathBuffer + int(binary.LittleEndian.Uint16(b[8:]))
	end := off + int(binary.LittleEndian.Uint16(b[10:]))
	if end > len(b) || (end-off)%2 != 0 {
		return "", fmt.Errorf("reparse data of %d bytes cannot hold a target at %d..%d", len(b), off, end)
	}
	u := make([]uint16, 0, (end-off)/2)
	for i := off; i < end; i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	return string(utf16.Decode(u)), nil
}
