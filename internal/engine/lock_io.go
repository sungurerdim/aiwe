package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The OS calls of the local claim table (SPEC §5.6): the claim files of lock.go through an *os.Root,
// the ticket that makes the removal of one claim exclusive, and the one process probe the table
// judges a holder with. Nothing here decides; lock.go holds the decisions, and this file answers
// with what the file system and the process table say.

// checkClaimDir reports whether dir is a directory that can serve as a claim table, by opening it as
// an *os.Root: the handle every later read and write of the table goes through.
func checkClaimDir(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	return root.Close()
}

// readClaimFile reads the claim file name of the claim table under dir through an *os.Root, so no
// link inside dir leaves the table; the read is capped at maxBytes, and a file that is no regular
// file of at most that size is refused unread (#199).
func readClaimFile(dir, name string, maxBytes int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return core.ReadRegularCapped(root, name, maxBytes)
}

// createClaimFile writes b to the claim file name of the claim table under dir, exclusively, through
// an *os.Root: an existing file, which another run holds, fails with fs.ErrExist and stays
// untouched. A write that fails is taken back: a half-written claim would be refused by every later
// read, so none ever stays. The exclusive create holds the name while this call owns it, so the
// take-back removes the half-written file and no claim of another run.
func createClaimFile(dir, name string, b []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		return cerr
	}
	return errors.Join(err, root.Remove(name))
}

// claimTicketName returns the name of the ticket of one claim of the claim table: the name of its
// claim file, the claimTicketInfix and the digest of the claim's bytes. Two runs that read one dead
// claim name one ticket, and the link of a ticket is exclusive, so at most one of them can remove
// that claim (DR-11).
func claimTicketName(name string, want []byte) string {
	sum := sha256.Sum256(want)
	return name + claimTicketInfix + hex.EncodeToString(sum[:claimTicketHashBytes])
}

// removeClaimFileIf removes the claim file name of the claim table under dir while it still holds
// want, and reports whether it removed it. The file is read back through the same *os.Root before
// the removal, so a reclaim that read a claim the table no longer holds, or the release of one run,
// never drops the claim another run took in the meantime (DR-11). The read back and the removal are
// two calls, and the table can change between them, so the removal runs while this call holds the
// ticket of that very claim and only while the ticket and the claim file name still name one file
// holding want. A claim file that changed, or that is already gone, is left alone and the caller
// starts its round over.
func removeClaimFileIf(dir, name string, want []byte) (bool, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	ticket := claimTicketName(name, want)
	held, err := holdClaimTicket(root, name, ticket, want)
	if err != nil {
		return false, err
	}
	if !held {
		return false, nil
	}
	err = root.Remove(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, errors.Join(fmt.Errorf("remove the claim file %s: %w", name, err),
			dropClaimTicket(root, ticket))
	}
	return true, dropClaimTicket(root, ticket)
}

// holdClaimTicket links the claim file name to ticket and reports whether this call holds it for the
// claim the caller read: the link fails while another run holds the ticket of the same claim, and a
// claim the table took after the read leaves a ticket that does not name the claim that was read, so
// this call drops the ticket again and removes nothing. Only the run that made a ticket removes it: a
// ticket a run left behind when it died in this window keeps its claim, the fail-closed answer DR-11
// gives every doubt.
func holdClaimTicket(root *os.Root, name, ticket string, want []byte) (bool, error) {
	if err := root.Link(name, ticket); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	held, err := ticketHoldsClaim(root, name, ticket, want)
	if err == nil && held {
		return true, nil
	}
	// The ticket does not name the claim that was read, or the table could not be read: this call
	// removes nothing, and the ticket it made goes again so that a later reclaim starts over.
	return false, errors.Join(err, dropClaimTicket(root, ticket))
}

// ticketHoldsClaim reports whether ticket and the claim file name name one and the same file and
// that file holds want, the claim the caller read as dead. A ticket that names another file, or a
// claim file the table replaced while the ticket was made, is not that claim (DR-11), so a removal
// that follows never drops a claim of another run.
func ticketHoldsClaim(root *os.Root, name, ticket string, want []byte) (bool, error) {
	b, err := core.ReadRegularCapped(root, ticket, maxClaimBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(b, want) {
		return false, nil
	}
	claim, err := root.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ticketFile, err := root.Stat(ticket)
	if err != nil {
		return false, err
	}
	return os.SameFile(claim, ticketFile), nil
}

// dropClaimTicket removes a ticket this call holds; a ticket that is already gone is no error.
func dropClaimTicket(root *os.Root, ticket string) error {
	err := root.Remove(ticket)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove the claim ticket %s: %w", ticket, err)
}

// probeProcess asks the OS whether the process with the id owner still runs, through
// core.OwnerAlive: the pid must run and its start must match the one the claim recorded. A process
// that exited is gone even while an open handle keeps the OS naming its pid: on Windows its exit
// code decides, never whether the pid opens, since Windows supports no signal 0 and the object of
// an exited process lives on while any handle (a scanner's, a parent's) is open (#380). A pid another
// process now runs answers gone once its start differs. A start the OS refuses to give counts as
// alive, which is how it answers for a live process of another user (fail-closed, DR-11).
func probeProcess(owner core.ProcessID) processState {
	if core.OwnerAlive(owner) {
		return processAlive
	}
	return processGone
}
