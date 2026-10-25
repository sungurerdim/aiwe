package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// appendFile appends rec to the ledger file at path, creating it; the file is opened for reading too,
// so Append can read the last record back for its prev (SPEC §8.5).
func appendFile(t *testing.T, path string, rec Record) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
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
		{"a prev in upper-case hex (two forms of one link)", strings.TrimSuffix(run, "\n"),
			`"prev":"` + genesisPrev(), `"prev":"` + strings.Repeat("A", 64)},
		{"a prev one digit short (a link that is no hash)", strings.TrimSuffix(run, "\n"),
			`"prev":"` + genesisPrev(), `"prev":"` + genesisPrev()[1:]},
		{"a line with no prev (a record linked to nothing)", strings.TrimSuffix(run, "\n"),
			`,"prev":"` + genesisPrev() + `"`, ``},
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

// genesisPrev is the prev of the first record of a ledger, written here once by hand so the tests
// pin the value rather than the constant (SPEC §8.5).
func genesisPrev() string { return strings.Repeat("0", 64) }

// oracleHash returns the link to line computed without canonicalBytes: the lowercase hex SHA-256 of
// the RFC 8785 form oracleBytes writes (L59).
func oracleHash(t *testing.T, line string) string {
	t.Helper()
	sum := sha256.Sum256(oracleBytes(t, []byte(line)))
	return hex.EncodeToString(sum[:])
}

// headerOf returns the header of rec.
func headerOf(t *testing.T, rec Record) Header {
	t.Helper()
	switch r := rec.(type) {
	case Run:
		return r.Header
	case Check:
		return r.Header
	case Event:
		return r.Header
	}
	t.Fatalf("record %T is not a run, check or event", rec)
	return Header{}
}

// readAll returns the records of the ledger body and its lines without their newlines.
func readAll(t *testing.T, body string) ([]Record, []string) {
	t.Helper()
	records, err := Read(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Read error = %v, want nil", err)
	}
	return records, strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

// appendAll appends records to a new ledger, in memory or in a file, and returns its bytes.
func appendAll(t *testing.T, records []Record, inFile bool) string {
	t.Helper()
	if !inFile {
		var buf bytes.Buffer
		for _, rec := range records {
			if err := Append(&buf, rec); err != nil {
				t.Fatalf("Append(%T) error = %v, want nil", rec, err)
			}
		}
		return buf.String()
	}
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	for _, rec := range records {
		appendFile(t, path, rec)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger %s: %v", path, err)
	}
	return string(body)
}

// TestPrevIsHashOfPreviousRecord guards against a broken link: each record's prev must be the
// lowercase hex SHA-256 of the RFC 8785 bytes of the record before it, read back from the ledger
// itself, so a changed or removed record shows (L59, SPEC §8.5).
func TestPrevIsHashOfPreviousRecord(t *testing.T) {
	long := ledgerTestCheck()
	long.Key = strings.Repeat("k", 3*tailChunk)
	cases := []struct {
		name    string
		records []Record
		inFile  bool
	}{
		{"in memory: a prev from a stale or another record",
			[]Record{ledgerTestRun(), ledgerTestEvent(EventVerdict), ledgerTestCheck()}, false},
		{"in a file: a prev not read back from the file", []Record{ledgerTestRun(), ledgerTestCheck(), ledgerTestRun()}, true},
		{"in a file whose last line is longer than one read: a head cut mid-line",
			[]Record{ledgerTestRun(), long, ledgerTestCheck(), ledgerTestRun()}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, lines := readAll(t, appendAll(t, tc.records, tc.inFile))
			if len(records) != len(tc.records) {
				t.Fatalf("Read returned %d records, want %d", len(records), len(tc.records))
			}
			for i := 1; i < len(records); i++ {
				if got, want := headerOf(t, records[i]).Prev, oracleHash(t, lines[i-1]); got != want {
					t.Errorf("record %d prev = %q, want %q, the hash of record %d", i+1, got, want, i)
				}
			}
		})
	}
}

// TestGenesisPrev guards against a first record linked to nothing or to a value of its own: the
// first record of a ledger carries the fixed genesis value, 64 zeros, and only the first does
// (L59, SPEC §8.5).
func TestGenesisPrev(t *testing.T) {
	cases := []struct {
		name   string
		inFile bool
	}{
		{"in memory", false},
		{"in a new file", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, _ := readAll(t, appendAll(t, []Record{ledgerTestRun(), ledgerTestCheck()}, tc.inFile))
			if got := headerOf(t, records[0]).Prev; got != genesisPrev() {
				t.Errorf("first record prev = %q, want 64 zeros", got)
			}
			if got := headerOf(t, records[1]).Prev; got == genesisPrev() {
				t.Errorf("second record prev = %q, the genesis value; want the hash of the first", got)
			}
		})
	}
}

// TestChainContinuesFromHead guards against a rotation that breaks the chain: a ledger started from
// a given head links its first record to that head, and the next record to the first (SPEC §8.5).
// A head in another form or a ledger that already holds a record is refused, and nothing is written.
func TestChainContinuesFromHead(t *testing.T) {
	sum := sha256.Sum256([]byte("the last record of the ledger before"))
	head := hex.EncodeToString(sum[:])
	t.Run("the first record links to the head and the next to the first", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Continue(&buf, head, ledgerTestRun()); err != nil {
			t.Fatalf("Continue error = %v, want nil", err)
		}
		if err := Append(&buf, ledgerTestCheck()); err != nil {
			t.Fatalf("Append after Continue error = %v, want nil", err)
		}
		records, lines := readAll(t, buf.String())
		if got := headerOf(t, records[0]).Prev; got != head {
			t.Errorf("first record prev = %q, want the head %q", got, head)
		}
		if got, want := headerOf(t, records[1]).Prev, oracleHash(t, lines[0]); got != want {
			t.Errorf("second record prev = %q, want %q, the hash of the first", got, want)
		}
	})
	runLine, _ := appendLine(t, ledgerTestRun())
	refused := []struct {
		name, head, ledger string
	}{
		{"a head in upper-case hex (two forms of one link)", strings.ToUpper(head), ""},
		{"a head one digit short", head[1:], ""},
		{"an empty head (a first record linked to nothing)", "", ""},
		{"a head that is not hex", "alice@example.com" + head[17:], ""},
		{"a ledger that already holds a record (a second chain in one file)", head, runLine},
		{"a ledger whose last line is torn", head, strings.TrimSuffix(runLine, "\n")},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tc.ledger)
			err := Continue(buf, tc.head, ledgerTestCheck())
			if err == nil {
				t.Fatalf("Continue(%q) = nil error, want a refusal", tc.head)
			}
			if buf.String() != tc.ledger {
				t.Errorf("Continue(%q) refused but wrote %q", tc.head, strings.TrimPrefix(buf.String(), tc.ledger))
			}
			if tc.head != "" && tc.head != head && strings.Contains(err.Error(), tc.head) {
				t.Errorf("refusal %q repeats the head it refuses", err)
			}
		})
	}
}

// TestAppendRefusesCallerPrev guards against a link chosen by the caller: only Append sets prev, so a
// record that arrives with a prev of its own is refused, never written with it, and the refusal does
// not repeat the value (L59, SPEC §8.5).
func TestAppendRefusesCallerPrev(t *testing.T) {
	run := ledgerTestRun()
	run.Prev = genesisPrev()
	check := ledgerTestCheck()
	check.Prev = strings.Repeat("ab", 32)
	event := ledgerTestEvent(EventVerdict)
	event.Prev = "alice@example.com"
	cases := []struct {
		name      string
		rec       Record
		held      bool
		continues bool
	}{
		{"a run with the genesis prev on an empty ledger (a forged first link)", run, false, false},
		{"a check with a well-formed prev after a record (a link the caller chose)", check, true, false},
		{"an event with a prev that is no hash", event, true, false},
		{"a run with a prev given to Continue (a forged rotation link)", run, false, true},
	}
	for _, tc := range cases {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if tc.held {
				if err := Append(&buf, ledgerTestRun()); err != nil {
					t.Fatalf("Append(run) error = %v, want nil", err)
				}
			}
			before := buf.String()
			var err error
			if tc.continues {
				err = Continue(&buf, strings.Repeat("cd", 32), tc.rec)
			} else {
				err = Append(&buf, tc.rec)
			}
			if err == nil || !strings.Contains(err.Error(), "prev of its own") {
				t.Fatalf("Append(%T with prev) error = %v, want the refusal of a prev of its own", tc.rec, err)
			}
			if buf.String() != before {
				t.Errorf("Append(%T with prev) refused but wrote %q", tc.rec, strings.TrimPrefix(buf.String(), before))
			}
			if strings.Contains(err.Error(), "alice@example.com") {
				t.Errorf("refusal %q repeats the prev it refuses", err)
			}
		})
	}
}

// countingWriter is a ledger Append cannot read back: it holds neither the bytes nor a file.
type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

// unreadableFile is a ledger file whose seek or read fails.
type unreadableFile struct {
	countingWriter
	seekFails bool
}

func (f *unreadableFile) Seek(int64, int) (int64, error) {
	if f.seekFails {
		return 0, errors.New("seek failed")
	}
	return tailChunk, nil
}

func (*unreadableFile) ReadAt([]byte, int64) (int, error) { return 0, errors.New("read failed") }

// failingBuffer is a ledger in memory whose write fails.
type failingBuffer struct{ bytes.Buffer }

func (*failingBuffer) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// ledgerSize returns how many bytes the ledger w holds.
func ledgerSize(t *testing.T, w io.Writer) int {
	t.Helper()
	switch l := w.(type) {
	case *bytes.Buffer:
		return l.Len()
	case *countingWriter:
		return l.n
	case *unreadableFile:
		return l.n
	case *os.File:
		info, err := l.Stat()
		if err != nil {
			t.Fatalf("stat ledger: %v", err)
		}
		return int(info.Size())
	}
	t.Fatalf("ledger %T has no size", w)
	return 0
}

// writeOnlyLedger returns a ledger file that holds one record, opened for writing only, so Append
// cannot read its last record back.
func writeOnlyLedger(t *testing.T) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	appendFile(t, path, ledgerTestRun())
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open ledger %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestAppendRefusesUnreadableLedger guards against a record linked to a head that was not read, or
// written in a form that cannot be hashed: Append refuses it and writes nothing (fail closed, L59).
func TestAppendRefusesUnreadableLedger(t *testing.T) {
	runLine, _ := appendLine(t, ledgerTestRun())
	noPrev := replaceOnce(t, runLine, `,"prev":"`+genesisPrev()+`"`, "")
	bigCount := ledgerTestRun()
	bigCount.TokensIn = core.Measured[int64]{Value: 1<<53 + 1, Known: true}
	nanCost := ledgerTestRun()
	nanCost.CostUSD = core.Measured[float64]{Value: math.NaN(), Known: true}
	run := ledgerTestRun()
	cases := []struct {
		name string
		w    io.Writer
		rec  Record
	}{
		{"a writer it cannot read back (a prev guessed, not read)", &countingWriter{}, ledgerTestRun()},
		{"a ledger whose last line is torn", bytes.NewBufferString(strings.TrimSuffix(runLine, "\n")), ledgerTestRun()},
		{"a ledger whose last line is not a record", bytes.NewBufferString("not a record\n"), ledgerTestRun()},
		{"a ledger whose last line has no prev (a line from before the chain)", bytes.NewBufferString(noPrev), ledgerTestRun()},
		{"a ledger file opened for writing only (its head unreadable)", writeOnlyLedger(t), ledgerTestRun()},
		{"a ledger file whose seek fails", &unreadableFile{seekFails: true}, ledgerTestRun()},
		{"a ledger file whose read fails", &unreadableFile{}, ledgerTestRun()},
		{"a record with no canonical form (an int64 above 2^53, rounded by float64)", &bytes.Buffer{}, bigCount},
		{"a record json.Marshal cannot write (a NaN cost)", &bytes.Buffer{}, nanCost},
		{"a pointer to a run (a type outside the closed set)", &bytes.Buffer{}, &run},
	}
	for _, tc := range cases {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			before := ledgerSize(t, tc.w)
			if err := Append(tc.w, tc.rec); err == nil {
				t.Fatalf("Append(%T, %T) = nil error, want a refusal", tc.w, tc.rec)
			}
			if after := ledgerSize(t, tc.w); after != before {
				t.Errorf("Append(%T, %T) refused but wrote %d bytes", tc.w, tc.rec, after-before)
			}
		})
	}
	t.Run("reports a write that fails", func(t *testing.T) {
		if err := Append(&failingBuffer{}, ledgerTestRun()); err == nil {
			t.Fatalf("Append to a ledger whose write fails = nil error, want the write error")
		}
	})
}
