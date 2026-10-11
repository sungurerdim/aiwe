package repocheck_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// TestDoctorGoFilesStaysInRoot catches a doctor walk that follows a link out of the repository
// and reads a file there: a link swapped in during the walk would point the read anywhere.
func TestDoctorGoFilesStaysInRoot(t *testing.T) {
	marker := []byte("package outside // read outside the walked root")
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, marker, 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "leak.go")); err != nil {
		t.Skipf("file symlink %s: %v; a junction links only a directory", outside, err)
	}
	files, err := repocheck.GoFiles(repo)
	for _, f := range files {
		if bytes.Equal(f.Body, marker) {
			t.Fatalf("GoFiles read %s through the link %s outside the repository root", outside, f.Path)
		}
	}
	if err == nil {
		t.Fatalf("GoFiles = %d files, nil error; want an error for the link out of the root", len(files))
	}
}

// TestDoctorGoFilesJunctionStaysInRoot catches the same escape on Windows through a directory
// junction, which needs no symlink privilege, so the case runs where the symlink case skips.
func TestDoctorGoFilesJunctionStaysInRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a directory junction exists only on Windows; the symlink case covers this OS")
	}
	marker := []byte("package outside // read outside the walked root")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.go"), marker, 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	link := filepath.Join(repo, "j.go")
	if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, outside, err, out)
	}
	files, err := repocheck.GoFiles(repo)
	for _, f := range files {
		if bytes.Equal(f.Body, marker) {
			t.Fatalf("GoFiles read a file outside the repository root through the junction %s", f.Path)
		}
	}
	// The root refuses the junction itself; a plain "is a directory" error would not prove that.
	if err == nil || !strings.Contains(err.Error(), "path escapes from parent") {
		t.Fatalf("GoFiles = %d files, error %v; want the root to refuse j.go as a path that escapes", len(files), err)
	}
}

// TestDoctorGoFilesReadsTree catches a root-scoped walk that loses a nested file, a
// repository-relative path, or the skip of hidden, testdata and vendor directories.
func TestDoctorGoFilesReadsTree(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("main.go", "package main\n")
	write("internal/core/env.go", "package core\n")
	write("internal/core/README.md", "not go\n")
	write(".git/hook.go", "package hidden\n")
	write("internal/core/testdata/fixture.go", "package fixture\n")
	write("vendor/dep/dep.go", "package dep\n")
	files, err := repocheck.GoFiles(repo)
	if err != nil {
		t.Fatalf("GoFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Body)
	}
	want := map[string]string{"main.go": "package main\n", "internal/core/env.go": "package core\n"}
	if len(got) != len(want) {
		t.Fatalf("GoFiles paths = %v; want %v", got, want)
	}
	for p, body := range want {
		if got[p] != body {
			t.Fatalf("GoFiles[%s] = %q; want %q (all: %v)", p, got[p], body, got)
		}
	}
}

// TestDoctorGoFilesEmptyRepo catches a repository without a .go file passing as zero findings.
func TestDoctorGoFilesEmptyRepo(t *testing.T) {
	if _, err := repocheck.GoFiles(t.TempDir()); err == nil {
		t.Fatal("GoFiles on a repository without a .go file = nil error; want an error")
	}
}
