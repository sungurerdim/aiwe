package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// removeTestRootLink makes link point at the directory target: a symlink on Unix, a directory
// junction on Windows, which needs no privilege and is what a user makes with mklink /J.
func removeTestRootLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink %s -> %s: %v", link, target, err)
		}
		return
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("junction %s -> %s: %v: %s", link, target, err, out)
	}
}

// removeTestNames returns the sorted entry names of dir.
func removeTestNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(es))
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// removeTestLinkForms returns the spellings of an absolute link path that must all be refused as
// the link itself. A trailing separator or a "." element makes Lstat follow the last link, so a
// check that only Lstats the given path accepts the link and writes into its target (#134).
func removeTestLinkForms() []struct{ name, prefix, suffix string } {
	if runtime.GOOS == "windows" {
		return []struct{ name, prefix, suffix string }{
			{"as named", "", ""},
			{"trailing backslash", "", `\`},
			{"trailing slash", "", "/"},
			{"trailing dot element", "", `\.\`},
			{"extended-length with trailing backslash", `\\?\`, `\`},
		}
	}
	return []struct{ name, prefix, suffix string }{
		{"as named", "", ""},
		{"trailing slash", "", "/"},
		{"trailing dot", "", "/."},
		{"trailing double slash", "", "//"},
		{"trailing dot element", "", "/./"},
	}
}

// TestOwnedRootLink catches a temp, cache or state root that is a link (Unix symlink, Windows
// junction) and is followed to its target, in any spelling of the link path. The target may be
// owner-only and still not the engine's: a link to the user's private dir would make the engine
// create and remove run dirs, or write the owner key, inside it. Every root is refused when it is a
// link; the user names the real path through AIWE_TEMP, AIWE_CACHE or the state root instead
// (#134, owner decision).
func TestOwnedRootLink(t *testing.T) {
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	for _, root := range []string{"temp", "cache", "state"} {
		for _, victim := range []bool{false, true} {
			for _, f := range removeTestLinkForms() {
				dest := "an owner-only dir"
				if victim {
					dest = "the user's private dir"
				}
				t.Run(root+" root links to "+dest+", "+f.name, func(t *testing.T) {
					base := t.TempDir()
					r := removeTestRoots(t, base)
					target := filepath.Join(base, "real-"+root)
					if victim {
						target = filepath.Join(r.Home, "private")
					}
					removeTestRootDir(t, target)
					if runtime.GOOS != "windows" {
						if err := os.Chmod(target, 0o700); err != nil {
							t.Fatalf("chmod %s 0700: %v", target, err)
						}
					}
					removeTestWrite(t, filepath.Join(target, "notes.txt"), "Kişisel notlar: şifre değil.\n")
					before := removeTestNames(t, target)
					link := filepath.Join(base, root+"-link")
					removeTestRootLink(t, target, link)
					given := f.prefix + link + f.suffix
					switch root {
					case "temp":
						r.Temp = given
					case "cache":
						r.Cache = given
					default:
						r.State = given
					}

					o, err := OpenOwner(r, self)
					if !errors.Is(err, ErrRootUnsafe) {
						t.Errorf("OpenOwner with a %s root %s that links to %s: owner %t, error %v; want %v",
							root, given, target, o != nil, err, ErrRootUnsafe)
					}
					if o != nil && root != "state" {
						// An accepted owner shows what a followed link costs: its run dir lands in the target.
						owned := map[string]OwnedRoot{"temp": TempRoot, "cache": CacheRoot}[root]
						if _, err := o.CreateOwned(context.Background(), owned, "run-20261011-0001"); err != nil {
							t.Logf("CreateOwned under the linked %s root: %v", root, err)
						}
					}
					if after := removeTestNames(t, target); !slices.Equal(after, before) {
						t.Errorf("link target %s has entries %q after OpenOwner, want %q unchanged", target, after, before)
					}
				})
			}
		}
	}
}
