package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The trust rules of the slot pool (SPEC §6.5, DR-10, DR-11): the names a record and a lease directory
// may hold, the place a record leads to, who may take over a slot, and the refusals that repeat no value.

// The names of the slot pool (SPEC §6.5): the files of the lease directory and the slot directories
// of the cache root. slot.go decides with them; slot_io.go reads and writes the files.
const (
	slotFilePrefix  = "s"
	leaseSuffix     = ".lease"
	recordSuffix    = ".slot"
	ticketPrefix    = "w"
	ticketSuffix    = ".wait"
	slotOwnerPrefix = "slot-"
	slotNameHex     = 16 // the hex digits core.Owner.CreateOwned appends to an owned directory's name
)

// slotUploadPack is the upload-pack of a collect (collectSlot), no program a slot configures (DR-10).
const slotUploadPack = "--upload-pack=git -c transfer.hideRefs=!refs -c transfer.hideRefs=!HEAD upload-pack"

// errSlotFile marks a lease directory file that does not parse; its content is never repeated.
var errSlotFile = errors.New("the file does not parse")

// checkPoolSpec refuses no owner, no slot, no restore program, a canonical repository that is no
// absolute path, or a KeepDirs or lock path outside the work tree; no refusal repeats the value.
func checkPoolSpec(spec SlotPoolSpec) error {
	switch {
	case spec.Owner == nil:
		return errors.New("slot pool: no owner")
	case spec.Size < 1:
		return errors.New("slot pool: the size is below 1")
	case len(spec.Restore) > 0 && spec.Restore[0] == "":
		return errors.New("slot pool: the restore command names no program")
	case !filepath.IsAbs(spec.Canonical):
		return errors.New("slot pool: the canonical repository is no absolute path")
	}
	for _, p := range slices.Concat(spec.KeepDirs, spec.LockFiles) {
		parts := strings.Split(p, "/")
		if strings.ContainsAny(p, `\:`) || strings.EqualFold(parts[0], ".git") ||
			slices.ContainsFunc(parts, func(e string) bool { return e == "" || e == "." || e == ".." }) {
			return errors.New("slot pool: a KeepDirs or lock path is no clean relative path in the work tree outside .git")
		}
	}
	return nil
}

// leaseName and recordName are the lease file and the record of slot k in the lease directory.
func leaseName(k int) string  { return slotFilePrefix + strconv.Itoa(k) + leaseSuffix }
func recordName(k int) string { return slotFilePrefix + strconv.Itoa(k) + recordSuffix }

// slotDirName reports whether name is the owned directory core.Owner.CreateOwned names for slot k:
// slot-<k>- and 16 lowercase hex digits, a single name that cannot leave the cache root.
func slotDirName(name string, k int) bool {
	suffix, ok := strings.CutPrefix(name, slotOwnerPrefix+strconv.Itoa(k)+"-")
	return ok && len(suffix) == slotNameHex && strings.Trim(suffix, "0123456789abcdef") == ""
}

// slotIndex returns n of a name <prefix><n><suffix>, n a canonical number of at least 1.
func slotIndex(name, prefix, suffix string) (int, bool) {
	mid, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return 0, false
	}
	if mid, ok = strings.CutSuffix(mid, suffix); !ok {
		return 0, false
	}
	n, err := strconv.Atoi(mid)
	if err != nil || n < 1 || strconv.Itoa(n) != mid {
		return 0, false
	}
	return n, true
}

// maxSlotIndex returns the highest n among the names <prefix><n><suffix>, or 0.
func maxSlotIndex(names []string, prefix, suffix string) int {
	high := 0
	for _, name := range names {
		if n, ok := slotIndex(name, prefix, suffix); ok && n > high {
			high = n
		}
	}
	return high
}

// slotOrder returns the slots 1..size a lease tries, recorded ones first, reused before a new one;
// a slot in skip, one this lease found unusable, is left out.
func slotOrder(names []string, size int, skip map[int]bool) []int {
	order := make([]int, 0, size)
	var fresh []int
	for k := 1; k <= size; k++ {
		switch {
		case skip[k]:
		case slices.Contains(names, recordName(k)):
			order = append(order, k)
		default:
			fresh = append(fresh, k)
		}
	}
	return append(order, fresh...)
}

// pauseSlot waits d, or returns the end of ctx with the queue position the lease waited at.
func pauseSlot(ctx context.Context, d time.Duration, pos int) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("slot lease: waited at queue position %d: %w", pos, ctx.Err())
	case <-t.C:
		return nil
	}
}

// reuse places s by its record, a slot directory of the cache root and never another place, collects
// its commits (collectSlot), and settles it unless its git control files changed since its last settle:
// no git call runs in a repository whose configuration this pool did not record (DR-10). It returns why
// s is dirty; a slot it could not collect is an error, so it is kept.
func (p *SlotPool) reuse(ctx context.Context, s *Slot, rec slotRecord, base string) (string, error) {
	if !slotDirName(rec.Name, s.K) {
		return "", errors.New("its record names no slot directory")
	}
	s.Root = filepath.Join(p.spec.Owner.CacheRoot(), rec.Name)
	s.Dir = filepath.Join(s.Root, cloneDirName)
	if err := checkSlotTree(s.Root, s.Dir); err != nil {
		return "", err
	}
	if err := p.collectSlot(ctx, s, base); err != nil {
		return "", err
	}
	ctl, err := controlDigest(s.Dir)
	if err != nil || rec.Ctl == "" || ctl != rec.Ctl {
		return "its git control files changed since its last settle", err
	}
	return p.settle(ctx, s, rec, base)
}

// adopt makes slot k, the owned directory name in the cache root, this pool's own when it already is or
// when the process that created it is gone (core.Owner.OpenOwned). An entry for another directory, left
// when another pool took the slot over, is dropped. Else the slot stays its creator's (core.ErrNotOwned).
func (p *SlotPool) adopt(ctx context.Context, k int, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d, ok := p.owned[k]; ok && (name == "" || filepath.Base(d.Path()) == name) {
		return nil
	}
	delete(p.owned, k)
	if !slotDirName(name, k) {
		return fmt.Errorf("slot %d: %w: its record names no slot directory", k, core.ErrNotOwned)
	}
	dir, err := p.spec.Owner.OpenOwned(ctx, core.CacheRoot, name)
	if err != nil {
		return fmt.Errorf("slot %d is its creator's to remove, a process that still runs (DR-11): %w", k, err)
	}
	p.owned[k] = dir
	return nil
}

// removeTaken removes slot k, whose lease the caller holds, when this pool owns it or takes it over by its
// record (adopt); a slot of a creator that still runs stays, with no error. A record that does not parse
// names no directory, so only this pool's own slot goes; a record that cannot be read is an error.
func (p *SlotPool) removeTaken(ctx context.Context, k int) (bool, error) {
	rec, _, rerr := readSlotRecord(p.spec.LeaseDir, k)
	if rerr != nil && !errors.Is(rerr, errSlotFile) {
		return false, rerr
	}
	if rerr != nil {
		rec = slotRecord{}
	}
	err := p.adopt(ctx, k, rec.Name)
	if errors.Is(err, core.ErrNotOwned) {
		return false, nil
	}
	if err == nil {
		err = p.collectIdle(ctx, k)
	}
	if err != nil {
		return false, err
	}
	return true, p.removeSlot(ctx, k)
}

// collectIdle collects the commits of slot k, which this pool owns, before its removal (DR-11): a slot
// whose clone is gone holds none, and one whose tree is no real directory or that cannot be collected
// is an error, so it is kept and reported.
func (p *SlotPool) collectIdle(ctx context.Context, k int) error {
	p.mu.Lock()
	dir, ok := p.owned[k]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("slot %d was created by another process, which alone can remove it (DR-11)", k)
	}
	s := &Slot{K: k, Root: dir.Path(), Dir: filepath.Join(dir.Path(), cloneDirName)}
	if cloneGone(s.Dir) {
		return nil
	}
	if err := checkSlotTree(s.Root, s.Dir); err != nil {
		return fmt.Errorf("slot %d: %w", k, err)
	}
	return p.collectSlot(ctx, s, "")
}

// shrinkOrder returns the slots a shrink tries: this pool's own first, then each other recorded slot 1..N,
// which it takes over only under the slot's lease and when its creator is gone (removeTaken).
func (p *SlotPool) shrinkOrder() ([]int, error) {
	names, err := listSlotDir(p.spec.LeaseDir)
	if err != nil {
		return nil, fmt.Errorf("slot shrink: %w", err)
	}
	p.mu.Lock()
	ks := slices.Sorted(maps.Keys(p.owned))
	p.mu.Unlock()
	for k := 1; k <= p.spec.Size; k++ {
		if !slices.Contains(ks, k) && slices.Contains(names, recordName(k)) {
			ks = append(ks, k)
		}
	}
	return ks, nil
}

// gitStep is a failed git step of a slot. Its text names the step alone: git repeats the paths and commit
// ids it was given, which a refusal never does (P5). Unwrap keeps git's error for errors.Is and errors.As.
type gitStep struct {
	step string
	err  error
}

func (e gitStep) Error() string { return e.step + " failed" }
func (e gitStep) Unwrap() error { return e.err }

// Sweep reaps dead holders' leases and tickets and returns the leases reaped (SPEC §6.5 Sweeper).
func (p *SlotPool) Sweep(ctx context.Context) (int, error) {
	names, err := listSlotDir(p.spec.LeaseDir)
	if err != nil {
		return 0, fmt.Errorf("slot sweep: %w", err)
	}
	reaped := 0
	var errs []error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return reaped, err
		}
		_, isLease := slotIndex(name, slotFilePrefix, leaseSuffix)
		if _, isTicket := slotIndex(name, ticketPrefix, ticketSuffix); !isLease && !isTicket {
			continue
		}
		gone, err := p.reapDead(name)
		errs = append(errs, err)
		if gone && isLease {
			reaped++
		}
	}
	return reaped, errors.Join(errs...)
}

// reapDead removes name when its holder is dead; an unreadable file is kept with its error. One that
// does not parse or names no process is kept while it may be mid-write, and removed once it is stale:
// a crash between its create and its write would else lock its slot forever (DR-11).
func (p *SlotPool) reapDead(name string) (bool, error) {
	l, b, held, err := readLease(p.spec.LeaseDir, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("slot sweep: %s: %w", name, err)
	}
	if held && p.spec.Alive(core.ProcessFrom(l.PID, l.Start, l.StartID)) {
		return false, nil
	}
	if !held {
		if stale, err := staleSlotFile(p.spec.LeaseDir, name); err != nil || !stale {
			return false, wrapErr("slot sweep: "+name, err)
		}
	}
	gone, err := removeClaimFileIf(p.spec.LeaseDir, name, b)
	return gone, wrapErr("slot sweep: "+name, err)
}

// collectSlot fetches into the canonical repository, as the run ref slot-<k>-<id> (RunRef), each tip of
// slot s that neither base, when given, nor a ref of the canonical repository reaches, before the slot is
// reset, cloned again or removed (DR-11, SPEC §6.5 Collect). Every call runs in the canonical repository and reads the slot
// over the file protocol, as Collect reads a run clone: the slot only serves its refs and objects, and no
// command that checks out, filters or reads its work tree runs before its control files are checked
// (DR-10). An error keeps the slot: its caller marks it unusable and removes nothing.
//
// The upload-pack git starts in the slot reads the slot's hideRefs, which could hide a tip from the
// collect, so it runs as slotUploadPack: git's own upload-pack, its command-line config read last and
// the last matching hideRefs entry deciding, exposes every ref and HEAD (S2-a of the 2026-10-26 review).
func (p *SlotPool) collectSlot(ctx context.Context, s *Slot, base string) error {
	if base != "" {
		if _, err := core.Git(ctx, p.spec.Canonical, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
			return fmt.Errorf("collect: the base is no commit of the canonical repository: %w", gitStep{"git rev-parse", err})
		}
	}
	source, err := fileURL(s.Dir)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	res, err := core.Git(ctx, p.spec.Canonical, "ls-remote", slotUploadPack, source)
	if err != nil {
		return fmt.Errorf("collect: the refs of slot %d: %w", s.K, gitStep{"git ls-remote", err})
	}
	tips, err := slotTips(res.Stdout)
	if err != nil {
		return fmt.Errorf("collect: the refs of slot %d: %w", s.K, err)
	}
	for _, tip := range tips {
		if p.reached(ctx, tip, base) {
			continue
		}
		refspec := "+" + tip + ":" + RunRef(slotOwnerPrefix+strconv.Itoa(s.K)+"-"+tip)
		if _, err := core.Git(ctx, p.spec.Canonical, "fetch", "--no-tags", "--no-write-fetch-head",
			slotUploadPack, source, refspec); err != nil {
			return fmt.Errorf("collect: a commit of slot %d: %w", s.K, gitStep{"git fetch", err})
		}
	}
	return nil
}

// slotTips returns the distinct object ids of the refs ls-remote listed, HEAD included and the peeled
// lines of tags left out. A line that holds no hex object id is an error, so no listed value becomes an
// argument of another git call.
func slotTips(out string) ([]string, error) {
	var tips []string
	for line := range strings.Lines(out) {
		id, ref, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
		if !ok || (len(id) != 40 && len(id) != 64) || strings.Trim(id, "0123456789abcdef") != "" {
			return nil, errors.New("a ref line holds no object id")
		}
		if !strings.HasSuffix(ref, "^{}") && !slices.Contains(tips, id) {
			tips = append(tips, id)
		}
	}
	return tips, nil
}

// reached reports whether base, when given, or a ref of the canonical repository reaches tip. A tip the
// canonical repository does not hold fails rev-list, so it is not reached and is collected.
func (p *SlotPool) reached(ctx context.Context, tip, base string) bool {
	args := []string{"rev-list", "-n", "1", tip, "--not", "--all"}
	if base != "" {
		args = append(args, base)
	}
	res, err := core.Git(ctx, p.spec.Canonical, args...)
	return err == nil && strings.TrimSpace(res.Stdout) == ""
}

// clearRefs deletes every ref of the slot work tree dir, its detached HEAD aside, and expires its reflogs,
// once the reset has checked out the base: no branch, tag or reflog entry of an earlier run, collected
// before the reset, is left for the next holder (DR-11). A symbolic ref is deleted itself, not its target.
func clearRefs(ctx context.Context, dir string) error {
	refs, err := slotGit(ctx, dir, "for-each-ref", "--format=delete %(refname)")
	if err != nil {
		return err
	}
	if refs != "" {
		if _, err := core.GitInput(ctx, dir, refs, "update-ref", "--no-deref", "--stdin"); err != nil {
			return fmt.Errorf("slot %s: %w", dir, gitStep{"git update-ref", err})
		}
	}
	_, err = slotGit(ctx, dir, "reflog", "expire", "--expire=all", "--all")
	return err
}

// resetDirty returns the reset error err once ctx has ended; else it names why the reset of the work
// tree dir failed, which makes the slot dirty: the git step, and a .git/index.lock that a stopped git
// call left and that fails every later reset (B3 of #470).
func resetDirty(ctx context.Context, dir string, err error) (string, error) {
	if ctx.Err() != nil {
		return "", err
	}
	why := "its reset failed"
	var step gitStep
	if errors.As(err, &step) {
		why += " at " + step.step
	}
	if staleIndexLock(dir) {
		why += "; a stale .git/index.lock is present"
	}
	return why, nil
}
