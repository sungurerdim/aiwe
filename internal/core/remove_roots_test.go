package core

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"testing"
)

// removeTestOpenRoot makes p, a temp or cache root that exists before OpenOwner runs, writable by
// other users: on Unix by mode, on Windows by an Authenticated Users modify grant. It fails the test
// when the result is not open, so a refusal is never measured on an owner-only root.
func removeTestOpenRoot(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		ownerKeyTestGrant(t, p, "(OI)(CI)(M)")
		_, sddl := ownerKeyTestSDDL(t, p)
		if allowed, _, _ := ownerKeyTestAllowed(t, p, sddl); !slices.Contains(allowed, "AU") &&
			!slices.Contains(allowed, "S-1-5-11") {
			t.Fatalf("%s does not allow Authenticated Users (SDDL %s); the case proves nothing", p, sddl)
		}
		return
	}
	// Chmod, not MkdirAll's mode: the umask would clear the group and other bits.
	if err := os.Chmod(p, mode); err != nil {
		t.Fatalf("chmod %s %#o: %v", p, mode, err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatalf("lstat %s: %v", p, err)
	}
	if fi.Mode().Perm() != mode {
		t.Fatalf("%s has mode %#o after chmod %#o; the case proves nothing", p, fi.Mode().Perm(), mode)
	}
}

// TestOwnedRootOwnerOnly catches a temp or cache root that exists before OpenOwner, is writable by
// group or others, and is still accepted. Without TMPDIR the temp root is the shared /tmp/aiwe, so
// another user who made it 0777 first could swap entries under it (#134, found in review of #109).
// The state root is refused in the same case (TestOwnerKeyMode, TestOwnerKeyACL).
func TestOwnedRootOwnerOnly(t *testing.T) {
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	for _, c := range []struct {
		name, root string
		mode       os.FileMode
	}{
		{"temp root world-writable", "temp", 0o777},
		{"temp root group-writable", "temp", 0o770},
		{"cache root world-writable", "cache", 0o777},
		{"cache root group-writable", "cache", 0o770},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			p := map[string]string{"temp": r.Temp, "cache": r.Cache}[c.root]
			removeTestOpenRoot(t, p, c.mode)
			if o, err := OpenOwner(r, self); !errors.Is(err, ErrRootUnsafe) {
				t.Errorf("OpenOwner with a %s root %s that other users can write: owner %t, error %v; want %v",
					c.root, p, o != nil, err, ErrRootUnsafe)
			}
		})
	}
}
