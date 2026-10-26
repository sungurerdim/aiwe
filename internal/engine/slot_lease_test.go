package engine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestSlotLeaseQueue guards SPEC §6.5 and §6.6: a lease takes a free slot, creates one while
// fewer than N exist, and at N waits, prints its queue position and is served in order; two
// leases at once never get one slot.
func TestSlotLeaseQueue(t *testing.T) {
	tests := []struct {
		name    string
		catches string
		run     func(t *testing.T, fx *slotTestFixture)
	}{
		{
			name:    "first lease creates slot 1 in the cache root at the base",
			catches: "a lease that hands out no clone, a clone outside the cache root, the wrong commit or no lease file",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 2, newSlotTestQueue())
				h := fx.holder(slotTestSession, 4101)
				s := fx.lease(t, p, h, fx.base)
				if s.K != 1 {
					t.Errorf("first slot K = %d, want 1", s.K)
				}
				if !engineTestUnder(fx.roots.Cache, s.Dir) || !engineTestUnder(fx.roots.Cache, s.Root) {
					t.Errorf("slot dir %q / root %q is not under the cache root %q", s.Dir, s.Root, fx.roots.Cache)
				}
				if !engineTestUnder(s.Root, s.Dir) {
					t.Errorf("slot dir %q is not inside its owned root %q", s.Dir, s.Root)
				}
				slotTestAssertClean(t, s, fx.base)
				lease := engineTestRead(t, fx.leaseFile(1))
				for _, want := range []string{slotTestSession, strconv.Itoa(h.Process.PID)} {
					if !strings.Contains(lease, want) {
						t.Errorf("lease file s1.lease = %q, want the holder's %q", lease, want)
					}
				}
			},
		},
		{
			name:    "a released slot is handed out again, not cloned anew",
			catches: "a pool that clones per lease (DR-10: never created per run) or keeps the old holder's lease",
			run: func(t *testing.T, fx *slotTestFixture) {
				p := fx.open(t, 2, newSlotTestQueue())
				a := fx.lease(t, p, fx.holder(slotTestSession, 4101), fx.base)
				sentinel := slotTestSentinel(t, a)
				fx.release(t, p, a)
				b := fx.lease(t, p, fx.holder(slotTestSecondSession, 4102), fx.base)
				if b.K != a.K || b.Dir != a.Dir {
					t.Errorf("second lease got slot %d at %q, want the free slot %d at %q", b.K, b.Dir, a.K, a.Dir)
				}
				if !slotTestExists(t, sentinel) {
					t.Error("a clean free slot was cloned again instead of reset")
				}
				if lease := engineTestRead(t, fx.leaseFile(b.K)); !strings.Contains(lease, slotTestSecondSession) || strings.Contains(lease, slotTestSession) {
					t.Errorf("lease file after the second lease = %q, want only %s", lease, slotTestSecondSession)
				}
			},
		},
		{
			name:    "below N a held slot makes the next lease create slot 2",
			catches: "a lease that waits or shares a held slot while the pool is below N",
			run: func(t *testing.T, fx *slotTestFixture) {
				q := newSlotTestQueue()
				p := fx.open(t, 2, q)
				a := fx.lease(t, p, fx.holder(slotTestSession, 4101), fx.base)
				b := fx.lease(t, p, fx.holder(slotTestSecondSession, 4102), fx.base)
				if b.K != 2 || b.Dir == a.Dir {
					t.Errorf("second lease got slot %d at %q while slot %d at %q is held, want a new slot 2", b.K, b.Dir, a.K, a.Dir)
				}
				if strings.Contains(q.String(), "queue position") {
					t.Errorf("lease below N printed a queue position: %q", q.String())
				}
				for _, k := range []int{1, 2} {
					if !slotTestExists(t, fx.leaseFile(k)) {
						t.Errorf("lease file s%d.lease is missing", k)
					}
				}
				slotTestAssertClean(t, b, fx.base)
			},
		},
		{
			name:    "at N the lease waits, prints its queue position and is served in order",
			catches: "a lease over N, one that fails at once, prints no position, or serves a later waiter first",
			run: func(t *testing.T, fx *slotTestFixture) {
				q1, q2 := newSlotTestQueue(), newSlotTestQueue()
				p1, p2 := fx.open(t, 1, q1), fx.open(t, 1, q2)
				a := fx.lease(t, p1, fx.holder(slotTestSession, 4101), fx.base)
				ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
				defer cancel()
				bc, cc := make(chan slotTestResult, 1), make(chan slotTestResult, 1)
				slotTestLeaseAsync(ctx, p1, fx.holder(slotTestSecondSession, 4102), fx.base, bc)
				q1.waitFor(t, "queue position 1")
				slotTestLeaseAsync(ctx, p2, fx.holder(slotTestThirdSession, 4103), fx.base, cc)
				q2.waitFor(t, "queue position 2")
				slotTestNotYet(t, bc, "first waiter")
				slotTestNotYet(t, cc, "second waiter")
				fx.release(t, p1, a)
				b := slotTestReceive(t, bc, "first waiter")
				if b.s.K != 1 || b.s.Dir != a.Dir {
					t.Errorf("first waiter got slot %d at %q, want slot 1 at %q (no slot over N)", b.s.K, b.s.Dir, a.Dir)
				}
				slotTestNotYet(t, cc, "second waiter while the first holds the slot")
				fx.release(t, p1, b.s)
				c := slotTestReceive(t, cc, "second waiter")
				if c.s.K != 1 {
					t.Errorf("second waiter got slot %d, want slot 1 (no slot over N)", c.s.K)
				}
				slotTestAssertClean(t, c.s, fx.base)
			},
		},
		{
			name:    "two leases at once below N get two slots",
			catches: "a race that hands one slot to two sessions or creates the same slot number twice",
			run: func(t *testing.T, fx *slotTestFixture) {
				q := newSlotTestQueue()
				p1, p2 := fx.open(t, 2, q), fx.open(t, 2, q)
				ha, hb := fx.holder(slotTestSession, 4101), fx.holder(slotTestSecondSession, 4102)
				ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
				defer cancel()
				out := make(chan slotTestResult, 2)
				start := make(chan struct{})
				for _, c := range []struct {
					p *SlotPool
					h SlotHolder
				}{{p1, ha}, {p2, hb}} {
					go func() {
						<-start
						s, err := c.p.Lease(ctx, c.h, fx.base)
						out <- slotTestResult{p: c.p, s: s, err: err, name: c.h.Session}
					}()
				}
				close(start)
				a := slotTestReceive(t, out, "first of two")
				b := slotTestReceive(t, out, "second of two")
				if a.s.K == b.s.K || a.s.Dir == b.s.Dir {
					t.Fatalf("two leases at once got slot %d at %q and slot %d at %q, want two slots", a.s.K, a.s.Dir, b.s.K, b.s.Dir)
				}
				slotTestAssertClean(t, a.s, fx.base)
				slotTestAssertClean(t, b.s, fx.base)
			},
		},
		{
			name:    "two leases at once at N hand out one slot and queue the other",
			catches: "a race at N that hands the one slot to both sessions or fails the loser instead of queueing it",
			run: func(t *testing.T, fx *slotTestFixture) {
				q := newSlotTestQueue()
				p1, p2 := fx.open(t, 1, q), fx.open(t, 1, q)
				ha, hb := fx.holder(slotTestSession, 4101), fx.holder(slotTestSecondSession, 4102)
				ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
				defer cancel()
				out := make(chan slotTestResult, 2)
				start := make(chan struct{})
				for _, c := range []struct {
					p *SlotPool
					h SlotHolder
				}{{p1, ha}, {p2, hb}} {
					go func() {
						<-start
						s, err := c.p.Lease(ctx, c.h, fx.base)
						out <- slotTestResult{p: c.p, s: s, err: err, name: c.h.Session}
					}()
				}
				close(start)
				first := slotTestReceive(t, out, "winner")
				q.waitFor(t, "queue position 1")
				slotTestNotYet(t, out, "loser")
				fx.release(t, first.p, first.s)
				second := slotTestReceive(t, out, "loser after release")
				if second.s.K != 1 || second.s.Dir != first.s.Dir {
					t.Errorf("loser got slot %d at %q, want the one slot at %q", second.s.K, second.s.Dir, first.s.Dir)
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

// TestSlotResetKeepsDeps guards the reset of SPEC §6.5: every lease fetches from the canonical
// repository, checks out the base detached, resets and cleans everything but KeepDirs, and runs
// the restore only when the lock-file digest changed. The second lease reuses the same slot.
func TestSlotResetKeepsDeps(t *testing.T) {
	tests := []struct {
		name         string
		catches      string
		between      func(t *testing.T, fx *slotTestFixture, s *Slot) string // returns the next base
		wantRestores int
		check        func(t *testing.T, fx *slotTestFixture, s *Slot)
	}{
		{
			name:         "an unchanged lock file keeps node_modules and installs nothing",
			catches:      "a clean that drops KeepDirs, or a restore on every lease",
			between:      func(t *testing.T, fx *slotTestFixture, s *Slot) string { return fx.base },
			wantRestores: 1,
		},
		{
			name:    "a new base without a lock change is fetched and keeps the install",
			catches: "a reset that skips the fetch from the canonical repository, or a restore keyed on the commit",
			between: func(t *testing.T, fx *slotTestFixture, s *Slot) string {
				return fx.commit(t, "src/index.js", "module.exports = () => 'merhaba'\n", "feat: greet in Turkish")
			},
			wantRestores: 1,
		},
		{
			name:    "a lock-digest change runs the restore once more",
			catches: "a restore that never reruns after package-lock.json changes",
			between: func(t *testing.T, fx *slotTestFixture, s *Slot) string {
				return fx.commit(t, slotTestLockFile, strings.Replace(slotTestLockBody, "1.3.0", "1.3.1", 1), "build(deps): bump left-pad")
			},
			wantRestores: 2,
		},
		{
			name:    "a run's edits, new files, ignored output, staged change and branch commit are gone",
			catches: "a reset without reset --hard, clean -ffdx (-x for ignored dist/) or checkout --detach",
			between: func(t *testing.T, fx *slotTestFixture, s *Slot) string {
				engineTestWrite(t, filepath.Join(s.Dir, "README.md"), "# greeter\n\nedited by the run\n")
				engineTestWrite(t, filepath.Join(s.Dir, "notes", "todo.md"), "- finish the greeting\n")
				engineTestWrite(t, filepath.Join(s.Dir, "dist", "bundle.js"), "/* built */\n")
				engineTestGit(t, s.Dir, "checkout", "-q", "-b", "feature/greeting")
				engineTestWrite(t, filepath.Join(s.Dir, "src", "greet.js"), "module.exports = 'hi'\n")
				engineTestGit(t, s.Dir, "add", "src/greet.js")
				engineTestGit(t, s.Dir, "-c", "user.name=Grace Hopper", "-c", "user.email=grace.hopper@example.com",
					"-c", "commit.gpgsign=false", "commit", "-q", "-m", "feat: greet module")
				engineTestWrite(t, filepath.Join(s.Dir, "package.json"), "{\"name\": \"greeter\", \"version\": \"9.9.9\"}\n")
				engineTestGit(t, s.Dir, "add", "package.json")
				return fx.base
			},
			wantRestores: 1,
			check: func(t *testing.T, fx *slotTestFixture, s *Slot) {
				for _, gone := range []string{"notes/todo.md", "dist/bundle.js", "src/greet.js"} {
					if slotTestExists(t, filepath.Join(s.Dir, filepath.FromSlash(gone))) {
						t.Errorf("%s of the last run is still in the slot", gone)
					}
				}
				if got := engineTestRead(t, filepath.Join(s.Dir, "README.md")); got != slotTestReadmeBody {
					t.Errorf("README.md = %q, want the base content", got)
				}
				if got := engineTestRead(t, filepath.Join(s.Dir, "package.json")); got != slotTestPackageBody {
					t.Errorf("package.json = %q, want the base content", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("catches: %s", tt.catches)
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			s := fx.lease(t, p, fx.holder(slotTestSession, 4201), fx.base)
			if n := fx.restores(t); n != 1 {
				t.Fatalf("first lease ran the restore %d times, want 1 (no recorded lock digest yet)", n)
			}
			first := strings.TrimSpace(engineTestRead(t, fx.restoreLog))
			if !engineTestUnder(s.Dir, first) && !engineTestUnder(first, s.Dir) {
				t.Errorf("restore ran in %q, want the slot %q", first, s.Dir)
			}
			sentinel := slotTestSentinel(t, s)
			next := tt.between(t, fx, s)
			fx.release(t, p, s)
			s2 := fx.lease(t, p, fx.holder(slotTestSecondSession, 4202), next)
			if s2.Dir != s.Dir || !slotTestExists(t, sentinel) {
				t.Errorf("second lease got %q (sentinel kept: %v), want the same slot %q reset in place", s2.Dir, slotTestExists(t, sentinel), s.Dir)
			}
			slotTestAssertClean(t, s2, next)
			if got := engineTestRead(t, filepath.Join(s2.Dir, filepath.FromSlash(slotTestInstalled))); got != slotTestInstalledBody {
				t.Errorf("%s = %q after the reset, want the installed file kept", slotTestInstalled, got)
			}
			if n := fx.restores(t); n != tt.wantRestores {
				t.Errorf("restore ran %d times over two leases, want %d", n, tt.wantRestores)
			}
			if tt.check != nil {
				tt.check(t, fx, s2)
			}
		})
	}
}

// slotTestSpecEdit changes one field of a pool spec before OpenSlotPool sees it.
type slotTestSpecEdit func(t *testing.T, fx *slotTestFixture, spec *SlotPoolSpec)

// TestSlotPoolRefusals guards the refusals of OpenSlotPool: a spec it cannot serve is an error and
// no pool, before anything is created (fail-closed, DR-10, DR-11).
func TestSlotPoolRefusals(t *testing.T) {
	tests := []struct {
		name string
		edit slotTestSpecEdit
	}{
		{"no owner", func(_ *testing.T, _ *slotTestFixture, s *SlotPoolSpec) { s.Owner = nil }},
		{"no slot", func(_ *testing.T, _ *slotTestFixture, s *SlotPoolSpec) { s.Size = 0 }},
		{"a restore command without a program", func(_ *testing.T, _ *slotTestFixture, s *SlotPoolSpec) { s.Restore = []string{""} }},
		{"a KeepDirs entry outside the work tree", func(_ *testing.T, _ *slotTestFixture, s *SlotPoolSpec) { s.KeepDirs = []string{"../node_modules"} }},
		{"a lock file inside .git", func(_ *testing.T, _ *slotTestFixture, s *SlotPoolSpec) { s.LockFiles = []string{".git/config"} }},
		{"a relative canonical repository", func(_ *testing.T, _ *slotTestFixture, s *SlotPoolSpec) { s.Canonical = "greeter" }},
		{"a missing lease directory", func(_ *testing.T, fx *slotTestFixture, s *SlotPoolSpec) {
			s.LeaseDir = filepath.Join(fx.outside, "no-such-slots")
		}},
		{"an engine environment that points git at another repository", func(t *testing.T, fx *slotTestFixture, _ *SlotPoolSpec) {
			t.Setenv("GIT_DIR", filepath.Join(fx.canonical, ".git"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			spec := fx.spec(1, newSlotTestQueue())
			tt.edit(t, fx, &spec)
			if p, err := OpenSlotPool(spec); err == nil || p != nil {
				t.Errorf("OpenSlotPool = %v, %v; want an error and no pool", p, err)
			}
		})
	}
}

// TestSlotLeaseRefusals guards the refusals of Lease and Release: a holder without a plain session,
// a pid or a start time, a base that is no commit id, a release of no slot or of a lease the
// sweeper reaped, and a slot record the pool cannot trust. Each is an error; none leaves a lease
// file, drops another holder's lease or removes a slot.
func TestSlotLeaseRefusals(t *testing.T) {
	start := slotTestEpoch().Add(-time.Hour)
	for _, tc := range []struct {
		name string
		h    SlotHolder
		base string // "" means the fixture's base
	}{
		{"no session", SlotHolder{Process: core.ProcessID{PID: 4501, Start: start}}, ""},
		{"a session that is a path", SlotHolder{Session: "../sess", Process: core.ProcessID{PID: 4501, Start: start}}, ""},
		{"no pid", SlotHolder{Session: slotTestSession, Process: core.ProcessID{Start: start}}, ""},
		{"no start time", SlotHolder{Session: slotTestSession, Process: core.ProcessID{PID: 4501}}, ""},
		{"a base git would read as an option", SlotHolder{Session: slotTestSession, Process: core.ProcessID{PID: 4501, Start: start}}, "--upload-pack=touch"},
	} {
		t.Run("Lease refuses "+tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			base := tc.base
			if base == "" {
				base = fx.base
			}
			ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
			defer cancel()
			if s, err := p.Lease(ctx, tc.h, base); err == nil || s != nil {
				t.Errorf("Lease = slot %v, err %v; want an error and no slot", s, err)
			}
			if names := slotTestNames(t, fx.leaseDir); len(names) != 0 {
				t.Errorf("a refused lease left %v in the lease directory", names)
			}
		})
	}

	t.Run("Release refuses no slot and a lease the sweeper reaped", func(t *testing.T) {
		fx := slotTestSetup(t)
		p := fx.open(t, 1, newSlotTestQueue())
		if err := p.Release(t.Context(), nil); err == nil {
			t.Error("Release(nil) = nil, want an error")
		}
		a := fx.lease(t, p, fx.holder(slotTestSession, 4501), fx.base)
		fx.procs.kill(4501)
		if reaped, err := p.Sweep(t.Context()); err != nil || reaped != 1 {
			t.Fatalf("Sweep = %d, %v; want the dead holder's lease reaped", reaped, err)
		}
		b := fx.lease(t, p, fx.holder(slotTestSecondSession, 4502), fx.base)
		if err := p.Release(t.Context(), a); err == nil {
			t.Error("Release of a reaped lease = nil, want an error")
		}
		if lease := engineTestRead(t, fx.leaseFile(b.K)); !strings.Contains(lease, slotTestSecondSession) {
			t.Errorf("the reaped holder's release changed the next holder's lease: %q", lease)
		}
	})

	for _, tc := range []struct{ name, record string }{
		{"a slot record that is no JSON", "not a slot record\n"},
		{"a slot record whose work tree is not in its root", `{"root":"slots","dir":"elsewhere"}`},
	} {
		t.Run("Lease refuses "+tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			a := fx.lease(t, p, fx.holder(slotTestSession, 4601), fx.base)
			fx.release(t, p, a)
			engineTestWrite(t, filepath.Join(fx.leaseDir, "s1.slot"), tc.record)
			ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
			defer cancel()
			if s, err := p.Lease(ctx, fx.holder(slotTestSecondSession, 4602), fx.base); err == nil || s != nil {
				t.Fatalf("Lease = slot %v, err %v; want an error and no slot", s, err)
			}
			if slotTestExists(t, fx.leaseFile(1)) {
				t.Error("a lease refused on its slot record left s1.lease behind")
			}
			if !slotTestExists(t, filepath.Join(a.Dir, "README.md")) {
				t.Error("a slot whose record the pool cannot trust was removed")
			}
		})
	}
}

// TestSlotRefusalsHideValue guards that a refusal names what it refused and never repeats the
// refused value, which comes from a caller or a file and may carry a secret (P5).
func TestSlotRefusalsHideValue(t *testing.T) {
	const canary = "canary-7f3a"
	for _, tc := range []struct {
		name string
		edit func(s *SlotPoolSpec)
	}{
		{"a KeepDirs entry", func(s *SlotPoolSpec) { s.KeepDirs = []string{"../" + canary} }},
		{"a lock file", func(s *SlotPoolSpec) { s.LockFiles = []string{".git/" + canary} }},
		{"a canonical repository", func(s *SlotPoolSpec) { s.Canonical = canary }},
	} {
		t.Run("OpenSlotPool on "+tc.name, func(t *testing.T) {
			spec := slotTestSetup(t).spec(1, newSlotTestQueue())
			tc.edit(&spec)
			if _, err := OpenSlotPool(spec); err == nil || strings.Contains(err.Error(), canary) {
				t.Errorf("OpenSlotPool error = %v; want a refusal without the refused value", err)
			}
		})
	}
	for _, tc := range []struct{ name, session, base, record string }{
		{"a session", "../" + canary, "", ""},
		{"a base", slotTestSession, "--" + canary, ""},
		{"a slot record", slotTestSession, "", `{"name":"../` + canary + `"}`},
	} {
		t.Run("Lease on "+tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			if tc.record != "" {
				fx.release(t, p, fx.lease(t, p, fx.holder(slotTestSecondSession, 4901), fx.base))
				engineTestWrite(t, filepath.Join(fx.leaseDir, "s1.slot"), tc.record)
			}
			base := tc.base
			if base == "" {
				base = fx.base
			}
			h := fx.holder(slotTestSession, 4902)
			h.Session = tc.session
			ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
			defer cancel()
			if _, err := p.Lease(ctx, h, base); err == nil || strings.Contains(err.Error(), canary) {
				t.Errorf("Lease error = %v; want a refusal without the refused value", err)
			}
		})
	}
}

// slotTestNames returns the names in dir.
func slotTestNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestSlotUnreadableRecord guards F1 of #470: a shrink that cannot read the record of a slot it would take
// over from a gone creator reports the slot and keeps it, not 0 and nil (a removeTaken that swallows it).
func TestSlotUnreadableRecord(t *testing.T) {
	fx := slotTestSetup(t)
	pg, err := OpenSlotPool(fx.goneSpec(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	a := fx.lease(t, pg, fx.holder(slotTestSession, 5801), fx.base)
	fx.release(t, pg, a)
	record := filepath.Join(fx.leaseDir, "s1.slot")
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(record, 0o700); err != nil {
		t.Fatal(err)
	}
	removed, err := fx.open(t, 1, newSlotTestQueue()).Shrink(t.Context(), slotTestOverCache())
	if err == nil || !strings.Contains(err.Error(), "slot 1") || removed != 0 {
		t.Errorf("Shrink = %d, %v; want 0 and an error naming slot 1", removed, err)
	}
	if !slotTestExists(t, a.Root) {
		t.Error("the slot of the unreadable record was removed")
	}
}
