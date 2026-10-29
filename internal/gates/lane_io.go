package gates

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The IO of the lanes (#596): ticket files, OS locks and the drop of dead owners (STD-10).

// laneDir is one lane directory of l, at path.
type laneDir struct {
	lanes *Lanes
	spec  laneSpec
	path  string
}

// laneWaiter is the one request of l that may wait in the OS locks of one lane: it holds the
// lane's gate. passed means a cancel abandoned an OS wait, which holds the gate now and frees it
// when the OS call returns, so no second request of l starts an OS wait there before (L78). gone
// holds each ticket it cannot read whose alive lock it got: that owner ended or died.
type laneWaiter struct {
	laneDir
	gate   chan struct{}
	passed bool
	gone   map[uint64]bool
}

// laneHold is one ticket of a request and the locks its owner holds; wait is nil once it started.
type laneHold struct {
	laneDir
	ticket      laneTicket
	wait, alive *core.FileLock
}

// take queues t in the lane of s and returns its hold once the lane admits it; a failure removes
// the ticket, so the next one moves up.
func (l *Lanes) take(ctx context.Context, s laneSpec, t laneTicket) (*laneHold, error) {
	gate := l.gate(s.dir)
	select {
	case gate <- struct{}{}: // requests of l behind this one queue here, in memory
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	w := &laneWaiter{laneDir: laneDir{lanes: l, spec: s, path: filepath.Join(l.Root, s.dir)}, gate: gate, gone: map[uint64]bool{}}
	defer func() {
		if !w.passed {
			<-gate
		}
	}()
	h, err := w.enqueue(ctx, t)
	if err != nil {
		return nil, err
	}
	if err := w.serve(ctx, h); err != nil {
		return nil, errors.Join(err, h.end())
	}
	err = h.wait.Release() // the ticket runs: the next may start beside it when capacity allows
	h.wait = nil
	h.tidy(h.ticket.seq, laneWaitExt)
	if err != nil {
		return nil, errors.Join(err, h.end())
	}
	return h, nil
}

// enqueue numbers t under the lane lock, takes its wait and alive locks, then writes its ticket,
// so no ticket is ever seen without its owner's locks. A ticket number cannot read holds a slot it
// cannot see: enqueue then waits on that ticket's alive lock, outside the lane lock, and numbers
// again, so no slot is handed out twice.
func (w *laneWaiter) enqueue(ctx context.Context, t laneTicket) (*laneHold, error) {
	if err := makeLaneDir(w.lanes.Root, w.spec.dir); err != nil {
		return nil, err
	}
	for {
		h, unread, err := w.numberLocked(ctx, t)
		if err != nil || unread == 0 {
			return h, err
		}
		if err := w.await(ctx, laneWait{seq: unread, ext: laneAliveExt}, nil); err != nil {
			return nil, err
		}
	}
}

// number gives t the next seq and, in the CPU lane, the lowest free slot, then takes its locks
// and writes its ticket. It runs under the lane lock. In the CPU lane a ticket it cannot read
// leaves t unnumbered: number returns that ticket's seq instead (seqs start at 1).
func (w *laneWaiter) number(ctx context.Context, t laneTicket) (*laneHold, uint64, error) {
	names, err := laneNames(w.path)
	if err != nil {
		return nil, 0, err
	}
	seq, ok := laneNextSeq(names)
	if !ok {
		return nil, 0, laneError("a free ticket number", "every number used in "+w.path, "remove the lane directory while no check runs")
	}
	if w.spec.slots {
		all, err := w.tickets(names, seq)
		if err != nil {
			return nil, 0, err
		}
		if i := slices.IndexFunc(all, func(a laneTicket) bool { return a.unread }); i >= 0 {
			return nil, all[i].seq, nil
		}
		t.Slot = laneFreeSlot(all)
	}
	t.seq = seq
	h := &laneHold{laneDir: w.laneDir, ticket: t}
	for ext, fl := range map[string]**core.FileLock{laneWaitExt: &h.wait, laneAliveExt: &h.alive} {
		got := w.lockAt(ctx, filepath.Join(w.path, laneSeqName(seq, ext)), nil) // a new file: free at once
		if got.err != nil {
			return nil, 0, errors.Join(got.err, h.end())
		}
		*fl = got.fl
	}
	if err := w.writeTicket(t); err != nil {
		return nil, 0, errors.Join(err, h.end())
	}
	return h, 0, nil
}

// serve waits until the lane admits h, printing each new position, and drops dead owners on its way.
func (w *laneWaiter) serve(ctx context.Context, h *laneHold) error {
	started := map[uint64]bool{}
	told := ""
	for {
		ahead, err := w.tickets(nil, h.ticket.seq)
		if err != nil {
			return err
		}
		next, wait := laneStep(ahead, started, h.ticket.Weight, w.spec.capacity)
		if !wait {
			return nil
		}
		if line := laneWaitLine(w.spec.label, ahead); line != told {
			told = line
			err = w.lanes.print(line)
		}
		if err == nil {
			err = w.await(ctx, next, started)
		}
		if err != nil {
			return err
		}
	}
}

// tickets returns the live tickets below seq in seq order, read from names or, when names is nil,
// from a fresh listing. A ticket whose owner no longer runs is dropped and logged on the way. A
// ticket it cannot read stays in line as a placeholder that holds the whole lane, until w got its
// alive lock (DR-6: a read that failed once is no proof the ticket is gone).
func (w *laneWaiter) tickets(names []string, below uint64) ([]laneTicket, error) {
	var err error
	if names == nil {
		if names, err = laneNames(w.path); err != nil {
			return nil, err
		}
	}
	var out []laneTicket
	for _, name := range names {
		seq, ext, ok := laneSeqOf(name)
		if !ok || ext != laneTicketExt || seq >= below {
			continue
		}
		t, keep, err := w.queued(seq)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b laneTicket) int { return cmp.Compare(a.seq, b.seq) })
	return out, nil
}

// queued reads ticket seq for tickets: keep is false when it is gone. A placeholder of a ticket
// it cannot read holds the whole lane; the ticket of a dead owner is dropped and logged.
func (w *laneWaiter) queued(seq uint64) (laneTicket, bool, error) {
	t, present, err := w.readTicket(seq)
	switch {
	case err != nil:
		return laneTicket{}, false, err
	case !present || t.unread && w.gone[seq]:
		return laneTicket{}, false, nil
	case t.unread:
		t.Weight = w.spec.capacity
		return t, true, nil
	case !core.OwnerAlive(t.owner()):
		return laneTicket{}, false, w.dropDead(t)
	}
	return t, true, nil
}

// readTicket reads ticket seq; present is false only when it is absent. A ticket that is there but
// empty or refused with a permission error, which is what Windows answers for a ticket pending
// delete while a reader holds it, comes back as a placeholder with unread set. The writer renames
// a whole ticket into place, so only a complete ticket that does not parse is corrupt.
func (d laneDir) readTicket(seq uint64) (laneTicket, bool, error) {
	name := laneSeqName(seq, laneTicketExt)
	b, err := d.lanes.readTicketFile(d.path, name)
	if errors.Is(err, fs.ErrNotExist) {
		return laneTicket{}, false, nil
	}
	if errors.Is(err, fs.ErrPermission) || err == nil && len(b) == 0 {
		return laneTicket{seq: seq, unread: true}, true, nil
	}
	var t laneTicket
	if err == nil {
		t, err = decodeLaneTicket(seq, b)
	}
	if err != nil {
		p := filepath.Join(d.path, name)
		return laneTicket{}, false, laneError("a lane ticket", fmt.Sprintf("%s: %v", p, err), "remove "+p+" while no check runs")
	}
	return t, true, nil
}

// readLaneTicket reads the ticket file name of the lane directory dir through an os.Root, so a link
// cannot lead it outside; it refuses a file that is no regular file or larger than any ticket.
func readLaneTicket(dir, name string) ([]byte, error) {
	fi, err := os.Lstat(filepath.Join(dir, name))
	switch {
	case err != nil:
		return nil, err
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("a %s, want a regular file", fi.Mode().Type())
	case fi.Size() > laneTicketMax:
		return nil, fmt.Errorf("%d bytes, want at most %d", fi.Size(), laneTicketMax)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	b, err := root.ReadFile(name)
	return b, errors.Join(err, root.Close())
}

// writeTicket writes t to a new temporary file and renames it to its ticket name, so a reader sees
// the whole ticket or none.
func (d laneDir) writeTicket(t laneTicket) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(d.path)
	if err != nil {
		return err
	}
	tmp := laneSeqName(t.seq, laneTempExt)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, err = f.Write(b)
		if err = errors.Join(err, f.Close()); err == nil {
			err = root.Rename(tmp, laneSeqName(t.seq, laneTicketExt))
		}
		if err != nil {
			_ = root.Remove(tmp)
		}
	}
	return errors.Join(err, root.Close())
}

// dropDead removes the ticket of a dead owner and its lock files and logs it with the owner's pid
// and start time; when another waiter removed it first, this one logs nothing.
func (d laneDir) dropDead(t laneTicket) error {
	removed, err := d.removeName(laneSeqName(t.seq, laneTicketExt))
	if err != nil || !removed {
		return err
	}
	d.tidy(t.seq, laneWaitExt)
	d.tidy(t.seq, laneAliveExt)
	return d.lanes.print(laneDropLine(d.spec.label, t))
}

// tidy removes the lock file ext of seq once nothing needs it: a wait file always, as it is got
// only after its owner started; an alive file only when its ticket is gone or cannot be read, as
// tidy runs only after the alive lock was got or its owner freed it. A failed remove is left to
// the next one to close the file: Windows refuses to delete a file another handle holds open, as
// the lock opens it without FILE_SHARE_DELETE (N1).
func (d laneDir) tidy(seq uint64, ext string) {
	if ext == laneAliveExt {
		if t, present, err := d.readTicket(seq); err != nil || present && !t.unread {
			return
		}
	}
	_, _ = d.removeName(laneSeqName(seq, ext))
}

// removeName removes the lane file name; removed is false when it was already gone.
func (d laneDir) removeName(name string) (bool, error) {
	err := os.Remove(filepath.Join(d.path, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// end removes the ticket of h, then frees its locks and tidies their files: the ticket goes first,
// so whoever gets the alive lock finds it gone and knows its owner ended, not died.
func (h *laneHold) end() error {
	_, err := h.removeName(laneSeqName(h.ticket.seq, laneTicketExt))
	for _, fl := range []**core.FileLock{&h.wait, &h.alive} {
		if *fl != nil {
			err = errors.Join(err, (*fl).Release())
			*fl = nil
		}
	}
	h.tidy(h.ticket.seq, laneWaitExt)
	h.tidy(h.ticket.seq, laneAliveExt)
	return err
}

// makeLaneDir makes the lane directory rel under root, each missing level with mode 0o700, and
// refuses a level that is a link or not a directory, so no ticket lands outside the root (A1).
func makeLaneDir(root, rel string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	p := root
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		if err := os.Mkdir(p, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		fi, err := os.Lstat(p)
		if err == nil && fi.Mode().Type() != fs.ModeDir {
			err = laneError("a plain lane directory", fmt.Sprintf("%s at %s", fi.Mode().Type(), p), "remove "+p+"; the lane makes it again")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// laneNames lists the file names of the lane directory dir, in name order.
func laneNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, err
}

// lookup reads one variable of the environment, through Lookup when the caller set one.
func (l *Lanes) lookup(name string) string {
	if l.Lookup == nil {
		return core.LookupEnv(name)
	}
	return l.Lookup(name)
}

func (l *Lanes) readTicketFile(dir, name string) ([]byte, error) {
	if l.read == nil {
		return readLaneTicket(dir, name)
	}
	return l.read(dir, name)
}

func (l *Lanes) now() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

// print writes one line to Log; a nil Log drops it.
func (l *Lanes) print(line string) error {
	if l.Log == nil {
		return nil
	}
	_, err := io.WriteString(l.Log, line)
	return err
}
