//go:build unix

package core

import (
	"errors"
	"fmt"
	"path/filepath"
)

// realPath returns the path of an existing p with every symlink resolved. An empty p is an error.
func realPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("real path: empty path")
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("real path of %q: %w", p, err)
	}
	return r, nil
}
