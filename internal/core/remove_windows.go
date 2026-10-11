package core

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// heldDir is a directory under a root, held open by a handle opened without following a link and
// without delete sharing: while it is held, no one can rename or delete the directory, so the
// handle and the directory the caller checks stay one. The removal goes through that handle.
type heldDir struct {
	f       *os.File
	deleted bool // the delete disposition was set through f
}

// openHeldDir opens name relative to the handle of rt as a directory, without following a link,
// with delete access and without delete sharing; a name that is not a directory fails. An open
// handle with delete access, held by anyone, makes it fail. The caller checks the identity. A failed
// close of the parent handle is returned (closeChecked) and the held directory is closed again.
func openHeldDir(rt *os.Root, name string) (held *heldDir, err error) {
	parent, err := rt.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := closeChecked("held parent", parent.Close); cerr != nil {
			if held != nil {
				cerr = errors.Join(cerr, held.f.Close())
			}
			held, err = nil, errors.Join(err, cerr)
		}
	}()
	objName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	oa := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent.Fd()),
		ObjectName:    objName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	err = windows.NtCreateFile(&h, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &oa,
		&windows.IO_STATUS_BLOCK{}, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|
			windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	path := filepath.Join(rt.Name(), name)
	var st windows.NTStatus
	if errors.As(err, &st) {
		return nil, &fs.PathError{Op: "open", Path: path, Err: st.Errno()}
	}
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return &heldDir{f: os.NewFile(uintptr(h), path)}, nil
}

// rmdir deletes the held directory through its own handle: it sets the delete disposition, with
// POSIX semantics where the system and the file system support them, else the classic one. A
// directory with an entry fails and stays. The name is never used, so a swapped name is never hit.
func (h *heldDir) rmdir() error {
	fd := windows.Handle(h.f.Fd())
	ex := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	err := windows.SetFileInformationByHandle(fd, windows.FileDispositionInfoEx, (*byte)(unsafe.Pointer(&ex)),
		uint32(unsafe.Sizeof(ex)))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		del := true // FILE_DISPOSITION_INFO: one BOOLEAN
		err = windows.SetFileInformationByHandle(fd, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&del)),
			uint32(unsafe.Sizeof(del)))
	}
	if err != nil {
		return &fs.PathError{Op: "rmdir", Path: h.f.Name(), Err: err}
	}
	h.deleted = true
	return nil
}

// gone reports whether rmdir set the delete disposition through the held handle. No other handle
// can hold delete access while this one is open, so no one can clear it; a delete-pending mark
// read from the directory is never taken as proof.
func (h *heldDir) gone() (bool, error) {
	return h.deleted, nil
}

// close closes the held handle; a directory marked by rmdir goes at this point.
func (h *heldDir) close() error {
	return h.f.Close()
}
