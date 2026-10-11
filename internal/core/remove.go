package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Sentinel errors of owned removal (SPEC §5.15).
var (
	ErrNotOwned      = errors.New("not_owned")
	ErrRootUnsafe    = errors.New("root_unsafe")
	ErrResidueLocked = errors.New("residue_locked")
)

// OwnedRoot names the root an owned directory lives under: TempRoot or CacheRoot.
type OwnedRoot int

// The two roots an owned directory may live under (Q14).
const (
	TempRoot OwnedRoot = iota + 1
	CacheRoot
)

const (
	maxOwnerName   = 64
	dirSuffixBytes = 8
	maxOwnedName   = maxOwnerName + 1 + 2*dirSuffixBytes
	removeAttempts = 5
	removeWait     = 200 * time.Millisecond
)

// OwnedRoots are the directories ownership is checked against. Temp and Cache hold owned
// directories; State holds the ledger and the owner key; Home and Canonical must stay out of reach.
type OwnedRoots struct {
	State, Temp, Cache, Home string
	Canonical                []string
}

// OwnedDir is a directory made by CreateOwned; only this package can make one.
type OwnedDir struct{ path string }

// Path returns the absolute path of d.
func (d OwnedDir) Path() string { return d.path }

// Owner creates, checks and removes owned directories for one process under checked roots.
type Owner struct {
	state, temp, cache string
	key                []byte
	self               ProcessID
}

// namedPath is a real path with the name used in errors.
type namedPath struct{ name, path string }

// OpenOwner checks the roots of r and loads, or makes, the owner key in the state root. A temp or
// cache root that is, or contains, the home, the state root, a canonical repository or a
// file-system root fails with ErrRootUnsafe; nothing is created under it. The owner records self
// as it is: a ProcessID built by hand gets no start identity.
func OpenOwner(r OwnedRoots, self ProcessID) (*Owner, error) {
	if self.PID <= 0 || self.Start.IsZero() {
		return nil, fmt.Errorf("owner process %+v: want a pid and a start time", self)
	}
	paths, err := resolveOwnedRoots(r)
	if err != nil {
		return nil, err
	}
	protected, err := protectedPaths(paths["state"], r)
	if err != nil {
		return nil, err
	}
	for _, n := range []string{"temp", "cache"} {
		if err := checkRootSafe(namedPath{n + " root", paths[n]}, protected); err != nil {
			return nil, err
		}
	}
	key, err := loadOwnerKey(paths["state"])
	if err != nil {
		return nil, err
	}
	return &Owner{state: paths["state"], temp: paths["temp"], cache: paths["cache"], key: key, self: self}, nil
}

// resolveOwnedRoots makes the state, temp and cache roots of r when they are missing and returns
// their real paths by name. A missing state root is made owner-only (mkdirOwnerOnly).
func resolveOwnedRoots(r OwnedRoots) (map[string]string, error) {
	paths := make(map[string]string, 3)
	for _, n := range []namedPath{{"state", r.State}, {"temp", r.Temp}, {"cache", r.Cache}} {
		if n.path == "" || !filepath.IsAbs(n.path) {
			return nil, fmt.Errorf("%w: %s root %q is not an absolute path", ErrRootUnsafe, n.name, n.path)
		}
		mkdir := func(p string) error { return os.MkdirAll(p, 0o700) }
		if n.name == "state" {
			mkdir = mkdirOwnerOnly
		}
		if err := mkdir(n.path); err != nil {
			return nil, fmt.Errorf("%s root: %w", n.name, err)
		}
		p, err := realDir(n)
		if err != nil {
			return nil, err
		}
		paths[n.name] = p
	}
	return paths, nil
}

// protectedPaths returns the real paths a temp or cache root may be neither the same as nor an
// ancestor of: the state root, the home and each canonical repository.
func protectedPaths(state string, r OwnedRoots) ([]namedPath, error) {
	protected := []namedPath{{"state root", state}}
	for _, n := range append([]namedPath{{"home", r.Home}}, canonicalPaths(r.Canonical)...) {
		if n.path == "" || !filepath.IsAbs(n.path) {
			return nil, fmt.Errorf("%w: %s %q is not an absolute path", ErrRootUnsafe, n.name, n.path)
		}
		p, err := realDir(n)
		if err != nil {
			return nil, err
		}
		protected = append(protected, namedPath{n.name, p})
	}
	return protected, nil
}

func canonicalPaths(c []string) []namedPath {
	out := make([]namedPath, len(c))
	for i, p := range c {
		out[i] = namedPath{"canonical repository", p}
	}
	return out
}

// realDir returns the real path of an existing directory; a path that cannot be resolved
// cannot be proven safe, so it fails with ErrRootUnsafe.
func realDir(n namedPath) (string, error) {
	p, err := realPath(n.path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrRootUnsafe, n.name, err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrRootUnsafe, n.name, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%w: %s %q is not a directory", ErrRootUnsafe, n.name, p)
	}
	return p, nil
}

// checkRootSafe refuses a root that is a file-system root, or the same directory as a protected
// path or one of its ancestors. Identity is compared by file, not by spelling, so letter case,
// short names and links cannot hide a match.
func checkRootSafe(root namedPath, protected []namedPath) error {
	if filepath.Dir(root.path) == root.path {
		return fmt.Errorf("%w: %s %q is a file-system root", ErrRootUnsafe, root.name, root.path)
	}
	ri, err := os.Stat(root.path)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrRootUnsafe, root.name, err)
	}
	for _, p := range protected {
		if err := checkAncestors(ri, root, p); err != nil {
			return err
		}
	}
	return nil
}

// checkAncestors refuses root, whose file info is ri, when it is the same directory as p or one of
// its ancestors.
func checkAncestors(ri os.FileInfo, root, p namedPath) error {
	for a := p.path; ; a = filepath.Dir(a) {
		ai, err := os.Stat(a)
		if err != nil {
			return fmt.Errorf("%w: %s %q: %v", ErrRootUnsafe, p.name, a, err)
		}
		if os.SameFile(ri, ai) {
			return fmt.Errorf("%w: %s %q is or contains the %s %q", ErrRootUnsafe, root.name, root.path, p.name, p.path)
		}
		if filepath.Dir(a) == a {
			return nil
		}
	}
}

// CreateOwned makes a new directory under root for owner, a run id or another plain name. The
// ledger record comes first, then the directory and its marker, so a crash never leaves a
// directory that looks owned without a record.
func (o *Owner) CreateOwned(ctx context.Context, root OwnedRoot, owner string) (OwnedDir, error) {
	if err := ctx.Err(); err != nil {
		return OwnedDir{}, err
	}
	rootName, rootPath, err := o.root(root)
	if err != nil {
		return OwnedDir{}, err
	}
	if !plainName(owner, maxOwnerName) {
		return OwnedDir{}, fmt.Errorf("owner %q: want 1 to %d letters, digits, '.', '_' or '-', starting with a letter or digit", owner, maxOwnerName)
	}
	suffix := make([]byte, dirSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return OwnedDir{}, fmt.Errorf("owned dir name: %w", err)
	}
	rec := ownedRecord{
		FormatVersion: ownedFormatVersion,
		Kind:          kindOwnedDir,
		At:            time.Now().UTC(),
		RepoID:        noneField,
		Goal:          noneField,
		Unit:          noneField,
		RunID:         noneField,
		Root:          rootName,
		Path:          owner + "-" + hex.EncodeToString(suffix),
		Owner:         owner,
		PID:           o.self.PID,
		Start:         o.self.Start.UTC(),
		StartID:       orNone(o.self.id),
	}
	rec.HMAC = o.mac(kindOwnedDir, rootPath, rec)
	if err := o.appendRecord(rec); err != nil {
		return OwnedDir{}, err
	}
	dir := filepath.Join(rootPath, rec.Path)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return OwnedDir{}, fmt.Errorf("owned dir: %w", err)
	}
	m, err := json.Marshal(ownedMarker{Path: rec.Path, Owner: rec.Owner, PID: rec.PID, Start: rec.Start,
		HMAC: o.mac(markerDomain, rootPath, rec)})
	if err != nil {
		return OwnedDir{}, fmt.Errorf("owner marker: %w", err)
	}
	if err := writeNewFile(filepath.Join(dir, markerFile), append(m, '\n')); err != nil {
		// The directory is still empty and has no marker; a plain remove takes it back.
		return OwnedDir{}, errors.Join(fmt.Errorf("owner marker: %w", err), os.Remove(dir))
	}
	return OwnedDir{path: dir}, nil
}

// root returns the name and real path of r.
func (o *Owner) root(r OwnedRoot) (string, string, error) {
	switch r {
	case TempRoot:
		return "temp", o.temp, nil
	case CacheRoot:
		return "cache", o.cache, nil
	default:
		return "", "", fmt.Errorf("owned root %d: want TempRoot or CacheRoot", int(r))
	}
}

// RemoveOwned removes d and everything in it, only when all four hold (§5.15): its real path is a
// direct child of the temp or cache root, the ledger holds a valid owned_dir record for it, its
// .owner marker matches that record, and no link inside it leaves that root. Any other directory
// fails with ErrNotOwned and nothing is removed. Links are removed, never followed.
func (o *Owner) RemoveOwned(ctx context.Context, d OwnedDir) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recs, err := o.records()
	if err != nil {
		return err
	}
	rootPath, name, err := o.checkOwned(d.path, recs)
	if err != nil {
		return err
	}
	return removeTree(ctx, rootPath, name)
}

// checkOwned returns the root path and name of p when p is owned under the rules of RemoveOwned.
func (o *Owner) checkOwned(p string, recs []ownedRecord) (string, string, error) {
	parent, name, err := checkOwnedDir(p)
	if err != nil {
		return "", "", err
	}
	for _, root := range []struct{ name, path string }{{"temp", o.temp}, {"cache", o.cache}} {
		if parent != root.path {
			continue
		}
		rec, ok := findRecord(recs, root.name, name)
		if !ok {
			continue
		}
		if err := o.checkMarker(root.path, p, rec); err != nil {
			return "", "", err
		}
		if err := checkLinks(root.path, p); err != nil {
			return "", "", err
		}
		return root.path, name, nil
	}
	return "", "", fmt.Errorf("%w: no valid owned_dir record for %q under the temp or cache root", ErrNotOwned, p)
}

// removeTree removes name under rootPath through an os.Root, which never leaves rootPath and never
// follows a link. A name that is not a plain name, or that is not a directory, such as one swapped
// for a link, fails with ErrNotOwned at once. A failed attempt, such as a file another process holds
// open, is retried 5 times, 200 ms apart, then fails with ErrResidueLocked (SPEC §6.4); the next
// sweep retries.
func removeTree(ctx context.Context, rootPath, name string) error {
	if !plainName(name, maxOwnedName) {
		return fmt.Errorf("%w: remove %q: not an owned directory name", ErrNotOwned, name)
	}
	rt, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	defer rt.Close()
	for attempt := 1; ; attempt++ {
		err = removeOnce(rt, name)
		if err == nil || errors.Is(err, ErrNotOwned) {
			return err
		}
		if attempt == removeAttempts {
			break
		}
		t := time.NewTimer(removeWait)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Join(fmt.Errorf("remove %q: %w", name, err), ctx.Err())
		case <-t.C:
		}
	}
	return fmt.Errorf("%w: remove %q: %d attempts %v apart: %w", ErrResidueLocked, name, removeAttempts, removeWait, err)
}

// removeOnce removes the entries of name, then its marker, then name itself, so a removal that
// stops part way leaves the marker and the next removal still finds the directory owned. A name that
// is not a directory, such as a link, fails with ErrNotOwned.
func removeOnce(rt *os.Root, name string) error {
	fi, err := rt.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%w: %q is not a directory (a link is never followed)", ErrNotOwned, name)
	}
	d, err := rt.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names, rerr := d.Readdirnames(-1)
	if err := errors.Join(rerr, d.Close()); err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		if n != markerFile {
			errs = append(errs, rt.RemoveAll(filepath.Join(name, n)))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := rt.Remove(filepath.Join(name, markerFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return rt.Remove(name)
}

// plainName reports whether s is 1 to max letters, digits, '.', '_' or '-', starting with a letter
// or a digit; such a name can be neither a path nor "." or "..".
func plainName(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return true
}
