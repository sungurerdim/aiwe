//go:build windows

package core

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() { JunctionForTest = junctionForTest }

// junctionForTest makes link a directory junction to the absolute dir target through
// FSCTL_SET_REPARSE_POINT, so no path goes through cmd (#154).
func junctionForTest(t *testing.T, target, link string) {
	t.Helper()
	if !filepath.IsAbs(target) {
		t.Fatalf("junction target %s is not absolute", target)
	}
	sub, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		t.Fatalf("junction target %s: %v", target, err)
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		t.Fatalf("junction target %s: %v", target, err)
	}
	// REPARSE_DATA_BUFFER for a mount point: tag, data length, reserved, then the offset and length
	// in bytes of the substitute name and of the print name, then both names, each ending in NUL.
	words := []uint16{0, uint16(2 * (len(sub) - 1)), uint16(2 * len(sub)), uint16(2 * (len(printName) - 1))}
	words = append(append(words, sub...), printName...)
	buf := make([]byte, 8+2*len(words))
	*(*uint32)(unsafe.Pointer(&buf[0])) = windows.IO_REPARSE_TAG_MOUNT_POINT
	*(*uint16)(unsafe.Pointer(&buf[4])) = uint16(len(buf) - 8)
	for i, v := range words {
		*(*uint16)(unsafe.Pointer(&buf[8+2*i])) = v
	}
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("make the junction dir %s: %v", link, err)
	}
	p, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatalf("junction %s: %v", link, err)
	}
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		t.Fatalf("open the junction dir %s: %v", link, err)
	}
	defer windows.CloseHandle(h)
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &n, nil); err != nil {
		t.Fatalf("junction %s -> %s: %v", link, target, err)
	}
}

// TestJunctionMetacharName catches a junction fixture that passes the test's temp path through
// cmd (#154): cmd splits an unquoted argument at a comma, so a subtest name with a comma and an
// apostrophe made the fixture fail for a fixture reason, which can fake a red proof.
func TestJunctionMetacharName(t *testing.T) {
	t.Run("a comma, and an apostrophe's name", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "target")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatalf("make the target dir %s: %v", target, err)
		}
		removeTestWrite(t, filepath.Join(target, "keep.txt"), "behind the junction")
		link := filepath.Join(base, "link")
		JunctionForTest(t, target, link)

		p, err := windows.UTF16PtrFromString(link)
		if err != nil {
			t.Fatalf("junction %s: %v", link, err)
		}
		var fd windows.Win32finddata
		h, err := windows.FindFirstFile(p, &fd)
		if err != nil {
			t.Fatalf("find %s: %v", link, err)
		}
		windows.FindClose(h)
		if fd.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || fd.Reserved0 != windows.IO_REPARSE_TAG_MOUNT_POINT {
			t.Errorf("%s: attributes %#x, reparse tag %#x; want a junction (tag %#x)", link, fd.FileAttributes, fd.Reserved0, uint32(windows.IO_REPARSE_TAG_MOUNT_POINT))
		}
		if b, err := os.ReadFile(filepath.Join(link, "keep.txt")); err != nil || string(b) != "behind the junction" {
			t.Errorf("file through the junction %s: content %q, error %v; want the target's file", link, b, err)
		}
	})
}
