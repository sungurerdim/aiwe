//go:build unix

package core

import (
	"fmt"
	"path/filepath"
)

// realPath returns the path of an existing p with every symlink resolved.
func realPath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("real path of %q: %w", p, err)
	}
	return r, nil
}
