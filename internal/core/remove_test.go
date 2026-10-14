package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// removeTestRoots builds a safe set of roots under one t.TempDir: temp, cache, a home and a
// repository, none inside another. The state root stays missing, for OpenOwner to make owner-only.
func removeTestRoots(t *testing.T, base string) OwnedRoots {
	t.Helper()
	r := OwnedRoots{
		State:     filepath.Join(base, "state"),
		Temp:      filepath.Join(base, "temp"),
		Cache:     filepath.Join(base, "cache"),
		Home:      filepath.Join(base, "home"),
		Canonical: []string{filepath.Join(base, "repo")},
	}
	for _, d := range []string{r.Temp, r.Cache, r.Home, r.Canonical[0]} {
		removeTestRootDir(t, d)
	}
	return r
}

// removeTestRootDir makes the root dir owner-only: MkdirAll alone would inherit the parent's ACL.
func removeTestRootDir(t *testing.T, dir string) {
	t.Helper()
	mkdir := func(p string) error { return os.MkdirAll(p, 0o700) }
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		mkdir = mkdirOwnerOnly
	}
	removeTestErr(t, mkdir(dir), "mkdir %s", dir)
}

// removeTestMkdir makes dir with MkdirAll; on Windows it inherits the parent's ACL.
func removeTestMkdir(t *testing.T, dir string) {
	t.Helper()
	removeTestErr(t, os.MkdirAll(dir, 0o755), "mkdir %s", dir)
}

func removeTestWrite(t *testing.T, path, content string) {
	t.Helper()
	removeTestMkdir(t, filepath.Dir(path))
	removeTestErr(t, os.WriteFile(path, []byte(content), 0o644), "write %s", path)
}

func removeTestExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatalf("lstat %s: %v", path, err)
	return false
}

// removeTestErr fails the test on err, so the plain error check stays one line.
func removeTestErr(t *testing.T, err error, format string, a ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf(format+": %v", append(a, err)...)
	}
}

func removeTestOpen(t *testing.T, r OwnedRoots) *Owner {
	t.Helper()
	self, err := CurrentProcess()
	removeTestErr(t, err, "CurrentProcess")
	o, err := OpenOwner(r, self)
	removeTestErr(t, err, "OpenOwner(%+v)", r)
	if o == nil {
		t.Fatalf("OpenOwner(%+v) = nil owner and no error", r)
	}
	return o
}

func removeTestCreate(t *testing.T, o *Owner, root OwnedRoot) OwnedDir {
	t.Helper()
	d, err := o.CreateOwned(t.Context(), root, "run-20261010-0001")
	removeTestErr(t, err, "CreateOwned")
	return d
}

// removeTestLink makes link point at target: a symlink where the OS allows it, else a junction.
func removeTestLink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
	JunctionForTest(t, target, link)
}

// removeTestUnderRoot fails the test unless dir is a real directory under the real root.
func removeTestUnderRoot(t *testing.T, dir, root string) {
	t.Helper()
	realRoot, err := filepath.EvalSymlinks(root)
	removeTestErr(t, err, "resolve root %q", root)
	realDir, err := filepath.EvalSymlinks(dir)
	removeTestErr(t, err, "created dir %q does not exist", dir)
	if !strings.HasPrefix(realDir, realRoot+string(filepath.Separator)) {
		t.Fatalf("created dir %q is not under the root %q", realDir, realRoot)
	}
}

// TestOwnedRemove catches recursive removal of a dir the engine did not create and mark as its own (DR-11).
func TestOwnedRemove(t *testing.T) {
	t.Run("created temp dir is removed with its contents", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "çalışma-ağacı")
		r := removeTestRoots(t, base)
		o := removeTestOpen(t, r)
		d := removeTestCreate(t, o, TempRoot)
		removeTestUnderRoot(t, d.Path(), r.Temp)
		if !removeTestExists(t, filepath.Join(d.Path(), ".owner")) {
			t.Fatalf("created dir %q has no .owner marker", d.Path())
		}
		removeTestWrite(t, filepath.Join(d.Path(), "src", "main.go"), "package main\n")
		removeTestErr(t, o.RemoveOwned(t.Context(), d), "RemoveOwned of an owned dir")
		if removeTestExists(t, d.Path()) {
			t.Fatalf("owned dir %q is still there after RemoveOwned", d.Path())
		}
	})

	t.Run("created cache dir is under the cache root and removable", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		o := removeTestOpen(t, r)
		d := removeTestCreate(t, o, CacheRoot)
		removeTestUnderRoot(t, d.Path(), r.Cache)
		removeTestErr(t, o.RemoveOwned(t.Context(), d), "RemoveOwned of an owned cache dir")
		if removeTestExists(t, d.Path()) {
			t.Fatalf("owned dir %q is still there after RemoveOwned", d.Path())
		}
	})

	t.Run("a link that stays inside the dir does not block removal", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		o := removeTestOpen(t, r)
		d := removeTestCreate(t, o, TempRoot)
		removeTestWrite(t, filepath.Join(d.Path(), "sub", "a.txt"), "a\n")
		removeTestLink(t, filepath.Join(d.Path(), "sub"), filepath.Join(d.Path(), "inner"))
		removeTestErr(t, o.RemoveOwned(t.Context(), d), "RemoveOwned with an inner link")
		if removeTestExists(t, d.Path()) {
			t.Fatalf("owned dir %q is still there after RemoveOwned", d.Path())
		}
	})

	// Each refusal case leaves a user file that must survive, and the call must fail with ErrNotOwned.
	refusals := []struct {
		name string
		// setup returns the forged handle to remove and the paths that must still exist.
		setup func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string)
	}{
		{"user dir outside every root", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			user := filepath.Join(base, "projects", "thesis")
			removeTestWrite(t, filepath.Join(user, "chapter1.md"), "# Chapter 1\n")
			return OwnedDir{path: user}, []string{filepath.Join(user, "chapter1.md")}
		}},
		{"the user's home", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			keep := filepath.Join(r.Home, "Documents", "taxes-2025.pdf")
			removeTestWrite(t, keep, "%PDF-1.7\n")
			return OwnedDir{path: r.Home}, []string{keep}
		}},
		{"the state root", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			return OwnedDir{path: r.State}, []string{filepath.Join(r.State, "owner.key")}
		}},
		{"the temp root itself", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			return OwnedDir{path: r.Temp}, []string{r.Temp, d.Path()}
		}},
		{"the canonical repository", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			keep := filepath.Join(r.Canonical[0], "go.mod")
			removeTestWrite(t, keep, "module example.com/app\n")
			return OwnedDir{path: r.Canonical[0]}, []string{keep}
		}},
		{"a path that climbs out of the temp root", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			keep := filepath.Join(base, "projects", "notes.txt")
			removeTestWrite(t, keep, "keep\n")
			return OwnedDir{path: filepath.Join(r.Temp, "..", "projects")}, []string{keep}
		}},
		{"unregistered dir under the temp root with a copied marker", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			marker, err := os.ReadFile(filepath.Join(d.Path(), ".owner"))
			removeTestErr(t, err, "read marker")
			x := filepath.Join(filepath.Dir(d.Path()), "unregistered")
			removeTestWrite(t, filepath.Join(x, ".owner"), string(marker))
			removeTestWrite(t, filepath.Join(x, "data.bin"), "data\n")
			return OwnedDir{path: x}, []string{filepath.Join(x, "data.bin")}
		}},
		{"unregistered dir under the temp root without a marker", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			x := filepath.Join(r.Temp, "someone-else")
			removeTestWrite(t, filepath.Join(x, "data.bin"), "data\n")
			return OwnedDir{path: x}, []string{filepath.Join(x, "data.bin")}
		}},
		{"owned dir whose marker is gone", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			removeTestErr(t, os.Remove(filepath.Join(d.Path(), ".owner")), "remove the marker")
			keep := filepath.Join(d.Path(), "work.txt")
			removeTestWrite(t, keep, "work\n")
			return OwnedDir{path: d.Path()}, []string{keep}
		}},
		{"owned dir whose ledger record is gone", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			removeTestErr(t, os.WriteFile(filepath.Join(r.State, "ledger.jsonl"), nil, 0o644), "empty the ledger")
			keep := filepath.Join(d.Path(), "work.txt")
			removeTestWrite(t, keep, "work\n")
			return OwnedDir{path: d.Path()}, []string{keep}
		}},
		{"owned dir with a link that leaves the root", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			user := filepath.Join(base, "photos")
			keep := filepath.Join(user, "2026-summer.jpg")
			removeTestWrite(t, keep, "jpeg\n")
			removeTestLink(t, user, filepath.Join(d.Path(), "escape"))
			return OwnedDir{path: d.Path()}, []string{keep, filepath.Join(d.Path(), ".owner")}
		}},
		{"owned dir swapped for a link to a user dir", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			moved := filepath.Join(base, "moved-out")
			removeTestErr(t, os.Rename(d.Path(), moved), "rename out of the temp root")
			keep := filepath.Join(moved, "report.docx")
			removeTestWrite(t, keep, "docx\n")
			removeTestLink(t, moved, d.Path())
			return OwnedDir{path: d.Path()}, []string{keep, filepath.Join(moved, ".owner")}
		}},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			base := t.TempDir()
			r := removeTestRoots(t, base)
			o := removeTestOpen(t, r)
			forged, keep := tc.setup(t, base, r, o)
			// A fresh owner reads every record from disk, as after a restart.
			fresh := removeTestOpen(t, r)
			if err := fresh.RemoveOwned(t.Context(), forged); !errors.Is(err, ErrNotOwned) {
				t.Fatalf("RemoveOwned(%q) = %v, want %v", forged.path, err, ErrNotOwned)
			}
			for _, p := range keep {
				if !removeTestExists(t, p) {
					t.Fatalf("refused removal still deleted %q", p)
				}
			}
		})
	}

	t.Run("refuses a dir recorded under another state root", func(t *testing.T) {
		base := t.TempDir()
		r := removeTestRoots(t, base)
		d := removeTestCreate(t, removeTestOpen(t, r), TempRoot)
		other := r
		other.State = filepath.Join(base, "other-state")
		keep := filepath.Join(d.Path(), "work.txt")
		removeTestWrite(t, keep, "work\n")
		err := removeTestOpen(t, other).RemoveOwned(t.Context(), d)
		if !errors.Is(err, ErrNotOwned) {
			t.Fatalf("RemoveOwned under another state = %v, want %v", err, ErrNotOwned)
		}
		if !removeTestExists(t, keep) {
			t.Fatalf("refused removal still deleted %q", keep)
		}
	})
}

// testSep is the separator of a file-system root path.
const testSep = string(filepath.Separator)

// TestOwnedRoot catches a root that is, or contains, the home, the state root, a repository or a
// file-system root (§5.15); each unsafe case names the refusal it needs, so no check hides a gap.
func TestOwnedRoot(t *testing.T) {
	self, err := CurrentProcess()
	removeTestErr(t, err, "CurrentProcess")
	home, state, repo := "is or contains the home", "is or contains the state root", "is or contains the canonical repository"
	type rootCase struct {
		name   string
		adjust func(t *testing.T, base string, r *OwnedRoots)
		refuse string // the refusal text; empty for safe roots
	}
	cases := []rootCase{
		{"separate roots", func(t *testing.T, base string, r *OwnedRoots) {}, ""},
		{"cache inside the home", func(t *testing.T, base string, r *OwnedRoots) {
			r.Cache = filepath.Join(r.Home, ".cache", "engine")
		}, ""},
		{"temp inside the home", func(t *testing.T, base string, r *OwnedRoots) {
			r.Temp = filepath.Join(r.Home, "AppData", "Local", "Temp", "engine")
		}, ""},
		{"temp is the home", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = r.Home }, home},
		{"cache is the home", func(t *testing.T, base string, r *OwnedRoots) { r.Cache = r.Home }, home},
		{"temp contains the home", func(t *testing.T, base string, r *OwnedRoots) { r.Home = filepath.Join(r.Temp, "users", "ayşe") }, home},
		{"cache contains the home", func(t *testing.T, base string, r *OwnedRoots) { r.Home = filepath.Join(r.Cache, "users", "ayşe") }, home},
		{"temp is the state root", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = r.State }, state},
		{"temp contains the state root", func(t *testing.T, base string, r *OwnedRoots) { r.State = filepath.Join(r.Temp, "state") }, state},
		{"cache contains the state root", func(t *testing.T, base string, r *OwnedRoots) { r.State = filepath.Join(r.Cache, "state") }, state},
		{"temp is a canonical repository", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = r.Canonical[0] }, repo},
		{"cache contains a canonical repository", func(t *testing.T, base string, r *OwnedRoots) {
			r.Canonical = append(r.Canonical, filepath.Join(r.Cache, "src", "app"))
		}, repo},
		{"temp is a file-system root", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = filepath.VolumeName(base) + testSep }, "is a file-system root"},
		{"cache is a file-system root", func(t *testing.T, base string, r *OwnedRoots) { r.Cache = filepath.VolumeName(base) + testSep }, "is a file-system root"},
		{"temp climbs to the home through ..", func(t *testing.T, base string, r *OwnedRoots) {
			removeTestMkdir(t, filepath.Join(r.Temp, "x"))
			r.Temp = filepath.Join(r.Temp, "x") + testSep + ".." +
				testSep + ".." + testSep + "home"
		}, home},
		{"temp is a link to the home's parent", func(t *testing.T, base string, r *OwnedRoots) {
			link := filepath.Join(base, "link-to-base")
			removeTestLink(t, base, link)
			r.Temp = link
		}, "is not a plain directory"},
		{"temp is the home's parent", func(t *testing.T, base string, r *OwnedRoots) {
			r.Home, r.Temp = filepath.Join(base, "users", "home"), filepath.Join(base, "users")
		}, home},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, rootCase{"temp is the home in other letter case", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = strings.ToUpper(r.Home) }, home})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			r := removeTestRoots(t, base)
			tc.adjust(t, base, &r)
			for _, d := range append([]string{r.Temp, r.Cache, r.Home}, r.Canonical...) {
				removeTestRootDir(t, d)
			}
			_, err := OpenOwner(r, self)
			if tc.refuse == "" {
				removeTestErr(t, err, "OpenOwner(%+v) of safe roots", r)
			} else if !errors.Is(err, ErrRootUnsafe) || !strings.Contains(err.Error(), tc.refuse) {
				t.Fatalf("OpenOwner(%+v) = %v, want %v that %s", r, err, ErrRootUnsafe, tc.refuse)
			}
		})
	}
}

// removeTestResidueRow returns the residue_locked row of the outcome table, or fails the test.
func removeTestResidueRow(t *testing.T) OutcomeRow {
	t.Helper()
	for _, r := range Outcomes() {
		if r.Outcome == "residue_locked" {
			return r
		}
	}
	t.Fatalf("outcome table has no residue_locked row (SPEC §6.4): want sentinel ErrResidueLocked")
	return OutcomeRow{}
}

// TestOwnedRemoveLocked: SPEC §6.4 retries 5 times, 200 ms apart, then reports residue_locked; the
// next removal still finds the directory owned.
func TestOwnedRemoveLocked(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("an open handle blocks removal only on Windows")
	}
	ctx := context.Background()
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d := removeTestCreate(t, o, TempRoot)
	held := filepath.Join(d.Path(), "build", "report.txt")
	removeTestWrite(t, held, "held open by another process")
	f, err := os.Open(held)
	removeTestErr(t, err, "open %s", held)
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := f.Close(); err != nil {
				t.Errorf("close %s: %v", held, err)
			}
		}
	})

	begin := time.Now()
	err = o.RemoveOwned(ctx, d)
	took := time.Since(begin)
	if err == nil {
		t.Fatalf("RemoveOwned of a dir with a held file = nil, want residue_locked")
	}
	// 5 attempts with 4 waits of 200 ms: at least 800 ms; a sixth attempt or a doubling wait 1 s.
	if took < 800*time.Millisecond || took >= 1000*time.Millisecond {
		t.Errorf("RemoveOwned gave up after %v, want 5 attempts 200 ms apart (800 ms to 1 s)", took)
	}
	row := removeTestResidueRow(t)
	if row.Err == nil || row.Err.Error() != "residue_locked" {
		t.Fatalf("residue_locked row sentinel = %v, want an error reading residue_locked", row.Err)
	}
	if !errors.Is(err, row.Err) {
		t.Errorf("RemoveOwned error = %v, want errors.Is residue_locked", err)
	}
	if got := ErrorResult(err).Outcome; got != "residue_locked" {
		t.Errorf("ErrorResult(%v).Outcome = %q, want residue_locked", err, got)
	}

	closed = true
	removeTestErr(t, f.Close(), "close %s", held)
	removeTestErr(t, o.RemoveOwned(ctx, d), "RemoveOwned after the handle closed; the next removal must still find the dir owned")
	if removeTestExists(t, d.Path()) {
		t.Errorf("dir %q left after the handle closed", d.Path())
	}
}

// TestRemoveTreeNilApproved catches a removeTree that, given no approved identity, approves the dir
// it finds and empties it (#109, finding 2a), so a nil approval fails with ErrNotOwned.
func TestRemoveTreeNilApproved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "temp")
	name := "run-20261011-0006-0123456789abcdef"
	keep := filepath.Join(root, name, "keep.txt")
	removeTestWrite(t, keep, "inside the temp root, never approved")

	err := removalHooks{}.removeTree(context.Background(), root, name, nil)
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("removeTree(%q, nil approved) = %v, want %v", name, err, ErrNotOwned)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q was removed though no ownership check approved %q", keep, name)
	}
}

// removeTestWalkDeny makes the read of dir fail for the walk, as a mode without read on Unix or a
// deny ACE on Windows does; where root bypasses the mode, hooks.walkRead fails it the same way.
func removeTestWalkDeny(t *testing.T, o *Owner, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		icacls := func(args ...string) {
			out, err := exec.CommandContext(context.Background(), "icacls", append([]string{dir}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("icacls %s %v: %v: %s", dir, args, err, out)
			}
		}
		icacls("/deny", "*S-1-1-0:(RD)")
		t.Cleanup(func() { icacls("/remove:d", "*S-1-1-0") })
	} else {
		removeTestErr(t, os.Chmod(dir, 0o111), "chmod %s 0111", dir)
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
	if _, err := os.ReadDir(dir); err == nil {
		o.hooks.walkRead = func(*os.File) ([]fs.DirEntry, error) { return nil, fs.ErrPermission }
	}
}

// removeTestHold makes the removal of keep fail while the walk still lists it: a held handle on
// Windows, a mode without write on Unix; where root bypasses the mode, hooks.entryRemove fails it.
func removeTestHold(t *testing.T, o *Owner, keep string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		f, err := os.Open(keep)
		removeTestErr(t, err, "open %s", keep)
		t.Cleanup(func() { _ = f.Close() })
		return
	}
	removeTestErr(t, os.Chmod(filepath.Dir(keep), 0o555), "chmod %s 0555", filepath.Dir(keep))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(keep), 0o700) })
	if os.Geteuid() == 0 { // root bypasses the mode through CAP_DAC_OVERRIDE
		o.hooks.entryRemove = func(*os.Root, string) error { return errors.New("remove: the entry is busy (test)") }
	}
}

// TestRemoveOwnedWalkDenied catches a denied walk refused at once as an unknown mount state (#243).
func TestRemoveOwnedWalkDenied(t *testing.T) {
	o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
	d := removeTestCreate(t, o, TempRoot)
	keep := filepath.Join(d.Path(), "locked", "keep.txt")
	removeTestWrite(t, keep, "owned work\n")
	var start time.Time // when the removal starts: the walk deny itself spawns icacls on Windows
	o.hooks.ownedChecked = func(string) { removeTestWalkDeny(t, o, filepath.Dir(keep)); start = time.Now() }
	err := o.RemoveOwned(t.Context(), d)
	if err == nil {
		t.Fatalf("RemoveOwned over a dir whose read is denied = nil; want an at-once not_owned refusal for an unknown mount state")
	}
	if took := time.Since(start); !errors.Is(err, ErrNotOwned) || !strings.Contains(err.Error(), "mount state is unknown") || took >= removeWait {
		t.Errorf("RemoveOwned over a dir whose read is denied = %v after %v; want an at-once not_owned refusal for an unknown mount state", err, took)
	}
	if !removeTestExists(t, keep) {
		t.Errorf("file %q was removed under a refused walk", keep)
	}
}
