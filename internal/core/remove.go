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
	hooks              removalHooks
}

// namedPath is a real path with the name used in errors.
type namedPath struct{ name, path string }

// OpenOwner checks the roots of r and loads, or makes, the owner key in the state root. A state,
// temp or cache root that is, or contains, the home, a canonical repository or a file-system root,
// a temp or cache root that is or contains the state root, and a root that is not owner-only fail
// with ErrRootUnsafe; nothing is created under it. The state root is checked by location first,
// so a refused home or "/" is never named for an access change. The owner records self as it is: a ProcessID built by hand gets no start identity.
func OpenOwner(r OwnedRoots, self ProcessID) (*Owner, error) {
	if self.PID <= 0 || self.Start.IsZero() {
		return nil, fmt.Errorf("owner process %+v: want a pid and a start time", self)
	}
	paths := make(map[string]string, 3)
	for _, n := range []namedPath{{"state", r.State}, {"temp", r.Temp}, {"cache", r.Cache}} {
		p, err := resolveOwnedRoot(n)
		if err != nil {
			return nil, err
		}
		paths[n.name] = p
	}
	protected, err := protectedPaths(paths["state"], r)
	if err != nil {
		return nil, err
	}
	if err := checkRootSafe(namedPath{"state root", paths["state"]}, protected[1:]); err != nil {
		return nil, fmt.Errorf("%w; next: set %s to a state root apart from the home, every repository and the file-system root",
			err, productEnv("HOME"))
	}
	for _, n := range []string{"temp", "cache"} {
		if err := checkRootSafe(namedPath{n + " root", paths[n]}, protected); err != nil {
			return nil, err
		}
		if err := checkOwnerOnly(paths[n]); err != nil {
			return nil, fmt.Errorf("%s root: %w", n, err)
		}
	}
	key, err := loadOwnerKey(paths["state"])
	if err != nil {
		return nil, err
	}
	return &Owner{state: paths["state"], temp: paths["temp"], cache: paths["cache"], key: key, self: self}, nil
}

// resolveOwnedRoot returns the real path of the root n. A missing root is made owner-only, and a
// root that is a link or junction is refused, never followed (mkdirOwnerOnly). An existing temp or
// cache root, such as a shared /tmp/aiwe another user made first, keeps its mode; OpenOwner
// checks it after checkRootSafe (checkOwnerOnly), and loadOwnerKey checks the state root.
func resolveOwnedRoot(n namedPath) (string, error) {
	if n.path == "" || !filepath.IsAbs(n.path) {
		return "", fmt.Errorf("%w: %s root %q is not an absolute path", ErrRootUnsafe, n.name, n.path)
	}
	// A trailing separator or "." element would make Lstat follow a link at the end; Clean drops
	// both, also after a \\?\ prefix.
	n.path = filepath.Clean(n.path)
	if err := mkdirOwnerOnly(n.path); err != nil {
		return "", fmt.Errorf("%s root: %w", n.name, err)
	}
	return realDir(n)
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
		return "", fmt.Errorf("%w: %s: %w", ErrRootUnsafe, n.name, err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrRootUnsafe, n.name, err)
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
		return fmt.Errorf("%w: %s: %w", ErrRootUnsafe, root.name, err)
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
			return fmt.Errorf("%w: %s %q: %w", ErrRootUnsafe, p.name, a, err)
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
		return OwnedDir{}, fmt.Errorf("owner %q: want 1 to %d letters, digits, '.', '_' or '-', starting with a letter or digit and not ending in '.'", owner, maxOwnerName)
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
	e, err := o.checkOwned(d.path, recs)
	if err != nil {
		return err
	}
	hookAt(o.hooks.ownedChecked, e.name)
	return o.hooks.removeTree(ctx, e.root, e.name, e.fi)
}

// ownedEntry is a directory the ownership check approved: its root path, its name, and the file
// info that names its identity, read through a handle. Removal compares each entry it removes with
// fi, so the directory removed is the one approved.
type ownedEntry struct {
	root, name string
	fi         fs.FileInfo
}

// checkOwned returns the entry of p when p is owned under the rules of RemoveOwned.
func (o *Owner) checkOwned(p string, recs []ownedRecord) (ownedEntry, error) {
	parent, name, err := checkOwnedDir(p)
	if err != nil {
		return ownedEntry{}, err
	}
	for _, root := range []struct{ name, path string }{{"temp", o.temp}, {"cache", o.cache}} {
		if parent != root.path {
			continue
		}
		rec, ok := findRecord(recs, root.name, name)
		if !ok {
			continue
		}
		fi, err := o.checkOwnedContent(root.path, name, rec)
		if err != nil {
			return ownedEntry{}, err
		}
		return ownedEntry{root: root.path, name: name, fi: fi}, nil
	}
	return ownedEntry{}, fmt.Errorf("%w: no valid owned_dir record for %q under the temp or cache root", ErrNotOwned, p)
}

// checkOwnedContent checks the marker and the links of name under rootPath and returns the file
// info of name, read through a handle, so it names one directory whatever the path names later.
// The marker is read through the directory opened on that identity. The links are walked by path,
// so name must still be that directory after the walk, else it fails with ErrNotOwned.
func (o *Owner) checkOwnedContent(rootPath, name string, rec ownedRecord) (fi fs.FileInfo, err error) {
	rt, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotOwned, err)
	}
	defer func() { err = errors.Join(err, o.hooks.closeChecked("owned root", rt.Close)) }()
	fi, err = rt.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotOwned, err)
	}
	if fi.Mode().Type() != fs.ModeDir {
		return nil, fmt.Errorf("%w: %q is not a directory (a link is never followed)", ErrNotOwned, name)
	}
	hookAt(o.hooks.ownedMarker, name)
	if err := o.checkMarkerOf(rt, rootPath, name, fi, rec); err != nil {
		return nil, err
	}
	if err := checkLinks(rootPath, filepath.Join(rootPath, name)); err != nil {
		return nil, err
	}
	if err := sameEntry(rt, name, fi); err != nil {
		return nil, err
	}
	return fi, nil
}

// checkMarkerOf opens name under rt once, proves it is the directory fi describes, and checks its
// marker through that handle; a name swapped in between fails with ErrNotOwned.
func (o *Owner) checkMarkerOf(rt *os.Root, rootPath, name string, fi fs.FileInfo, rec ownedRecord) (err error) {
	sub, err := openChecked(rt, name, fi)
	if err != nil && !errors.Is(err, ErrNotOwned) {
		err = fmt.Errorf("%w: %w", ErrNotOwned, err)
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, o.hooks.closeChecked("owned directory", sub.Close)) }()
	d, err := sub.Open(".")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotOwned, err)
	}
	di, err := d.Stat()
	if err := errors.Join(err, d.Close()); err != nil {
		return fmt.Errorf("%w: %w", ErrNotOwned, err)
	}
	if !os.SameFile(fi, di) {
		return fmt.Errorf("%w: %q was swapped during the ownership check (a link is never followed)", ErrNotOwned, name)
	}
	return o.checkMarker(rootPath, sub, filepath.Join(rootPath, name), rec)
}

// removeEntryTree removes name under sub and everything in it; os.Root never follows a link out of
// sub. It is the one recursive removal (DR-25), and its callers reach it only for an approved owned
// directory (emptyOwned).
func removeEntryTree(sub *os.Root, name string) error {
	return sub.RemoveAll(name)
}

// plainName reports whether s is 1 to max letters, digits, '.', '_' or '-', starting with a letter
// or a digit and not ending in '.'; such a name can be neither a path nor "." or "..", and Windows
// cannot strip a trailing dot to make it name another entry.
func plainName(s string, max int) bool {
	if s == "" || len(s) > max || s[len(s)-1] == '.' {
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
