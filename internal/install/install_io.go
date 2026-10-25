package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sungurerdim/aiwe/internal/core"
)

// maxSettingsBytes caps the settings file install reads; a larger file is refused unread (#199).
const maxSettingsBytes = 1 << 20

// newSettingsMode is the mode of a settings file install creates: the owner's alone, because a
// settings file can hold permissions and environment values.
const newSettingsMode fs.FileMode = 0o600

// Apply wires the row's hook entries into the settings of one harness under home and returns what
// it changed. Every file access goes through an *os.Root at home, so no link inside it leads out.
// A missing file or directory is created; a file that is no regular file, is too large or cannot
// be merged is refused and left as it was. Nothing is written when nothing changed, and the write
// replaces the file in one rename, so a failed write never leaves half a settings file.
func Apply(home string, h Harness) ([]Change, error) {
	if home == "" {
		return nil, refuse("a home directory", "none", nil)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, refuse("a readable home directory", err.Error(), err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Join(h.Dir, h.File)
	current, err := core.ReadRegularCapped(root, name, maxSettingsBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, refuse("a readable settings file of a regular file", err.Error(), err)
	}
	merged, changes, err := Merge(current, h.Entries())
	if err != nil || len(changes) == 0 {
		return changes, err
	}
	if err := replace(root, h, name, merged); err != nil {
		return nil, refuse("a writable settings directory", err.Error(), err)
	}
	return changes, nil
}

// replace writes b as the file name of root through a temporary file and one rename. The mode of an
// existing file is kept.
func replace(root *os.Root, h Harness, name string, b []byte) error {
	mode := newSettingsMode
	if info, serr := root.Stat(name); serr == nil {
		mode = info.Mode().Perm()
	}
	if err := root.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	tmp := name + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		return errors.Join(err, root.Remove(tmp))
	}
	return nil
}
