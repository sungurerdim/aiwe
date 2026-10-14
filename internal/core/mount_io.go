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
// unknown and refuses too, at once, never retried. So does a walk error a retry cannot clear
// (walkRefusal): a denied read of a directory must not become residue_locked (#243).
func (h removalHooks) refuseMounts(sub *os.Root, owned, dir, outcome string) (err error) {
	where := filepath.Join(owned, filepath.FromSlash(dir))
	d, err := sub.Open(dir)
	if err != nil {
		return walkRefusal(err, where, outcome)
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	check := mountPointAt
	if h.mountCheck != nil {
		check = h.mountCheck
	}
	ents, err := h.readWalk(d)
	if err != nil {
		return walkRefusal(err, where, outcome)
	}
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

// walkRefusal turns a walk error a retry cannot clear, a denied read (fs.ErrPermission), into the
// mount-unknown refusal, so the removal stops at once instead of being retried into residue_locked
// (#243). shown names the directory the walk could not read; outcome is what the removal removed
// before it. A transient error (busy, a sharing violation) is returned as is and stays retryable.
func walkRefusal(err error, shown, outcome string) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: %q: the mount state is unknown (%w); %s", ErrNotOwned, shown, err, outcome)
	}
	return err
}

// emptyEntries removes every entry of the walk root sub but the marker. It reports whether an entry
// actually went, so a refusal may name a partial removal only then (#243): an entry whose removal
// failed stays and counts as nothing removed. The marker goes last, by the caller. A removal a test
// fails through entryRemove counts as one that took nothing too.
func (h removalHooks) emptyEntries(sub *os.Root, names []string) (removed bool, err error) {
	remove := removeEntryTree
	if h.entryRemove != nil {
		remove = h.entryRemove
	}
	var errs []error
	for _, n := range names {
		if n == markerFile {
			continue
		}
		if err := remove(sub, n); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = true
	}
	return removed, errors.Join(errs...)
}

// readWalk reads the directory d of the walk as refuseMounts does, through the walkRead hook when a
// test set it: a mode cannot deny the read where the process bypasses it (root) (#243).
func (h removalHooks) readWalk(d *os.File) ([]fs.DirEntry, error) {
	if h.walkRead != nil {
		return h.walkRead(d)
	}
	return d.ReadDir(-1)
}
