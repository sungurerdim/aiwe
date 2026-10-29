package gates

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestLaneSlotIsTheCallers pins B1 of audit-596-r2 (SPEC 6.5, 7.8, L63): the slot variable is the index of
// the slot lease the caller passes in LaneRequest.Slot, whatever tickets the lane holds, and a
// request with no slot from 1 is refused before any IO.
func TestLaneSlotIsTheCallers(t *testing.T) {
	t.Run("the slot is not renumbered", func(t *testing.T) {
		t.Logf("catches: the lane hands out its own lowest free number, a second source of %s beside the slot lease", laneSlotEnv())
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
		t.Logf("catches: a request with no slot lease runs with %s unset or 0, and a project derives its port from it", laneSlotEnv())
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

// TestLaneNestedCallKeepsTheHoldersSlot pins the low finding of audit-596-r3: a call inside a holder's
// check runs with the holder's slot, so a request that names another slot is refused, not dropped in
// silence; one that names the holder's slot is the same call and takes no ticket.
func TestLaneNestedCallKeepsTheHoldersSlot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		slot    int
		wantErr bool
		catches string
	}{
		{"the holder's slot", 3, false, "a nested call that names its own slot is refused"},
		{"another slot", 5, true, "a nested call that names another slot runs with the holder's, and the caller's mistake goes unseen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			root := t.TempDir()
			outer := mustAcquire(t, newTestLanes(t, root, 4), LaneRequest{Repo: "outer", Kind: "unit", Exclusive: []string{"db"}, Workers: 1, Slot: 3})
			env := map[string]string{}
			for _, kv := range outer.Env() {
				k, v, _ := strings.Cut(kv, "=")
				env[k] = v
			}
			inner := newTestLanes(t, root, 4)
			inner.Lookup = func(k string) string { return env[k] }
			lease, err := inner.Acquire(t.Context(), LaneRequest{Repo: "inner", Kind: "unit", Exclusive: []string{"db"}, Workers: 1, Slot: tc.slot})
			if (err != nil) != tc.wantErr {
				t.Fatalf("nested Acquire with slot %d = %v, want an error: %v", tc.slot, err, tc.wantErr)
			}
			if err == nil {
				if lease.Slot() != 3 {
					t.Errorf("nested Slot() = %d, want the holder's 3", lease.Slot())
				}
				mustRelease(t, lease)
			}
			mustRelease(t, outer)
		})
	}
}
