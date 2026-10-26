package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSlotSafetyLinkToCopy guards F3 of #470. It kills the mutant that disables checkSlotTree
// (slot_io.go:262, the function returns nil at once). A slot root, work tree or git directory that is a
// link to a moved-aside copy of itself keeps the recorded git control files, so the control digest cannot
// refuse it: only checkSlotTree does, and the lease must fail promptly.
func TestSlotSafetyLinkToCopy(t *testing.T) {
	for _, tc := range []struct {
		name string
		pick func(a *Slot) string // the path that becomes a link to its own moved-aside copy
	}{
		{"catches: a slot root that is a link to a copy of itself", func(a *Slot) string { return a.Root }},
		{"catches: a slot work tree that is a link to a copy of itself", func(a *Slot) string { return a.Dir }},
		{"catches: a slot git directory that is a link to a copy of itself", func(a *Slot) string { return filepath.Join(a.Dir, ".git") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			a := fx.lease(t, p, fx.holder(slotTestSession, 5811), fx.base)
			fx.release(t, p, a)
			moved := tc.pick(a)
			if err := os.Rename(moved, moved+"-copy"); err != nil {
				t.Fatal(err)
			}
			slotSafetyLink(t, moved, moved+"-copy")
			s, err := slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5812), fx.base)
			if err == nil || s != nil || errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Lease = slot %v, err %v; want a prompt refusal of the linked slot", s, err)
			}
		})
	}
}

// TestSlotQueuePositionRules guards F4 of #470. It kills the mutant that breaks the ticket order rule
// (slot_io.go:126, queuePosition counts a ticket whose holder holds a lease file, or one queued after
// mine). Only a ticket queued before mine, that parses and whose holder holds no lease file, is a
// waiter ahead of me.
func TestSlotQueuePositionRules(t *testing.T) {
	const holder = `{"session":"sess-a","pid":4801,"start":"2026-10-25T09:00:00Z","at":"2026-10-25T09:00:00Z"}`
	const other = `{"session":"sess-b","pid":4802,"start":"2026-10-25T09:00:00Z","at":"2026-10-25T09:00:00Z"}`
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"catches: a ticket queued before mine that is no waiter ahead", map[string]string{"w1.wait": other, "w2.wait": other}, 2},
		{"catches: a ticket whose holder holds a lease file counted as a waiter", map[string]string{"s1.lease": holder, "w1.wait": holder, "w2.wait": other}, 1},
		{"catches: a ticket that does not parse counted as a waiter", map[string]string{"w1.wait": "", "w2.wait": other}, 1},
		{"catches: a ticket queued after mine counted as a waiter ahead", map[string]string{"w2.wait": other, "w3.wait": other}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var names []string
			for name, body := range tc.files {
				engineTestWrite(t, filepath.Join(dir, name), body)
				names = append(names, name)
			}
			if pos, err := queuePosition(dir, names, "w2.wait"); err != nil || pos != tc.want {
				t.Errorf("queuePosition = %d, %v; want %d", pos, err, tc.want)
			}
		})
	}
}

// slotTestRunBranch is the branch a holder commits its run on in its slot.
const slotTestRunBranch = "run/greeting"

// slotTestWork commits a run's work on slotTestRunBranch in slot s, as a holder does before its
// collect, and returns the commit.
func slotTestWork(t *testing.T, s *Slot) string {
	t.Helper()
	engineTestGit(t, s.Dir, "switch", "-q", "-c", slotTestRunBranch)
	engineTestWrite(t, filepath.Join(s.Dir, "src", "work.js"), "module.exports = 'two hours of agent work'\n")
	engineTestGit(t, s.Dir, "add", "-A")
	engineTestGit(t, s.Dir, "-c", "user.name=Grace Hopper", "-c", "user.email=grace.hopper@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "feat: work")
	return engineTestGit(t, s.Dir, "rev-parse", "HEAD")
}

// slotTestCollected reports whether a run ref of the canonical repository holds commit.
func slotTestCollected(t *testing.T, fx *slotTestFixture, commit string) bool {
	t.Helper()
	return strings.Contains(engineTestGit(t, fx.canonical, "for-each-ref", "--format=%(objectname)", RunRef("")), commit)
}

// slotTestNoRun fails when a ref or the HEAD reflog of slot s still names the earlier run's branch or commit.
func slotTestNoRun(t *testing.T, s *Slot, commit string) {
	t.Helper()
	refs := engineTestGit(t, s.Dir, "for-each-ref", "--format=%(objectname) %(refname)")
	log := exec.CommandContext(t.Context(), "git", "reflog", "show", "--format=%H %gs", "HEAD")
	log.Dir = s.Dir
	out, _ := log.Output() // a slot with no reflog names no earlier run
	for what, text := range map[string]string{"a ref": refs, "the HEAD reflog": string(out)} {
		if strings.Contains(text, commit) || strings.Contains(text, slotTestRunBranch) {
			t.Errorf("%s of the slot handed to the next holder names the earlier run:\n%s", what, text)
		}
	}
}

// TestSlotCollectBeforeReclone guards B1 of #470 (DR-11). A holder that committed and died before its
// collect leaves the commit in its slot alone. The next lease collects it into a run ref of the canonical
// repository before it resets the slot or clones it again, and the next holder sees no ref of that run.
func TestSlotCollectBeforeReclone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dirty   func(t *testing.T, s *Slot) // what the holder did after its commit, before it died
		reclone bool
	}{
		{"catches: a reset in place that leaves the run's branch to the next holder", func(*testing.T, *Slot) {}, false},
		{"catches: a git config write that drops the uncollected commit with the slot", func(t *testing.T, s *Slot) {
			engineTestGit(t, s.Dir, "config", "user.email", "grace.hopper@example.com")
		}, true},
		{"catches: a KeepDirs change that drops the uncollected commit with the slot", func(t *testing.T, s *Slot) {
			engineTestWrite(t, filepath.Join(s.Dir, slotTestKeepDir, "is-odd", "index.js"), "module.exports = n => n % 2 === 1\n")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			h := fx.holder(slotTestSession, 5901)
			s := fx.lease(t, p, h, fx.base)
			work := slotTestWork(t, s)
			tc.dirty(t, s)
			fx.procs.kill(h.Process.PID) // the holder dies before its collect
			s2 := fx.lease(t, p, fx.holder(slotTestSecondSession, 5902), fx.base)
			if got := s2.Dir != s.Dir; got != tc.reclone {
				t.Errorf("the next lease recloned the slot: %v, want %v", got, tc.reclone)
			}
			if !slotTestCollected(t, fx, work) {
				t.Errorf("commit %s of the dead holder is in no run ref of the canonical repository (DR-11)", work[:12])
			}
			slotTestAssertClean(t, s2, fx.base)
			slotTestNoRun(t, s2, work)
		})
	}
}

// TestSlotUncollectableKept guards the refusal of B1 of #470: a slot whose commits cannot be collected,
// here one whose git config git cannot read, is not removed with them; the lease reports it unusable.
func TestSlotUncollectableKept(t *testing.T) {
	fx := slotTestSetup(t)
	p := fx.open(t, 1, newSlotTestQueue())
	h := fx.holder(slotTestSession, 5921)
	a := fx.lease(t, p, h, fx.base)
	slotTestWork(t, a)
	engineTestWrite(t, filepath.Join(a.Dir, ".git", "config"), "[core\n")
	fx.procs.kill(h.Process.PID)
	s, err := slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5922), fx.base)
	if err == nil || s != nil || !strings.Contains(err.Error(), "collect") {
		t.Errorf("Lease over a slot it cannot collect = slot %v, err %v; want a refusal naming the collect", s, err)
	}
	if !slotTestExists(t, filepath.Join(a.Dir, "src", "work.js")) {
		t.Error("catches: a slot removed with commits that were never collected (DR-11)")
	}
}

// TestSlotResetFailureRecloned guards B3 of #470. A reset that fails, here on the .git/index.lock a killed
// git call left, makes the slot dirty like a polluted one: its creator collects it and clones it again, and
// a pool that may not remove it keeps it and names the cause.
func TestSlotResetFailureRecloned(t *testing.T) {
	fx := slotTestSetup(t)
	p := fx.open(t, 1, newSlotTestQueue())
	h := fx.holder(slotTestSession, 5911)
	a := fx.lease(t, p, h, fx.base)
	work := slotTestWork(t, a)
	lock := filepath.Join(a.Dir, ".git", "index.lock")
	engineTestWrite(t, lock, "")
	fx.procs.kill(h.Process.PID)
	s, err := slotSafetyTry(t, fx.open(t, 1, newSlotTestQueue()), fx.holder(slotTestSecondSession, 5912), fx.base)
	if err == nil || s != nil || !strings.Contains(err.Error(), "index.lock") {
		t.Errorf("Lease by a pool that may not remove the slot = slot %v, err %v; want a refusal naming the index.lock", s, err)
	}
	if !slotTestExists(t, lock) {
		t.Error("a pool removed a slot another process created (DR-11)")
	}
	s = fx.lease(t, p, fx.holder(slotTestThirdSession, 5913), fx.base)
	if s.Dir == a.Dir || slotTestExists(t, filepath.Join(s.Dir, ".git", "index.lock")) {
		t.Errorf("catches: the creator got the slot %q back with its stale index.lock, want it cloned again", s.Dir)
	}
	slotTestAssertClean(t, s, fx.base)
	if !slotTestCollected(t, fx, work) {
		t.Errorf("commit %s of the slot whose reset failed is in no run ref (DR-11)", work[:12])
	}
}

// TestSlotShrinkCollects guards DR-11 for the shrink of SPEC §6.4: an idle slot over the cache ceiling is
// removed only after its dead holder's commit is collected into a run ref, and a slot it cannot collect
// is kept and reported.
func TestSlotShrinkCollects(t *testing.T) {
	for _, broken := range []bool{false, true} {
		name := "catches: a shrink that removes a dead holder's uncollected commit with its slot"
		if broken {
			name = "catches: a shrink that removes a slot it cannot collect"
		}
		t.Run(name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			h := fx.holder(slotTestSession, 5931)
			a := fx.lease(t, p, h, fx.base)
			work := slotTestWork(t, a)
			if broken {
				engineTestWrite(t, filepath.Join(a.Dir, ".git", "config"), "[core\n")
			}
			fx.procs.kill(h.Process.PID)
			if reaped, err := p.Sweep(t.Context()); err != nil || reaped != 1 {
				t.Fatalf("Sweep = %d, %v; want the dead holder's lease reaped", reaped, err)
			}
			removed, err := p.Shrink(t.Context(), slotTestOverCache())
			switch {
			case broken && (err == nil || removed != 0 || !slotTestExists(t, filepath.Join(a.Dir, "src", "work.js"))):
				t.Errorf("Shrink over a slot it cannot collect = %d, %v; want 0, an error and the slot kept", removed, err)
			case !broken && (err != nil || removed != 1):
				t.Errorf("Shrink = %d, %v; want the idle slot removed", removed, err)
			case !broken && !slotTestCollected(t, fx, work):
				t.Errorf("commit %s of the dead holder is in no run ref after the shrink (DR-11)", work[:12])
			}
		})
	}
}

// TestSlotCollectHiddenRefs guards S2-a of the 2026-10-26 security review (DR-11). The collect reads a slot's
// refs through the upload-pack git starts in the slot, which reads the slot's own hideRefs. A dead holder's
// commit must still reach a run ref of the canonical repository, and a slot whose commit was not collected is
// kept: on the lease path, which clones a slot with a changed git config again, and on the shrink path.
func TestSlotCollectHiddenRefs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hide   []string // hideRefs entries the holder set in the slot's git config, in order
		detach bool     // the work is at a detached HEAD alone, with no branch; else HEAD went back to the base
		shrink bool
	}{
		{"catches: a slot uploadpack.hideRefs=refs/heads that hides the run's branch from the collect",
			[]string{"uploadpack.hideRefs", "refs/heads"}, false, false},
		{"catches: a slot transfer.hideRefs=refs/ that hides every ref from the collect",
			[]string{"transfer.hideRefs", "refs/"}, false, false},
		{"catches: a slot transfer.hideRefs=HEAD that hides the work at a detached HEAD from the collect",
			[]string{"transfer.hideRefs", "refs/", "transfer.hideRefs", "HEAD"}, true, false},
		{"catches: a shrink that removes a slot whose hideRefs hid the commit from its collect",
			[]string{"transfer.hideRefs", "refs/"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			h := fx.holder(slotTestSession, 5941)
			a := fx.lease(t, p, h, fx.base)
			work := slotTestWork(t, a)
			if tc.detach {
				engineTestGit(t, a.Dir, "switch", "-q", "--detach")
				engineTestGit(t, a.Dir, "branch", "-q", "-D", slotTestRunBranch)
			} else {
				engineTestGit(t, a.Dir, "switch", "-q", "--detach", fx.base)
			}
			for i := 0; i+1 < len(tc.hide); i += 2 {
				engineTestGit(t, a.Dir, "config", "--add", tc.hide[i], tc.hide[i+1])
			}
			fx.procs.kill(h.Process.PID) // the holder dies before its collect
			err := slotTestCollectDead(t, fx, p, tc.shrink)
			collected, kept := slotTestCollected(t, fx, work), slotTestExists(t, a.Dir)
			switch {
			case !collected && !kept:
				t.Errorf("commit %s hidden by the slot's hideRefs is in no run ref, and its slot was removed (DR-11); err %v", work[:12], err)
			case !collected:
				t.Errorf("commit %s hidden by the slot's hideRefs is in no run ref of the canonical repository (DR-11); err %v", work[:12], err)
			case err != nil:
				t.Errorf("the collect of a slot with hideRefs set: %v; want the commit collected and no error", err)
			}
		})
	}
}

// slotTestCollectDead runs what collects a dead holder's slot of pool p: the next lease, or the sweep of
// its lease and a shrink over the cache ceiling. It returns the error of the lease or the shrink.
func slotTestCollectDead(t *testing.T, fx *slotTestFixture, p *SlotPool, shrink bool) error {
	t.Helper()
	if !shrink {
		_, err := slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5942), fx.base)
		return err
	}
	if reaped, err := p.Sweep(t.Context()); err != nil || reaped != 1 {
		t.Fatalf("Sweep = %d, %v; want the dead holder's lease reaped", reaped, err)
	}
	_, err := p.Shrink(t.Context(), slotTestOverCache())
	return err
}
