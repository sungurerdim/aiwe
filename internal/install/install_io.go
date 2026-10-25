package install

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
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

// ErrSettingsLink is the cause of a refused settings file that is a symbolic link: the rename would
// replace the link with a plain file, so the file it names, a dotfiles copy say, would stop getting
// the entries. It is a constant of its own error type, not an outcome sentinel: the refusal's
// outcome stays error, and errors.Is still finds it.
const ErrSettingsLink settingsError = "the settings file is a symbolic link"

// settingsError is an error that is a constant string.
type settingsError string

func (e settingsError) Error() string { return string(e) }

// Apply wires the row's hook entries into the settings of one harness under home and returns what
// it changed. Every file access goes through an *os.Root at home, so no link inside it leads out.
// A home that is not absolute is refused, so a project's settings never take the entries. A
// missing file or directory is created; a file that is a symbolic link, no regular file, too
// large or cannot be merged is refused and left as it was. Nothing is written when nothing
// changed, and the write replaces the file in one rename, so a failed write never leaves half a
// settings file.
func Apply(home string, h Harness) ([]Change, error) {
	if !filepath.IsAbs(home) {
		return nil, refuse("an absolute home directory", fmt.Sprintf("%q", home), nil)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, refuse("a readable home directory", err.Error(), err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Join(h.Dir, h.File)
	current, err := readSettings(root, name)
	if err != nil {
		return nil, err
	}
	merged, changes, err := Merge(current, h.Entries())
	if err != nil || bytes.Equal(merged, current) {
		return changes, err
	}
	if err := replace(root, h, name, merged); err != nil {
		return nil, refuse("a writable settings directory", err.Error(), err)
	}
	return changes, nil
}

// readSettings reads the settings file name of root; a missing file is none. A symbolic link is
// refused, seen by Lstat on the same root, so the rename never replaces a link.
func readSettings(root *os.Root, name string) ([]byte, error) {
	if info, err := root.Lstat(name); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, refuse("a settings file that is no symbolic link", "a link; edit the file it names",
			ErrSettingsLink)
	}
	b, err := core.ReadRegularCapped(root, name, maxSettingsBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, refuse("a readable settings file of a regular file", err.Error(), err)
	}
	return b, nil
}

// replace writes b as the file name of root through a temporary file and one rename. The mode of an
// existing file is kept. The temporary file has a random name of its own in the same directory and
// is created exclusively (os.Root has no CreateTemp), so two installs never write the same one.
func replace(root *os.Root, h Harness, name string, b []byte) error {
	mode := newSettingsMode
	if info, serr := root.Stat(name); serr == nil {
		mode = info.Mode().Perm()
	}
	if err := root.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	tmp := name + "." + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
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
