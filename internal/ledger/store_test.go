package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
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

// TestReadRefusesLineNotAsWritten guards against a line that Append would never write: encoding/json
// skips an unknown key, keeps the last of two equal keys and matches a key in any case, so personal
// data could sit in the line while the record read from it is clean (SPEC §4, §8.5).
func TestReadRefusesLineNotAsWritten(t *testing.T) {
	verdict := eventLine(t, ledgerTestEvent(EventVerdict))
	run, _ := appendLine(t, ledgerTestRun())
	check, _ := appendLine(t, ledgerTestCheck())
	cases := []struct {
		name, line, old, repl string
	}{
		{"an unknown key in an event (Read: unknown key skipped)", verdict,
			`"event":"verdict"`, `"event":"verdict","email":"alice@example.com"`},
		{"an unknown key in the actor (Read: unknown key skipped)", verdict,
			`"actor":{`, `"actor":{"email":"alice@example.com",`},
		{"an unknown object in an event (Read: unknown key skipped)", verdict,
			`"event":"verdict"`, `"event":"verdict","host":{"home":"` + drivePath("C", `\\`, "alice") + `"}`},
		{"a duplicate evidence key (Read: last key wins)", verdict,
			`"evidence":"`, `"evidence":"in ` + homePath("alice") + "/" + core.Product + `","evidence":"`},
		{"a duplicate unit key (Read: last key wins)", verdict,
			`"unit":"503"`, `"unit":"` + homePath("alice") + `","unit":"503"`},
		{"an upper-case evidence key next to the real one (Read: key in any case)", verdict,
			`"evidence":"`, `"EVIDENCE":"by alice@example.com","evidence":"`},
		{"an evidence key in another case (Read: key in any case)", verdict,
			`"evidence":"`, `"Evidence":"`},
		{"an unknown key in a run (Read: unknown key skipped)", strings.TrimSuffix(run, "\n"),
			`"kind":"run"`, `"kind":"run","email":"alice@example.com"`},
		{"an unknown key in a check (Read: unknown key skipped)", strings.TrimSuffix(check, "\n"),
			`"kind":"check"`, `"kind":"check","cwd":"` + homePath("alice") + "/" + core.Product + `"`},
	}
	for _, tc := range cases {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			mustRefuseLine(t, replaceOnce(t, tc.line, tc.old, tc.repl))
		})
	}
	t.Run("the line Append writes reads back for every kind", func(t *testing.T) {
		for _, line := range []string{verdict, run, check} {
			if _, err := Read(strings.NewReader(strings.TrimSuffix(line, "\n") + "\n")); err != nil {
				t.Errorf("Read(%s) error = %v, want nil", line, err)
			}
		}
	})
}
