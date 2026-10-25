package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestClaims guards DR-11 and SPEC §7.3 step 2: one open run per unit through a local lock. It
// catches a second run on one unit and a lock a dead holder keeps.
func TestClaims(t *testing.T) {
	ctx := t.Context()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	dir := t.TempDir()
	claims, err := OpenClaims(dir)
	if err != nil {
		t.Fatalf("OpenClaims: %v", err)
	}

	const unit = 35
	if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestRunID, Owner: self}); err != nil {
		t.Fatalf("Take the first claim of unit %d: %v", unit, err)
	}

	t.Run("a second claim of the same unit is refused with the holder named", func(t *testing.T) {
		// Catches a second run that starts while the first one is still open.
		err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestSecondRunID, Owner: self})
		if err == nil {
			t.Fatalf("a second claim of unit %d = nil error, want a refusal", unit)
		}
		var held *UnitClaimedError
		if !errors.As(err, &held) {
			t.Fatalf("a second claim of unit %d = %v, want a *UnitClaimedError", unit, err)
		}
		if held.Holder.RunID != engineTestRunID {
			t.Errorf("the refusal names the holder %q, want %q", held.Holder.RunID, engineTestRunID)
		}
		if held.Holder.Unit != unit {
			t.Errorf("the refusal names unit %d, want %d", held.Holder.Unit, unit)
		}
		if held.Holder.Owner.PID != self.PID {
			t.Errorf("the refusal names the holder pid %d, want %d", held.Holder.Owner.PID, self.PID)
		}
	})

	t.Run("another unit is free", func(t *testing.T) {
		// Catches a lock that blocks every unit of the project.
		if err := claims.Take(ctx, Claim{Unit: 36, RunID: engineTestSecondRunID, Owner: self}); err != nil {
			t.Fatalf("Take unit 36 while unit %d is claimed: %v", unit, err)
		}
	})

	t.Run("a second claim table sees the holder", func(t *testing.T) {
		// Catches a lock that lives in memory only, so another process claims the same unit.
		reopened, err := OpenClaims(dir)
		if err != nil {
			t.Fatalf("OpenClaims: %v", err)
		}
		holder, ok, err := reopened.Holder(ctx, unit)
		if err != nil {
			t.Fatalf("Holder(%d): %v", unit, err)
		}
		if !ok || holder.RunID != engineTestRunID || holder.Unit != unit {
			t.Errorf("Holder(%d) = %+v, %v; want the claim of %q", unit, holder, ok, engineTestRunID)
		}
		if err := reopened.Take(ctx, Claim{Unit: unit, RunID: engineTestSecondRunID, Owner: self}); err == nil {
			t.Errorf("a claim taken from a second claim table = nil error, want a refusal")
		}
	})

	t.Run("a released claim is free again", func(t *testing.T) {
		// Catches a lock a finished run keeps, so the unit is never claimed again.
		if err := claims.Release(ctx, unit, engineTestRunID); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if _, ok, err := claims.Holder(ctx, unit); err != nil || ok {
			t.Errorf("Holder(%d) after Release = %v, %v; want no holder", unit, ok, err)
		}
		if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestSecondRunID, Owner: self}); err != nil {
			t.Fatalf("Take after Release: %v", err)
		}
	})

	t.Run("a dead holder's lock is reclaimed", func(t *testing.T) {
		// Catches a lock left by a run that died, which would block the unit forever.
		ctx := t.Context()
		const deadUnit = 37
		if err := claims.Take(ctx, Claim{Unit: deadUnit, RunID: engineTestDeadRunID, Owner: engineTestDeadProcess(t)}); err != nil {
			t.Fatalf("Take unit %d for a dead holder: %v", deadUnit, err)
		}
		if err := claims.Take(ctx, Claim{Unit: deadUnit, RunID: engineTestRunID, Owner: self}); err != nil {
			t.Fatalf("Take unit %d while its holder is dead: %v", deadUnit, err)
		}
		holder, ok, err := claims.Holder(ctx, deadUnit)
		if err != nil {
			t.Fatalf("Holder(%d): %v", deadUnit, err)
		}
		if !ok || holder.RunID != engineTestRunID {
			t.Errorf("Holder(%d) = %+v, %v; want the live claim %q", deadUnit, holder, ok, engineTestRunID)
		}
	})
}

// TestClaimsConcurrent guards DR-11 and SPEC §7.3 step 2: of many runs that claim one unit at the
// same time, exactly one takes it and the table keeps the claim of that run. It catches a claim that
// is decided on a read alone, which would let two runs hold one unit.
func TestClaimsConcurrent(t *testing.T) {
	ctx := t.Context()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	const unit = 44
	claims, err := OpenClaims(t.TempDir())
	if err != nil {
		t.Fatalf("OpenClaims: %v", err)
	}

	const runs = 8
	ids := make([]string, runs)
	for i := range ids {
		ids[i] = engineTestRunID + strconv.Itoa(i)
	}
	errs := make([]error, runs)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = claims.Take(ctx, Claim{Unit: unit, RunID: ids[i], Owner: self})
		}()
	}
	close(start)
	wg.Wait()

	taken := make([]string, 0, runs)
	for i, err := range errs {
		if err == nil {
			taken = append(taken, ids[i])
		}
	}
	if len(taken) != 1 {
		t.Fatalf("claims of unit %d taken at once = %v, want exactly one taken", unit, taken)
	}
	holder, ok, err := claims.Holder(ctx, unit)
	if err != nil {
		t.Fatalf("Holder(%d): %v", unit, err)
	}
	if !ok || holder.RunID != taken[0] {
		t.Errorf("Holder(%d) = %+v, %v; want the claim of the run that took the unit %q",
			unit, holder, ok, taken[0])
	}
}

// TestClaimReclaimConcurrent guards DR-11 and SPEC §7.3 step 2: of many runs that reclaim one unit
// whose holder died at the same time, exactly one takes it, and the table keeps that one claim and
// no ticket. It catches a reclaim that removes the dead claim by name without holding the claim's
// ticket, which would let two runs remove one claim and both take the unit, and a ticket left
// behind, which would block the unit for good.
func TestClaimReclaimConcurrent(t *testing.T) {
	ctx := t.Context()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	const unit = 45
	dir := t.TempDir()
	claims, err := OpenClaims(dir)
	if err != nil {
		t.Fatalf("OpenClaims: %v", err)
	}
	if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestDeadRunID,
		Owner: engineTestDeadProcess(t)}); err != nil {
		t.Fatalf("Take unit %d for a dead holder: %v", unit, err)
	}

	const runs = 8
	ids := make([]string, runs)
	for i := range ids {
		ids[i] = engineTestRunID + strconv.Itoa(i)
	}
	errs := make([]error, runs)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = claims.Take(ctx, Claim{Unit: unit, RunID: ids[i], Owner: self})
		}()
	}
	close(start)
	wg.Wait()

	taken := make([]string, 0, runs)
	for i, err := range errs {
		if err == nil {
			taken = append(taken, ids[i])
		}
	}
	if len(taken) != 1 {
		t.Fatalf("reclaims of unit %d taken at once = %v, want exactly one taken", unit, taken)
	}
	holder, ok, err := claims.Holder(ctx, unit)
	if err != nil {
		t.Fatalf("Holder(%d): %v", unit, err)
	}
	if !ok || holder.RunID != taken[0] {
		t.Errorf("Holder(%d) = %+v, %v; want the claim of the run that took the unit %q",
			unit, holder, ok, taken[0])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the claim table %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{claimName(unit)}; !slices.Equal(names, want) {
		t.Errorf("the claim table holds %v, want only the claim of unit %d", names, unit)
	}
}

// TestClaimReclaimRemovesOnlyTheClaimItRead guards DR-11 and SPEC §7.3 step 2: a reclaim removes the
// claim it read as dead, and never the claim another run took in the meantime. It catches a reclaim
// that removes the claim file by name, which drops the live claim of the run that reclaimed the unit
// first and puts two runs on one unit.
func TestClaimReclaimRemovesOnlyTheClaimItRead(t *testing.T) {
	ctx := t.Context()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	const unit = 38
	dir := t.TempDir()
	claims, err := OpenClaims(dir)
	if err != nil {
		t.Fatalf("OpenClaims: %v", err)
	}
	if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestDeadRunID,
		Owner: engineTestDeadProcess(t)}); err != nil {
		t.Fatalf("Take unit %d for a dead holder: %v", unit, err)
	}
	// The claim a run read before another run reclaimed the unit.
	stale, err := readClaimFile(dir, claimName(unit), maxClaimBytes)
	if err != nil {
		t.Fatalf("read the claim of unit %d: %v", unit, err)
	}
	if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestRunID, Owner: self}); err != nil {
		t.Fatalf("Take unit %d while its holder is dead: %v", unit, err)
	}

	t.Run("an outdated reclaim leaves the live claim alone", func(t *testing.T) {
		// Catches a reclaim that removes the claim file by name, whatever it holds by then.
		removed, err := removeClaimFileIf(dir, claimName(unit), stale)
		if err != nil {
			t.Fatalf("remove the outdated claim of unit %d: %v", unit, err)
		}
		if removed {
			t.Errorf("a reclaim of an outdated claim removed the live claim of unit %d", unit)
		}
		holder, ok, err := claims.Holder(ctx, unit)
		if err != nil {
			t.Fatalf("Holder(%d): %v", unit, err)
		}
		if !ok || holder.RunID != engineTestRunID {
			t.Fatalf("Holder(%d) = %+v, %v; want the live claim %q", unit, holder, ok, engineTestRunID)
		}

		// The run that read the outdated table is refused, naming the live holder: the unit never
		// reaches two open runs.
		err = claims.Take(ctx, Claim{Unit: unit, RunID: engineTestSecondRunID, Owner: self})
		var held *UnitClaimedError
		if !errors.As(err, &held) {
			t.Fatalf("Take unit %d while a live claim holds it = %v, want a refusal", unit, err)
		}
		if held.Holder.RunID != engineTestRunID {
			t.Errorf("the refusal names the holder %q, want %q", held.Holder.RunID, engineTestRunID)
		}
	})
}

// engineTestProbe answers one state for every process, the way an OS that cannot inspect a live
// holder answers: no probe touches a process, so the answer is the whole point of the test.
func engineTestProbe(state processState) processProbe {
	return func(core.ProcessID) processState { return state }
}

// TestClaimHolderUninspected guards DR-11 and SPEC §7.3 step 2: a holder the run cannot inspect
// keeps its unit, and a holder the OS confirms is gone frees it. It catches a lock reclaimed on an
// answer that is no proof the holder is gone, which would put two runs on one unit.
func TestClaimHolderUninspected(t *testing.T) {
	ctx := t.Context()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	const unit = 43
	dir := t.TempDir()
	claims, err := OpenClaims(dir)
	if err != nil {
		t.Fatalf("OpenClaims: %v", err)
	}
	if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestRunID, Owner: self}); err != nil {
		t.Fatalf("Take the first claim of unit %d: %v", unit, err)
	}

	t.Run("a holder the OS refuses to inspect keeps its unit", func(t *testing.T) {
		// Catches a second run that takes the unit because the probe could not inspect the holder,
		// which an OS answers for a live process of another user (access denied).
		claims.probe = engineTestProbe(processUnknown)
		err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestSecondRunID, Owner: self})
		var held *UnitClaimedError
		if !errors.As(err, &held) {
			t.Fatalf("a claim while the holder cannot be inspected = %v, want a refusal", err)
		}
		if held.Holder.RunID != engineTestRunID {
			t.Errorf("the refusal names the holder %q, want %q", held.Holder.RunID, engineTestRunID)
		}
	})

	t.Run("a holder the OS confirms is gone frees its unit", func(t *testing.T) {
		// Catches a probe whose answer is never read, which would block the unit for good.
		claims.probe = engineTestProbe(processGone)
		if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestSecondRunID, Owner: self}); err != nil {
			t.Fatalf("Take while the holder is confirmed gone: %v", err)
		}
		holder, ok, err := claims.Holder(ctx, unit)
		if err != nil {
			t.Fatalf("Holder(%d): %v", unit, err)
		}
		if !ok || holder.RunID != engineTestSecondRunID {
			t.Errorf("Holder(%d) = %+v, %v; want the new claim %q", unit, holder, ok, engineTestSecondRunID)
		}
	})
}

// TestClaimFileRefused guards DR-6 and #199: a claim file that is no claim of its unit is refused,
// never read as a free unit, so a damaged table cannot hand one unit to two runs.
func TestClaimFileRefused(t *testing.T) {
	ctx := t.Context()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	const unit = 41
	dir := t.TempDir()
	engineTestWrite(t, filepath.Join(dir, claimName(unit)), "{\n")

	claims, err := OpenClaims(dir)
	if err != nil {
		t.Fatalf("OpenClaims: %v", err)
	}
	if _, _, err := claims.Holder(ctx, unit); err == nil {
		t.Errorf("Holder(%d) of a claim file that is no claim = nil error, want a refusal", unit)
	}
	if err := claims.Take(ctx, Claim{Unit: unit, RunID: engineTestRunID, Owner: self}); err == nil {
		t.Errorf("Take of unit %d while its claim file is no claim = nil error, want a refusal", unit)
	}
}
