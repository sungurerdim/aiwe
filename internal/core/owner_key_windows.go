package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileDeleteChild is the right to delete an entry of a directory; x/sys/windows has no name for it.
const fileDeleteChild = 0x40

// ownerOnlyMask is every right that reads or replaces the owner key, or the entries of the state
// root: read, write, append, delete, delete an entry, change the ACL or the owner, and their
// generic forms.
const ownerOnlyMask = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
	fileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
	windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_ALL

// checkOwnerOnly refuses p, the temp or cache root, when a principal other
// than the current user, SYSTEM or Administrators owns it or can read or write it through its
// DACL; the 0o600/0o700 modes do nothing on Windows. CREATOR OWNER counts as the user: it becomes
// the creator of each entry made below p. A missing DACL grants everyone every right, and an ACE
// type other than allow or deny cannot be proven safe, so both are refused (fail-closed). A root
// that is a junction or link never reaches it: mkdirOwnerOnly refuses it first. The state root and
// the owner key are checked on their open handles (checkOwnerOnlyFile).
func checkOwnerOnly(p string) error {
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, ownerOnlyInfo)
	return checkOwnerOnlySD(p, true, sd, err)
}

// ownerKeyOpenFlag opens the owner key; an open on Windows does not wait for a writer.
const ownerKeyOpenFlag = os.O_RDONLY

// checkOwnerOnlyFile is checkOwnerOnly on the open file f, named p: the type and the security
// descriptor are read from the handle, so they belong to the file f reads, whatever p names later.
// Only a plain file or a directory passes.
func checkOwnerOnlyFile(f *os.File, p string) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, p, err)
	}
	if t := fi.Mode().Type(); t&^fs.ModeDir != 0 {
		return fmt.Errorf("%w: owner-only check of %q: a %v is not a plain file or directory", ErrRootUnsafe, p, t)
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, ownerOnlyInfo)
	return checkOwnerOnlySD(p, fi.IsDir(), sd, err)
}

// ownerOnlyInfo is the part of a security descriptor that the owner-only check reads.
const ownerOnlyInfo = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

// checkOwnerOnlySD checks sd, the security descriptor of p read with error err, for checkOwnerOnly;
// dir tells whether p is a directory, which a refusal never names for delete (ownerOnlyNext).
func checkOwnerOnlySD(p string, dir bool, sd *windows.SECURITY_DESCRIPTOR, err error) error {
	if err != nil {
		return fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, p, err)
	}
	trusted, err := ownerOnlySIDs()
	if err != nil {
		return fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, p, err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !sidIn(owner, trusted) {
		return fmt.Errorf("%w: %q is not owned by the current user, SYSTEM or Administrators%s", ErrRootUnsafe, p,
			ownerOnlyNext(p, dir, true))
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%w: %q has no DACL, so everyone can read and write it%s", ErrRootUnsafe, p, ownerOnlyNext(p, dir, false))
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%w: owner-only check of %q: ACE %d: %v", ErrRootUnsafe, p, i, err)
		}
		if err := checkOwnerOnlyACE(p, dir, ace, trusted); err != nil {
			return err
		}
	}
	return nil
}

// checkOwnerOnlyACE refuses ace of p when it is neither an allow nor a deny ACE, or when it allows
// a principal outside trusted a right of ownerOnlyMask.
func checkOwnerOnlyACE(p string, dir bool, ace *windows.ACCESS_ALLOWED_ACE, trusted []*windows.SID) error {
	switch ace.Header.AceType {
	case windows.ACCESS_DENIED_ACE_TYPE:
		return nil
	case windows.ACCESS_ALLOWED_ACE_TYPE:
	default:
		return fmt.Errorf("%w: %q has an ACE of type %d that cannot be checked", ErrRootUnsafe, p, ace.Header.AceType)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Mask&ownerOnlyMask == 0 || sidIn(sid, trusted) {
		return nil
	}
	return fmt.Errorf("%w: %q grants %s access mask %#x; only the current user, SYSTEM and "+
		"Administrators may read or write it%s", ErrRootUnsafe, p, sid, uint32(ace.Mask), ownerOnlyNext(p, dir, false))
}

// mkdirOwnerOnly makes the state, temp or cache root p, and any missing parent, when p is missing
// (createOwnerOnlyDir). A p that exists is left as it is, so CreateDirectory never runs on a
// file-system root: checkOwnerOnly refuses p when it is open. A p that is not a plain directory,
// such as a junction or link, fails with ErrRootUnsafe before anything follows it.
func mkdirOwnerOnly(p string) error {
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		if err := createOwnerOnlyDir(p); err != nil {
			return err
		}
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%w: %q is not a plain directory (a junction or link is never followed)", ErrRootUnsafe, p)
	}
	return nil
}

// createOwnerOnlyDir makes p, and any missing parent. p itself is made in one step with a
// protected DACL that allows only the current user, SYSTEM and Administrators, and that each entry
// below it inherits, such as the owner key; no ACE of an open parent reaches it. A p made by
// another process in the meantime is left as it is.
func createOwnerOnlyDir(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + u.User.Sid.String() +
		")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	if err := windows.CreateDirectory(name, &sa); err != nil && !errors.Is(err, fs.ErrExist) {
		return &fs.PathError{Op: "mkdir", Path: p, Err: err}
	}
	return nil
}

// ownerOnlySIDs returns the principals that may read and write the owner key: the user of the
// current process, CREATOR OWNER, SYSTEM and Administrators.
func ownerOnlySIDs() ([]*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	out := []*windows.SID{u.User.Sid}
	for _, t := range []windows.WELL_KNOWN_SID_TYPE{windows.WinCreatorOwnerSid, windows.WinLocalSystemSid,
		windows.WinBuiltinAdministratorsSid} {
		s, err := windows.CreateWellKnownSid(t)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// sidIn reports whether s is one of set.
func sidIn(s *windows.SID, set []*windows.SID) bool {
	for _, t := range set {
		if s.Equals(t) {
			return true
		}
	}
	return false
}
