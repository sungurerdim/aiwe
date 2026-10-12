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
// never descends into a mount point, so no data on a mounted file system is removed (#136). The
// refusal names outcome: what the removal removed before it. A failed check leaves the mount state
// unknown and refuses too, at once, never retried.
func (h removalHooks) refuseMounts(sub *os.Root, owned, dir, outcome string) (err error) {
	d, err := sub.Open(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	check := mountPointAt
	if h.mountCheck != nil {
		check = h.mountCheck
	}
	ents, err := d.ReadDir(-1)
	for i := 0; err == nil && i < len(ents); i++ {
		p := path.Join(dir, ents[i].Name())
		shown := filepath.Join(owned, filepath.FromSlash(p))
		switch mount, merr := check(d, ents[i].Name()); {
		case merr != nil:
			err = fmt.Errorf("%w: %q: the mount state is unknown (%w); %s", ErrNotOwned, shown, merr, outcome)
		case mount:
			err = fmt.Errorf("%w: %q is a mount point; %s", ErrNotOwned, shown, outcome)
		case ents[i].Type() == fs.ModeDir:
			err = h.refuseMounts(sub, owned, p, outcome)
		}
	}
	return err
}
