package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// ErrFootprintExceeded reports a state, cache or temp footprint over its ceiling (SPEC §6.4).
var ErrFootprintExceeded = errors.New("footprint_exceeded")

// Footprint is the measured disk use of the roots: bytes and files of state and cache, and the
// registered temp directories whose owner is dead, owned or not.
type Footprint struct {
	StateBytes, StateFiles int64
	CacheBytes, CacheFiles int64
	TempOrphans            int64
}

// FootprintCeilings are the highest allowed values of a Footprint.
type FootprintCeilings struct {
	StateBytes, StateFiles int64
	CacheBytes, CacheFiles int64
	TempOrphans            int64
}

// DefaultFootprintCeilings returns the default ceilings of SPEC §6.4: state 64 MB and 1 000 files,
// cache 2 GB and 2 000 files, no temp directory without a live owner.
func DefaultFootprintCeilings() FootprintCeilings {
	return FootprintCeilings{
		StateBytes:  64_000_000,
		StateFiles:  1000,
		CacheBytes:  2_000_000_000,
		CacheFiles:  2000,
		TempOrphans: 0,
	}
}

// Footprint measures the state and cache roots and counts the registered temp directories of dead
// owners, including those that fail the ownership checks (DR-6, DR-11). Links are counted as
// files and never followed.
func (o *Owner) Footprint(ctx context.Context) (Footprint, error) {
	var f Footprint
	var err error
	if f.StateBytes, f.StateFiles, err = treeSize(ctx, o.state); err != nil {
		return Footprint{}, err
	}
	if f.CacheBytes, f.CacheFiles, err = treeSize(ctx, o.cache); err != nil {
		return Footprint{}, err
	}
	dirs, err := o.ownedTemps(ctx)
	if err != nil {
		return Footprint{}, err
	}
	for _, d := range dirs {
		if !d.live {
			f.TempOrphans++
		}
	}
	return f, nil
}

// Check returns ErrFootprintExceeded naming every value over its ceiling in c, else nil.
func (f Footprint) Check(c FootprintCeilings) error {
	var over []string
	for _, v := range []struct {
		name     string
		got, max int64
	}{
		{"state bytes", f.StateBytes, c.StateBytes},
		{"state files", f.StateFiles, c.StateFiles},
		{"cache bytes", f.CacheBytes, c.CacheBytes},
		{"cache files", f.CacheFiles, c.CacheFiles},
		{"temp orphans", f.TempOrphans, c.TempOrphans},
	} {
		if v.got > v.max {
			over = append(over, fmt.Sprintf("%s %d over %d", v.name, v.got, v.max))
		}
	}
	if len(over) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrFootprintExceeded, strings.Join(over, ", "))
}

// treeSize returns the bytes of the regular files under root and the count of every entry that is
// not a directory. An entry removed during the walk is skipped; any other failure is an error,
// never a zero.
func treeSize(ctx context.Context, root string) (int64, int64, error) {
	var size, files int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return walkError(root, p, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		n, present, err := entryBytes(d)
		if err != nil || !present {
			return err // an entry removed during the walk is skipped, not counted
		}
		size += n
		files++
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("footprint of %q: %w", root, err)
	}
	return size, files, nil
}

// walkError returns what the walk of root does with err at p: nothing for an entry removed during
// the walk, else err itself, which ends the walk; a missing root is an error.
func walkError(root, p string, err error) error {
	if p != root && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// entryBytes returns the bytes of d when it is a regular file, 0 for any other entry, and whether d
// is still there: an entry removed during the walk is absent, not an error.
func entryBytes(d fs.DirEntry) (int64, bool, error) {
	if !d.Type().IsRegular() {
		return 0, true, nil
	}
	fi, err := d.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return fi.Size(), true, nil
}
