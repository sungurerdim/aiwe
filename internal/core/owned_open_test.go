package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ownedOpenGone opens an owner of r whose process has exited: the pid of a child that already ended.
func ownedOpenGone(t *testing.T, r OwnedRoots) *Owner {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	removeTestErr(t, cmd.Run(), "run a child to exit")
	o, err := OpenOwner(r, ProcessID{PID: cmd.ProcessState.Pid(), Start: time.Now().Add(-time.Hour)})
	removeTestErr(t, err, "OpenOwner of a gone process")
	return o
}

// TestOwnedOpen guards OpenOwned and CacheRoot: an owner reopens a cache directory an owner process
// created that no longer runs; a live creator's directory, one outside the cache root and one no
// owner created are refused with ErrNotOwned, and no refusal repeats the name or the path.
func TestOwnedOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, r OwnedRoots, live *Owner) string // returns the name to open
		ok   bool
	}{
		{"opens: a cache directory of a gone creator", func(t *testing.T, r OwnedRoots, _ *Owner) string {
			return filepath.Base(removeTestCreate(t, ownedOpenGone(t, r), CacheRoot).Path())
		}, true},
		{"catches: a cache directory of a creator that still runs", func(t *testing.T, _ OwnedRoots, live *Owner) string {
			return filepath.Base(removeTestCreate(t, live, CacheRoot).Path())
		}, false},
		{"catches: a temp directory of a gone creator opened as a cache directory", func(t *testing.T, r OwnedRoots, _ *Owner) string {
			return filepath.Base(removeTestCreate(t, ownedOpenGone(t, r), TempRoot).Path())
		}, false},
		{"catches: a name that climbs out of the cache root", func(t *testing.T, r OwnedRoots, _ *Owner) string {
			return "../temp/" + filepath.Base(removeTestCreate(t, ownedOpenGone(t, r), TempRoot).Path())
		}, false},
		{"catches: a cache directory no owner created", func(t *testing.T, r OwnedRoots, _ *Owner) string {
			removeTestMkdir(t, filepath.Join(r.Cache, "run-20261010-0001-0123456789abcdef"))
			return "run-20261010-0001-0123456789abcdef"
		}, false},
		{"catches: a gone creator's cache directory whose marker was replaced", func(t *testing.T, r OwnedRoots, _ *Owner) string {
			d := removeTestCreate(t, ownedOpenGone(t, r), CacheRoot)
			removeTestWrite(t, filepath.Join(d.Path(), markerFile), "{}\n")
			return filepath.Base(d.Path())
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			live := removeTestOpen(t, r)
			name := tc.make(t, r, live)
			d, err := live.OpenOwned(t.Context(), CacheRoot, name)
			if tc.ok {
				if err != nil || d.Path() != filepath.Join(live.CacheRoot(), name) {
					t.Errorf("OpenOwned(%s) = %q, %v; want the directory under the cache root %q", name, d.Path(), err, live.CacheRoot())
				}
				return
			}
			if !errors.Is(err, ErrNotOwned) || d.Path() != "" {
				t.Fatalf("OpenOwned(%s) = %q, %v; want not_owned", name, d.Path(), err)
			}
			if msg := err.Error(); strings.Contains(msg, filepath.Base(name)) || strings.Contains(msg, r.Cache) ||
				strings.Contains(msg, live.CacheRoot()) {
				t.Errorf("the refusal %q repeats the name or the path", msg)
			}
		})
	}
}
