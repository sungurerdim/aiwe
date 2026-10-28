package core

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// lockShare lets every holder open the lock file while another holds the lock, and lets the file
// be removed; the lock, not the share mode, decides who holds it.
const lockShare = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// lockHandle is the open lock file and the overlapped record its lock calls share. The record is
// a pointer, so it stays on the heap at one address: the kernel writes it after LockFileEx
// returns ERROR_IO_PENDING, while the calling goroutine may grow its stack.
type lockHandle struct {
	h  windows.Handle
	ol *windows.Overlapped
}

// lockFile opens path for overlapped IO, creating it when absent, and waits for an exclusive
// LockFileEx lock on its first byte. The handle is not inherited, so a child of the holder never
// keeps the lock alive.
func lockFile(ctx context.Context, path string) (lockHandle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return lockHandle{}, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, lockShare, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return lockHandle{}, fmt.Errorf("open: %w", err)
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil) // manual reset, as overlapped IO requires
	if err != nil {
		return lockHandle{}, errors.Join(fmt.Errorf("create the wait event: %w", err), windows.CloseHandle(h))
	}
	l := lockHandle{h: h, ol: &windows.Overlapped{HEvent: ev}}
	if err := l.wait(ctx); err != nil {
		return lockHandle{}, errors.Join(err, l.close())
	}
	return l, nil
}

// wait asks for the lock and blocks in GetOverlappedResult until the OS grants it. A goroutine
// cancels the request with CancelIoEx when ctx ends, which ends the wait with
// ERROR_OPERATION_ABORTED; wait returns only after that goroutine has ended, so nothing touches
// the handle afterwards. A lock granted before the cancel lands is kept and returned.
func (l lockHandle) wait(ctx context.Context) error {
	err := windows.LockFileEx(l.h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, l.ol)
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return err // granted at once, or refused
	}
	stop := make(chan struct{})
	cancelled := make(chan error, 1)
	go func() { cancelled <- l.cancelOnDone(ctx, stop) }()
	err = l.result()
	close(stop)
	if cancelErr := <-cancelled; cancelErr != nil {
		return errors.Join(err, fmt.Errorf("cancel the wait: %w", cancelErr), l.unlockIfHeld(err))
	}
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) && ctx.Err() != nil {
		return fmt.Errorf("wait for the lock: %w", ctx.Err())
	}
	return err
}

// cancelOnDone cancels the pending lock request when ctx ends before stop closes. ERROR_NOT_FOUND
// means the request already completed, so there is nothing to cancel.
func (l lockHandle) cancelOnDone(ctx context.Context, stop <-chan struct{}) error {
	select {
	case <-stop:
		return nil
	case <-ctx.Done():
	}
	err := windows.CancelIoEx(l.h, l.ol)
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return nil
	}
	return err
}

// unlockIfHeld frees a lock the wait got (waitErr nil) when wait fails anyway.
func (l lockHandle) unlockIfHeld(waitErr error) error {
	if waitErr != nil {
		return nil
	}
	return l.unlock()
}

// result blocks until the overlapped request on l ends and returns its outcome.
func (l lockHandle) result() error {
	var n uint32
	return windows.GetOverlappedResult(l.h, l.ol, &n, true)
}

// unlock frees the lock with UnlockFileEx, reusing the overlapped record with a reset event.
func (l lockHandle) unlock() error {
	if err := windows.ResetEvent(l.ol.HEvent); err != nil {
		return fmt.Errorf("reset the wait event: %w", err)
	}
	*l.ol = windows.Overlapped{HEvent: l.ol.HEvent}
	err := windows.UnlockFileEx(l.h, 0, 1, 0, l.ol)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = l.result()
	}
	return err
}

// close closes the file and the event; closing the file would free a lock still held.
func (l lockHandle) close() error {
	return errors.Join(windows.CloseHandle(l.h), windows.CloseHandle(l.ol.HEvent))
}

// release frees the lock explicitly, so a waiter wakes at once, then closes the handles.
func (l lockHandle) release() error {
	return errors.Join(l.unlock(), l.close())
}
