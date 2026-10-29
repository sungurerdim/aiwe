package gates

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaneLockFilesPersist pins B2 of audit-596-r2: a lane deletes no lock file, so no open of one
// meets a delete in progress and no wait polls for it. After a holder and a waiter ended, their
// wait and alive files are still there; only the tickets are gone.
func TestLaneLockFilesPersist(t *testing.T) {
	t.Log("catches: a lane removes .wait or .alive files, so a waiter's open can meet a pending delete and a poll is needed")
	root := t.TempDir()
	holder := mustAcquire(t, newTestLanes(t, root, 4), dbRequest("holder", 1))
	lines := newLaneLines()
	w := newTestLanes(t, root, 4)
	w.Log = lines
	done := goAcquire(t.Context(), w, dbRequest("waiter", 1))
	lines.next(t, "position 1")
	mustRelease(t, holder)
	mustRelease(t, leaseOf(t, awaitResult(t, done, laneTestWait)))
	if got := laneTicketFiles(t, dbLane(root)); len(got) != 0 {
		t.Errorf("tickets %q are left after both ended, want none", got)
	}
	for seq := uint64(1); seq <= 2; seq++ {
		for _, ext := range []string{laneWaitExt, laneAliveExt} {
			if _, err := os.Lstat(filepath.Join(dbLane(root), laneSeqName(seq, ext))); err != nil {
				t.Errorf("lock file %s of ticket %d = %v, want it kept", ext, seq, err)
			}
		}
	}
}

// TestLaneLogsAnOwnerlessEmptyTicket pins the low finding of audit-596-r2: an empty ticket of no
// owner, which takes a lease once its alive lock is free, is logged with its path in one line and
// removed, so the lane can go idle (audit-596-r3).
func TestLaneLogsAnOwnerlessEmptyTicket(t *testing.T) {
	t.Log("catches: a lease passes an empty ticket of no owner and says nothing, so the residue stays unseen")
	root := t.TempDir()
	ticket := filepath.Join(dbLane(root), laneSeqName(1, laneTicketExt))
	if err := errors.Join(os.MkdirAll(dbLane(root), 0o700), os.WriteFile(ticket, nil, 0o600)); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	l := newTestLanes(t, root, 4)
	l.Log = &log
	mustRelease(t, mustAcquire(t, l, dbRequest("alpha", 1)))
	n := 0
	for line := range strings.Lines(log.String()) {
		if strings.Contains(line, ticket) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the log holds %d lines with the ticket path %s, want one; log = %q", n, ticket, log.String())
	}
	if _, err := os.Lstat(ticket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the ownerless ticket is still there (Lstat = %v), want it removed so the lane can go idle", err)
	}
}
