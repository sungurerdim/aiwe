package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// refuseMounts fails with ErrNotOwned when an entry under dir of the sub-root sub, at any depth, is
// a mount point (mountPointAt), named under owned; it reads through sub, never follows a link and
// never descends into a mount point, so no data on a mounted file system is removed (#136).
func refuseMounts(sub *os.Root, owned, dir string) (err error) {
	d, err := sub.Open(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	ents, err := d.ReadDir(-1)
	for i := 0; err == nil && i < len(ents); i++ {
		p := path.Join(dir, ents[i].Name())
		switch mount, merr := mountPointAt(d, ents[i].Name()); {
		case merr != nil:
			err = merr
		case mount:
			err = fmt.Errorf("%w: %q is a mount point; nothing removed", ErrNotOwned, filepath.Join(owned, filepath.FromSlash(p)))
		case ents[i].Type() == fs.ModeDir:
			err = refuseMounts(sub, owned, p)
		}
	}
	return err
}
