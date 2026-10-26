package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The IO of the slot pool (SPEC §5.6): lease directory files, slot tree checks and digests, every
// git call on a slot via core.Git (DR-10), the restore via core.Start. slot.go and clone.go decide.

const (
	// maxSlotFileBytes caps a lease file, a queue ticket, a slot record and a git control file.
	maxSlotFileBytes = 64 << 10
	// maxLockBytes caps one lock file read for its digest.
	maxLockBytes   = 64 << 20
	maxRestoreTail = 2048 // bytes of restore output an error carries
	// slotWriteGrace is how long a lease file or ticket may stay unparsable: its writer is still writing.
	slotWriteGrace = time.Minute
)

// slotLease is a lease file or a queue ticket: the holder and the time.
type slotLease struct {
	Session string    `json:"session"`
	PID     int       `json:"pid"`
	Start   time.Time `json:"start"`
	StartID string    `json:"start_id"`
	At      time.Time `json:"at"`
}

// slotRecord is what a slot keeps between leases: the name of its owned directory in the cache
// root, and the lock digest, KeepDirs manifest and git control digest of its last clean settle.
type slotRecord struct {
	Name string `json:"name"`
	Lock string `json:"lock,omitempty"`
	Keep string `json:"keep,omitempty"`
	Ctl  string `json:"ctl,omitempty"`
}

// take creates the lease file of slot k exclusively; an existing one means the slot is held.
func (p *SlotPool) take(k int, lease []byte) (bool, error) {
	err := createClaimFile(p.spec.LeaseDir, leaseName(k), lease)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	return err == nil, wrapErr("slot lease: slot "+strconv.Itoa(k), err)
}

// drop removes the lease file of s while it still holds this lease.
func (p *SlotPool) drop(s *Slot) error {
	held, err := removeClaimFileIf(p.spec.LeaseDir, leaseName(s.K), s.lease)
	if err == nil && !held {
		err = errors.New("its lease file no longer holds this lease")
	}
	return wrapErr("release slot "+strconv.Itoa(s.K), err)
}

// removeSlot removes slot k via core.Owner.RemoveOwned, then its record; only its creator can (DR-11).
func (p *SlotPool) removeSlot(ctx context.Context, k int) error {
	p.mu.Lock()
	dir, ok := p.owned[k]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("slot %d was created by another process, which alone can remove it (DR-11)", k)
	}
	if err := p.spec.Owner.RemoveOwned(ctx, dir); err != nil {
		return fmt.Errorf("remove slot %d at %s: %w", k, dir.Path(), err)
	}
	p.mu.Lock()
	delete(p.owned, k)
	p.mu.Unlock()
	return removeSlotFile(p.spec.LeaseDir, recordName(k))
}

// removeIdle leases idle slot k for its removal, removes it and drops that lease; a held slot stays,
// and so does one this pool may not take over (removeTaken).
func (p *SlotPool) removeIdle(ctx context.Context, k int) (bool, error) {
	b, err := p.leaseBytes(shrinkSession, p.self)
	if err != nil {
		return false, err
	}
	taken, err := p.take(k, b)
	if err != nil || !taken {
		return false, err
	}
	gone, rerr := p.removeTaken(ctx, k)
	_, derr := removeClaimFileIf(p.spec.LeaseDir, leaseName(k), b)
	if err := errors.Join(rerr, derr); err != nil {
		return false, fmt.Errorf("slot shrink: %w", err)
	}
	return gone, nil
}

// queuePosition returns 1 plus the number of waiting tickets queued before ticket in dir. A ticket that
// does not parse, such as one its writer left empty, is no waiter, nor is one whose holder holds a lease
// file: it is preparing a slot, and keeps its place should that slot be unusable.
func queuePosition(dir string, names []string, ticket string) (int, error) {
	leased, err := heldLeases(dir, names)
	if err != nil {
		return 0, err
	}
	mine, _ := slotIndex(ticket, ticketPrefix, ticketSuffix)
	pos := 1
	for _, name := range names {
		if n, ok := slotIndex(name, ticketPrefix, ticketSuffix); !ok || n >= mine {
			continue
		}
		_, b, held, err := readLease(dir, name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("slot lease: queue ticket: %w", err)
		}
		if held && !leased[string(b)] {
			pos++
		}
	}
	return pos, nil
}

// heldLeases returns the leases the lease files among names in dir hold.
func heldLeases(dir string, names []string) (map[string]bool, error) {
	held := map[string]bool{}
	for _, name := range names {
		if _, ok := slotIndex(name, slotFilePrefix, leaseSuffix); !ok {
			continue
		}
		b, err := readClaimFile(dir, name, maxSlotFileBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("slot lease: lease file: %w", err)
		}
		held[string(b)] = true
	}
	return held, nil
}

// staleSlotFile reports whether name in dir was last written longer than slotWriteGrace ago.
func staleSlotFile(dir, name string) (bool, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	fi, err := root.Lstat(name)
	if err != nil {
		return false, err
	}
	return time.Since(fi.ModTime()) > slotWriteGrace, nil
}

// createTicket creates the next queue ticket of the lease directory dir exclusively, holding lease;
// a number another waiter took first is retried with the next one.
func createTicket(ctx context.Context, dir string, lease []byte) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		names, err := listSlotDir(dir)
		if err != nil {
			return "", fmt.Errorf("slot lease: %w", err)
		}
		name := ticketPrefix + strconv.Itoa(maxSlotIndex(names, ticketPrefix, ticketSuffix)+1) + ticketSuffix
		if err := createClaimFile(dir, name, lease); !errors.Is(err, fs.ErrExist) {
			return name, wrapErr("slot lease: queue ticket", err)
		}
	}
}

// listSlotDir returns the names in the lease directory dir.
func listSlotDir(dir string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return names, err
}

// removeSlotFile removes name from the lease directory dir; a name already gone is no error.
func removeSlotFile(dir, name string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// readLease reads the lease file or ticket name of dir; held is false for one that does not parse
// or names no process, such as a file its writer left empty.
func readLease(dir, name string) (l slotLease, b []byte, held bool, err error) {
	b, err = readClaimFile(dir, name, maxSlotFileBytes)
	if err != nil {
		return l, nil, false, err
	}
	return l, b, json.Unmarshal(b, &l) == nil && l.PID > 0, nil
}

// readSlotRecord reads the record of slot k; a missing record is reported, not an error.
func readSlotRecord(dir string, k int) (slotRecord, bool, error) {
	var rec slotRecord
	b, err := readClaimFile(dir, recordName(k), maxSlotFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return rec, false, nil
	}
	if err == nil && json.Unmarshal(b, &rec) != nil {
		err = errSlotFile
	}
	return rec, err == nil, wrapErr("slot "+strconv.Itoa(k)+" record", err)
}

// writeSlotRecord replaces the record of slot k by a rename, so no reader sees half a record.
func writeSlotRecord(dir string, k int, rec slotRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("slot %d record: %w", k, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("slot %d record: %w", k, err)
	}
	defer func() { _ = root.Close() }()
	tmp := recordName(k) + ".tmp"
	if err := root.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("slot %d record: %w", k, err)
	}
	if err := root.Rename(tmp, recordName(k)); err != nil {
		return fmt.Errorf("slot %d record: %w", k, errors.Join(err, root.Remove(tmp)))
	}
	return nil
}

// checkSlotTree refuses a slot whose root, work tree or git directory is not a real directory: a
// link, a junction or a gitfile would lead a reset or a clean out of the slot (DR-11).
func checkSlotTree(root, dir string) error {
	for _, p := range []string{root, dir, filepath.Join(dir, ".git")} {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode().Type() != fs.ModeDir {
			return fmt.Errorf("%s is not a real directory", p)
		}
	}
	return nil
}

// cloneGone reports whether the work tree dir holds no .git: the clone of its slot is gone.
func cloneGone(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return errors.Is(err, fs.ErrNotExist)
}

// staleIndexLock reports whether the work tree dir holds a .git/index.lock, as a stopped git call leaves.
func staleIndexLock(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git", "index.lock"))
	return err == nil
}

// controlDigest returns the digest of the files of the git directory of dir that can name a program
// or move the repository: config holds filter.<driver>.clean/smudge/process, include.path and
// core.worktree (git-config(1) FILES, gitattributes(5) filter); config.worktree is read once
// extensions.worktreeConfig is set; info/attributes assigns filters; commondir moves the repository.
func controlDigest(dir string) (string, error) {
	ctl := []string{"config", "config.worktree", "info/attributes", "commondir"}
	d, err := digestFiles(filepath.Join(dir, ".git"), ctl, maxSlotFileBytes)
	return d, wrapErr("git control files", err)
}

// digestFiles returns the digest of the files names of dir, each read through an *os.Root as a regular
// file of at most limit bytes; a missing file counts as absent, so adding one changes the digest.
func digestFiles(dir string, names []string, limit int64) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	h := sha256.New()
	for _, name := range names {
		b, err := core.ReadRegularCapped(root, filepath.FromSlash(name), limit)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			_, err = fmt.Fprintf(h, "%q absent\n", name)
		case err == nil:
			_, err = fmt.Fprintf(h, "%q %d %x\n", name, len(b), sha256.Sum256(b))
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resetSlot runs the reset of SPEC §6.5 in the work tree dir: fetch base from the canonical
// repository at source, check it out detached, reset --hard, and clean -ffdx keeping only keep.
func resetSlot(ctx context.Context, dir, source, base string, keep []string) error {
	clean := []string{"clean", "-ffdx"}
	for _, k := range keep {
		clean = append(clean, "-e", "/"+k+"/")
	}
	for _, args := range [][]string{{"fetch", "--depth", "1", "--no-tags", source, base},
		{"checkout", "--detach", "--force", base}, {"reset", "--hard", base}, clean} {
		if _, err := slotGit(ctx, dir, args...); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
	}
	return nil
}

// slotGit runs git args in the work tree dir of a slot through core.Git (DR-10), returning stdout.
func slotGit(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := core.Git(ctx, dir, args...)
	if err != nil {
		return "", fmt.Errorf("slot %s: %w", dir, gitStep{"git " + args[0], err})
	}
	return res.Stdout, nil
}

// runRestore runs the restore command argv in the work tree dir with env, through core.Start.
func runRestore(ctx context.Context, dir string, argv, env []string) error {
	var out bytes.Buffer
	child, err := core.Start(ctx, core.ChildSpec{Argv: argv, Dir: dir, Env: env, Stdout: &out, Stderr: &out})
	if err != nil {
		return fmt.Errorf("restore in the slot %s: %w", dir, err)
	}
	if err := child.Wait(); err != nil {
		tail := out.Bytes()
		tail = tail[max(0, len(tail)-maxRestoreTail):]
		return fmt.Errorf("restore in the slot %s: %w: %s", dir, err, bytes.TrimSpace(tail))
	}
	return nil
}

// keepManifest returns the digest of the manifest of each KeepDirs directory of the work tree dir:
// every path under it with its size, modification time and mode. Links are listed, never followed.
func keepManifest(dir string, keep []string) (string, error) {
	h := sha256.New()
	for _, k := range keep {
		if err := writeManifest(h, dir, k); err != nil {
			return "", fmt.Errorf("manifest of %s: %w", k, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeManifest writes the manifest lines of the KeepDirs directory keep of dir to w. Each path is
// read by Lstat, since a Windows directory entry holds a lazily updated copy of the size and time;
// a directory lists only its path and mode, as its entries are listed themselves.
func writeManifest(w io.Writer, dir, keep string) error {
	top := filepath.Join(dir, filepath.FromSlash(keep))
	return filepath.WalkDir(top, func(p string, _ fs.DirEntry, err error) error {
		if p == top && errors.Is(err, fs.ErrNotExist) {
			_, err = fmt.Fprintf(w, "%q absent\n", keep)
			return err
		}
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		size, at := info.Size(), info.ModTime().UnixNano()
		if info.IsDir() {
			size, at = 0, 0
		}
		_, err = fmt.Fprintf(w, "%q %d %d %s\n", filepath.ToSlash(rel), size, at, info.Mode())
		return err
	})
}
