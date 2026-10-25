package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The claim table of one project (SPEC §7.3 step 2): one file per unit under the project's own
// directory. It records the run that took the unit and the process that holds it, so a second claim
// of the same unit is refused with the holder named, and the claim of a holder that died is
// reclaimed (DR-11). The remote claim across machines is [us-10], not this table.
//
// This file holds the decisions. The file system and the process table live in lock_io.go, which
// SPEC §5.6 keeps them in: every read and write goes through an *os.Root there, so no link inside
// the project's directory leaves the claim table, and the environment is read through core.
const (
	// claimVersion is the format version of a claim file; any other value is refused (SPEC §8).
	claimVersion = 1
	// claimFilePrefix and claimFileSuffix make the file name of the claim of one unit.
	claimFilePrefix = "claim-"
	claimFileSuffix = ".json"
	// claimTicketInfix and claimTicketHashBytes make the file name of the ticket of one claim: the
	// claim file name, the infix and the digest of the claim's own bytes. Two runs that read one dead
	// claim name one ticket, and the file system links a name to an existing file once, so the ticket
	// keeps a reclaim exclusive (DR-11).
	claimTicketInfix     = ".dead-"
	claimTicketHashBytes = 16
	// maxClaimBytes caps a claim file; a larger one, or one that is no regular file, is refused
	// unread (#199).
	maxClaimBytes = 4096
	// claimAttempts bounds the read, decide and write rounds of one take.
	claimAttempts = 3
)

// Claim is one open run of one unit: the unit, its run id and the process that holds the claim.
type Claim struct {
	Unit  int
	RunID string
	Owner core.ProcessID
}

// claimRecord is the claim file of one unit (SPEC §8: a format version and camelCase keys).
type claimRecord struct {
	FormatVersion int       `json:"formatVersion"`
	Unit          int       `json:"unit"`
	RunID         string    `json:"runId"`
	PID           int       `json:"pid"`
	Start         time.Time `json:"start"`
}

// UnitClaimedError reports a unit that already has an open run; Holder names the run that holds it.
type UnitClaimedError struct {
	Holder Claim
}

// Error names the unit, the run that holds it and the process behind that run.
func (e *UnitClaimedError) Error() string {
	return fmt.Sprintf("unit %d is already claimed by run %s of process %d",
		e.Holder.Unit, e.Holder.RunID, e.Holder.Owner.PID)
}

// Claims is the local claim table of one project: one open run per unit (SPEC §6.6, §7.3 step 2).
type Claims struct {
	dir string
	// probe answers whether the process of a claim still runs. OpenClaims sets the probe that asks
	// the OS; a test injects one, so the answer an OS gives for a process the run may not inspect is
	// decided without such a process (lock_io.go holds the OS probe).
	probe processProbe
}

// OpenClaims returns the claim table stored under dir, the project's own directory; dir has to be
// an absolute path of an existing directory. Every later read and write goes through an os.Root at
// dir, so no link inside the project's directory leaves the claim table.
func OpenClaims(dir string) (*Claims, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("claim table: directory %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if err := checkClaimDir(dir); err != nil {
		return nil, fmt.Errorf("claim table: %w", err)
	}
	return &Claims{dir: dir, probe: probeProcess}, nil
}

// Take claims claim.Unit for claim.RunID, or returns a *UnitClaimedError naming the run that holds
// it. The claim of a holder whose process no longer runs is reclaimed. The claim file is created
// exclusively, and a reclaim removes the dead claim only while it holds that claim's own ticket, the
// link only one run can make for one claim, so of two claims of one unit at most one is taken
// (SPEC §7.3 step 2).
func (c *Claims) Take(ctx context.Context, claim Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkClaim(claim); err != nil {
		return err
	}
	for attempt := 0; attempt < claimAttempts; attempt++ {
		done, err := c.takeOnce(ctx, claim)
		if done || err != nil {
			return err
		}
	}
	return fmt.Errorf("claim of unit %d: the claim table changed under every attempt", claim.Unit)
}

// takeOnce runs one round of Take: it reports done once the claim is taken or refused, and done
// false when another run changed the claim of the unit while this round read it.
func (c *Claims) takeOnce(ctx context.Context, claim Claim) (bool, error) {
	holder, raw, err := c.held(ctx, claim.Unit)
	switch {
	case err == nil && c.holderAlive(holder.Owner):
		return true, &UnitClaimedError{Holder: holder}
	case err == nil:
		// The holder is gone: the unit is free again (DR-11). The dead claim goes only while this run
		// holds that claim's own ticket, the link only one run can make for one claim, and only while
		// the ticket still names the claim that was read as dead (lock_io.go), so a reclaim that read a
		// stale table never drops the live claim another run took in the meantime. A file that changed
		// under the reclaim sends this round back to the table as it is now.
		removed, rerr := removeClaimFileIf(c.dir, claimName(claim.Unit), raw)
		if rerr != nil {
			return true, fmt.Errorf("claim of unit %d: %w", claim.Unit, rerr)
		}
		if !removed {
			return false, nil
		}
	case !errors.Is(err, fs.ErrNotExist):
		return true, err
	}
	err = c.create(ctx, claim)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	return true, err
}

// Holder returns the claim recorded for unit and whether there is one; it never writes and it never
// judges the holder, so a caller sees exactly what the table holds.
func (c *Claims) Holder(ctx context.Context, unit int) (Claim, bool, error) {
	holder, _, err := c.held(ctx, unit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Claim{}, false, nil
	case err != nil:
		return Claim{}, false, err
	}
	return holder, true, nil
}

// Release drops the claim of unit when runID holds it; the claim of another run is never released,
// and a unit with no claim is already free. The claim goes only while the file still holds the claim
// that was read: a claim another run took in the meantime is never dropped by this run (DR-11).
func (c *Claims) Release(ctx context.Context, unit int, runID string) error {
	holder, raw, err := c.held(ctx, unit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case holder.RunID != runID:
		return fmt.Errorf("release unit %d: the holder is run %s, not %s", unit, holder.RunID, runID)
	}
	removed, err := removeClaimFileIf(c.dir, claimName(unit), raw)
	if err != nil {
		return fmt.Errorf("claim of unit %d: %w", unit, err)
	}
	if !removed {
		return fmt.Errorf("release unit %d: the claim of run %s changed under the release", unit, runID)
	}
	return nil
}

// held returns the claim recorded for unit with the bytes of its claim file, or fs.ErrNotExist when
// the unit is free. A file that is no claim of that unit is refused, never read as no claim (DR-6),
// and the bytes come back with the claim, so a caller removes only the claim it read.
func (c *Claims) held(ctx context.Context, unit int) (Claim, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Claim{}, nil, err
	}
	if unit <= 0 {
		return Claim{}, nil, fmt.Errorf("claim: unit %d: want a positive unit number", unit)
	}
	name := claimName(unit)
	b, err := readClaimFile(c.dir, name, maxClaimBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Claim{}, nil, fmt.Errorf("claim of unit %d: %w", unit, fs.ErrNotExist)
	}
	if err != nil {
		return Claim{}, nil, fmt.Errorf("claim of unit %d: %s: %w", unit, name, err)
	}
	claim, err := parseClaim(unit, b)
	if err != nil {
		return Claim{}, nil, err
	}
	return claim, b, nil
}

// parseClaim reads the bytes of the claim file of unit; a file of another shape is an error.
func parseClaim(unit int, b []byte) (Claim, error) {
	var rec claimRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return Claim{}, fmt.Errorf("claim of unit %d: %w", unit, err)
	}
	if rec.FormatVersion != claimVersion {
		return Claim{}, fmt.Errorf("claim of unit %d: format version %d: want %d",
			unit, rec.FormatVersion, claimVersion)
	}
	if rec.Unit != unit || rec.PID <= 0 || !plainName(rec.RunID) {
		return Claim{}, fmt.Errorf("claim of unit %d: %+v: want the claim of the unit's own run", unit, rec)
	}
	return Claim{
		Unit:  rec.Unit,
		RunID: rec.RunID,
		Owner: core.ProcessID{PID: rec.PID, Start: rec.Start},
	}, nil
}

// create writes the claim of claim.Unit exclusively: an existing claim file, which another run
// holds, fails with fs.ErrExist and stays untouched.
func (c *Claims) create(ctx context.Context, claim Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := json.Marshal(claimRecord{
		FormatVersion: claimVersion,
		Unit:          claim.Unit,
		RunID:         claim.RunID,
		PID:           claim.Owner.PID,
		Start:         claim.Owner.Start.UTC(),
	})
	if err != nil {
		return fmt.Errorf("claim of unit %d: %w", claim.Unit, err)
	}
	name := claimName(claim.Unit)
	err = createClaimFile(c.dir, name, append(b, '\n'))
	if err != nil {
		// The failed write is taken back inside createClaimFile: a half-written claim, which would
		// be refused by every later read, never stays.
		return fmt.Errorf("claim of unit %d: %s: %w", claim.Unit, name, err)
	}
	return nil
}

// processState is what a probe learned about the process behind one claim.
type processState int

const (
	// processUnknown is a probe that learned nothing about the id: no proof that it is gone. The
	// claim stays (fail-closed, DR-11). probeProcess folds it into processAlive, which keeps the
	// claim the same way.
	processUnknown processState = iota
	// processAlive is an id core.OwnerAlive does not prove gone: a running process with the
	// recorded start, or one whose start the OS would not give (fail-closed).
	processAlive
	// processGone is an id the OS confirms is no longer there.
	processGone
)

// processProbe asks the OS about the process that holds a claim. It is a field of Claims, so the
// answer an OS gives for a live process the run may not inspect is decided without such a process;
// lock_io.go holds the one implementation that touches the process table.
type processProbe func(core.ProcessID) processState

// holderAlive reports whether the process that holds a claim still runs. Only a probe that confirms
// the process is gone frees its unit: an id the OS refuses to inspect, which is how it answers for a
// live process of another user, keeps the claim, and every other answer that is no proof of death
// keeps it too (fail-closed, DR-11); lock_io.go holds which answers those are. A pid another process
// now runs is judged by the start the claim recorded (core.OwnerAlive): where the OS start moves with
// the clock (Linux) a running pid keeps its claim until an owner releases it (DR-11).
func (c *Claims) holderAlive(owner core.ProcessID) bool {
	if owner.PID <= 0 {
		return false
	}
	if c.probe == nil {
		return true // no probe answered: nothing proved the holder is gone (fail-closed)
	}
	return c.probe(owner) != processGone
}

// checkClaim refuses a claim that names no unit, no plain run id or no holder process.
func checkClaim(claim Claim) error {
	if claim.Unit <= 0 {
		return fmt.Errorf("claim: unit %d: want a positive unit number", claim.Unit)
	}
	if !plainName(claim.RunID) {
		return fmt.Errorf("claim of unit %d: run id %q: want 1 to %d letters, digits, '.', '_' or '-'",
			claim.Unit, claim.RunID, maxNameBytes)
	}
	if claim.Owner.PID <= 0 {
		return fmt.Errorf("claim of unit %d: process %d: want a process id", claim.Unit, claim.Owner.PID)
	}
	return nil
}

// claimName returns the name of the claim file of unit under the directory of the claim table.
func claimName(unit int) string {
	return claimFilePrefix + strconv.Itoa(unit) + claimFileSuffix
}
