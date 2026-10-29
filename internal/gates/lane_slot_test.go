package gates

import (
	"slices"
	"strconv"
	"testing"
)

// TestLaneSlotIsTheCallers pins B1 of audit-596-r2 (SPEC 6.5, 7.8, L63): AIWE_SLOT is the index of
// the slot lease the caller passes in LaneRequest.Slot, whatever tickets the lane holds, and a
// request with no slot from 1 is refused before any IO.
func TestLaneSlotIsTheCallers(t *testing.T) {
	t.Run("the slot is not renumbered", func(t *testing.T) {
		t.Log("catches: the lane hands out its own lowest free number, a second source of AIWE_SLOT beside the slot lease")
		root := t.TempDir()
		first := mustAcquire(t, newTestLanes(t, root, 4), LaneRequest{Repo: "a", Kind: "unit", Workers: 1, Slot: 7})
		second := mustAcquire(t, newTestLanes(t, root, 4), LaneRequest{Repo: "b", Kind: "unit", Workers: 1, Slot: 2})
		for _, tc := range []struct {
			lease *LaneLease
			slot  int
		}{{first, 7}, {second, 2}} {
			if tc.lease.Slot() != tc.slot {
				t.Errorf("Slot() = %d, want the leased %d", tc.lease.Slot(), tc.slot)
			}
			if want := laneSlotEnv() + "=" + strconv.Itoa(tc.slot); !slices.Contains(tc.lease.Env(), want) {
				t.Errorf("Env() = %q, want it to hold %q", tc.lease.Env(), want)
			}
		}
		mustRelease(t, second)
		mustRelease(t, first)
	})
	t.Run("a waiting check keeps its slot", func(t *testing.T) {
		t.Log("catches: a waiter is numbered again after the holder left, and takes another slot than the one it leased")
		root := t.TempDir()
		holder := mustAcquire(t, newTestLanes(t, root, 1), LaneRequest{Repo: "h", Kind: "unit", Workers: 1, Slot: 1})
		lines := newLaneLines()
		w := newTestLanes(t, root, 1)
		w.Log = lines
		done := goAcquire(t.Context(), w, LaneRequest{Repo: "w", Kind: "unit", Workers: 1, Slot: 4})
		lines.next(t, "position 1")
		mustRelease(t, holder)
		waiter := leaseOf(t, awaitResult(t, done, laneTestWait))
		if waiter.Slot() != 4 {
			t.Errorf("Slot() = %d, want the leased 4", waiter.Slot())
		}
		mustRelease(t, waiter)
	})
	t.Run("a request with no slot is refused", func(t *testing.T) {
		t.Log("catches: a request with no slot lease runs with AIWE_SLOT unset or 0, and a project derives its port from it")
		root := t.TempDir()
		l := newTestLanes(t, root, 4)
		locks := countLocks(l)
		for _, slot := range []int{0, -1} {
			if lease, err := l.Acquire(t.Context(), LaneRequest{Repo: "a", Kind: "unit", Workers: 1, Slot: slot}); err == nil {
				mustRelease(t, lease)
				t.Errorf("Acquire with slot %d = a lease, want it refused", slot)
			}
		}
		if locks.Load() != 0 {
			t.Errorf("Acquire opened %d lock files, want none for a refused request", locks.Load())
		}
	})
}
