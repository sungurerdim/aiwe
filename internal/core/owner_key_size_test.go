package core

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ownerKeyTestHex is a well-formed owner key: 64 hex characters, 32 bytes.
var ownerKeyTestHex = strings.Repeat("5a", ownerKeySize)

// ownerKeyTestState makes a state root with an owner-only key through OpenOwner and returns the
// state root and the key's path.
func ownerKeyTestState(t *testing.T) (string, string) {
	t.Helper()
	r := removeTestRoots(t, t.TempDir())
	removeTestOpen(t, r)
	return r.State, filepath.Join(r.State, ownerKeyFile)
}

// TestOwnerKeySize catches a key read whole whatever its size, or a key past 4096 bytes that loads
// because only its first bytes were read (#124, audit): the limit is on the bytes read, and the
// bytes past it are refused, not dropped.
func TestOwnerKeySize(t *testing.T) {
	for _, c := range []struct {
		name   string
		spaces int
		ok     bool
	}{
		{"64 hex and 4032 spaces, 4096 bytes, loads", maxOwnerKeyBytes - 2*ownerKeySize, true},
		{"64 hex and 4033 spaces, 4097 bytes, is refused", maxOwnerKeyBytes - 2*ownerKeySize + 1, false},
		{"64 hex and 8000 spaces is refused", 8000, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			state, key := ownerKeyTestState(t)
			if err := os.WriteFile(key, []byte(ownerKeyTestHex+strings.Repeat(" ", c.spaces)), 0o600); err != nil {
				t.Fatal(err)
			}
			k, err := loadOwnerKey(state)
			if c.ok && (err != nil || !bytes.Equal(k, bytes.Repeat([]byte{0x5a}, ownerKeySize))) {
				t.Errorf("loadOwnerKey of a %d-byte key = %x, %v; want the key 5a…5a", len(ownerKeyTestHex)+c.spaces, k, err)
			}
			if !c.ok && (err == nil || !strings.Contains(err.Error(), "hex-encoded bytes")) {
				t.Errorf("loadOwnerKey of a %d-byte key = %x, %v; want a refusal of its shape", len(ownerKeyTestHex)+c.spaces, k, err)
			}
		})
	}

	t.Run("a 16 MiB key is not read whole", func(t *testing.T) {
		state, key := ownerKeyTestState(t)
		if err := os.Truncate(key, 16<<20); err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := loadOwnerKey(state)
		runtime.ReadMemStats(&after)
		if alloc := after.TotalAlloc - before.TotalAlloc; err == nil || alloc > 1<<20 {
			t.Errorf("loadOwnerKey of a 16 MiB key allocated %d bytes, error %v; want a refusal within 1 MiB", alloc, err)
		}
	})
}

// TestOwnerKeyFIFO catches a key whose open blocks: a FIFO under the key's name has no writer, so a
// blocking open hangs loadOwnerKey and every OpenOwner after it (#124, security review). The key
// must be opened without blocking and refused because it is not a regular file.
func TestOwnerKeyFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a FIFO is a Unix file type")
	}
	state, key := ownerKeyTestState(t)
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mkfifo", "-m", "600", key).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v: %s", key, err, out)
	}
	done := make(chan error, 1)
	go func() {
		_, err := loadOwnerKey(state)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRootUnsafe) {
			t.Errorf("loadOwnerKey of a FIFO key = %v, want %v", err, ErrRootUnsafe)
		}
	case <-time.After(time.Second):
		t.Errorf("loadOwnerKey of a FIFO key still blocked after 1 s; want a refusal")
		// A writer releases the blocked open, so the goroutine ends with the test.
		if w, err := os.OpenFile(key, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
	}
}
