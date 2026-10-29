package gates

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The bounds of the lane tests. laneTestWait bounds every wait, far above one lane hand-over;
// laneTestHold is how long a holder keeps its lanes, so an overlap shows in the record times;
// laneTestBlocked is how long a request must stay blocked to count as waiting.
const (
	laneTestWait    = 20 * time.Second
	laneTestHold    = 40 * time.Millisecond
	laneTestBlocked = 300 * time.Millisecond
)

// laneTestT0 is the fixed time a test gives a job's queue entry and a dead owner's start.
func laneTestT0() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

// newTestLanes returns the lanes of one simulated process on root: pool measured tickets, no
// inherited lane token (the test may itself run inside a lane), and a cleanup that waits for every
// lock wait a cancel left behind before the temp dir goes.
func newTestLanes(t *testing.T, root string, pool int64) *Lanes {
	t.Helper()
	l := &Lanes{Root: root, Pool: core.Measured[int64]{Value: pool, Known: true}, MaxWait: laneTestWait,
		Lookup: func(string) string { return "" }}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), laneTestWait)
		defer cancel()
		if err := l.Drain(ctx); err != nil {
			t.Errorf("Drain = %v, want every abandoned lock wait ended", err)
		}
	})
	return l
}

// laneLines is a lane Log that hands every printed line to the test as it is written.
type laneLines chan string

func newLaneLines() laneLines { return make(laneLines, 64) }

func (c laneLines) Write(p []byte) (int, error) {
	c <- string(p)
	return len(p), nil
}

// next returns the first line still to come that holds want; it fails the test after laneTestWait.
func (c laneLines) next(t *testing.T, want string) string {
	t.Helper()
	timer := time.NewTimer(laneTestWait)
	defer timer.Stop()
	for {
		select {
		case line := <-c:
			if strings.Contains(line, want) {
				return line
			}
		case <-timer.C:
			t.Fatalf("no lane line holds %q within %v", want, laneTestWait)
			return ""
		}
	}
}

// laneResult is what a request started in its own goroutine returned.
type laneResult struct {
	lease *LaneLease
	err   error
}

// goAcquire starts req on l in its own goroutine and returns the channel its result arrives on.
func goAcquire(ctx context.Context, l *Lanes, req LaneRequest) <-chan laneResult {
	done := make(chan laneResult, 1)
	go func() {
		lease, err := l.Acquire(ctx, req)
		done <- laneResult{lease, err}
	}()
	return done
}

// goHold acquires req on l in its own goroutine, holds it for laneTestHold and releases it; the
// released lease arrives with its records, which then carry their end.
func goHold(ctx context.Context, l *Lanes, req LaneRequest) <-chan laneResult {
	done := make(chan laneResult, 1)
	go func() {
		lease, err := l.Acquire(ctx, req)
		if err == nil {
			time.Sleep(laneTestHold)
			err = lease.Release()
		}
		done <- laneResult{lease, err}
	}()
	return done
}

// awaitResult returns the result on done, or fails the test when none arrives within bound.
func awaitResult(t *testing.T, done <-chan laneResult, bound time.Duration) laneResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(bound):
		t.Fatalf("the request returned nothing within %v", bound)
		return laneResult{}
	}
}

// leaseOf returns the lease of r, or fails the test with the error the request returned.
func leaseOf(t *testing.T, r laneResult) *LaneLease {
	t.Helper()
	if r.err != nil {
		t.Fatalf("Acquire = %v, want a lease", r.err)
	}
	return r.lease
}

func mustAcquire(t *testing.T, l *Lanes, req LaneRequest) *LaneLease {
	t.Helper()
	lease, err := l.Acquire(t.Context(), req)
	if err != nil {
		t.Fatalf("Acquire(%+v) = %v, want a lease", req, err)
	}
	return lease
}

func mustRelease(t *testing.T, lease *LaneLease) {
	t.Helper()
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() = %v, want nil", err)
	}
}

// laneRecordOf returns the record of the lane label in lease.
func laneRecordOf(t *testing.T, lease *LaneLease, label string) LaneRecord {
	t.Helper()
	for _, r := range lease.Records() {
		if r.Lane == label {
			return r
		}
	}
	t.Fatalf("Records() = %+v, want one of lane %q", lease.Records(), label)
	return LaneRecord{}
}

func dbLane(root string) string { return filepath.Join(root, laneExclusiveDir, "db") }

func dbRequest(repo string, workers int) LaneRequest {
	return LaneRequest{Repo: repo, Kind: "unit", Exclusive: []string{"db"}, Workers: workers, Slot: 1}
}

func cpuRequest(repo string, workers int) LaneRequest {
	return LaneRequest{Repo: repo, Kind: "unit", Workers: workers, Slot: 1}
}

// TestExclusiveResourceRequestsSerialise pins L63 (2): requests that name one exclusive resource
// hold it one at a time, as their record times show, whether they come from separate processes
// (one Lanes each) or from one process (one shared Lanes).
func TestExclusiveResourceRequestsSerialise(t *testing.T) {
	for _, tc := range []struct {
		name    string
		shared  bool
		catches string
	}{
		{"three processes", false, "two processes hold one exclusive resource at once: the OS lock or the ticket order lets a second one in"},
		{"three requests of one process", true, "two requests of one process hold one exclusive resource at once: the in-memory queue lets one past the holder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			shared := newTestLanes(t, root, 8)
			var dones []<-chan laneResult
			for i := range 3 {
				l := shared
				if !tc.shared {
					l = newTestLanes(t, root, 8)
				}
				dones = append(dones, goHold(t.Context(), l, dbRequest(fmt.Sprintf("repo-%d", i), 1)))
			}
			var recs []LaneRecord
			for _, d := range dones {
				r := awaitResult(t, d, laneTestWait)
				if r.err != nil {
					t.Fatalf("hold the db lane = %v, want a lease and a release", r.err)
				}
				recs = append(recs, laneRecordOf(t, r.lease, laneResourceLabel("db")))
			}
			slices.SortFunc(recs, func(a, b LaneRecord) int { return a.Started.Compare(b.Started) })
			for i := 1; i < len(recs); i++ {
				if recs[i].Started.Before(recs[i-1].Ended) {
					t.Errorf("record %+v started before record %+v ended: the holds overlap", recs[i], recs[i-1])
				}
			}
		})
	}
}

// TestSeparableChecksRunInParallelWithTheirSlot pins L63 (1): checks with no shared exclusive
// resource hold their lanes at once, and each runs with the slot its caller leased, in its env.
func TestSeparableChecksRunInParallelWithTheirSlot(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second []string
		catches       string
	}{
		{"no exclusive resource", nil, nil, "a check with no exclusive resource waits for another one: one lane serves every check"},
		{"two different resources", []string{"db-a"}, []string{"db-b"}, "two different resources share one lane"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			a := mustAcquire(t, newTestLanes(t, root, 4), LaneRequest{Repo: "alpha", Kind: "unit", Exclusive: tc.first, Workers: 1, Slot: 3})
			ctx, cancel := context.WithTimeout(t.Context(), laneTestWait/4)
			defer cancel()
			b, err := newTestLanes(t, root, 4).Acquire(ctx, LaneRequest{Repo: "beta", Kind: "unit", Exclusive: tc.second, Workers: 1, Slot: 5})
			if err != nil {
				t.Fatalf("second Acquire while the first holds = %v, want a lease at once", err)
			}
			if a.Slot() != 3 || b.Slot() != 5 {
				t.Errorf("slots = %d and %d, want the 3 and 5 the callers leased, different, from 1", a.Slot(), b.Slot())
			}
			for _, lease := range []*LaneLease{a, b} {
				if want := laneSlotEnv() + "=" + strconv.Itoa(lease.Slot()); !slices.Contains(lease.Env(), want) {
					t.Errorf("Env() = %q, want it to hold %q", lease.Env(), want)
				}
			}
			mustRelease(t, b)
			mustRelease(t, a)
		})
	}
}

// TestFullRunTakesTheWholeTicketPool pins L63 (3): a check takes as many CPU tickets as its
// workers, at most the pool, so a full run runs alone, a 1-worker check waits for it, and no
// 1-worker check overtakes a full run that waits.
func TestFullRunTakesTheWholeTicketPool(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pool        int64
		full, first int // workers of the full run, and of a check already running when it asks (0: none)
		catches     string
	}{
		{"a full run makes a 1-worker check wait", 4, 4, 0, "a 1-worker check runs beside a run that holds the whole pool"},
		{"workers above the pool take the whole pool", 4, 9, 0, "a run with more workers than the pool waits for tickets that never exist, or leaves some for others"},
		{"a 1-worker check does not overtake a waiting full run", 2, 2, 1, "a 1-worker check that fits jumps a full run queued before it, so the full run starves"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			var first *LaneLease
			if tc.first > 0 {
				first = mustAcquire(t, newTestLanes(t, root, tc.pool), cpuRequest("first", tc.first))
			}
			fullLines := newLaneLines()
			fl := newTestLanes(t, root, tc.pool)
			fl.Log = fullLines
			fullDone := goAcquire(t.Context(), fl, cpuRequest("full", tc.full))
			var full *LaneLease
			if first == nil {
				full = leaseOf(t, awaitResult(t, fullDone, laneTestWait))
			} else {
				fullLines.next(t, "position 1")
			}
			ctx, cancel := context.WithTimeout(t.Context(), laneTestBlocked)
			defer cancel()
			if _, err := newTestLanes(t, root, tc.pool).Acquire(ctx, cpuRequest("small", 1)); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("1-worker Acquire = %v, want it to wait until %v", err, context.DeadlineExceeded)
			}
			if first != nil {
				mustRelease(t, first)
				full = leaseOf(t, awaitResult(t, fullDone, laneTestWait))
			}
			if full == nil {
				t.Fatal("the full run got no lease")
			}
			afterDone := goAcquire(t.Context(), newTestLanes(t, root, tc.pool), cpuRequest("after", 1))
			time.Sleep(laneTestHold)
			mustRelease(t, full)
			after := awaitResult(t, afterDone, laneTestWait)
			if after.err != nil {
				t.Fatalf("Acquire after the full run = %v, want a lease", after.err)
			}
			if a, f := laneRecordOf(t, after.lease, laneCPULabel), laneRecordOf(t, full, laneCPULabel); a.Started.Before(f.Ended) {
				t.Errorf("the 1-worker check started at %v, before the full run ended at %v", a.Started, f.Ended)
			}
			mustRelease(t, after.lease)
		})
	}
}

// TestLaneDeadOwnerTicketDrops pins that the ticket of a dead owner is dropped and logged with its
// pid and start time, in a resource lane and in the CPU lane.
func TestLaneDeadOwnerTicketDrops(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dir     func(root string) string
		req     LaneRequest
		catches string
	}{
		{"an exclusive resource lane", dbLane, dbRequest("alive", 1), "a ticket whose owner died keeps its resource lane closed for good"},
		{"the cpu lane", func(root string) string { return filepath.Join(root, laneCPUDir) }, cpuRequest("alive", 1), "the tickets of a dead owner stay taken from the pool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			dir := tc.dir(root)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			pid := exitedPID(t)
			ticket := fmt.Sprintf(`{"pid":%d,"start":"2026-01-02T03:04:05Z","start_id":"","repo":"ghost","kind":"unit",`+
				`"since":"2026-01-02T03:04:05Z","weight":1,"slot":1}`, pid)
			name := filepath.Join(dir, laneSeqName(1, laneTicketExt))
			if err := os.WriteFile(name, []byte(ticket), 0o600); err != nil {
				t.Fatal(err)
			}
			lines := newLaneLines()
			l := newTestLanes(t, root, 1)
			l.Log = lines
			lease := mustAcquire(t, l, tc.req)
			line := lines.next(t, "dead owner")
			for _, want := range []string{"pid " + strconv.Itoa(pid), "started 2026-01-02T03:04:05Z"} {
				if !strings.Contains(line, want) {
					t.Errorf("drop line = %q, want it to hold %q", line, want)
				}
			}
			if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the dead ticket is still there (Lstat = %v)", err)
			}
			mustRelease(t, lease)
		})
	}
}

// TestLaneNestedCallTakesNoTicket pins that a call whose env carries the lane token of a live
// holder takes no ticket and keeps the holder's slot; a stale token queues as any call, and a
// malformed one is refused.
func TestLaneNestedCallTakesNoTicket(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       func(outer *LaneLease, dead int) []string
		req       LaneRequest
		wantErr   bool
		wantLocks bool
		catches   string
	}{
		{"inside the check that holds the lanes", func(outer *LaneLease, _ int) []string { return outer.Env() },
			dbRequest("inner", 1), false, false, "a nested call queues behind its own parent and waits for good"},
		{"a token whose owner is dead", func(_ *LaneLease, dead int) []string {
			return []string{laneTokenEnv() + "=" + laneToken(core.ProcessFrom(dead, laneTestT0(), "")), laneSlotEnv() + "=1"}
		}, cpuRequest("stale", 1), false, true, "a stale token left in a shell skips every lane"},
		{"a malformed token", func(*LaneLease, int) []string { return []string{laneTokenEnv() + "=not-a-token", laneSlotEnv() + "=1"} },
			dbRequest("forged", 1), true, false, "a malformed token is read as a nested call and skips every lane"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			outer := mustAcquire(t, newTestLanes(t, root, 4), dbRequest("outer", 1))
			env := map[string]string{}
			for _, kv := range tc.env(outer, exitedPID(t)) {
				k, v, _ := strings.Cut(kv, "=")
				env[k] = v
			}
			inner := newTestLanes(t, root, 4)
			inner.Lookup = func(k string) string { return env[k] }
			var locks atomic.Int32
			inner.lock = func(ctx context.Context, p string) (*core.FileLock, error) {
				locks.Add(1)
				return core.AcquireFileLock(ctx, p)
			}
			ctx, cancel := context.WithTimeout(t.Context(), laneTestWait/4)
			defer cancel()
			lease, err := inner.Acquire(ctx, tc.req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("nested Acquire = %v, want an error: %v", err, tc.wantErr)
			}
			if got := locks.Load() > 0; got != tc.wantLocks {
				t.Errorf("nested Acquire opened %d lock files, want some: %v", locks.Load(), tc.wantLocks)
			}
			if err == nil && !tc.wantLocks && lease.Slot() != outer.Slot() {
				t.Errorf("nested slot = %d, want the holder's %d", lease.Slot(), outer.Slot())
			}
			if err == nil {
				mustRelease(t, lease)
			}
			if n := len(laneTicketFiles(t, dbLane(root))); n != 1 {
				t.Errorf("db lane holds %d tickets, want the holder's one", n)
			}
			mustRelease(t, outer)
		})
	}
}

// TestLaneWaiterPrintsPositionAndJobAhead pins that a waiter prints its position and the job just
// ahead of it: repo, kind and the time that job asked.
func TestLaneWaiterPrintsPositionAndJobAhead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ahead   int
		want    []string
		catches string
	}{
		{"one job ahead", 1, []string{"position 1", `repo "alpha"`, `kind "unit"`, "since 2026-01-02T03:04:05Z"},
			"a waiter prints nothing, or not the holder it waits for"},
		{"two jobs ahead", 2, []string{"position 2", `repo "beta"`, `kind "lint"`, "since 2026-01-02T04:04:05Z"},
			"a waiter counts only the holder, or names the holder instead of the job just ahead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			holder := newTestLanes(t, root, 4)
			holder.Now = laneTestT0
			a := mustAcquire(t, holder, LaneRequest{Repo: "alpha", Kind: "unit", Exclusive: []string{"db"}, Workers: 1, Slot: 1})
			var middle <-chan laneResult
			if tc.ahead == 2 {
				ml := newLaneLines()
				m := newTestLanes(t, root, 4)
				m.Log, m.Now = ml, func() time.Time { return laneTestT0().Add(time.Hour) }
				middle = goAcquire(t.Context(), m, LaneRequest{Repo: "beta", Kind: "lint", Exclusive: []string{"db"}, Workers: 1, Slot: 1})
				ml.next(t, "position 1")
			}
			bl := newLaneLines()
			b := newTestLanes(t, root, 4)
			b.Log = bl
			bDone := goAcquire(t.Context(), b, dbRequest("gamma", 1))
			line := bl.next(t, "position")
			for _, want := range tc.want {
				if !strings.Contains(line, want) {
					t.Errorf("wait line = %q, want it to hold %q", line, want)
				}
			}
			mustRelease(t, a)
			if middle != nil {
				mustRelease(t, leaseOf(t, awaitResult(t, middle, laneTestWait)))
			}
			mustRelease(t, leaseOf(t, awaitResult(t, bDone, laneTestWait)))
		})
	}
}

// TestLaneCancelRemovesTicket pins that a cancel or a timeout removes the waiter's ticket at once
// and the next waiter on the resource lane moves up.
func TestLaneCancelRemovesTicket(t *testing.T) {
	for _, tc := range []struct {
		name    string
		maxWait time.Duration // the canceller's MaxWait; 0 keeps the default and cancels instead
		wantErr error
		catches string
	}{
		{"a cancel", 0, context.Canceled, "a cancelled waiter leaves its ticket, so the waiter behind it waits for a job that never runs"},
		{"a timeout", 2 * time.Second, context.DeadlineExceeded, "a waiter that reaches MaxWait leaves its ticket in the lane"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			a := mustAcquire(t, newTestLanes(t, root, 4), dbRequest("alpha", 1))
			bl := newLaneLines()
			b := newTestLanes(t, root, 4)
			b.Log = bl
			if tc.maxWait > 0 {
				b.MaxWait = tc.maxWait
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			bDone := goAcquire(ctx, b, dbRequest("beta", 1))
			bl.next(t, "position 1")
			cl := newLaneLines()
			c := newTestLanes(t, root, 4)
			c.Log = cl
			cDone := goAcquire(t.Context(), c, dbRequest("gamma", 1))
			cl.next(t, "position 2")
			if tc.maxWait == 0 {
				cancel()
			}
			if r := awaitResult(t, bDone, laneTestWait); !errors.Is(r.err, tc.wantErr) {
				t.Fatalf("Acquire = %v, want %v", r.err, tc.wantErr)
			}
			if n := len(laneTicketFiles(t, dbLane(root))); n != 2 {
				t.Errorf("db lane holds %d tickets after the waiter left, want 2 (the holder and the waiter behind)", n)
			}
			if line := cl.next(t, "position 1"); !strings.Contains(line, `repo "alpha"`) {
				t.Errorf("moved-up line = %q, want it behind the holder alpha", line)
			}
			mustRelease(t, a)
			mustRelease(t, leaseOf(t, awaitResult(t, cDone, laneTestWait)))
		})
	}
}
