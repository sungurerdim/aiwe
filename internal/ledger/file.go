package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sungurerdim/aiwe/internal/core"
)

// lockSuffix is added to the path of the ledger file to name its lock file. The lock sits on a file
// of its own: Windows locks the bytes of the file it locks against every other handle, so a lock on
// the ledger would stop the ledger's own reads and writes.
const lockSuffix = ".lock"

// ledgerFile is the ledger file as AppendFile uses it: opened for reading, so Append reads the last
// record back, and for appending, with the means to flush the line to disk.
type ledgerFile interface {
	io.Writer
	io.ReaderAt
	io.Seeker
	io.Closer
	Sync() error
}

// AppendFile appends rec to the ledger file at path, creating the file when absent, under the OS
// file lock of core (SPEC §6.6). The lock covers reading the head and writing the line, so writers in
// other goroutines and other processes each link to the record before them and lose none. It waits
// for the lock until ctx ends; then it returns an error that wraps ctx.Err() and writes nothing. The
// line is flushed to disk before AppendFile returns. A record Append refuses is not written.
func AppendFile(ctx context.Context, path string, rec Record) (err error) {
	lock, err := core.AcquireFileLock(ctx, path+lockSuffix)
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}
	defer func() { err = errors.Join(err, lock.Release()) }()
	f, err := openLedger(path)
	if err != nil {
		return fmt.Errorf("ledger open: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if err := Append(f, rec); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("ledger sync: %w", err)
	}
	return nil
}
