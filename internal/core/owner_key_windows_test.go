package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// ownerKeyTestChildEnv names the case the re-executed test binary runs; it is empty in the parent.
const ownerKeyTestChildEnv = "OWNER_KEY_TEST_UNREADABLE_SD"

// ownerKeyTestUnreadable maps each case to the check it runs, for the state root dir, on a
// security descriptor part that cannot be read. A nil descriptor or entry crashes the process
// outside Go's recover, so each case runs in a child process.
func ownerKeyTestUnreadable(dir string) map[string]func() error {
	return map[string]func() error{
		"nil descriptor": func() error { return checkOwnerOnlySD(dir, true, nil, nil) },
		"nil entry": func() error {
			trusted, err := ownerOnlySIDs()
			if err != nil {
				return fmt.Errorf("trusted SIDs: %w", err)
			}
			return checkOwnerOnlyACE(filepath.Join(dir, ownerKeyFile), false, (*windows.ACCESS_ALLOWED_ACE)(nil), trusted)
		},
	}
}

// TestOwnerKeyUnreadableSD guards against a nil dereference on a security descriptor, or one of
// its entries, that cannot be read: the owner-only check must refuse it with ErrRootUnsafe.
func TestOwnerKeyUnreadableSD(t *testing.T) {
	if name := os.Getenv(ownerKeyTestChildEnv); name != "" {
		err := ownerKeyTestUnreadable(filepath.Join(os.TempDir(), "owner-key-test-state"))[name]() // os.Exit skips t.TempDir cleanup
		if !errors.Is(err, ErrRootUnsafe) {
			fmt.Fprintf(os.Stderr, "child %q: err = %v; want ErrRootUnsafe\n", name, err)
			os.Exit(3)
		}
		os.Exit(0)
	}
	for name := range ownerKeyTestUnreadable(t.TempDir()) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnerKeyUnreadableSD$", "-test.count=1")
			cmd.Env = append(os.Environ(), ownerKeyTestChildEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				tail := out
				if len(tail) > 400 {
					tail = tail[:400]
				}
				t.Errorf("child %q: %v; want exit 0 with ErrRootUnsafe; output head:\n%s", name, err, tail)
			}
		})
	}
}
