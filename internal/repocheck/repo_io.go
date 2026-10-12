package repocheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sungurerdim/aiwe/internal/core"
)

// errPathEscapesRepo is returned when a repository-relative path leaves the repository, through
// `..` or a link that points out of it; such a path is refused, never read and never absent.
var errPathEscapesRepo = errors.New("path_escapes_repo")

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
	escape, err := escapeError(root)
	if err == nil {
		err = fn(root, name)
	}
	if cerr := root.Close(); err == nil {
		err = cerr
	}
	if escape != nil && errors.Is(err, escape) {
		return fmt.Errorf("%s: %w", rel, errPathEscapesRepo)
	}
	return err
}

// escapeError returns the error os.Root gives a path that leaves the root; the os package does
// not export it, so it is taken from a stat of "..". A root that does not refuse ".." is an error.
func escapeError(root *os.Root) (error, error) {
	_, err := root.Stat("..")
	var pe *fs.PathError
	switch {
	case err == nil:
		return nil, errors.New(`os.Root did not refuse ".."`)
	case !errors.As(err, &pe) || pe.Err == nil:
		return nil, fmt.Errorf(`os.Root refused ".." without a path error: %w`, err)
	}
	return pe.Err, nil
}

// maxRepoFileBytes caps one repository file read by repocheck, a Go file included; a larger file
// is refused unread (#199).
const maxRepoFileBytes = 4 << 20

// readRepoFile reads the slash-separated, repository-relative file rel of repo; a non-regular
// file, such as a FIFO, or a file over maxRepoFileBytes is an error that names rel.
func readRepoFile(repo, rel string) ([]byte, error) {
	var body []byte
	err := inRepo(repo, rel, func(root *os.Root, name string) error {
		var rerr error
		body, rerr = core.ReadRegularCapped(root, name, maxRepoFileBytes)
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return fmt.Errorf("%s: %w", rel, rerr)
		}
		return rerr
	})
	return body, err
}

// readRepoFileIfPresent reads the repository-relative file rel and reports whether it exists; an
// error other than absence is returned, never read as absence (DR-6).
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
// absence is returned, never read as absence (DR-6).
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
