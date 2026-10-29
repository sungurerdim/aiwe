package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The OS locks of the lanes (#596): the checks around each lock (A1, A3 of security-591) and the
// gate that keeps one OS wait per lane in one process (L78).

// laneReplaced is the next step when a lane file was replaced under its lock (A3).
const laneReplaced = "stop the process that replaces lane files, then run again"

// laneLocked is a lock got on a path, what the path was before the open and under the lock, or
// the error that left no lock.
type laneLocked struct {
	fl            *core.FileLock
	before, after fs.FileInfo
	err           error
}

// same reports that the path still names the file it named before the open (A3).
func (k laneLocked) same() bool {
	return k.before != nil && k.after != nil && os.SameFile(k.before, k.after)
}

// lockMutex takes the lane lock and checks under it that the path still names the file it locked
// (A3). A lock file the open created has nothing to compare with, so that lock is freed and taken
// once more, on the file that is there now.
func (w *laneWaiter) lockMutex(ctx context.Context) (*core.FileLock, error) {
	p := filepath.Join(w.path, laneMutexName)
	for range 2 {
		got := w.lockAt(ctx, p, nil)
		if got.err != nil {
			return nil, got.err
		}
		if got.same() {
			return got.fl, nil
		}
		if err := got.fl.Release(); err != nil || got.before != nil {
			return nil, errors.Join(err, laneError("the lane lock that was there before the open", "another file at "+p, laneReplaced))
		}
	}
	return nil, laneError("one lane lock file", "a new file at "+p+" at each open", laneReplaced)
}

// lockAt takes the OS lock on p. It refuses a p that is a link or not a regular file, before the
// open and again under the lock (A1), and keeps what it saw, so the caller can tell whether p
// still names the file it locked (A3). tidy runs after an abandoned wait got and freed the lock.
// The full A3 check, an fstat of the locked handle, needs a core.FileLock that exposes it.
func (w *laneWaiter) lockAt(ctx context.Context, p string, tidy func()) laneLocked {
	before, err := laneStat(p)
	if err != nil {
		return laneLocked{err: err}
	}
	got := w.osWait(ctx, p, tidy)
	if got.err != nil {
		return got
	}
	if got.after, err = laneStat(p); err != nil {
		return laneLocked{err: errors.Join(err, got.fl.Release())}
	}
	got.before = before
	return got
}

// osWait waits for the OS lock on p in a goroutine. When ctx ends first it returns at once, and the
// goroutine keeps the gate until the OS call returns, then frees the lock it may have got: a
// cancelled flock stays blocked in the OS on Unix (L78), so l keeps one OS wait per lane. That
// wait is bounded by MaxWait again (DR-1).
func (w *laneWaiter) osWait(ctx context.Context, p string, tidy func()) laneLocked {
	if err := ctx.Err(); err != nil {
		return laneLocked{err: err}
	}
	drain, stop := context.WithTimeout(context.WithoutCancel(ctx), w.lanes.MaxWait)
	got := make(chan laneLocked) // unbuffered: a send succeeds only while the waiter takes it
	abandon := make(chan struct{})
	go w.lockOrDrain(drain, stop, p, tidy, got, abandon)
	select {
	case r := <-got:
		return r
	case <-ctx.Done():
		close(abandon)
		w.passed = true
		return laneLocked{err: ctx.Err()}
	}
}

// lockOrDrain blocks in the OS lock on p and hands the outcome to the waiter of osWait; when the
// waiter abandoned the wait, it frees the lock it got instead, tidies the file and frees the gate.
func (w *laneWaiter) lockOrDrain(ctx context.Context, stop context.CancelFunc, p string, tidy func(),
	got chan<- laneLocked, abandon <-chan struct{}) {
	defer stop()
	fl, err := w.lanes.lockFile(ctx, p)
	select {
	case got <- laneLocked{fl: fl, err: err}:
		return
	case <-abandon:
	}
	if err == nil && fl.Release() == nil && tidy != nil {
		tidy()
	}
	<-w.gate
}

// laneStat returns the info of p without following a link, nil when p is absent (the open then
// creates it), and an error when p is anything but a regular file (A1).
func laneStat(p string) (fs.FileInfo, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil && !fi.Mode().IsRegular() {
		err = laneError("a regular lane file", fmt.Sprintf("%s at %s", fi.Mode().Type(), p), "remove "+p+"; the lane makes it again")
	}
	return fi, err
}

// gate returns the in-memory gate of the lane dir: a request holds it from its ticket's numbering
// until the lane admits it, so one request of l at a time waits in that lane's OS locks (L78).
func (l *Lanes) gate(dir string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gates == nil {
		l.gates = map[string]chan struct{}{}
	}
	if _, ok := l.gates[dir]; !ok {
		l.gates[dir] = make(chan struct{}, 1)
	}
	return l.gates[dir]
}

// Drain waits until no lock wait a cancel abandoned still runs in l, so the caller may remove the
// lanes; ctx bounds it.
func (l *Lanes) Drain(ctx context.Context) error {
	l.mu.Lock()
	gates := slices.Collect(maps.Values(l.gates))
	l.mu.Unlock()
	for _, g := range gates {
		select {
		case g <- struct{}{}:
			<-g
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (l *Lanes) lockFile(ctx context.Context, p string) (*core.FileLock, error) {
	if l.lock == nil {
		return core.AcquireFileLock(ctx, p)
	}
	return l.lock(ctx, p)
}
