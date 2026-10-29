package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// laneLockFiles counts the wait and alive files in the lane directory dir.
func laneLockFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the lane directory: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), laneWaitExt) || strings.HasSuffix(e.Name(), laneAliveExt) {
			n++
		}
	}
	return n
}

// TestLaneSweepsAnIdleLane pins the blocking finding of audit-596-r3 (SPEC 6.4, DR-11, STD-11): the
// lock files of ended tickets do not pile up. The numbering of a ticket, under the lane lock, removes
// every wait, alive and temporary file of a lane that holds no ticket; a lane that holds one keeps
// the files of its tickets.
func TestLaneSweepsAnIdleLane(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  func(root string) string
		req  func(repo string, workers int) LaneRequest
	}{
		{"a resource lane", dbLane, dbRequest},
		{"the cpu lane", func(root string) string { return filepath.Join(root, laneCPUDir) }, cpuRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Log("catches: the lock files of every ended ticket stay for good, so the state root outgrows its file ceiling")
			root := t.TempDir()
			dir := tc.dir(root)
			for range 5 {
				mustRelease(t, mustAcquire(t, newTestLanes(t, root, 4), tc.req("done", 1)))
			}
			if err := os.WriteFile(filepath.Join(dir, laneSeqName(40, laneTempExt)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			idle := mustAcquire(t, newTestLanes(t, root, 4), tc.req("idle", 1))
			if n := laneLockFiles(t, dir); n != 2 {
				t.Errorf("the lane holds %d wait and alive files beside one ticket after an idle numbering, want its own 2", n)
			}
			if _, err := os.Lstat(filepath.Join(dir, laneSeqName(40, laneTempExt))); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the stale temporary file is still there (Lstat = %v)", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, laneMutexName)); err != nil {
				t.Errorf("the lane lock = %v, want it kept", err)
			}
			mustRelease(t, idle)
		})
	}
	t.Run("a lane with a ticket keeps its files", func(t *testing.T) {
		t.Log("catches: a numbering sweeps the lock files of a holder, so a waiter on them opens a file that was removed")
		root := t.TempDir()
		mustRelease(t, mustAcquire(t, newTestLanes(t, root, 4), dbRequest("done", 1)))
		holder := mustAcquire(t, newTestLanes(t, root, 4), dbRequest("holder", 1))
		lines := newLaneLines()
		w := newTestLanes(t, root, 4)
		w.Log = lines
		done := goAcquire(t.Context(), w, dbRequest("waiter", 1))
		lines.next(t, "position 1")
		if n := laneLockFiles(t, dbLane(root)); n != 4 {
			t.Errorf("the lane holds %d wait and alive files with a holder and a waiter, want their own 4", n)
		}
		mustRelease(t, holder)
		mustRelease(t, leaseOf(t, awaitResult(t, done, laneTestWait)))
	})
}

// TestLaneSweepFailureIsLogged pins the second condition of the sweep in audit-596-r3: a file the
// sweep cannot remove is not dropped in silence. Only Windows refuses to delete a file another
// handle holds open, so only there can the test make the removal fail.
func TestLaneSweepFailureIsLogged(t *testing.T) {
	t.Log("catches: a failed removal of a stale lock file is dropped, so the residue grows with no line to show it")
	if runtime.GOOS != "windows" {
		t.Skip("Unix removes an open file by design")
	}
	root := t.TempDir()
	stale := filepath.Join(dbLane(root), laneSeqName(9, laneAliveExt))
	if err := os.MkdirAll(dbLane(root), 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := core.AcquireFileLock(t.Context(), stale)
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	l := newTestLanes(t, root, 4)
	l.Log = &log
	mustRelease(t, mustAcquire(t, l, dbRequest("alpha", 1)))
	if err := held.Release(); err != nil {
		t.Error(err)
	}
	n := 0
	for line := range strings.Lines(log.String()) {
		if strings.Contains(line, stale) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the log holds %d lines with the stale file %s, want one; log = %q", n, stale, log.String())
	}
}

// TestLaneLockOpenErrorNamesTheNextStep pins the low finding of audit-596-r3 (DR-6, STD-11): a lock
// open the OS refuses fails Acquire at once with an error that keeps its cause and names the next
// step, not with the raw lock error.
func TestLaneLockOpenErrorNamesTheNextStep(t *testing.T) {
	t.Log("catches: a refused lock file of the ticket ahead comes back as a raw OS message with no next step")
	root := t.TempDir()
	holder := mustAcquire(t, newTestLanes(t, root, 4), dbRequest("holder", 1))
	refused := filepath.Join(dbLane(root), laneSeqName(1, laneAliveExt))
	w := newTestLanes(t, root, 4)
	w.lock = func(ctx context.Context, p string) (*core.FileLock, error) {
		if p == refused {
			return nil, fmt.Errorf("lock %s: open: %w", p, fs.ErrPermission)
		}
		return core.AcquireFileLock(ctx, p)
	}
	_, err := w.Acquire(t.Context(), dbRequest("waiter", 1))
	var ce *core.Error
	switch {
	case !errors.Is(err, fs.ErrPermission):
		t.Errorf("Acquire = %v, want it to keep the cause %v", err, fs.ErrPermission)
	case !errors.As(err, &ce) || !strings.Contains(ce.Next, refused):
		t.Errorf("Acquire = %v, want a core error whose next step names %s", err, refused)
	}
	mustRelease(t, holder)
}
