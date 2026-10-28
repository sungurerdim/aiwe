package core

import (
	"context"
	"errors"
)

// FileLock is an exclusive lock the operating system holds on one file: LockFileEx on Windows,
// flock on Unix. The OS frees it when its holder dies. Stub: #591 implements it.
type FileLock struct{}

// AcquireFileLock opens path, creating it when absent, and blocks in the OS call until the lock
// is free; it never polls. When ctx ends first it returns an error that wraps ctx.Err() and holds
// no lock. Stub: #591 implements it.
func AcquireFileLock(ctx context.Context, path string) (*FileLock, error) {
	return nil, errors.New("not implemented")
}

// Release frees the lock and closes the file; the file stays in place. Stub: #591 implements it.
func (l *FileLock) Release() error {
	return errors.New("not implemented")
}
