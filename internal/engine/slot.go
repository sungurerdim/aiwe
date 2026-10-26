package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The slot pool of one repository (SPEC §6.5, §6.6): this file decides, slot_io.go does the IO.
const (
	shrinkSession   = "shrink" // the holder of the lease a shrink takes on the idle slot it removes
	defaultSlotPoll = 500 * time.Millisecond
)

// errSlotUnusable marks a slot a lease cannot use, so it tries another one.
var errSlotUnusable = errors.New("slot unusable")

// SlotPoolSpec names one repository's pool: owner, canonical repository, lease directory
// <state>/projects/<repo-id>/slots, bound N, the stack profile's KeepDirs, lock files and restore.
type SlotPoolSpec struct {
	Owner                        *core.Owner
	Canonical, LeaseDir          string
	Size                         int
	KeepDirs, LockFiles, Restore []string
	Now                          func() time.Time
	Alive                        func(core.ProcessID) bool
	Queue                        io.Writer
	Poll                         time.Duration
}

// SlotHolder is the session and the process that lease a slot.
type SlotHolder struct {
	Session string
	Process core.ProcessID
}

// Slot is one leased slot: number K, work tree Dir, and Root, its owned directory in the cache root.
type Slot struct {
	K         int
	Dir, Root string

	lease []byte // this lease's file, so a release drops this lease and no other
}

// SlotPool hands out the slots of one repository; many pools share one lease directory. Only the
// pool that created a slot holds its owned directory, so only it can remove the slot (DR-11).
type SlotPool struct {
	spec   SlotPoolSpec
	source string
	self   core.ProcessID
	mu     sync.Mutex
	owned  map[int]core.OwnedDir
}

// OpenSlotPool checks spec and returns its pool; nothing is created before a lease needs a slot.
func OpenSlotPool(spec SlotPoolSpec) (*SlotPool, error) {
	if err := errors.Join(checkPoolSpec(spec), checkForeignRepo()); err != nil {
		return nil, err
	}
	source, err := fileURL(spec.Canonical)
	if err != nil {
		return nil, fmt.Errorf("slot pool: %w", err)
	}
	if err := checkClaimDir(spec.LeaseDir); err != nil {
		return nil, fmt.Errorf("slot pool: lease directory: %w", err)
	}
	self, err := core.CurrentProcess()
	if err != nil {
		return nil, fmt.Errorf("slot pool: %w", err)
	}
	return &SlotPool{spec: withPoolDefaults(spec), source: source, self: self, owned: map[int]core.OwnedDir{}}, nil
}

// withPoolDefaults fills the clock, the liveness check core.OwnerAlive, the queue output and the poll.
func withPoolDefaults(spec SlotPoolSpec) SlotPoolSpec {
	if spec.Now == nil {
		spec.Now = time.Now
	}
	if spec.Alive == nil {
		spec.Alive = core.OwnerAlive
	}
	spec.Queue = cmp.Or(spec.Queue, io.Writer(io.Discard))
	spec.Poll = cmp.Or(max(spec.Poll, 0), defaultSlotPoll)
	return spec
}

// Lease hands h a clean slot at base: a free one, a new one below N, or at N the first freed after
// earlier waiters (SPEC §6.5, §6.6). An unusable slot is skipped and the lease keeps its place in the
// queue; it fails when no slot is usable. A failed lease leaves no lease and no ticket.
func (p *SlotPool) Lease(ctx context.Context, h SlotHolder, base string) (*Slot, error) {
	if checkCommit(base) != nil {
		return nil, errors.New("slot lease: the base is no commit id")
	}
	lease, err := p.leaseBytes(h.Session, h.Process)
	if err != nil {
		return nil, err
	}
	ticket, err := createTicket(ctx, p.spec.LeaseDir, lease)
	if err != nil {
		return nil, err
	}
	s, err := p.queued(ctx, ticket, lease, base)
	if rerr := removeSlotFile(p.spec.LeaseDir, ticket); rerr != nil {
		err = errors.Join(err, fmt.Errorf("slot lease: drop the queue ticket: %w", rerr))
		if s != nil {
			return nil, errors.Join(err, p.drop(s))
		}
	}
	return s, err
}

// queued serves the ticket a slot and prepares it, skipping each slot it cannot use; the ticket stays,
// so a skip keeps the lease's place in the queue.
func (p *SlotPool) queued(ctx context.Context, ticket string, lease []byte, base string) (*Slot, error) {
	skip := map[int]bool{}
	var unusable error
	for {
		s, err := p.serve(ctx, ticket, lease, skip)
		if err != nil {
			return nil, errors.Join(err, unusable)
		}
		err = p.prepare(ctx, s, base)
		if err == nil {
			return s, nil
		}
		if derr := p.drop(s); derr != nil || !errors.Is(err, errSlotUnusable) {
			return nil, errors.Join(err, derr)
		}
		skip[s.K] = true
		unusable = errors.Join(unusable, err)
	}
}

// leaseBytes returns the lease file of a holder with a plain session, a pid and a start time;
// its start identity is stored too, and is empty where the OS gives none.
func (p *SlotPool) leaseBytes(session string, proc core.ProcessID) ([]byte, error) {
	if !plainName(session) || proc.PID <= 0 || proc.Start.IsZero() {
		return nil, errors.New("slot lease: the holder needs a plain session, a pid and a start time")
	}
	b, err := json.Marshal(slotLease{Session: session, PID: proc.PID, Start: proc.Start.UTC(), StartID: proc.StartID(), At: p.spec.Now().UTC()})
	return b, wrapErr("slot lease", err)
}

// serve waits until the ticket is first and a slot is free, printing each new queue position.
func (p *SlotPool) serve(ctx context.Context, ticket string, lease []byte, skip map[int]bool) (*Slot, error) {
	told := 0
	for {
		s, pos, err := p.try(ctx, ticket, lease, skip)
		if err != nil || s != nil {
			return s, err
		}
		if pos != told {
			if _, err := fmt.Fprintf(p.spec.Queue, "waiting for one of %d slots: queue position %d\n", p.spec.Size, pos); err != nil {
				return nil, fmt.Errorf("slot lease: queue output: %w", err)
			}
			told = pos
		}
		if err := pauseSlot(ctx, p.spec.Poll, pos); err != nil {
			return nil, err
		}
	}
}

// try reaps dead holders, then, with the ticket first, leases the first free slot not in skip,
// recorded ones first; it fails when skip holds every slot.
func (p *SlotPool) try(ctx context.Context, ticket string, lease []byte, skip map[int]bool) (*Slot, int, error) {
	if _, err := p.Sweep(ctx); err != nil {
		return nil, 0, err
	}
	names, err := listSlotDir(p.spec.LeaseDir)
	if err != nil {
		return nil, 0, fmt.Errorf("slot lease: %w", err)
	}
	pos, err := queuePosition(p.spec.LeaseDir, names, ticket)
	if err != nil || pos > 1 {
		return nil, pos, err
	}
	order := slotOrder(names, p.spec.Size, skip)
	if len(order) == 0 {
		return nil, pos, errors.New("slot lease: no slot of the pool is usable")
	}
	for _, k := range order {
		taken, err := p.take(k, lease)
		if err != nil {
			return nil, pos, err
		}
		if taken {
			return &Slot{K: k, lease: lease}, pos, nil
		}
	}
	return nil, pos, nil
}

// prepare resets the recorded slot s in place at base, or clones it fresh when it is unrecorded, or
// dirty and this pool's own; a recorded slot's commits are collected first (reuse). A slot it cannot
// use, one it could not collect among them, errs with errSlotUnusable and is kept (DR-11).
func (p *SlotPool) prepare(ctx context.Context, s *Slot, base string) error {
	rec, found, err := readSlotRecord(p.spec.LeaseDir, s.K)
	if err == nil && !found {
		return p.fresh(ctx, s, base)
	}
	dirty := ""
	if err == nil {
		dirty, err = p.reuse(ctx, s, rec, base)
	}
	if err == nil && dirty != "" {
		if err = p.adopt(ctx, s.K, rec.Name); err == nil {
			err = p.removeSlot(ctx, s.K)
		}
		if err == nil {
			return p.fresh(ctx, s, base)
		}
		err = fmt.Errorf("dirty (%s): %w", dirty, err)
	}
	if err != nil {
		return fmt.Errorf("%w: slot %d: %w", errSlotUnusable, s.K, err)
	}
	return nil
}

// fresh clones slot s at base into a new owned cache directory; not clean afterwards is an error.
func (p *SlotPool) fresh(ctx context.Context, s *Slot, base string) error {
	dir, err := p.spec.Owner.CreateOwned(ctx, core.CacheRoot, slotOwnerPrefix+strconv.Itoa(s.K))
	if err != nil {
		return fmt.Errorf("slot %d: %w", s.K, err)
	}
	p.mu.Lock()
	p.owned[s.K] = dir
	p.mu.Unlock()
	s.Root, s.Dir = dir.Path(), filepath.Join(dir.Path(), cloneDirName)
	if err := cloneAt(ctx, s.Root, p.spec.Canonical, s.Dir, base); err != nil {
		return errors.Join(fmt.Errorf("slot %d: %w", s.K, gitStep{"clone at the base", err}), p.removeSlot(ctx, s.K))
	}
	rec := slotRecord{Name: filepath.Base(s.Root)}
	if err := writeSlotRecord(p.spec.LeaseDir, s.K, rec); err != nil {
		return err
	}
	dirty, err := p.settle(ctx, s, rec, base)
	if err == nil && dirty != "" {
		err = fmt.Errorf("slot %d is not clean after a fresh clone: %s", s.K, dirty)
	}
	return err
}

// settle resets (SPEC §6.5), so a reset that fails makes the slot dirty, clears the refs of earlier runs,
// refuses a KeepDirs manifest changed since the last restore, restores on a lock-digest change and refuses
// git control files the restore changed before any further git call (DR-10), verifies, and records the
// slot with its control digest; it returns why it is dirty, or "".
func (p *SlotPool) settle(ctx context.Context, s *Slot, rec slotRecord, base string) (string, error) {
	if err := resetSlot(ctx, s.Dir, p.source, base, p.spec.KeepDirs); err != nil {
		return resetDirty(ctx, s.Dir, err)
	}
	if err := clearRefs(ctx, s.Dir); err != nil {
		return "", err
	}
	if changed, err := p.keptChanged(s.Dir, rec.Keep); err != nil || changed {
		return "a kept directory changed since the last restore", err
	}
	ctl, err := controlDigest(s.Dir)
	if err != nil {
		return "", err
	}
	lock, err := p.restoreOnChange(ctx, s.Dir, rec.Lock)
	if err != nil {
		return "", err
	}
	if after, err := controlDigest(s.Dir); err != nil || after != ctl {
		return "the restore changed its git control files", err
	}
	next := slotRecord{Name: rec.Name, Lock: lock, Ctl: ctl}
	if next.Keep, err = keepManifest(s.Dir, p.spec.KeepDirs); err != nil {
		return "", err
	}
	if dirty, err := p.verify(ctx, s.Dir, base); err != nil || dirty != "" {
		return dirty, err
	}
	return "", writeSlotRecord(p.spec.LeaseDir, s.K, next)
}

// keptChanged reports whether the KeepDirs manifest of dir differs from recorded, once one is.
func (p *SlotPool) keptChanged(dir, recorded string) (bool, error) {
	if recorded == "" {
		return false, nil
	}
	keep, err := keepManifest(dir, p.spec.KeepDirs)
	return err == nil && keep != recorded, err
}

// restoreOnChange runs the restore when the lock digest differs from recorded, and returns the digest.
func (p *SlotPool) restoreOnChange(ctx context.Context, dir, recorded string) (string, error) {
	lock, err := digestFiles(dir, p.spec.LockFiles, maxLockBytes)
	if err != nil || lock == recorded || len(p.spec.Restore) == 0 {
		return lock, wrapErr("lock digest", err)
	}
	return lock, runRestore(ctx, dir, p.spec.Restore, parentEnv())
}

// verify returns why dir is not clean (SPEC §6.5): HEAD off base, a non-KeepDirs status line, a worktree.
func (p *SlotPool) verify(ctx context.Context, dir, base string) (string, error) {
	head, err := slotGit(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	want, werr := slotGit(ctx, dir, "rev-parse", "--verify", base+"^{commit}")
	if err = errors.Join(err, werr); err != nil || head != want {
		return "HEAD is not at the base", err
	}
	status, err := slotGit(ctx, dir, "status", "--porcelain", "--ignored", "--untracked-files=normal")
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(status) {
		if !slices.ContainsFunc(p.spec.KeepDirs, func(k string) bool { return line == "!! "+k+"/\n" || line == "?? "+k+"/\n" }) {
			return "the status line " + strconv.Quote(line) + " is outside KeepDirs", nil
		}
	}
	trees, err := slotGit(ctx, dir, "worktree", "list", "--porcelain")
	if n := strings.Count("\n"+trees, "\nworktree "); err == nil && n != 1 {
		return strconv.Itoa(n) + " worktrees, want the slot alone", nil
	}
	return "", err
}

// Release drops the lease of s; the slot stays and the next lease resets it (SPEC §6.5).
func (p *SlotPool) Release(_ context.Context, s *Slot) error {
	if s == nil {
		return errors.New("slot release: no slot")
	}
	return p.drop(s)
}

// Shrink is the idle-slot step of SPEC §6.4: while the cache is over its ceiling it removes idle slots
// this pool created or takes over from a gone creator, only via core.Owner.RemoveOwned. Idle time alone
// removes none (§6.5, §15).
func (p *SlotPool) Shrink(ctx context.Context, c core.FootprintCeilings) (int, error) {
	ks, err := p.shrinkOrder()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, k := range ks {
		f, err := p.spec.Owner.Footprint(ctx)
		if err != nil || (f.CacheBytes <= c.CacheBytes && f.CacheFiles <= c.CacheFiles) {
			return removed, wrapErr("slot shrink", err)
		}
		gone, err := p.removeIdle(ctx, k)
		if err != nil {
			return removed, err
		}
		if gone {
			removed++
		}
	}
	return removed, nil
}
