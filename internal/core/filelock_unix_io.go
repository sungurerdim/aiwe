//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package core

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// lockHandle is the descriptor that holds the flock lock.
type lockHandle struct {
	fd int
}

// lockFile opens path, creating it when absent, and waits for flock(LOCK_EX) on it. The
// descriptor is close-on-exec, so a child of the holder never keeps the lock alive.
//
// A blocked flock cannot be interrupted: Go installs its signal handlers with SA_RESTART and a
// close of the descriptor does not wake it. So the call runs in a goroutine that owns the
// descriptor until it hands it over. When ctx ends first, lockFile returns at once and the
// goroutine, once flock returns, closes the descriptor, which frees a lock taken after the
// cancel; until then it stays blocked in the OS, not polling. That goroutine has no waiting
// owner: SPEC §16 L78 records this exception to §5.18, and the lane (#596) bounds it to one OS
// waiter per resource per process.
func lockFile(ctx context.Context, path string) (lockHandle, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return lockHandle{}, fmt.Errorf("open: %w", err)
	}
	got := make(chan error) // unbuffered: a send succeeds only when lockFile takes the descriptor
	abandon := make(chan struct{})
	go flockOrClose(fd, got, abandon)
	select {
	case err := <-got:
		if err != nil {
			return lockHandle{}, errors.Join(fmt.Errorf("flock: %w", err), unix.Close(fd))
		}
		return lockHandle{fd: fd}, nil
	case <-ctx.Done():
		close(abandon)
		return lockHandle{}, fmt.Errorf("wait for the lock: %w", ctx.Err())
	}
}

// flockOrClose blocks in flock on fd and hands the outcome to the waiter; when the waiter has
// abandoned the wait, it closes fd instead, so no lock outlives the cancel.
func flockOrClose(fd int, got chan<- error, abandon <-chan struct{}) {
	err := flockExclusive(fd)
	select {
	case got <- err:
	case <-abandon:
		_ = unix.Close(fd) // nobody waits for the outcome; the close frees the lock all the same
	}
}

// flockExclusive blocks until fd holds an exclusive flock lock. It calls flock again only when a
// signal interrupted it (EINTR), so it never polls.
func flockExclusive(fd int) error {
	for {
		err := unix.Flock(fd, unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

// release unlocks fd, so a waiter wakes at once, then closes it.
func (l lockHandle) release() error {
	return errors.Join(unix.Flock(l.fd, unix.LOCK_UN), unix.Close(l.fd))
}
