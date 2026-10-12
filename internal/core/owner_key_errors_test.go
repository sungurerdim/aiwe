package core

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestOwnerKeyWrappedCauses guards against an owner-only or ownership refusal that flattens its
// cause to text: a caller must still match the cause with errors.Is, next to the refusal sentinel.
func TestOwnerKeyWrappedCauses(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "state-gone")
	closed, err := os.Create(filepath.Join(t.TempDir(), ownerKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		err      func() error
		sentinel error
		cause    error
	}{
		{"loadOwnerKey of a missing state root", func() error { _, err := loadOwnerKey(missing); return err },
			ErrRootUnsafe, fs.ErrNotExist},
		{"checkOwnerOnly of a missing root", func() error { return checkOwnerOnly(missing) },
			ErrRootUnsafe, fs.ErrNotExist},
		{"checkOwnerOnlyFile of a closed handle", func() error { return checkOwnerOnlyFile(closed, closed.Name()) },
			ErrRootUnsafe, os.ErrClosed},
		{"checkOwnedDir of a missing directory", func() error { _, _, err := checkOwnedDir(missing); return err },
			ErrNotOwned, fs.ErrNotExist},
		{"checkLinks of a missing directory", func() error { return checkLinks(missing, missing) },
			ErrNotOwned, fs.ErrNotExist},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ownerKeyTestWantCause(t, c.err(), c.sentinel, c.cause)
		})
	}
}

// TestOwnerKeyDanglingLinkCause guards against checkLinks hiding why a link inside an owned
// directory cannot be resolved: a link to a missing target must match fs.ErrNotExist.
func TestOwnerKeyDanglingLinkCause(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run-dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(filepath.Join(root, "target-gone.json"), link); err != nil {
		t.Skipf("file symlink %s: %v", link, err)
	}
	rp, err := realPath(root)
	if err != nil {
		t.Fatal(err)
	}
	ownerKeyTestWantCause(t, checkLinks(rp, dir), ErrNotOwned, fs.ErrNotExist)
}

// ownerKeyTestWantCause fails the test unless err matches both sentinel and cause.
func ownerKeyTestWantCause(t *testing.T, err, sentinel, cause error) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil; want %v wrapping %v", sentinel, cause)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(%v, %v) = false", err, sentinel)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, %v) = false; the cause is flattened to text", err, cause)
	}
}
