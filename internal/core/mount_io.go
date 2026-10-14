package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// refuseMounts fails with ErrNotOwned when an entry under dir of the sub-root sub, at any depth, is
// a mount point (mountPointAt), named under owned; it reads through sub, never follows a link and
// never descends into a mount point, so no data on a mounted file system is removed (#136). The
// refusal names outcome: what the removal removed before it. A failed check leaves the mount state
// unknown and refuses too, at once, never retried. So does a walk error a retry cannot clear
// (walkRefusal): a denied read of a directory must not become residue_locked (#243). It appends
// the slash path of every entry it checks to walked, so emptyEntries can tell what a failed
// removal took.
func (h removalHooks) refuseMounts(sub *os.Root, owned, dir, outcome string, walked *[]string) (err error) {
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
		*walked = append(*walked, p)
		switch mount, merr := check(d, ents[i].Name()); {
		case merr != nil:
			err = fmt.Errorf("%w: %q: the mount state is unknown (%w); %s", ErrNotOwned, shown, merr, outcome)
		case mount:
			err = fmt.Errorf("%w: %q is a mount point; %s", ErrNotOwned, shown, outcome)
		case ents[i].Type() == fs.ModeDir:
			err = h.refuseMounts(sub, owned, p, outcome, walked)
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

// emptyEntries removes every entry of the walk root sub but the marker. It reports whether anything
// actually went, so a refusal names a partial removal then and only then (#243). A failed removal
// may have taken part of its tree first, as RemoveAll does: it counts as removed when tookPart says
// so. walked holds the paths refuseMounts checked. The marker goes last, by the caller.
func (h removalHooks) emptyEntries(sub *os.Root, names, walked []string) (removed bool, err error) {
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
			removed = removed || tookPart(sub, n, walked)
			continue
		}
		removed = true
	}
	return removed, errors.Join(errs...)
}

// tookPart reports whether the failed removal of the entry n of sub may have removed something: a
// path of walked at or under n is gone, or its state is unknown. An entry the walk never saw counts
// as taken too: what it held is unknown. So the outcome never says less went than did (#243).
func tookPart(sub *os.Root, n string, walked []string) bool {
	seen := false
	for _, p := range walked {
		if p != n && !strings.HasPrefix(p, n+"/") {
			continue
		}
		seen = true
		if _, err := sub.Lstat(filepath.FromSlash(p)); err != nil {
			return true
		}
	}
	return !seen
}

// readWalk reads the directory d of the walk as refuseMounts does, through the walkRead hook when a
// test set it: a mode cannot deny the read where the process bypasses it (root) (#243).
func (h removalHooks) readWalk(d *os.File) ([]fs.DirEntry, error) {
	if h.walkRead != nil {
		return h.walkRead(d)
	}
	return d.ReadDir(-1)
}
