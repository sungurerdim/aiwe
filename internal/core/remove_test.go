package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// removeTestRoots builds a safe set of roots under one t.TempDir: temp, cache, a user home and a
// canonical repository, each a real directory, none inside another. The state root is named but
// left missing: OpenOwner makes it owner-only, and a state dir made beforehand inherits the
// parent's ACL, which may let other users in.
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

// removeTestRootDir makes the root dir owner-only when it is missing, the same as OpenOwner: a dir
// made with MkdirAll on Windows inherits the parent's ACL, which may let other users in.
func removeTestRootDir(t *testing.T, dir string) {
	t.Helper()
	mkdir := func(p string) error { return os.MkdirAll(p, 0o700) }
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		mkdir = mkdirOwnerOnly
	}
	if err := mkdir(dir); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// removeTestMkdir makes dir with MkdirAll; on Windows it inherits the parent's ACL.
func removeTestMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func removeTestWrite(t *testing.T, path, content string) {
	t.Helper()
	removeTestMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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

func removeTestOpen(t *testing.T, r OwnedRoots) *Owner {
	t.Helper()
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	o, err := OpenOwner(r, self)
	if err != nil {
		t.Fatalf("OpenOwner(%+v): %v", r, err)
	}
	return o
}

func removeTestCreate(t *testing.T, o *Owner, root OwnedRoot) OwnedDir {
	t.Helper()
	d, err := o.CreateOwned(t.Context(), root, "run-20261010-0001")
	if err != nil {
		t.Fatalf("CreateOwned: %v", err)
	}
	return d
}

// JunctionForTest makes link a directory junction to the absolute dir target without passing a
// path through cmd (#154). junction_windows_test.go sets it; it is nil on an OS without junctions.
var JunctionForTest func(t *testing.T, target, link string)

// removeTestLink makes link point at target: a symlink where the OS allows it, else (Windows
// without the symlink privilege) a directory junction, which needs no privilege.
func removeTestLink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if JunctionForTest == nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
	JunctionForTest(t, target, link)
}

// TestOwnedRemove catches recursive removal of a directory the engine did not create, register
// and mark as its own (DR-11, §5.15).
func TestOwnedRemove(t *testing.T) {
	t.Run("created temp dir is removed with its contents", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "çalışma-ağacı")
		r := removeTestRoots(t, base)
		o := removeTestOpen(t, r)
		d := removeTestCreate(t, o, TempRoot)
		realTemp, err := filepath.EvalSymlinks(r.Temp)
		if err != nil {
			t.Fatal(err)
		}
		realDir, err := filepath.EvalSymlinks(d.Path())
		if err != nil {
			t.Fatalf("created dir %q does not exist: %v", d.Path(), err)
		}
		if !strings.HasPrefix(realDir, realTemp+string(filepath.Separator)) {
			t.Fatalf("created dir %q is not under the temp root %q", realDir, realTemp)
		}
		if !removeTestExists(t, filepath.Join(d.Path(), ".owner")) {
			t.Fatalf("created dir %q has no .owner marker", d.Path())
		}
		removeTestWrite(t, filepath.Join(d.Path(), "src", "main.go"), "package main\n")
		if err := o.RemoveOwned(t.Context(), d); err != nil {
			t.Fatalf("RemoveOwned of an owned dir: %v", err)
		}
		if removeTestExists(t, d.Path()) {
			t.Fatalf("owned dir %q is still there after RemoveOwned", d.Path())
		}
	})

	t.Run("created cache dir is under the cache root and removable", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		o := removeTestOpen(t, r)
		d := removeTestCreate(t, o, CacheRoot)
		realCache, err := filepath.EvalSymlinks(r.Cache)
		if err != nil {
			t.Fatal(err)
		}
		realDir, err := filepath.EvalSymlinks(d.Path())
		if err != nil {
			t.Fatalf("created dir %q does not exist: %v", d.Path(), err)
		}
		if !strings.HasPrefix(realDir, realCache+string(filepath.Separator)) {
			t.Fatalf("created dir %q is not under the cache root %q", realDir, realCache)
		}
		if err := o.RemoveOwned(t.Context(), d); err != nil {
			t.Fatalf("RemoveOwned of an owned cache dir: %v", err)
		}
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
		if err := o.RemoveOwned(t.Context(), d); err != nil {
			t.Fatalf("RemoveOwned with an inner link: %v", err)
		}
		if removeTestExists(t, d.Path()) {
			t.Fatalf("owned dir %q is still there after RemoveOwned", d.Path())
		}
	})

	// Each refusal case leaves a user file that must survive, and the call must fail with
	// ErrNotOwned.
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
			if err != nil {
				t.Fatalf("read marker: %v", err)
			}
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
			if err := os.Remove(filepath.Join(d.Path(), ".owner")); err != nil {
				t.Fatal(err)
			}
			keep := filepath.Join(d.Path(), "work.txt")
			removeTestWrite(t, keep, "work\n")
			return OwnedDir{path: d.Path()}, []string{keep}
		}},
		{"owned dir whose ledger record is gone", func(t *testing.T, base string, r OwnedRoots, o *Owner) (OwnedDir, []string) {
			d := removeTestCreate(t, o, TempRoot)
			if err := os.WriteFile(filepath.Join(r.State, "ledger.jsonl"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
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
			if err := os.Rename(d.Path(), moved); err != nil {
				t.Fatalf("rename: %v", err)
			}
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
			err := fresh.RemoveOwned(t.Context(), forged)
			if !errors.Is(err, ErrNotOwned) {
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

// TestOwnedRoot catches a temp or cache root that is, or contains, the user's home, the state
// root, a canonical repository or a file-system root and is accepted instead of root_unsafe
// (§5.15). Each unsafe case names the refusal it needs, so another check cannot hide a gap.
func TestOwnedRoot(t *testing.T) {
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	home, state, repo := "is or contains the home", "is or contains the state root", "is or contains the canonical repository"
	cases := []struct {
		name   string
		adjust func(t *testing.T, base string, r *OwnedRoots)
		refuse string // the refusal text; empty for safe roots
	}{
		{"separate roots", func(t *testing.T, base string, r *OwnedRoots) {}, ""},
		{"cache inside the home", func(t *testing.T, base string, r *OwnedRoots) {
			r.Cache = filepath.Join(r.Home, ".cache", "engine")
		}, ""},
		{"temp inside the home", func(t *testing.T, base string, r *OwnedRoots) {
			r.Temp = filepath.Join(r.Home, "AppData", "Local", "Temp", "engine")
		}, ""},
		{"temp is the home", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = r.Home }, home},
		{"cache is the home", func(t *testing.T, base string, r *OwnedRoots) { r.Cache = r.Home }, home},
		{"temp contains the home", func(t *testing.T, base string, r *OwnedRoots) {
			r.Home = filepath.Join(r.Temp, "users", "ayşe")
		}, home},
		{"cache contains the home", func(t *testing.T, base string, r *OwnedRoots) {
			r.Home = filepath.Join(r.Cache, "users", "ayşe")
		}, home},
		{"temp is the state root", func(t *testing.T, base string, r *OwnedRoots) { r.Temp = r.State }, state},
		{"temp contains the state root", func(t *testing.T, base string, r *OwnedRoots) {
			r.State = filepath.Join(r.Temp, "state")
		}, state},
		{"cache contains the state root", func(t *testing.T, base string, r *OwnedRoots) {
			r.State = filepath.Join(r.Cache, "state")
		}, state},
		{"temp is a canonical repository", func(t *testing.T, base string, r *OwnedRoots) {
			r.Temp = r.Canonical[0]
		}, repo},
		{"cache contains a canonical repository", func(t *testing.T, base string, r *OwnedRoots) {
			r.Canonical = append(r.Canonical, filepath.Join(r.Cache, "src", "app"))
		}, repo},
		{"temp is a file-system root", func(t *testing.T, base string, r *OwnedRoots) {
			r.Temp = filepath.VolumeName(base) + string(filepath.Separator)
		}, "is a file-system root"},
		{"cache is a file-system root", func(t *testing.T, base string, r *OwnedRoots) {
			r.Cache = filepath.VolumeName(base) + string(filepath.Separator)
		}, "is a file-system root"},
		{"temp climbs to the home through ..", func(t *testing.T, base string, r *OwnedRoots) {
			removeTestMkdir(t, filepath.Join(r.Temp, "x"))
			r.Temp = filepath.Join(r.Temp, "x") + string(filepath.Separator) + ".." +
				string(filepath.Separator) + ".." + string(filepath.Separator) + "home"
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
		cases = append(cases, struct {
			name   string
			adjust func(t *testing.T, base string, r *OwnedRoots)
			refuse string
		}{"temp is the home in other letter case", func(t *testing.T, base string, r *OwnedRoots) {
			r.Temp = strings.ToUpper(r.Home)
		}, home})
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
			if tc.refuse != "" {
				if !errors.Is(err, ErrRootUnsafe) || !strings.Contains(err.Error(), tc.refuse) {
					t.Fatalf("OpenOwner(%+v) = %v, want %v that %s", r, err, ErrRootUnsafe, tc.refuse)
				}
				return
			}
			if err != nil {
				t.Fatalf("OpenOwner(%+v) of safe roots: %v", r, err)
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

// TestOwnedRemoveLocked catches a removal that gives up on a held file too early or under another
// outcome: SPEC §6.4 retries 5 times, 200 ms apart, then reports residue_locked, and the next
// removal still finds the directory owned.
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
	if err != nil {
		t.Fatalf("open %s: %v", held, err)
	}
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
	// 5 attempts with 4 waits of 200 ms: at least 800 ms; a sixth attempt or a doubling wait
	// reaches 1 000 ms.
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
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", held, err)
	}
	if err := o.RemoveOwned(ctx, d); err != nil {
		t.Fatalf("RemoveOwned after the handle closed: %v; the next removal must still find the dir owned", err)
	}
	if removeTestExists(t, d.Path()) {
		t.Errorf("dir %q left after the handle closed", d.Path())
	}
}

// TestRemoveTreeNilApproved catches a removeTree that, given no approved identity, approves the dir
// it finds and empties it (#109, finding 2a). Only the ownership check may approve a dir, so a nil
// approval fails with ErrNotOwned and removes nothing.
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
