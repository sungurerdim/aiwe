package core_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

func footprintTestRoots(t *testing.T) core.OwnedRoots {
	t.Helper()
	base := t.TempDir()
	r := core.OwnedRoots{
		State:     filepath.Join(base, "state"),
		Temp:      filepath.Join(base, "temp"),
		Cache:     filepath.Join(base, "cache"),
		Home:      filepath.Join(base, "home"),
		Canonical: []string{filepath.Join(base, "repo")},
	}
	// OpenOwner makes the temp and cache roots owner-only; MkdirAll would inherit an open ACL on Windows.
	for _, d := range []string{r.Home, r.Canonical[0]} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func footprintTestSelf(t *testing.T) core.ProcessID {
	t.Helper()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	return self
}

func footprintTestOpen(t *testing.T, r core.OwnedRoots, self core.ProcessID) *core.Owner {
	t.Helper()
	o, err := core.OpenOwner(r, self)
	if err != nil {
		t.Fatalf("OpenOwner: %v", err)
	}
	return o
}

func footprintTestMeasure(ctx context.Context, t *testing.T, o *core.Owner) core.Footprint {
	t.Helper()
	f, err := o.Footprint(ctx)
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	return f
}

// footprintTestDead returns the identity of a child process that has already exited.
func footprintTestDead(ctx context.Context, t *testing.T) core.ProcessID {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	start := time.Now()
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	return core.ProcessID{PID: pid, Start: start}
}

func footprintTestWriteFiles(t *testing.T, dir string, n, size int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.Repeat("x", size))
	for i := range n {
		p := filepath.Join(dir, "f"+strconv.Itoa(i)+".bin")
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFootprint catches a test that makes temporary directories and leaves
// one behind; and a state or cache over its byte or file ceiling that is not red (DR-11, §6.4).
func TestFootprint(t *testing.T) {
	ctx := context.Background()

	t.Run("default ceilings", func(t *testing.T) {
		c := core.DefaultFootprintCeilings()
		if c.StateFiles != 1000 || c.CacheFiles != 2000 || c.TempOrphans != 0 {
			t.Fatalf("DefaultFootprintCeilings() = %+v, want StateFiles 1000, CacheFiles 2000, TempOrphans 0", c)
		}
		if c.StateBytes <= 0 || c.CacheBytes <= c.StateBytes {
			t.Fatalf("DefaultFootprintCeilings() = %+v, want 0 < StateBytes < CacheBytes", c)
		}
	})

	// Each case is a test that makes temporary directories; a leftover must be counted
	// and must turn the footprint red.
	bc2 := []struct {
		name        string
		run         func(t *testing.T, r core.OwnedRoots)
		wantOrphans int64
	}{
		{"removes what it made", func(t *testing.T, r core.OwnedRoots) {
			o := footprintTestOpen(t, r, footprintTestSelf(t))
			for range 3 {
				d, err := o.CreateOwned(ctx, core.TempRoot, "run-bc2")
				if err != nil {
					t.Fatal(err)
				}
				if err := o.RemoveOwned(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
		}, 0},
		{"leaves one behind and exits", func(t *testing.T, r core.OwnedRoots) {
			o := footprintTestOpen(t, r, footprintTestDead(ctx, t))
			if _, err := o.CreateOwned(ctx, core.TempRoot, "run-bc2"); err != nil {
				t.Fatal(err)
			}
		}, 1},
		{"leaves two behind, removes one", func(t *testing.T, r core.OwnedRoots) {
			o := footprintTestOpen(t, r, footprintTestDead(ctx, t))
			for i := range 3 {
				d, err := o.CreateOwned(ctx, core.TempRoot, "run-bc2")
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					if err := o.RemoveOwned(ctx, d); err != nil {
						t.Fatal(err)
					}
				}
			}
		}, 2},
		{"still running, so the dir has a live owner", func(t *testing.T, r core.OwnedRoots) {
			o := footprintTestOpen(t, r, footprintTestSelf(t))
			if _, err := o.CreateOwned(ctx, core.TempRoot, "run-bc2"); err != nil {
				t.Fatal(err)
			}
		}, 0},
	}
	for _, tc := range bc2 {
		t.Run(tc.name, func(t *testing.T) {
			r := footprintTestRoots(t)
			tc.run(t, r)
			o := footprintTestOpen(t, r, footprintTestSelf(t))
			f := footprintTestMeasure(ctx, t, o)
			if f.TempOrphans != tc.wantOrphans {
				t.Fatalf("TempOrphans = %d, want %d", f.TempOrphans, tc.wantOrphans)
			}
			err := f.Check(core.DefaultFootprintCeilings())
			if tc.wantOrphans == 0 {
				if err != nil {
					t.Fatalf("Check with no leftover = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, core.ErrFootprintExceeded) {
				t.Fatalf("Check with %d leftovers = %v, want %v", tc.wantOrphans, err, core.ErrFootprintExceeded)
			}
			// The sweep clears the leftovers, and the footprint is green again.
			if _, err := o.Sweep(ctx); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			after := footprintTestMeasure(ctx, t, o)
			if after.TempOrphans != 0 {
				t.Fatalf("TempOrphans after sweep = %d, want 0", after.TempOrphans)
			}
			if err := after.Check(core.DefaultFootprintCeilings()); err != nil {
				t.Fatalf("Check after sweep = %v, want nil", err)
			}
		})
	}

	t.Run("state and cache bytes and files are counted", func(t *testing.T) {
		r := footprintTestRoots(t)
		o := footprintTestOpen(t, r, footprintTestSelf(t))
		before := footprintTestMeasure(ctx, t, o)
		footprintTestWriteFiles(t, filepath.Join(r.State, "projects", "github.com", "acme", "app"), 3, 1000)
		footprintTestWriteFiles(t, filepath.Join(r.Cache, "bare", "objects"), 5, 2048)
		after := footprintTestMeasure(ctx, t, o)
		if got := after.StateFiles - before.StateFiles; got != 3 {
			t.Errorf("state files grew by %d, want 3", got)
		}
		if got := after.StateBytes - before.StateBytes; got != 3000 {
			t.Errorf("state bytes grew by %d, want 3000", got)
		}
		if got := after.CacheFiles - before.CacheFiles; got != 5 {
			t.Errorf("cache files grew by %d, want 5", got)
		}
		if got := after.CacheBytes - before.CacheBytes; got != 5*2048 {
			t.Errorf("cache bytes grew by %d, want %d", got, 5*2048)
		}
	})

	ceilings := []struct {
		name  string
		fill  func(t *testing.T, r core.OwnedRoots)
		limit func(f core.Footprint) core.FootprintCeilings
	}{
		{"state files", func(t *testing.T, r core.OwnedRoots) {
			footprintTestWriteFiles(t, filepath.Join(r.State, "projects"), 1, 1)
		}, func(f core.Footprint) core.FootprintCeilings {
			c := core.DefaultFootprintCeilings()
			c.StateFiles = f.StateFiles
			return c
		}},
		{"state bytes", func(t *testing.T, r core.OwnedRoots) {
			footprintTestWriteFiles(t, filepath.Join(r.State, "projects"), 1, 1)
		}, func(f core.Footprint) core.FootprintCeilings {
			c := core.DefaultFootprintCeilings()
			c.StateBytes = f.StateBytes
			return c
		}},
		{"cache files", func(t *testing.T, r core.OwnedRoots) {
			footprintTestWriteFiles(t, filepath.Join(r.Cache, "bare"), 1, 1)
		}, func(f core.Footprint) core.FootprintCeilings {
			c := core.DefaultFootprintCeilings()
			c.CacheFiles = f.CacheFiles
			return c
		}},
		{"cache bytes", func(t *testing.T, r core.OwnedRoots) {
			footprintTestWriteFiles(t, filepath.Join(r.Cache, "bare"), 1, 1)
		}, func(f core.Footprint) core.FootprintCeilings {
			c := core.DefaultFootprintCeilings()
			c.CacheBytes = f.CacheBytes
			return c
		}},
	}
	for _, tc := range ceilings {
		t.Run("ceiling on "+tc.name, func(t *testing.T) {
			r := footprintTestRoots(t)
			o := footprintTestOpen(t, r, footprintTestSelf(t))
			at := footprintTestMeasure(ctx, t, o)
			c := tc.limit(at)
			if err := at.Check(c); err != nil {
				t.Fatalf("Check at the ceiling = %v, want nil", err)
			}
			tc.fill(t, r)
			over := footprintTestMeasure(ctx, t, o)
			if err := over.Check(c); !errors.Is(err, core.ErrFootprintExceeded) {
				t.Fatalf("Check one over the ceiling = %v, want %v", err, core.ErrFootprintExceeded)
			}
		})
	}
}

// footprintTestLink makes link point at target: a symlink where the OS allows it, else (Windows
// without the symlink privilege) a directory junction.
func footprintTestLink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
	core.JunctionForTest(t, target, link)
}

// TestFootprintUnownedOrphan catches a registered temp dir of a dead owner that fails the
// ownership check and is left out of TempOrphans, so the ceiling of 0 stays green (DR-11, DR-6).
func TestFootprintUnownedOrphan(t *testing.T) {
	ctx := context.Background()
	r := footprintTestRoots(t)
	base := filepath.Dir(r.Temp)
	create := func() core.OwnedDir {
		t.Helper()
		d, err := footprintTestOpen(t, r, footprintTestDead(ctx, t)).CreateOwned(ctx, core.TempRoot, "run-20261010-0003")
		if err != nil {
			t.Fatalf("CreateOwned: %v", err)
		}
		return d
	}

	unmarked := create()
	if err := os.Remove(filepath.Join(unmarked.Path(), ".owner")); err != nil {
		t.Fatal(err)
	}
	dangling := create()
	gone := filepath.Join(base, "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	footprintTestLink(t, gone, filepath.Join(dangling.Path(), "cache-link"))
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	escaping := create()
	footprintTestLink(t, r.Home, filepath.Join(escaping.Path(), "home"))

	f := footprintTestMeasure(ctx, t, footprintTestOpen(t, r, footprintTestSelf(t)))
	if f.TempOrphans != 3 {
		t.Errorf("TempOrphans = %d, want 3 (no marker, dangling link, link out of the root)", f.TempOrphans)
	}
	if err := f.Check(core.DefaultFootprintCeilings()); !errors.Is(err, core.ErrFootprintExceeded) {
		t.Errorf("Check(default ceilings) = %v, want footprint_exceeded for 3 temp orphans", err)
	}
}
