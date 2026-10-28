package core

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestFileLockReleaseTwice checks that a second Release of one lock returns errFileLockReleased
// and does nothing: on Unix a second close could close a descriptor number already reused.
func TestFileLockReleaseTwice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		catches string
	}{
		{
			name:    "a second release",
			catches: "a second Release closes the handle again or reports success",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("catches: %s", tc.catches)
			lock, err := AcquireFileLock(t.Context(), filepath.Join(t.TempDir(), "queue.lock"))
			if err != nil {
				t.Fatalf("AcquireFileLock = %v, want a lock on a free file", err)
			}
			if err := lock.Release(); err != nil {
				t.Fatalf("first Release() = %v, want nil", err)
			}
			if err := lock.Release(); !errors.Is(err, errFileLockReleased) {
				t.Fatalf("second Release() = %v, want an error wrapping %v", err, errFileLockReleased)
			}
		})
	}
}
