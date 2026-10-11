package repocheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// readRepoFile reads the slash-separated, repository-relative file rel of repo.
func readRepoFile(repo, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
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
	_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", rel, err)
}
