package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// heldDir is an entry under a root, held open by a handle opened without following a link, so its
// identity outlives a swap of its name.
type heldDir struct {
	f    *os.File
	path string
}

// fileStandardInfo is FILE_STANDARD_INFO; x/sys/windows has no type for it.
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  bool
	Directory      bool
}

// openHeldDir opens name under rt without following a link. The handle shares delete access, so it
// never blocks the removal. It may name a file; the caller checks the type and the identity.
func openHeldDir(rt *os.Root, name string) (*heldDir, error) {
	path := filepath.Join(rt.Name(), name)
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return &heldDir{f: os.NewFile(uintptr(h), path), path: path}, nil
}

// rmdir removes the entry by its path only when it is a directory with no entries
// (RemoveDirectory): a file fails and is never removed.
func (h *heldDir) rmdir() error {
	p, err := windows.UTF16PtrFromString(h.path)
	if err != nil {
		return err
	}
	if err := windows.RemoveDirectory(p); err != nil {
		return &fs.PathError{Op: "rmdir", Path: h.path, Err: err}
	}
	return nil
}

// gone reports whether the held entry is deleted or waits for deletion.
func (h *heldDir) gone() (bool, error) {
	var info fileStandardInfo
	err := windows.GetFileInformationByHandleEx(windows.Handle(h.f.Fd()), windows.FileStandardInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		return false, &fs.PathError{Op: "query", Path: h.path, Err: err}
	}
	return info.DeletePending || info.NumberOfLinks == 0, nil
}

// close closes the held handle.
func (h *heldDir) close() error {
	return h.f.Close()
}
