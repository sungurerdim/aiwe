//go:build windows

package core

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

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
		removeTestLink(t, target, link)

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
