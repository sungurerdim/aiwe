package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// laneTicketFiles returns the ticket file names in the lane directory dir.
func laneTicketFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the lane directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), laneTicketExt) {
			names = append(names, e.Name())
		}
	}
	return names
}

// exitedPID runs this test binary with no test and returns its pid once it has exited: the pid of
// a dead owner.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run a child that exits: %v", err)
	}
	return cmd.ProcessState.Pid()
}

// countLocks makes every OS lock of l go through a counter and returns it.
func countLocks(l *Lanes) *atomic.Int32 {
	var n atomic.Int32
	l.lock = func(ctx context.Context, p string) (*core.FileLock, error) {
		n.Add(1)
		return core.AcquireFileLock(ctx, p)
	}
	return &n
}

// laneAlivePath returns the alive lock file of the one ticket in the lane directory dir.
func laneAlivePath(t *testing.T, dir string) string {
	t.Helper()
	names := laneTicketFiles(t, dir)
	if len(names) != 1 {
		t.Fatalf("lane holds tickets %q, want one", names)
	}
	seq, _, ok := laneSeqOf(names[0])
	if !ok {
		t.Fatalf("ticket name %q has no sequence number", names[0])
	}
	return filepath.Join(dir, laneSeqName(seq, laneAliveExt))
}

// swapFile puts a new file in place of p by a rename, as another process could.
func swapFile(t *testing.T, p string) error {
	t.Helper()
	tmp := p + ".swap"
	if err := os.WriteFile(tmp, []byte("swapped"), 0o600); err != nil {
		t.Fatal(err)
	}
	return os.Rename(tmp, p)
}

// TestLaneRefusesLinkedLockFile pins A1 (security-591): a lock path or a lane directory that is a
// link, or a lock path that is not a regular file, is refused before the OS lock opens it.
func TestLaneRefusesLinkedLockFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plant   func(root, outside string) error
		catches string
	}{
		{"the lane lock is a link to a file outside", func(root, outside string) error {
			target := filepath.Join(outside, "target")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				return err
			}
			return os.Symlink(target, filepath.Join(dbLane(root), laneMutexName))
		}, "the OS lock follows a link at the lock path and locks, or creates, a file outside the lane"},
		{"the lane lock is a directory", func(root, _ string) error {
			return os.Mkdir(filepath.Join(dbLane(root), laneMutexName), 0o700)
		}, "a lock path that is not a regular file goes to the OS lock"},
		{"the lane directory is a link to a directory outside", func(root, outside string) error {
			if err := os.Remove(dbLane(root)); err != nil {
				return err
			}
			return os.Symlink(outside, dbLane(root))
		}, "a linked lane directory puts the tickets and locks outside the lane root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root, outside := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(dbLane(root), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := tc.plant(root, outside); err != nil {
				t.Skipf("this user cannot make the link or entry the row needs: %v", err)
			}
			before, _ := os.ReadDir(outside)
			l := newTestLanes(t, root, 4)
			locks := countLocks(l)
			if _, err := l.Acquire(t.Context(), dbRequest("alpha", 1)); err == nil {
				t.Fatal("Acquire = nil, want the planted entry refused")
			}
			if locks.Load() != 0 {
				t.Errorf("Acquire opened %d lock files, want none past the refused entry", locks.Load())
			}
			if after, _ := os.ReadDir(outside); len(after) != len(before) {
				t.Errorf("the directory outside holds %d entries, want the %d it had", len(after), len(before))
			}
		})
	}
}

// TestLaneRefusesOutsidePathParts pins A2 (security-591): a lane root or a resource name from
// outside that climbs with "..", or names a UNC, device or Windows device path, is refused before
// any IO; an open of a UNC path sends the user's NTLM credentials to that host.
func TestLaneRefusesOutsidePathParts(t *testing.T) {
	sep := string(filepath.Separator)
	for _, tc := range []struct {
		name     string
		root     func(base string) string
		resource string
		wantErr  bool
		catches  string
	}{
		{"a plain root and name", nil, "db", false, "the check refuses every root or name, so no lane ever runs"},
		{"a resource name of ..", nil, "..", true, "a resource name of .. puts the lane outside the lane root"},
		{"a resource name with a slash path", nil, "a/../../b", true, "a resource name with a path climbs out of the lane root"},
		{"a resource name with a backslash path", nil, `..\..\b`, true, "a backslash path in a resource name climbs out on Windows"},
		{"a resource name that is a UNC path", nil, `\\localhost\share`, true, "a UNC resource name is opened, which sends NTLM credentials"},
		{"a resource name that is a device path", nil, `\\.\pipe\lane`, true, "a device path in a resource name opens a pipe or device"},
		{"a resource name that is a Windows device", nil, "nul", true, "a lane directory named nul is the null device on Windows"},
		{"a resource name that is a numbered Windows device", nil, "com1", true, "a lane directory named com1 is a serial port on Windows"},
		{"an empty resource name", nil, "", true, "an empty name makes the exclusive directory itself one lane"},
		{"a relative root", func(string) string { return "lanes" }, "db", true, "a relative root lands wherever the process runs"},
		{"a UNC root", func(string) string { return `\\localhost\share\lanes` }, "db", true, "a UNC lane root is opened, which sends NTLM credentials to that host"},
		{"a UNC root with slashes", func(string) string { return "//localhost/share/lanes" }, "db", true, "the slash form of a UNC root slips past a backslash check"},
		{"a device root", func(string) string { return `\\.\pipe\lanes` }, "db", true, "a device namespace root bypasses the Win32 path checks"},
		{"a long path root", func(string) string { return `\\?\UNC\localhost\share\lanes` }, "db", true, "the long path form of a UNC root is opened"},
		{"a root that climbs with ..", func(base string) string { return base + sep + ".." + sep + "lanes" }, "db", true,
			"a root with .. is cleaned to a directory outside the one given"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			base := t.TempDir()
			root := filepath.Join(base, "lanes")
			if tc.root != nil {
				root = tc.root(base)
			}
			l := newTestLanes(t, root, 4)
			locks := countLocks(l)
			lease, err := l.Acquire(t.Context(), LaneRequest{Repo: "alpha", Kind: "unit", Exclusive: []string{tc.resource}, Workers: 1, Slot: 1})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Acquire = %v, want an error: %v", err, tc.wantErr)
			}
			if err == nil {
				mustRelease(t, lease)
				return
			}
			if locks.Load() != 0 {
				t.Errorf("Acquire opened %d lock files, want none for a refused path", locks.Load())
			}
			if entries, _ := os.ReadDir(base); len(entries) != 0 {
				t.Errorf("the base directory holds %d entries, want none made for a refused path", len(entries))
			}
		})
	}
}

// TestLaneLockFileSwapIsRefused pins A3 (security-591): on Unix a lock file can be replaced between
// the open and the lock, so the lane stats the path again under the lock and never runs on a file
// that is no longer the one at the path.
func TestLaneLockFileSwapIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows opens a lock file without FILE_SHARE_DELETE, so it cannot be replaced while open; " +
			"TestLaneLockFileCannotBeDeletedWhileHeld pins that")
	}
	for _, tc := range []struct {
		name    string
		mutex   bool // swap the lane lock under the lock; else swap the alive file of a live holder
		catches string
	}{
		{"the lane lock is swapped under the lock", true, "two processes number tickets at once, each under its own file at one path"},
		{"the alive file of a live holder is swapped", false, "a waiter that locks the swapped file runs beside the live holder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			var holder *LaneLease
			if !tc.mutex {
				holder = mustAcquire(t, newTestLanes(t, root, 4), dbRequest("holder", 1))
				if err := swapFile(t, laneAlivePath(t, dbLane(root))); err != nil {
					t.Fatal(err)
				}
			}
			l := newTestLanes(t, root, 4)
			if tc.mutex {
				l.lock = func(ctx context.Context, p string) (*core.FileLock, error) {
					fl, err := core.AcquireFileLock(ctx, p)
					if err == nil && filepath.Base(p) == laneMutexName {
						err = swapFile(t, p)
					}
					return fl, err
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*laneTestBlocked)
			defer cancel()
			if lease, err := l.Acquire(ctx, dbRequest("waiter", 1)); err == nil {
				mustRelease(t, lease)
				t.Fatal("Acquire = a lease, want it refused on the swapped lock file")
			}
			if holder != nil {
				mustRelease(t, holder)
			}
		})
	}
}

// TestLaneLockFileCannotBeDeletedWhileHeld pins N1 (security-591): Windows opens the lock file
// without FILE_SHARE_DELETE, so it cannot be removed or replaced while open. The lane never
// deletes a lock file (B2 of audit-596-r2), so a release beside a waiter that holds the file open
// has nothing to delete and succeeds; only the tickets go.
func TestLaneLockFileCannotBeDeletedWhileHeld(t *testing.T) {
	for _, tc := range []struct {
		name        string
		windowsOnly bool
		catches     string
	}{
		{"a held alive file cannot be removed or replaced", true,
			"the lock file is opened with FILE_SHARE_DELETE, so it can be removed and a second holder takes a new file"},
		{"a release while a waiter holds the file open", false,
			"a release fails, or leaves a ticket behind, when a waiter holds a lock file of the lane open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			if tc.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("Unix removes an open file by design; TestLaneLockFileSwapIsRefused pins the guard there")
			}
			root := t.TempDir()
			holder := mustAcquire(t, newTestLanes(t, root, 4), dbRequest("holder", 1))
			if tc.windowsOnly {
				alive := laneAlivePath(t, dbLane(root))
				if err := os.Remove(alive); err == nil {
					t.Error("os.Remove of the held alive file = nil, want a sharing violation")
				}
				if err := swapFile(t, alive); err == nil {
					t.Error("a rename over the held alive file = nil, want a sharing violation")
				}
				mustRelease(t, holder)
				return
			}
			lines := newLaneLines()
			w := newTestLanes(t, root, 4)
			w.Log = lines
			done := goAcquire(t.Context(), w, dbRequest("waiter", 1))
			lines.next(t, "position 1")
			mustRelease(t, holder)
			mustRelease(t, leaseOf(t, awaitResult(t, done, laneTestWait)))
			entries, err := os.ReadDir(dbLane(root))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), laneTicketExt) || strings.HasSuffix(e.Name(), laneTempExt) {
					t.Errorf("the lane still holds %q after every lease ended", e.Name())
				}
			}
		})
	}
}

// TestLaneReleaseClosesLockHandles pins N2 (security-591): a release closes every lock file handle
// it opened. A handle left open makes the removal of its file fail on Windows (the test removes
// every file left); on Linux the open descriptors of the process grow.
func TestLaneReleaseClosesLockHandles(t *testing.T) {
	t.Run("every lane file can be removed after a release", func(t *testing.T) {
		t.Log("catches: a release leaves a lock file handle open, so Windows refuses its delete and the handle keeps the lock")
		root := t.TempDir()
		mustRelease(t, mustAcquire(t, newTestLanes(t, root, 4), dbRequest("alpha", 2)))
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.HasSuffix(d.Name(), laneTicketExt) || strings.HasSuffix(d.Name(), laneTempExt) {
				t.Errorf("%s is left after the release", p)
			}
			return os.Remove(p)
		})
		if err != nil {
			t.Errorf("remove the lane files after the release: %v, want every handle closed", err)
		}
	})
	t.Run("the open descriptors of the process do not grow", func(t *testing.T) {
		t.Log("catches: each lease leaks a descriptor on Unix")
		if runtime.GOOS != "linux" {
			t.Skip("only Linux lists the open descriptors of a process in /proc/self/fd")
		}
		root := t.TempDir()
		l := newTestLanes(t, root, 4)
		cycle := func() { mustRelease(t, mustAcquire(t, l, dbRequest("alpha", 2))) }
		cycle()
		n0 := openFDs(t)
		for range 5 {
			cycle()
		}
		if n1 := openFDs(t); n1 > n0 {
			t.Errorf("open descriptors grew from %d to %d over 5 leases", n0, n1)
		}
	})
}

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// TestLaneOneOSWaiterPerResource pins the L78 bound: at most one request of one process waits in
// the OS lock of one lane, the others queue in memory, and a cancel does not let a second one in
// while the abandoned OS wait still runs. The lock here ignores the cancel, as flock does on Unix.
func TestLaneOneOSWaiterPerResource(t *testing.T) {
	t.Log("catches: every waiter of a process blocks in the OS, or a cancel frees the gate while its OS wait " +
		"still runs, so cancels pile up blocked threads")
	root := t.TempDir()
	holder := mustAcquire(t, newTestLanes(t, root, 8), dbRequest("holder", 1))
	w := newTestLanes(t, root, 8)
	lines := newLaneLines()
	w.Log = lines
	var mu sync.Mutex
	inflight, peak := 0, 0
	w.lock = func(ctx context.Context, p string) (*core.FileLock, error) {
		if !strings.HasPrefix(p, dbLane(root)) {
			return core.AcquireFileLock(ctx, p)
		}
		mu.Lock()
		inflight++
		peak = max(peak, inflight)
		mu.Unlock()
		defer func() { mu.Lock(); inflight--; mu.Unlock() }()
		return core.AcquireFileLock(context.WithoutCancel(ctx), p)
	}
	peakNow := func() int { mu.Lock(); defer mu.Unlock(); return peak }
	var cancels []context.CancelFunc
	var dones []<-chan laneResult
	for i := range 3 {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cancels = append(cancels, cancel)
		dones = append(dones, goAcquire(ctx, w, dbRequest("waiter", 1)))
		if i == 0 {
			lines.next(t, "position 1") // waiter 0 is the one in the OS wait
		}
	}
	time.Sleep(laneTestBlocked)
	for i := range 2 {
		cancels[i]()
		if r := awaitResult(t, dones[i], 3*laneTestBlocked); !errors.Is(r.err, context.Canceled) {
			t.Fatalf("cancelled waiter %d = %v, want %v at once", i, r.err, context.Canceled)
		}
		time.Sleep(laneTestBlocked)
		if p := peakNow(); p > 1 {
			t.Fatalf("after %d cancels %d OS waits ran at once in one lane, want at most 1", i+1, p)
		}
	}
	mustRelease(t, holder)
	mustRelease(t, leaseOf(t, awaitResult(t, dones[2], laneTestWait)))
	if p := peakNow(); p > 1 {
		t.Errorf("%d OS waits ran at once in one lane, want at most 1", p)
	}
}

// TestLaneFileInFlux pins Q1 and F1 of audit-596 (DR-6, STD-11): an empty or unreadable ticket of a
// live holder (seq 1) keeps its place until its alive lock is got, and an error at the open of a lock
// ahead, a permission one too, fails Acquire at once with no retry (B2 of audit-596-r2: no deletes).
func TestLaneFileInFlux(t *testing.T) {
	for _, tc := range []struct {
		name, ticket, lock string // ticket 1: "emptied" or "refused" (each read) under the holder, or "ownerless" and empty
		cpu                bool   // the cpu lane, else the db lane
		refusals           int32  // how many opens of the lock file lock are refused; 1<<20 is for good
		lockErr, want      error  // the error of those opens; what Acquire returns while the holder holds (nil: a lease)
		catches            string
	}{
		{"an emptied ticket in a resource lane", "emptied", "", false, 0, nil, context.DeadlineExceeded,
			"an empty ticket of a live holder is skipped, so a lane of capacity 1 gets a second holder"},
		{"a refused ticket in a resource lane", "refused", "", false, 0, nil, context.DeadlineExceeded,
			"a ticket read refused with a permission error is skipped, so a lane of capacity 1 gets a second holder"},
		{"an emptied ticket in the cpu lane", "emptied", "", true, 0, nil, context.DeadlineExceeded,
			"an empty ticket of a live holder is skipped, so the cpu lane admits a second check beside a full pool"},
		{"a refused ticket in the cpu lane", "refused", "", true, 0, nil, context.DeadlineExceeded,
			"a ticket read refused with a permission error is skipped, so the cpu lane admits a second check beside a full pool"},
		{"an empty ticket of no owner", "ownerless", "", false, 0, nil, nil,
			"an empty ticket of no owner is read as corrupt, or holds the lane for good once its alive lock is free"},
		{"the alive lock ahead refused once", "", laneSeqName(1, laneAliveExt), false, 1, fs.ErrPermission, fs.ErrPermission,
			"a permission error at the alive file of the ticket ahead is retried on a timer instead of failing closed"},
		{"the wait lock ahead refused once", "", laneSeqName(1, laneWaitExt), false, 1, fs.ErrPermission, fs.ErrPermission,
			"a permission error at the wait file of the ticket ahead is retried on a timer instead of failing closed"},
		{"the alive lock ahead refused for good", "", laneSeqName(1, laneAliveExt), false, 1 << 20, fs.ErrPermission, fs.ErrPermission,
			"a lock file refused for good is retried until the waiter's bound, a busy wait"},
		{"the alive lock ahead refused with another error", "", laneSeqName(1, laneAliveExt), false, 1, fs.ErrInvalid, fs.ErrInvalid,
			"a retry swallows an open error, so a broken lock file is waited on until MaxWait"},
		{"the request's own alive lock refused", "", laneSeqName(2, laneAliveExt), false, 1, fs.ErrPermission, fs.ErrPermission,
			"a retry on the request's own lock file hides a lane it cannot write and waits out MaxWait"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			dir, req := dbLane(root), dbRequest
			if tc.cpu {
				dir, req = filepath.Join(root, laneCPUDir), cpuRequest
			}
			var holder *LaneLease
			if tc.ticket != "ownerless" {
				holder = mustAcquire(t, newTestLanes(t, root, 4), req("holder", 1))
			}
			ticket := filepath.Join(dir, laneSeqName(1, laneTicketExt))
			if tc.ticket == "emptied" || tc.ticket == "ownerless" {
				if err := errors.Join(os.MkdirAll(dir, 0o700), os.WriteFile(ticket, nil, 0o600)); err != nil {
					t.Fatal(err)
				}
			}
			w := newTestLanes(t, root, 4)
			w.read = func(d, name string) ([]byte, error) {
				if tc.ticket == "refused" && filepath.Join(d, name) == ticket {
					return nil, &fs.PathError{Op: "open", Path: ticket, Err: fs.ErrPermission}
				}
				return readLaneTicket(d, name)
			}
			var opens atomic.Int32
			w.lock = func(ctx context.Context, p string) (*core.FileLock, error) {
				if p == filepath.Join(dir, tc.lock) && opens.Add(1) <= tc.refusals {
					return nil, fmt.Errorf("lock %s: open: %w", p, tc.lockErr)
				}
				return core.AcquireFileLock(ctx, p)
			}
			ctx, cancel := context.WithTimeout(t.Context(), laneTestBlocked)
			defer cancel()
			lease, err := w.Acquire(ctx, req("waiter", 1))
			switch {
			case err == nil && tc.want != nil:
				t.Errorf("Acquire = a lease in slot %d beside the holder in slot %d, want %v", lease.Slot(), holder.Slot(), tc.want)
			case !errors.Is(err, tc.want) || !errors.Is(tc.want, context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded):
				t.Errorf("Acquire = %v, want %v", err, tc.want)
			case tc.refusals > 0 && opens.Load() != 1:
				t.Errorf("Acquire opened %s %d times, want once: a refused open is not retried, and the row must open it", tc.lock, opens.Load())
			}
			if err == nil {
				mustRelease(t, lease)
			}
			if holder != nil {
				opens.Store(1 << 30) // no refusal once the holder ends
				mustRelease(t, holder)
			}
			mustRelease(t, leaseOf(t, awaitResult(t, goAcquire(t.Context(), w, req("waiter", 1)), laneTestWait)))
		})
	}
}

// FuzzParseLaneToken pins that the lane token parser, which reads text from the environment, never
// panics and that every token it accepts round-trips (STD-19).
func FuzzParseLaneToken(f *testing.F) {
	f.Add(laneToken(core.ProcessFrom(4242, laneTestT0(), "")))
	f.Add(laneToken(core.ProcessFrom(1, laneTestT0(), "12:34")))
	for _, s := range []string{"", ":", "::", "1:2:3", "-1:0:", "0:0:", "x:y:z", "99999999999999999999:1:", "+5:1:"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, ok := parseLaneToken(s)
		if !ok {
			return
		}
		if p.PID < 1 {
			t.Fatalf("parseLaneToken(%q) accepted pid %d, want only pids from 1", s, p.PID)
		}
		q, ok := parseLaneToken(laneToken(p))
		if !ok || q.PID != p.PID || !q.Start.Equal(p.Start) || q.StartID() != p.StartID() {
			t.Fatalf("token %q does not round-trip: %+v then %+v (ok %v)", s, p, q, ok)
		}
	})
}
