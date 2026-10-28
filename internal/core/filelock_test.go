package core_test

// The file lock under test (#591). The engineer implements exactly this API:
//
//	// FileLock is an exclusive lock the operating system holds on one file: LockFileEx on
//	// Windows, flock on Unix. The OS frees it when its holder dies.
//	type FileLock struct{ ... }
//
//	// AcquireFileLock opens path, creating it when absent, and blocks in the OS call until
//	// the lock is free; it never polls. When ctx ends first it returns an error that wraps
//	// ctx.Err() and holds no lock.
//	func AcquireFileLock(ctx context.Context, path string) (*FileLock, error)
//
//	// Release frees the lock and closes the file; the file stays in place.
//	func (l *FileLock) Release() error
//
// A holder is the test binary run again as a child: init answers the role set in the
// environment before the test framework starts. The child takes the lock, records "held" and
// waits for its stdin to close; then "release" releases the lock and records when, and "keep"
// exits holding it. The test process is the second holder.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

const (
	fileLockName = "queue.lock"
	// fileLockBlockWindow is how long a second holder must stay waiting to count as blocked.
	fileLockBlockWindow = 200 * time.Millisecond
	// fileLockWakeBound bounds the wake of a waiter after a release: the 500 ms poll the lock
	// replaces (internal/engine/slot.go defaultSlotPoll). A waiter woken only by its context
	// timeout, or by a poll slower than 500 ms, misses it; a faster poll passes, so "no polling"
	// rests on reading the code.
	fileLockWakeBound = 500 * time.Millisecond
)

// fileLockTestEnv returns the env variable name of suffix (ROLE, DIR), derived from the
// product constant (DR-17).
func fileLockTestEnv(suffix string) string {
	return strings.ToUpper(core.Product) + "_FILELOCK_TEST_" + suffix
}

func init() {
	role := os.Getenv(fileLockTestEnv("ROLE"))
	if role == "" {
		return
	}
	os.Exit(runFileLockHolder(role, os.Getenv(fileLockTestEnv("DIR"))))
}

// runFileLockHolder is the body of a holder process; a failure to take the lock is recorded in
// "error", so the test reports it instead of a timeout.
func runFileLockHolder(role, dir string) int {
	ctx, cancel := context.WithTimeout(context.Background(), processHelperLifetime)
	defer cancel()
	if err := writeProcessFile(filepath.Join(dir, "pids"), []byte(strconv.Itoa(os.Getpid()))); err != nil {
		return 11
	}
	lock, err := core.AcquireFileLock(ctx, filepath.Join(dir, fileLockName))
	if err != nil {
		_ = writeProcessFile(filepath.Join(dir, "error"), []byte(err.Error())) // the exit code still tells
		return 20
	}
	if err := writeProcessFile(filepath.Join(dir, "held"), nil); err != nil {
		return 11
	}
	read := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin) // returns once the test closes the pipe
		close(read)
	}()
	select {
	case <-read:
	case <-ctx.Done():
		return 15
	}
	switch role {
	case "release":
		at := time.Now()
		if err := lock.Release(); err != nil {
			_ = writeProcessFile(filepath.Join(dir, "error"), []byte(err.Error())) // the exit code still tells
			return 21
		}
		if err := writeProcessFile(filepath.Join(dir, "released"), []byte(strconv.FormatInt(at.UnixNano(), 10))); err != nil {
			return 11
		}
	case "keep":
		// exits holding the lock: the OS must free it
	default:
		return 12
	}
	return 0
}

// fileLockHolder is a running holder process.
type fileLockHolder struct {
	child  *core.Child
	dir    string
	stdin  *os.File
	exited chan error
}

func (h *fileLockHolder) lockPath() string { return filepath.Join(h.dir, fileLockName) }

// startFileLockHolder starts a holder in role and returns once it holds the lock.
func startFileLockHolder(t *testing.T, role string) *fileLockHolder {
	t.Helper()
	dir := t.TempDir()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() = %v", err)
	}
	env := append(os.Environ(), fileLockTestEnv("ROLE")+"="+role, fileLockTestEnv("DIR")+"="+dir)
	child, err := core.Start(t.Context(), core.ChildSpec{Argv: []string{os.Args[0]}, Dir: dir, Env: env, Stdin: r})
	_ = r.Close() // the child holds its own copy
	if err != nil {
		_ = w.Close()
		t.Fatalf("Start(%s holder) = %v, want a running child", role, err)
	}
	h := &fileLockHolder{child: child, dir: dir, stdin: w, exited: make(chan error, 1)}
	go func() { h.exited <- child.Wait() }()
	t.Cleanup(func() {
		_ = w.Close()                              // a second close of a closed pipe is harmless
		_ = child.StopTree(200 * time.Millisecond) // best effort: the holder may be gone
		killProcessTreeAfterFailure(t, dir)
	})
	h.waitHeld(t)
	return h
}

// waitHeld waits until the holder records "held"; a holder that exits first fails the test with
// the error it recorded.
func (h *fileLockHolder) waitHeld(t *testing.T) {
	t.Helper()
	end := time.Now().Add(processBound)
	for {
		if _, err := os.Stat(filepath.Join(h.dir, "held")); err == nil {
			return
		}
		select {
		case err := <-h.exited:
			msg, _ := os.ReadFile(filepath.Join(h.dir, "error")) // absent when the child failed elsewhere
			t.Fatalf("the holder exited (%v) before it held the lock: %s", err, msg)
		default:
		}
		if time.Now().After(end) {
			t.Fatalf("the holder did not hold the lock within %v", processBound)
		}
		time.Sleep(processPollEvery)
	}
}

// lockWaiter is a second holder waiting in AcquireFileLock in the test process.
type lockWaiter struct {
	done chan struct{}
	lock *core.FileLock
	err  error
	at   time.Time
}

// waitForLock starts AcquireFileLock on path with a limit of bound; cleanup releases what it got.
func waitForLock(t *testing.T, path string, bound time.Duration) *lockWaiter {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), bound)
	w := &lockWaiter{done: make(chan struct{})}
	go func() {
		defer close(w.done)
		w.lock, w.err = core.AcquireFileLock(ctx, path)
		w.at = time.Now()
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.done:
			if w.lock != nil {
				_ = w.lock.Release() // best effort: the test has checked what it needs
			}
		case <-time.After(processBound):
			t.Errorf("AcquireFileLock did not return within %v of its context ending", processBound)
		}
	})
	return w
}

// requireStillWaiting fails when the waiter returns within window: a second holder took a held
// lock, or the wait failed.
func requireStillWaiting(t *testing.T, w *lockWaiter, window time.Duration) {
	t.Helper()
	select {
	case <-w.done:
		t.Fatalf("AcquireFileLock returned (%v) while another process held the lock, want it to wait", w.err)
	case <-time.After(window):
	}
}

// requireReturned waits bound for the waiter to return.
func requireReturned(t *testing.T, w *lockWaiter, bound time.Duration, why string) {
	t.Helper()
	select {
	case <-w.done:
	case <-time.After(bound):
		t.Fatalf("AcquireFileLock did not return within %v %s", bound, why)
	}
}

func readReleasedAt(t *testing.T, dir string) time.Time {
	t.Helper()
	path := filepath.Join(dir, "released")
	requireProcessFile(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the release time: %v", err)
	}
	ns, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		t.Fatalf("release time %q: %v", data, err)
	}
	return time.Unix(0, ns)
}

// requireFreeAfterTheHolderEnds ends the holder and requires a fresh AcquireFileLock to take the
// lock within processBound: a wait that gave up when its context ended must not keep it.
func requireFreeAfterTheHolderEnds(t *testing.T, h *fileLockHolder) {
	t.Helper()
	_ = h.stdin.Close() // tells the holder to end
	select {
	case <-h.exited:
	case <-time.After(processBound):
		t.Fatalf("the holder did not end within %v", processBound)
	}
	w := waitForLock(t, h.lockPath(), processBound)
	requireReturned(t, w, processBound, "after the holder ended and the earlier wait gave up")
	if w.err != nil || w.lock == nil {
		t.Fatalf("AcquireFileLock = %v, %v, want the lock: the wait that gave up keeps it", w.lock, w.err)
	}
}

// TestFileLockBlocksASecondHolder checks that a second holder waits in the OS while another
// process holds the lock, wakes promptly when it is released and gives up when its context ends.
func TestFileLockBlocksASecondHolder(t *testing.T) {
	for _, tc := range []struct {
		name string
		// catches names the failure the row catches.
		catches string
		role    string
		// limit is the waiter's context limit; wantErr is what its context ending wraps.
		limit   time.Duration
		wantErr error
	}{
		{
			name:    "a waiter wakes when the holder releases",
			catches: "a second holder takes a held lock, or wakes only by its context timeout or a poll slower than 500 ms",
			role:    "release",
			limit:   processOuterBound,
		},
		{
			name:    "a waiter whose context ends returns without the lock",
			catches: "a wait ignores its context and hangs while the lock is held, or keeps the lock after it gave up",
			role:    "keep",
			limit:   fileLockBlockWindow,
			wantErr: context.DeadlineExceeded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			h := startFileLockHolder(t, tc.role)
			w := waitForLock(t, h.lockPath(), tc.limit)
			if tc.wantErr != nil {
				requireReturned(t, w, processBound, "of its context ending")
				if !errors.Is(w.err, tc.wantErr) || w.lock != nil {
					t.Fatalf("AcquireFileLock = %v, %v, want nil and an error wrapping %v", w.lock, w.err, tc.wantErr)
				}
				requireFreeAfterTheHolderEnds(t, h)
				return
			}
			requireStillWaiting(t, w, fileLockBlockWindow)
			closed := time.Now()
			_ = h.stdin.Close() // tells the holder to release
			requireReturned(t, w, processBound, "of the holder releasing")
			if w.err != nil || w.lock == nil {
				t.Fatalf("AcquireFileLock = %v, %v, want a lock once the holder released", w.lock, w.err)
			}
			// The release time comes from another process's wall clock, whose tick is coarse, so
			// only the monotonic span from the close of stdin is logged: an upper bound of the wake.
			t.Logf("wake within %v of telling the holder to release (an upper bound)", w.at.Sub(closed))
			latency := w.at.Sub(readReleasedAt(t, h.dir))
			if latency < 0 {
				t.Errorf("the waiter took the lock %v before the holder released it", -latency)
			}
			if latency > fileLockWakeBound {
				t.Errorf("wake latency %v, want at most %v: the waiter is not woken by the release", latency, fileLockWakeBound)
			}
		})
	}
}

// TestFileLockFreedWhenTheHolderDies checks that the OS frees the lock of a holder that dies
// holding it, so a waiting second holder takes it with no sweep.
func TestFileLockFreedWhenTheHolderDies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		catches string
		end     func(t *testing.T, h *fileLockHolder)
	}{
		{
			name:    "the holder is killed",
			catches: "a lock outlives a killed holder until a sweep removes it",
			end: func(t *testing.T, h *fileLockHolder) {
				if err := h.child.StopTree(0); err != nil {
					t.Fatalf("StopTree(0) = %v, want the holder killed", err)
				}
			},
		},
		{
			name:    "the holder exits without releasing",
			catches: "a lock outlives a holder that exits without releasing it",
			end:     func(_ *testing.T, h *fileLockHolder) { _ = h.stdin.Close() },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			h := startFileLockHolder(t, "keep")
			w := waitForLock(t, h.lockPath(), processOuterBound)
			requireStillWaiting(t, w, fileLockBlockWindow)
			ended := time.Now()
			tc.end(t, h)
			select {
			case <-h.exited:
			case <-time.After(processBound):
				t.Fatalf("the holder did not end within %v", processBound)
			}
			requireReturned(t, w, processBound, "of the holder dying")
			if w.err != nil || w.lock == nil {
				t.Fatalf("AcquireFileLock = %v, %v, want the lock the dead holder left", w.lock, w.err)
			}
			t.Logf("wake latency after the holder was ended: %v", w.at.Sub(ended))
		})
	}
}
