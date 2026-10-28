package core

import (
	"context"
	"errors"
	"fmt"
)

// The OS file lock (#591): this file holds the type and its contract; filelock_windows.go and
// filelock_unix.go make the OS calls through golang.org/x/sys and import no os (SPEC §5.6).

// errFileLockReleased is returned by a second Release of one lock.
var errFileLockReleased = errors.New("file lock already released")

// FileLock is an exclusive lock the operating system holds on one file: LockFileEx on Windows,
// flock on Unix. The OS frees it when its holder dies, because it closes the holder's handle, so
// no sweep is needed. A FileLock is used by one goroutine.
type FileLock struct {
	path     string
	handle   lockHandle
	released bool
}

// AcquireFileLock opens path, creating it when absent, and blocks in the OS call until the lock
// is free; it never polls. When ctx ends first it returns an error that wraps ctx.Err() and holds
// no lock.
func AcquireFileLock(ctx context.Context, path string) (*FileLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	h, err := lockFile(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &FileLock{path: path, handle: h}, nil
}

// Release frees the lock and closes the file; the file stays in place. A second call returns an
// error and does nothing.
func (l *FileLock) Release() error {
	if l.released {
		return fmt.Errorf("release %s: %w", l.path, errFileLockReleased)
	}
	l.released = true
	if err := l.handle.release(); err != nil {
		return fmt.Errorf("release %s: %w", l.path, err)
	}
	return nil
}
