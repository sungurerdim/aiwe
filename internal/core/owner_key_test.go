package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// removeTestHex finds the HMAC-SHA256 hex digest (64 lowercase hex characters) in a record or a
// marker.
var removeTestHex = regexp.MustCompile(`[0-9a-f]{64}`)

// removeTestFlipHex changes one character of the first 64-hex token in s; it fails the test when s
// carries no such token.
func removeTestFlipHex(t *testing.T, what, s string) string {
	t.Helper()
	loc := removeTestHex.FindStringIndex(s)
	if loc == nil {
		t.Fatalf("%s carries no hex HMAC-SHA256: %q", what, s)
	}
	b := []byte(s)
	b[loc[0]] = "10"[min(1, int(b[loc[0]]-'0'))] // '0' becomes '1'; any other hex digit becomes '0'
	return string(b)
}

// ownerKeyTestRead returns the bytes of the file p, or fails the test when p is unreadable or empty.
func ownerKeyTestRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		t.Fatalf("read %s: %d bytes, error %v; want its bytes", p, len(b), err)
	}
	return b
}

// removeTestStartID finds the start_id field of an owned_dir record.
var removeTestStartID = regexp.MustCompile(`"start_id":"[^"]*"`)

// removeTestEditRecord rewrites, with edit, every owned_dir line of the ledger that names d; it
// fails the test when the ledger holds none.
func removeTestEditRecord(t *testing.T, r OwnedRoots, d OwnedDir, edit func(line string) string) {
	t.Helper()
	p := filepath.Join(r.State, "ledger.jsonl")
	b := ownerKeyTestRead(t, p)
	lines := strings.Split(string(b), "\n")
	name := filepath.Base(d.Path())
	found := false
	for i, l := range lines {
		if strings.Contains(l, "owned_dir") && strings.Contains(l, name) {
			lines[i] = edit(l)
			found = true
		}
	}
	if !found {
		t.Fatalf("ledger holds no owned_dir record naming %q", name)
	}
	removeTestWrite(t, p, strings.Join(lines, "\n"))
}

// TestOwnedHMAC catches an owned_dir record or .owner marker without a valid HMAC under the key
// that removal accepts (§5.15), and a record whose start_id is not bound by its HMAC, so a forged
// start identity passes the ownership check.
func TestOwnedHMAC(t *testing.T) {
	t.Run("key lives in the state root and stays out of the marker", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		d := removeTestCreate(t, removeTestOpen(t, r), TempRoot)
		key := ownerKeyTestRead(t, filepath.Join(r.State, "owner.key"))
		marker := ownerKeyTestRead(t, filepath.Join(d.Path(), ".owner"))
		if strings.Contains(string(marker), string(key)) || strings.Contains(string(marker), strings.TrimSpace(string(key))) {
			t.Fatal("the .owner marker carries the owner key")
		}
		if !removeTestHex.Match(marker) {
			t.Fatalf("the .owner marker carries no hex HMAC-SHA256: %q", marker)
		}
	})
	tampers := []struct {
		name   string
		tamper func(t *testing.T, r OwnedRoots, d OwnedDir)
	}{
		{"marker HMAC changed", func(t *testing.T, r OwnedRoots, d OwnedDir) {
			p := filepath.Join(d.Path(), ".owner")
			removeTestWrite(t, p, removeTestFlipHex(t, "marker", string(ownerKeyTestRead(t, p))))
		}},
		{"record HMAC changed", func(t *testing.T, r OwnedRoots, d OwnedDir) {
			removeTestEditRecord(t, r, d, func(l string) string {
				return removeTestFlipHex(t, "owned_dir record", l)
			})
		}},
		{"record start_id changed", func(t *testing.T, r OwnedRoots, d OwnedDir) {
			removeTestEditRecord(t, r, d, func(l string) string {
				old := removeTestStartID.FindString(l)
				if old == "" {
					t.Fatalf("owned_dir record carries no start_id: %q", l)
				}
				// Another start identity of the same shape: "none" for an exact one, an exact one
				// (boot id and start tick) for "none".
				forged := `"start_id":"none"`
				if old == forged {
					forged = `"start_id":"0f1e2d3c-4b5a-4978-8796-a5b4c3d2e1f0/4242"`
				}
				return strings.Replace(l, old, forged, 1)
			})
		}},
		{"owner key replaced", func(t *testing.T, r OwnedRoots, d OwnedDir) {
			// A key of the same shape, made by another state root.
			other := r
			other.State = filepath.Join(filepath.Dir(r.State), "other-state")
			removeTestOpen(t, other)
			removeTestWrite(t, filepath.Join(r.State, "owner.key"), string(ownerKeyTestRead(t, filepath.Join(other.State, "owner.key"))))
		}},
	}
	for _, tc := range tampers {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			o := removeTestOpen(t, r)
			d := removeTestCreate(t, o, TempRoot)
			control := removeTestCreate(t, o, TempRoot)
			keep := filepath.Join(d.Path(), "work.txt")
			removeTestWrite(t, keep, "work\n")
			tc.tamper(t, r, d)
			fresh := removeTestOpen(t, r)
			err := fresh.RemoveOwned(t.Context(), OwnedDir{path: d.Path()})
			if !errors.Is(err, ErrNotOwned) {
				t.Fatalf("RemoveOwned after %s = %v, want %v", tc.name, err, ErrNotOwned)
			}
			if !removeTestExists(t, keep) {
				t.Fatalf("refused removal still deleted %q", keep)
			}
			if tc.name == "owner key replaced" {
				return
			}
			// The untouched sibling proves the refusal comes from the tampering.
			if err := fresh.RemoveOwned(t.Context(), OwnedDir{path: control.Path()}); err != nil {
				t.Fatalf("RemoveOwned of the untouched dir: %v", err)
			}
		})
	}
}

// TestOwnedOpenConcurrent catches a race on the first OpenOwner of an empty state root: a reader
// that sees the key file before its bytes fails, or two processes keep different keys.
func TestOwnedOpenConcurrent(t *testing.T) {
	const attempts, workers = 40, 16
	self := sweepTestSelf(t)
	base := t.TempDir()
	for i := 0; i < attempts; i++ {
		r := removeTestRoots(t, filepath.Join(base, strconv.Itoa(i)))
		var wg sync.WaitGroup
		start := make(chan struct{})
		owners := make([]*Owner, workers)
		errs := make([]error, workers)
		for w := 0; w < workers; w++ {
			wg.Go(func() {
				<-start
				owners[w], errs[w] = OpenOwner(r, self)
			})
		}
		close(start)
		wg.Wait()
		for w, err := range errs {
			if err != nil {
				t.Errorf("attempt %d, worker %d: OpenOwner on an empty state root: %v", i, w, err)
			}
		}
		if t.Failed() {
			return
		}
		for w, o := range owners {
			if !bytes.Equal(o.key, owners[0].key) {
				t.Fatalf("attempt %d: worker %d holds another owner key than worker 0; want one key", i, w)
			}
		}
	}
}

// TestOwnedLedgerRotation catches a directory that reads not_owned once its owned_dir record sits
// in a rotated ledger file, ledger.<n>.jsonl.gz (SPEC §8.5).
func TestOwnedLedgerRotation(t *testing.T) {
	ctx := context.Background()
	r := removeTestRoots(t, t.TempDir())
	d := removeTestCreate(t, removeTestOpen(t, r), TempRoot)
	ledger := filepath.Join(r.State, "ledger.jsonl")
	b := ownerKeyTestRead(t, ledger)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, werr := zw.Write(b)
	if err := errors.Join(werr, zw.Close()); err != nil {
		t.Fatalf("gzip ledger: %v", err)
	}
	if err := os.WriteFile(filepath.Join(r.State, "ledger.1.jsonl.gz"), gz.Bytes(), 0o600); err != nil {
		t.Fatalf("write rotation: %v", err)
	}
	// Rotation leaves a fresh, empty ledger behind.
	if err := os.WriteFile(ledger, nil, 0o600); err != nil {
		t.Fatalf("reset ledger: %v", err)
	}
	if err := removeTestOpen(t, r).RemoveOwned(ctx, d); err != nil {
		t.Fatalf("RemoveOwned after rotation: %v, want the rotated owned_dir record read", err)
	}
	if removeTestExists(t, d.Path()) {
		t.Errorf("dir %q left after RemoveOwned", d.Path())
	}
}

// TestOwnedRecordFields catches an owned_dir record that drops the fields of TempDirRecord in
// SPEC §8.5: RecordBase (format_version, kind, at, repo_id, goal, unit, run_id), path, pid and
// removed_at.
func TestOwnedRecordFields(t *testing.T) {
	r := removeTestRoots(t, t.TempDir())
	d := removeTestCreate(t, removeTestOpen(t, r), TempRoot)
	b := ownerKeyTestRead(t, filepath.Join(r.State, "ledger.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("ledger holds %d lines after one CreateOwned, want 1", len(lines))
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("ledger line %q: %v", lines[0], err)
	}
	for _, k := range []string{"format_version", "kind", "at", "repo_id", "goal", "unit", "run_id", "path", "pid", "removed_at"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("owned_dir record %s has no %q field", lines[0], k)
		}
	}
	for _, k := range []string{"repo_id", "goal", "unit", "run_id"} {
		var s string
		if raw, ok := rec[k]; ok && (json.Unmarshal(raw, &s) != nil || s == "") {
			t.Errorf("owned_dir record field %q = %s, want a string, \"none\" when absent", k, raw)
		}
	}
	if raw, ok := rec["removed_at"]; ok && string(raw) != "null" {
		t.Errorf("owned_dir record removed_at = %s before removal, want null", raw)
	}
	var kind, path string
	var pid int
	if json.Unmarshal(rec["kind"], &kind) != nil || kind != "owned_dir" {
		t.Errorf("owned_dir record kind = %s, want \"owned_dir\"", rec["kind"])
	}
	if json.Unmarshal(rec["path"], &path) != nil || path != filepath.Base(d.Path()) {
		t.Errorf("owned_dir record path = %s, want %q", rec["path"], filepath.Base(d.Path()))
	}
	if json.Unmarshal(rec["pid"], &pid) != nil || pid != os.Getpid() {
		t.Errorf("owned_dir record pid = %s, want %d", rec["pid"], os.Getpid())
	}
}

// TestOwnerKeyMode catches a state dir or owner key that group or others can write and that
// OpenOwner still loads on Unix, so another user swaps the key and forges ownership (#109, medium).
// Root ignores modes on access, so the check reads the mode, not an access attempt.
func TestOwnerKeyMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes; TestOwnerKeyACL covers Windows")
	}
	self := sweepTestSelf(t)
	t.Run("made by OpenOwner", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		removeTestOpen(t, r)
		for p, want := range map[string]os.FileMode{r.State: 0o700, filepath.Join(r.State, ownerKeyFile): 0o600} {
			if fi, err := os.Stat(p); err != nil {
				t.Errorf("stat %s: %v", p, err)
			} else if fi.Mode().Perm() != want {
				t.Errorf("%s has mode %#o; want %#o", p, fi.Mode().Perm(), want)
			}
		}
	})
	t.Run("state root / is never named for delete", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		r.State = "/"
		if o, err := OpenOwner(r, self); !errors.Is(err, ErrRootUnsafe) || strings.Contains(ErrorResult(err).Detail, "delete") {
			t.Errorf("OpenOwner with state root /: owner %t, error %v; want %v naming no delete", o != nil, err, ErrRootUnsafe)
		}
	})
	for _, c := range []struct {
		name, target  string
		mode          os.FileMode
		preset, chown bool
	}{
		{"state dir 0777 made beforehand", ".", 0o777, true, false},
		{"state dir 0755 made beforehand, like a home", ".", 0o755, true, false},
		{"state dir group-writable", ".", 0o770, false, false},
		{"key 0666", ownerKeyFile, 0o666, false, false},
		{"key group-writable", ownerKeyFile, 0o620, false, false},
		{"temp root 0777", "../temp", 0o777, false, false},
		{"state dir 0700 owned by another user", ".", 0o700, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			if c.preset {
				removeTestMkdir(t, r.State)
			} else {
				removeTestOpen(t, r)
			}
			target := filepath.Join(r.State, c.target)
			if c.chown && os.Geteuid() != 0 {
				t.Skip("giving the state dir to uid 65534 needs root; the Linux container runs this case as root")
			}
			if err := os.Chmod(target, c.mode); err != nil || (c.chown && os.Chown(target, 65534, -1) != nil) {
				t.Fatalf("chmod %#o (chown %t) %s: %v", c.mode, c.chown, target, err)
			}
			o, err := OpenOwner(r, self)
			ownerKeyTestRefused(t, "at mode "+c.mode.String(), target, o, err, c.target != ownerKeyFile, c.chown)
			if c.preset && removeTestExists(t, filepath.Join(r.State, ownerKeyFile)) {
				t.Errorf("OpenOwner refused %s yet wrote a key into it", r.State)
			}
		})
	}
}
