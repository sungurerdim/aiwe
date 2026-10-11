package repocheck

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
}
