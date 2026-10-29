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
		got := w.lockAt(ctx, p)
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

// numberLocked runs number under the lane lock. A failed release of that lock fails the call and
// ends a hold number made, so no ticket stays behind a lock that may not be free.
func (w *laneWaiter) numberLocked(ctx context.Context, t laneTicket) (*laneHold, error) {
	mutex, err := w.lockMutex(ctx)
	if err != nil {
		return nil, err
	}
	h, err := w.number(ctx, t)
	if rerr := mutex.Release(); rerr != nil {
		err = errors.Join(err, rerr)
		if h != nil {
			err, h = errors.Join(err, h.end()), nil
		}
	}
	return h, err
}

// await blocks on the lock next names and frees it at once: getting it shows that its owner
// started (wait) or ended (alive). An alive lock got while the ticket is still there means its
// owner died; when that owner still runs and the path names the file the open saw, the lock file
// was replaced under a live owner (A3), and the request is refused rather than run beside it. An
// alive lock got while the ticket cannot be read marks it gone, with one log line: a live owner would
// hold the lock. An error at the open, a permission error too, fails the wait, and nothing is
// retried: only sweep removes a lock file, on an idle lane (see there).
func (w *laneWaiter) await(ctx context.Context, next laneWait, started map[uint64]bool) error {
	lock := filepath.Join(w.path, laneSeqName(next.seq, next.ext))
	got := w.lockAt(ctx, lock)
	if got.err != nil {
		return laneLockError(lock, got.err)
	}
	if err := got.fl.Release(); err != nil {
		return err
	}
	if next.ext == laneWaitExt {
		started[next.seq] = true
		return nil
	}
	t, present, err := w.readTicket(next.seq)
	switch {
	case err != nil:
		return err
	case t.unread:
		w.gone[next.seq] = true
		_, rerr := w.removeName(laneSeqName(t.seq, laneTicketExt))
		return w.lanes.print(laneUnreadLine(w.spec.label, filepath.Join(w.path, laneSeqName(t.seq, laneTicketExt)), rerr))
	case !present:
		return nil
	case !core.OwnerAlive(t.owner()):
		return w.dropDead(t)
	case got.same():
		return laneError("the alive lock of a live owner held", fmt.Sprintf("ticket %d of pid %d with its lock free", t.seq, t.PID), laneReplaced)
	}
	return nil
}

// laneDrainLine is the line logged when the drain of an abandoned wait cannot free the lock it got.
func laneDrainLine(label, path string, err error) string {
	return fmt.Sprintf("lane %s: could not free the lock %s of a cancelled wait: %v\n", label, path, err)
}

// laneLockError names the next step for a lock open the OS refused, keeping its cause. A cancel, a
// timeout and an error that is a lane error already pass unchanged.
func laneLockError(path string, err error) error {
	var ce *core.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ce) {
		return err
	}
	return &core.Error{Outcome: core.OutcomeError, Expected: "a lane lock file this process may open", Found: err.Error(),
		Next: "fix or remove " + path + " while no check runs", Err: err}
}

// lockAt takes the OS lock on p. It refuses a p that is a link or not a regular file, before the
// open and again under the lock (A1), and keeps what it saw, so the caller can tell whether p
// still names the file it locked (A3). The full A3 check, an fstat of the locked handle, needs a
// core.FileLock that exposes it.
func (w *laneWaiter) lockAt(ctx context.Context, p string) laneLocked {
	before, err := laneStat(p)
	if err != nil {
		return laneLocked{err: err}
	}
	got := w.osWait(ctx, p)
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
func (w *laneWaiter) osWait(ctx context.Context, p string) laneLocked {
	if err := ctx.Err(); err != nil {
		return laneLocked{err: err}
	}
	drain, stop := context.WithTimeout(context.WithoutCancel(ctx), w.lanes.MaxWait)
	got := make(chan laneLocked) // unbuffered: a send succeeds only while the waiter takes it
	abandon := make(chan struct{})
	go w.lockOrDrain(drain, stop, p, got, abandon)
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
// waiter abandoned the wait, it frees the lock it got instead and frees the gate. It has nobody to
// return a failed release to, so it logs one line with the lock path.
func (w *laneWaiter) lockOrDrain(ctx context.Context, stop context.CancelFunc, p string,
	got chan<- laneLocked, abandon <-chan struct{}) {
	defer stop()
	fl, err := w.lanes.lockFile(ctx, p)
	select {
	case got <- laneLocked{fl: fl, err: err}:
		return
	case <-abandon:
	}
	if err == nil {
		if rerr := fl.Release(); rerr != nil {
			_ = w.lanes.print(laneDrainLine(w.spec.label, p, rerr)) // a failed Log write has nowhere to go either
		}
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
