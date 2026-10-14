package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appendFile appends rec to the ledger file at path, creating it (SPEC §8.5).
func appendFile(t *testing.T, path string, rec Record) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open ledger %s: %v", path, err)
	}
	if err := Append(f, rec); err != nil {
		t.Fatalf("Append(%T) error = %v, want nil", rec, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close ledger %s: %v", path, err)
	}
}

// TestReadEmptyLedger pins the store: an empty ledger holds no record and gives no error.
func TestReadEmptyLedger(t *testing.T) {
	records, err := Read(bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("Read of an empty ledger error = %v, want nil", err)
	}
	if len(records) != 0 {
		t.Fatalf("Read of an empty ledger returned %d records, want 0", len(records))
	}
}

// TestAppendIsAppendOnly pins the To do: run and check records are append-only JSONL, oldest first,
// and a later append never rewrites an earlier line (SPEC §4, §8.5).
func TestAppendIsAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	appendFile(t, path, ledgerTestRun())
	runLine, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger %s: %v", path, err)
	}
	appendFile(t, path, ledgerTestCheck())
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger %s: %v", path, err)
	}
	if !bytes.HasPrefix(body, runLine) {
		t.Fatalf("Append(check) rewrote the run line:\n run line %q\n ledger   %q", runLine, body)
	}
	if lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n")); len(lines) != 2 {
		t.Fatalf("ledger holds %d lines, want 2: %q", len(lines), body)
	}
	records, err := Read(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Read error = %v, want nil", err)
	}
	if len(records) != 2 {
		t.Fatalf("Read returned %d records, want 2", len(records))
	}
	if _, ok := records[0].(Run); !ok {
		t.Errorf("first record is %T, want Run", records[0])
	}
	if _, ok := records[1].(Check); !ok {
		t.Errorf("second record is %T, want Check", records[1])
	}
}

// TestReadTornLastLine pins the To do: a torn last line is reported, not silently dropped (SPEC §8.5).
func TestReadTornLastLine(t *testing.T) {
	prefix, _ := appendLine(t, ledgerTestRun())
	cases := []struct {
		name string
		tail string
	}{
		{"run line cut inside its timestamp", `{"formatVersion":1,"kind":"run","at":"2026-10-14T10:0`},
		{"check line cut inside its first key", `{"kind":"check","key":"te`},
		{"single opening brace", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := prefix + tc.tail
			if _, err := Read(strings.NewReader(body)); !errors.Is(err, ErrTornLine) {
				t.Fatalf("Read(%q) error = %v, want ErrTornLine", body, err)
			}
		})
	}
	t.Run("a complete ledger has no torn line", func(t *testing.T) {
		records, err := Read(strings.NewReader(prefix))
		if err != nil {
			t.Fatalf("Read of a complete ledger error = %v, want nil", err)
		}
		if len(records) != 1 {
			t.Fatalf("Read returned %d records, want 1", len(records))
		}
	})
}
