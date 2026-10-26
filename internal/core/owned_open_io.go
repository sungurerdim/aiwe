package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// errNotOpenable is the one text of an OpenOwned refusal: it names neither the directory nor its path.
var errNotOpenable = errors.New("no owned directory of a process that is gone")

// OpenOwned returns the directory name under root when RemoveOwned would accept it and the process that
// created it no longer runs (OwnerAlive on its ledger record), so a later process of this owner reuses or
// removes it (DR-11). A directory whose creator still runs, this process too, any directory RemoveOwned
// refuses, and a name that is no direct child of root fail with ErrNotOwned; the refusal repeats neither
// the name nor the path.
func (o *Owner) OpenOwned(ctx context.Context, root OwnedRoot, name string) (OwnedDir, error) {
	if err := ctx.Err(); err != nil {
		return OwnedDir{}, err
	}
	rootName, rootPath, err := o.root(root)
	if err != nil {
		return OwnedDir{}, err
	}
	recs, err := o.records()
	if err != nil {
		return OwnedDir{}, err
	}
	rec, ok := findRecord(recs, rootName, name)
	if !ok || OwnerAlive(recordCreator(rec)) {
		return OwnedDir{}, fmt.Errorf("%w: %w", ErrNotOwned, errNotOpenable)
	}
	p := filepath.Join(rootPath, name)
	if _, err := o.checkOwned(p, recs); err != nil {
		if errors.Is(err, ErrNotOwned) {
			return OwnedDir{}, fmt.Errorf("%w: %w", ErrNotOwned, errNotOpenable)
		}
		return OwnedDir{}, err
	}
	return OwnedDir{path: p}, nil
}

// recordCreator returns the process an owned_dir record names, with its exact start identity when the
// record holds one.
func recordCreator(rec ownedRecord) ProcessID {
	creator := ProcessID{PID: rec.PID, Start: rec.Start}
	if rec.StartID != noneField {
		creator.id = rec.StartID
	}
	return creator
}

// CacheRoot returns the real path of the cache root, the parent of every owned cache directory.
func (o *Owner) CacheRoot() string { return o.cache }
