package core

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ownerKeyFile       = "owner.key"
	ledgerFile         = "ledger.jsonl"
	ledgerPrefix       = "ledger."
	rotationSuffix     = ".jsonl.gz"
	noneField          = "none"
	markerFile         = ".owner"
	ownerKeySize       = 32
	ownedFormatVersion = 1
	kindOwnedDir       = "owned_dir"
	markerDomain       = "owned_marker"
	maxMarkerBytes     = 4096
	maxOwnerKeyBytes   = 4096
	maxLedgerLine      = 4 << 20
)

// ownedRecord is the owned_dir line of the ledger, the TempDirRecord of SPEC §8.5 with the fields
// ownership needs; Path is the directory name under its root. RepoID, Goal, Unit and RunID are
// "none" when absent, as is StartID where the OS gives no exact start identity or the ProcessID
// was built by hand; RemovedAt is null until a removal is recorded.
type ownedRecord struct {
	FormatVersion int        `json:"format_version"`
	Kind          string     `json:"kind"`
	At            time.Time  `json:"at"`
	RepoID        string     `json:"repo_id"`
	Goal          string     `json:"goal"`
	Unit          string     `json:"unit"`
	RunID         string     `json:"run_id"`
	Root          string     `json:"root"`
	Path          string     `json:"path"`
	Owner         string     `json:"owner"`
	PID           int        `json:"pid"`
	Start         time.Time  `json:"start"`
	StartID       string     `json:"start_id"`
	RemovedAt     *time.Time `json:"removed_at"`
	HMAC          string     `json:"hmac"`
}

// ownedMarker is the content of the .owner file inside an owned directory.
type ownedMarker struct {
	Path  string    `json:"path"`
	Owner string    `json:"owner"`
	PID   int       `json:"pid"`
	Start time.Time `json:"start"`
	HMAC  string    `json:"hmac"`
}

// orNone returns s, or "none" when s is empty.
func orNone(s string) string {
	if s == "" {
		return noneField
	}
	return s
}

// ownerKeyTakenHook, when set, runs right after loadOwnerKey has opened the key and before it
// checks the key's access; a test uses it to swap the key at that point.
var ownerKeyTakenHook func()

// loadOwnerKey reads <state>/owner.key, or makes it with a fresh random key when it is missing. Each
// access is checked on an open handle: the key is made and opened through the checked state root and
// read from its own checked handle. A root or key that another principal can read or write fails with ErrRootUnsafe.
func loadOwnerKey(state string) ([]byte, error) {
	rt, err := openOwnerOnlyRoot(state)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	p := filepath.Join(state, ownerKeyFile)
	f, err := rt.Open(ownerKeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		if err := createOwnerKey(rt); err != nil {
			return nil, err
		}
		f, err = rt.Open(ownerKeyFile)
	}
	if err != nil {
		return nil, fmt.Errorf("owner key: %w", err)
	}
	defer f.Close()
	if ownerKeyTakenHook != nil {
		ownerKeyTakenHook()
	}
	if err := checkOwnerOnlyFile(f, p); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxOwnerKeyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("owner key: %w", err)
	}
	k, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(b) > maxOwnerKeyBytes || len(k) != ownerKeySize {
		return nil, fmt.Errorf("owner key %s: want %d hex-encoded bytes", p, ownerKeySize)
	}
	return k, nil
}

// openOwnerOnlyRoot opens the state root and checks its access on the open handle.
func openOwnerOnlyRoot(state string) (*os.Root, error) {
	rt, err := os.OpenRoot(state)
	if err != nil {
		return nil, fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, state, err)
	}
	dir, err := rt.Open(".")
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%w: owner-only check of %q: %v", ErrRootUnsafe, state, err), rt.Close())
	}
	if err := errors.Join(checkOwnerOnlyFile(dir, state), dir.Close()); err != nil {
		return nil, errors.Join(err, rt.Close())
	}
	return rt, nil
}

// createOwnerKey makes the key file under the state root rt in one step: a fresh key is written and
// synced to a file of its own, then hard-linked to the key's name, which fails when it exists. A reader
// never sees the key without its bytes; of two racing processes, the loser reads the winner's key.
func createOwnerKey(rt *os.Root) error {
	k := make([]byte, ownerKeySize)
	tag := make([]byte, dirSuffixBytes)
	if _, err := rand.Read(k); err != nil {
		return fmt.Errorf("owner key: %w", err)
	}
	if _, err := rand.Read(tag); err != nil {
		return fmt.Errorf("owner key: %w", err)
	}
	tmp := ownerKeyFile + "." + hex.EncodeToString(tag) + ".tmp"
	if err := writeNew(rt.OpenFile, tmp, []byte(hex.EncodeToString(k)+"\n")); err != nil {
		rerr := rt.Remove(tmp)
		if errors.Is(rerr, fs.ErrNotExist) {
			rerr = nil
		}
		return errors.Join(fmt.Errorf("owner key: %w", err), rerr)
	}
	lerr := rt.Link(tmp, ownerKeyFile)
	if errors.Is(lerr, fs.ErrExist) {
		lerr = nil
	}
	if err := errors.Join(lerr, rt.Remove(tmp)); err != nil {
		return fmt.Errorf("owner key: %w", err)
	}
	return nil
}

// writeNewFile writes b to a file that must not exist yet, and syncs it.
func writeNewFile(p string, b []byte) error {
	return writeNew(os.OpenFile, p, b)
}

// writeNew writes b to the file name, which must not exist yet, opened with open, and syncs it.
func writeNew(open func(string, int, fs.FileMode) (*os.File, error), name string, b []byte) error {
	f, err := open(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	serr := f.Sync()
	cerr := f.Close()
	return errors.Join(werr, serr, cerr)
}

// mac returns the hex HMAC-SHA256 of the record fields under the owner key, bound to domain and
// to the real root path.
func (o *Owner) mac(domain, rootPath string, r ownedRecord) string {
	h := hmac.New(sha256.New, o.key)
	for _, f := range []string{domain, r.Root, rootPath, r.Path, r.Owner, strconv.Itoa(r.PID),
		r.Start.UTC().Format(time.RFC3339Nano), r.StartID} {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// validMAC reports whether got is the HMAC of r under domain.
func (o *Owner) validMAC(domain, rootPath string, r ownedRecord, got string) bool {
	want, _ := hex.DecodeString(o.mac(domain, rootPath, r))
	g, err := hex.DecodeString(got)
	return err == nil && hmac.Equal(want, g)
}

// appendRecord appends rec as one line of the ledger and syncs it.
func (o *Owner) appendRecord(rec ownedRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("owned_dir record: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(o.state, ledgerFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("owned_dir record: %w", err)
	}
	_, werr := f.Write(append(b, '\n'))
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return fmt.Errorf("owned_dir record: %w", err)
	}
	return nil
}

// records returns every owned_dir record whose HMAC is valid for its root, from the ledger and
// from each of its rotations, ledger.<n>.jsonl.gz (SPEC §8.5).
func (o *Owner) records() ([]ownedRecord, error) {
	files, err := ledgerFiles(o.state)
	if err != nil {
		return nil, err
	}
	var out []ownedRecord
	for _, p := range files {
		recs, err := o.fileRecords(p)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// ledgerFiles returns the path of the ledger and of each rotation in the state root.
func ledgerFiles(state string) ([]string, error) {
	ents, err := os.ReadDir(state)
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	files := []string{filepath.Join(state, ledgerFile)}
	for _, e := range ents {
		n, ok := strings.CutPrefix(e.Name(), ledgerPrefix)
		n, gz := strings.CutSuffix(n, rotationSuffix)
		if ok && gz && n != "" && strings.Trim(n, "0123456789") == "" && e.Type().IsRegular() {
			files = append(files, filepath.Join(state, e.Name()))
		}
	}
	return files, nil
}

// fileRecords returns the valid owned_dir records of one ledger file; a rotation is gzip-compressed.
// A file that does not exist holds no record.
func (o *Owner) fileRecords(p string) ([]ownedRecord, error) {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(p, rotationSuffix) {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("ledger %s: %w", p, err)
		}
		defer zr.Close()
		r = zr
	}
	var out []ownedRecord
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLedgerLine)
	for sc.Scan() {
		if rec, ok := o.parseRecord(sc.Bytes()); ok {
			out = append(out, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("ledger %s: %w", p, err)
	}
	return out, nil
}

// parseRecord returns line as an owned_dir record when it is one, its Path is a plain name, and its
// HMAC is valid for its root. A Path that is not a plain name could leave its root, so it is no record.
func (o *Owner) parseRecord(line []byte) (ownedRecord, bool) {
	var r ownedRecord
	if !bytes.Contains(line, []byte(kindOwnedDir)) ||
		json.Unmarshal(line, &r) != nil || r.Kind != kindOwnedDir || r.FormatVersion != ownedFormatVersion ||
		!plainName(r.Path, maxOwnedName) {
		return ownedRecord{}, false
	}
	var rootPath string
	switch r.Root {
	case "temp":
		rootPath = o.temp
	case "cache":
		rootPath = o.cache
	default:
		return ownedRecord{}, false
	}
	return r, o.validMAC(kindOwnedDir, rootPath, r, r.HMAC)
}

func findRecord(recs []ownedRecord, root, name string) (ownedRecord, bool) {
	for _, r := range recs {
		if r.Root == root && r.Path == name {
			return r, true
		}
	}
	return ownedRecord{}, false
}

// checkMarker checks that the .owner marker of the directory dir, opened at path, is a plain file
// that matches rec under the key. The marker is read through dir, so it belongs to the directory
// dir was opened on, whatever path names later.
func (o *Owner) checkMarker(rootPath string, dir *os.Root, path string, rec ownedRecord) error {
	p := filepath.Join(path, markerFile)
	fi, err := dir.Lstat(markerFile)
	if err != nil {
		return fmt.Errorf("%w: marker: %v", ErrNotOwned, err)
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxMarkerBytes {
		return fmt.Errorf("%w: marker %q is not a plain file of at most %d bytes", ErrNotOwned, p, maxMarkerBytes)
	}
	b, err := dir.ReadFile(markerFile)
	if err != nil {
		return fmt.Errorf("%w: marker: %v", ErrNotOwned, err)
	}
	var m ownedMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("%w: marker %q: %v", ErrNotOwned, p, err)
	}
	if m.Path != rec.Path || m.Owner != rec.Owner || m.PID != rec.PID || !m.Start.Equal(rec.Start) ||
		!o.validMAC(markerDomain, rootPath, rec, m.HMAC) {
		return fmt.Errorf("%w: marker %q does not match its owned_dir record", ErrNotOwned, p)
	}
	return nil
}

// checkOwnedDir checks that p is a clean absolute path with an owned directory name, a directory
// that is not a link, and its own real path. It returns the parent and the name of p.
func checkOwnedDir(p string) (string, string, error) {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", "", fmt.Errorf("%w: %q is not a clean absolute path", ErrNotOwned, p)
	}
	parent, name := filepath.Dir(p), filepath.Base(p)
	if !plainName(name, maxOwnedName) {
		return "", "", fmt.Errorf("%w: %q is not an owned directory name", ErrNotOwned, p)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrNotOwned, err)
	}
	if !fi.IsDir() {
		return "", "", fmt.Errorf("%w: %q is not a directory (a link is never followed)", ErrNotOwned, p)
	}
	if rp, err := realPath(p); err != nil || rp != p {
		return "", "", fmt.Errorf("%w: %q is not its own real path", ErrNotOwned, p)
	}
	return parent, name, nil
}

// checkLinks refuses dir when a link inside it resolves outside rootPath or cannot be resolved.
// The walk never follows a link.
func checkLinks(rootPath, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotOwned, err)
		}
		if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
			return nil
		}
		rp, err := realPath(p)
		if err != nil {
			return fmt.Errorf("%w: link %q: %v", ErrNotOwned, p, err)
		}
		if !within(rootPath, rp) {
			return fmt.Errorf("%w: link %q leaves the root %q for %q", ErrNotOwned, p, rootPath, rp)
		}
		return nil
	})
}

// within reports whether p is root or below it; both are real paths.
func within(root, p string) bool {
	if p == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(p, root)
}
