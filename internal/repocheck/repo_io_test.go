package repocheck

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// repoWithOutside returns a repository directory holding go.mod and, beside it, a file outside
// the repository that a path must not reach.
func repoWithOutside(t *testing.T) (repo, outside string) {
	t.Helper()
	dir := t.TempDir()
	repo = filepath.Join(dir, "repo")
	outside = filepath.Join(dir, "secret.txt")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("token=outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, outside
}

// checkEscapeRefused fails unless every repository read of rel is refused as path_escapes_repo;
// an escape read as absence or as content would let a SPEC-listed path reach outside the repo.
func checkEscapeRefused(t *testing.T, repo, rel string) {
	t.Helper()
	if body, err := readRepoFile(repo, rel); !errors.Is(err, errPathEscapesRepo) {
		t.Errorf("readRepoFile(%q) = %q, %v; want %v", rel, body, err, errPathEscapesRepo)
	}
	if body, present, err := readRepoFileIfPresent(repo, rel); !errors.Is(err, errPathEscapesRepo) {
		t.Errorf("readRepoFileIfPresent(%q) = %q, %v, %v; want %v", rel, body, present, err, errPathEscapesRepo)
	}
	if present, err := fileExists(repo, rel); !errors.Is(err, errPathEscapesRepo) {
		t.Errorf("fileExists(%q) = %v, %v; want %v", rel, present, err, errPathEscapesRepo)
	}
}

// TestRepoIO catches a repository read that reaches outside the repository, through `..` or a
// link, or that reads an escape as an absent file (DR-13).
func TestRepoIO(t *testing.T) {
	t.Run("reads a file inside the repository", func(t *testing.T) {
		repo, _ := repoWithOutside(t)
		body, err := readRepoFile(repo, "go.mod")
		if err != nil || string(body) != "module example.com/x\n" {
			t.Fatalf("readRepoFile(go.mod) = %q, %v; want the module line", body, err)
		}
		present, err := fileExists(repo, "go.mod")
		if err != nil || !present {
			t.Fatalf("fileExists(go.mod) = %v, %v; want true", present, err)
		}
	})

	t.Run("reports an absent file as absent, never as an error", func(t *testing.T) {
		repo, _ := repoWithOutside(t)
		if body, present, err := readRepoFileIfPresent(repo, "docs/SPEC.md"); err != nil || present || body != nil {
			t.Fatalf("readRepoFileIfPresent(docs/SPEC.md) = %q, %v, %v; want absent", body, present, err)
		}
		if present, err := fileExists(repo, "docs/SPEC.md"); err != nil || present {
			t.Fatalf("fileExists(docs/SPEC.md) = %v, %v; want false", present, err)
		}
	})

	t.Run("refuses a path that climbs out of the repository", func(t *testing.T) {
		repo, _ := repoWithOutside(t)
		checkEscapeRefused(t, repo, "../secret.txt")
	})

	t.Run("refuses a link that points out of the repository", func(t *testing.T) {
		repo, outside := repoWithOutside(t)
		if err := os.Symlink(outside, filepath.Join(repo, "LICENSE")); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink needs a privilege on Windows: %v; the Linux and macOS CI jobs run this case", err)
			}
			t.Fatal(err)
		}
		checkEscapeRefused(t, repo, "LICENSE")
	})

	t.Run("refuses a directory link (a junction on Windows) out of the repository", func(t *testing.T) {
		repo, outside := repoWithOutside(t)
		dirLink(t, filepath.Dir(outside), filepath.Join(repo, "docs"))
		checkEscapeRefused(t, repo, "docs/secret.txt")
	})

	t.Run("refuses a directory link with an absolute target inside the repository", func(t *testing.T) {
		repo, _ := repoWithOutside(t)
		if err := os.Mkdir(filepath.Join(repo, "internal"), 0o700); err != nil {
			t.Fatal(err)
		}
		dirLink(t, repo, filepath.Join(repo, "internal", "self"))
		checkEscapeRefused(t, repo, "internal/self/go.mod")
	})

	t.Run("labels an escape by identity, not by its message text", func(t *testing.T) {
		repo, _ := repoWithOutside(t)
		lookalike := &fs.PathError{Op: "open", Path: "go.mod", Err: errors.New("path escapes from parent")}
		err := inRepo(repo, "go.mod", func(*os.Root, string) error { return lookalike })
		if !errors.Is(err, lookalike) || errors.Is(err, errPathEscapesRepo) {
			t.Fatalf("inRepo(lookalike error) = %v; want the error itself, not %v", err, errPathEscapesRepo)
		}
	})
}

// dirLink makes link a link to the directory target: a junction on Windows, which needs no
// privilege, so the case runs there too; a symlink elsewhere.
func dirLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

// repoIOTestWithin runs read and fails when it still blocks after 1 s; a writer opened on path
// then releases a blocked FIFO open.
func repoIOTestWithin(t *testing.T, path string, read func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- read() }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatalf("read of %s still blocked after 1 s; want a named error", path)
		return nil
	}
}

// TestRepoIONotRegular catches a repository read that hangs on a FIFO or reads a file of any size
// (#199): a FIFO under a SPEC-listed path blocks a plain open forever on Unix; a 4 MiB cap bounds
// the read.
func TestRepoIONotRegular(t *testing.T) {
	const repoCap = 4 << 20
	sized := func(size int) func(t *testing.T, path string) {
		return func(t *testing.T, path string) {
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	tests := []struct {
		name    string
		unix    bool
		make    func(t *testing.T, path string)
		wantErr bool
	}{
		{name: "file at the cap refused", make: sized(repoCap)},
		{name: "file one byte over the cap read", make: sized(repoCap + 1), wantErr: true},
		{name: "FIFO blocks the read", unix: true, wantErr: true, make: func(t *testing.T, path string) {
			if out, err := exec.CommandContext(t.Context(), "mkfifo", "-m", "600", path).CombinedOutput(); err != nil {
				t.Fatalf("mkfifo: %v: %s", err, out)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unix && runtime.GOOS == "windows" {
				t.Skip("a FIFO is a Unix file type")
			}
			repo, _ := repoWithOutside(t)
			path := filepath.Join(repo, "SPEC.md")
			tc.make(t, path)
			err := repoIOTestWithin(t, path, func() error {
				_, present, err := readRepoFileIfPresent(repo, "SPEC.md")
				if err == nil && !present {
					return errors.New("read as absent")
				}
				return err
			})
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "SPEC.md")) {
				t.Fatalf("readRepoFileIfPresent(SPEC.md) = %v; want an error that names SPEC.md", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("readRepoFileIfPresent(SPEC.md) = %v; want a file at the cap read", err)
			}
		})
	}
}
