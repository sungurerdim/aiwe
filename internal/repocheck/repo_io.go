package repocheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// errPathEscapesRepo is returned when a repository-relative path leaves the repository, through
// `..` or a link that points out of it; such a path is refused, never read and never absent.
var errPathEscapesRepo = errors.New("path_escapes_repo")

// escapeMessage is the text os.Root gives a path that leaves the root; the os package does not
// export the error, and the repo_io test pins this text for the toolchain in go.mod.
const escapeMessage = "path escapes from parent"

// inRepo opens repo as a root and runs fn on it with the slash-separated, repository-relative
// path rel; a rel or a link that leaves repo returns errPathEscapesRepo.
func inRepo(repo, rel string, fn func(root *os.Root, name string) error) error {
	name := filepath.FromSlash(rel)
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%s: %w", rel, errPathEscapesRepo)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return err
	}
	err = fn(root, name)
	if cerr := root.Close(); err == nil {
		err = cerr
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err.Error() == escapeMessage {
		return fmt.Errorf("%s: %w", rel, errPathEscapesRepo)
	}
	return err
}

// readRepoFile reads the slash-separated, repository-relative file rel of repo.
func readRepoFile(repo, rel string) ([]byte, error) {
	var body []byte
	err := inRepo(repo, rel, func(root *os.Root, name string) error {
		var rerr error
		body, rerr = root.ReadFile(name)
		return rerr
	})
	return body, err
}

// readRepoFileIfPresent reads the repository-relative file rel and reports whether it exists; an
// error other than absence is returned, never read as absence (DR-13).
func readRepoFileIfPresent(repo, rel string) ([]byte, bool, error) {
	body, err := readRepoFile(repo, rel)
	switch {
	case err == nil:
		return body, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read %s: %w", rel, err)
}

// fileExists reports whether the repository-relative file rel exists; an error other than
// absence is returned, never read as absence (DR-13).
func fileExists(repo, rel string) (bool, error) {
	err := inRepo(repo, rel, func(root *os.Root, name string) error {
		_, serr := root.Stat(name)
		return serr
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", rel, err)
}
