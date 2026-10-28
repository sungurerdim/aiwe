package ledger

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// ErrTornLine marks a ledger whose last line was never finished: the append that would have
// completed it did not. A reader reports that line and stops, so it never reads the ledger as if the
// record had never been written (DR-6, SPEC §8.5). The text of a sentinel is the name of the outcome
// its condition maps to (SPEC §5.3): a ledger the reader cannot trust leaves the command unable to
// decide, which is the error-class outcome of the table.
var ErrTornLine = errors.New(string(core.OutcomeError))

// Append writes rec as one line of the JSONL ledger in w, after every line written before it, and
// links it to the last of them: prev is the hash of the last record in w, or the genesis value when w
// holds none (L59, SPEC §8.5). It never rewrites or removes an earlier line (SPEC §4). w is the ledger
// itself, so Append reads its last record back: a file opened for reading and appending, or a buffer.
// The caller holds the ledger lock (SPEC §6.6) across the call, which reads the head and writes the
// line, so no other append comes between the two. A record that breaks the schema, that arrives with
// a prev of its own or that has no canonical form is refused, and then nothing is written.
func Append(w io.Writer, rec Record) error {
	head, _, err := headOf(w)
	if err != nil {
		return err
	}
	return appendAfter(w, head, rec)
}

// Continue writes rec as the first record of the empty ledger in w and links it to head, the hash of
// the last record of the ledger before it, as a rotation does (SPEC §8.5); each later record is
// written with Append. A head that is not 64 lowercase hex digits is refused, and so is a ledger that
// already holds a record, whose own last record is its head. The caller holds the ledger lock.
func Continue(w io.Writer, head string, rec Record) error {
	if !isHash(head) {
		return refuse("a head of 64 lowercase hex digits", "a head in another form")
	}
	_, held, err := headOf(w)
	switch {
	case err != nil:
		return err
	case held:
		return refuse("an empty ledger to continue the chain in", "a ledger that already holds a record")
	}
	return appendAfter(w, head, rec)
}

// appendAfter writes rec to the ledger in w, linked to prev. The line is hashed before it is written,
// so a record whose line has no canonical form is refused rather than written as a head no later
// append could hash (fail closed).
func appendAfter(w io.Writer, prev string, rec Record) error {
	rec, err := linked(rec, prev)
	if err != nil {
		return err
	}
	if err := validateRecord(rec); err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("ledger record: %w", err)
	}
	if _, err := hashLine(line); err != nil {
		return err
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("ledger append: %w", err)
	}
	return nil
}

// bufferLedger is a ledger held in memory that shows its bytes, as a bytes.Buffer does.
type bufferLedger interface{ Bytes() []byte }

// fileLedger is a ledger file opened for reading, as an *os.File is.
type fileLedger interface {
	io.ReaderAt
	io.Seeker
}

// kindOwnedDir and ownedFormatVersion name the owned_dir lines of internal/core (SPEC §8.5), which
// share the ledger file and sit outside the hash chain.
const (
	kindOwnedDir       = "owned_dir"
	ownedFormatVersion = 1
)

// tailChunk is how many bytes headOfFile reads at a time from the end of a ledger file.
const tailChunk = 4096

// headOf returns the head of the ledger in w, the hash of its last record, and whether it holds a
// record; the head of an empty ledger is the genesis value (L59, SPEC §8.5). The owned_dir lines core
// writes into the ledger are no record and no link of the chain, so the head is the last line before
// them. A ledger Append cannot read back, a torn last line and a last line that is not a record are
// refused: a record is never linked to a head that was not read (fail closed).
func headOf(w io.Writer) (string, bool, error) {
	switch l := w.(type) {
	case bufferLedger:
		head, held, _, err := headOfTail(l.Bytes(), true)
		return head, held, err
	case fileLedger:
		return headOfFile(l)
	}
	return "", false, refuse("a ledger Append can read back: a file opened for reading or a buffer",
		fmt.Sprintf("%T", w))
}

// headOfFile returns the head of the ledger file f and whether it holds a record. It reads back from
// the end until it holds the last record line, so an append reads about one line when the ledger ends
// in a record, never the whole file.
func headOfFile(f fileLedger) (string, bool, error) {
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return "", false, fmt.Errorf("ledger head: %w", err)
	}
	var tail []byte
	for {
		head, held, more, err := headOfTail(tail, end == 0)
		if err != nil || !more {
			return head, held, err
		}
		chunk := make([]byte, min(end, tailChunk))
		end -= int64(len(chunk))
		if n, err := f.ReadAt(chunk, end); n < len(chunk) {
			return "", false, fmt.Errorf("ledger head: %w", cmp.Or(err, io.ErrUnexpectedEOF))
		}
		tail = append(chunk, tail...)
	}
}

// headOfTail returns the head of a ledger whose last bytes are tail, and whether the ledger holds a
// record. whole says tail starts at the first byte of the ledger; when it does not and the last record
// line may begin before tail, more is true and the head is not known yet. The last record line is read
// as a record, so a line Read refuses is never a head, and its RFC 8785 bytes are hashed.
func headOfTail(tail []byte, whole bool) (head string, held, more bool, err error) {
	line, found, more, err := lastRecordLine(tail, whole)
	switch {
	case err != nil:
		return "", true, false, err
	case more:
		return "", false, true, nil
	case !found:
		return genesis, false, false, nil
	}
	if _, err := parseRecord(string(line)); err != nil {
		return "", true, false, fmt.Errorf("ledger last line: %w", err)
	}
	head, err = hashLine(line)
	return head, true, false, err
}

// lastRecordLine returns the last line of tail that is not an owned_dir line. found is false when
// tail holds no such line; more is true when that line may begin before tail, which is not the first
// byte of the ledger unless whole. A tail whose last line was never finished is a torn line.
func lastRecordLine(tail []byte, whole bool) (line []byte, found, more bool, err error) {
	if len(tail) == 0 {
		return nil, false, !whole, nil
	}
	body, complete := bytes.CutSuffix(tail, []byte{'\n'})
	if !complete {
		return nil, false, false, fmt.Errorf("ledger last line: %w", tornLine())
	}
	for {
		start := bytes.LastIndexByte(body, '\n') + 1
		switch {
		case start == 0 && !whole:
			return nil, false, true, nil
		case !isOwnedDirLine(body[start:]):
			return body[start:], true, false, nil
		case start == 0:
			return nil, false, false, nil
		}
		body = body[:start-1]
	}
}

// isOwnedDirLine reports whether line is an owned_dir line, the line core writes for a directory it
// owns (SPEC §8.5). It is no record of this package and carries no prev; core alone checks its HMAC.
// A line of another format version is not passed over, so it is refused as a line of unknown kind.
func isOwnedDirLine(line []byte) bool {
	var probe struct {
		FormatVersion int    `json:"format_version"`
		Kind          string `json:"kind"`
	}
	return json.Unmarshal(line, &probe) == nil && probe.Kind == kindOwnedDir &&
		probe.FormatVersion == ownedFormatVersion
}

// Read returns the records of the JSONL ledger in r, oldest first; an empty ledger holds no record
// (SPEC §8.5). It passes over the owned_dir lines core writes into the ledger, which are no record.
// A last line that was never finished is ErrTornLine, and a line that is not a record of the schema
// is refused: no line is ever dropped silently (SPEC §4).
func Read(r io.Reader) ([]Record, error) {
	br := bufio.NewReader(r)
	var out []Record
	for n := 1; ; n++ {
		line, err := br.ReadString('\n')
		if errors.Is(err, io.EOF) {
			if line == "" {
				return out, nil
			}
			return nil, fmt.Errorf("ledger line %d: %w", n, tornLine())
		}
		if err != nil {
			return nil, fmt.Errorf("ledger line %d: %w", n, err)
		}
		rec, err := parseLine(strings.TrimSuffix(line, "\n"), n)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
}

// tornLine returns the refusal of a ledger whose last line was never finished (SPEC §5.3, §8.5).
func tornLine() error {
	return &core.Error{Outcome: core.OutcomeError, Expected: "a ledger whose every line is complete", Found: "a last line that was never finished", Err: ErrTornLine}
}

// parseLine decodes one complete ledger line, which is line n of the ledger (SPEC §8.5).
func parseLine(line string, n int) (Record, error) {
	rec, err := parseRecord(line)
	if err != nil {
		return nil, fmt.Errorf("ledger line %d: %w", n, err)
	}
	return rec, nil
}

// parseRecord decodes one complete ledger line and refuses a line that is not a record of the schema
// in the form Append writes (SPEC §4, §8.5).
func parseRecord(line string) (Record, error) {
	var head Header
	if err := json.Unmarshal([]byte(line), &head); err != nil {
		return nil, refuse("one JSON object", err.Error())
	}
	rec, err := decodeRecord(head.Kind, line)
	if err == nil {
		err = sameAsWritten(rec, line)
	}
	if err == nil {
		err = validateRecord(rec)
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// decodeRecord decodes line into the record of kind; a kind this package does not read is refused,
// never skipped (SPEC §4).
func decodeRecord(kind Kind, line string) (Record, error) {
	switch kind {
	case KindRun:
		return decodeAs[Run](line, "a run record")
	case KindCheck:
		return decodeAs[Check](line, "a check record")
	case KindEvent:
		return decodeAs[Event](line, "an event record")
	}
	return nil, refuse(fmt.Sprintf("kind %q, %q or %q", KindRun, KindCheck, KindEvent), "a kind outside the set")
}

// decodeAs decodes line into a record of type R and refuses a key R does not hold, as the gate
// definition does (STD-11); what names the record in the refusal.
func decodeAs[R Record](line, what string) (Record, error) {
	var rec R
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return nil, refuse(what, err.Error())
	}
	return rec, nil
}

// sameAsWritten refuses a line that is not the line Append writes for the record read from it: a key
// twice, a key in another case, a value in another form or anything after the record. encoding/json
// keeps the last of two equal keys and matches a key in any case, so without this check personal data
// could sit in the line while the record read from it is clean (SPEC §4). A key the line leaves out
// reads absent (L32, DR-6), so the line is compared with the written form of the keys it names.
func sameAsWritten(rec Record, line string) error {
	written, err := json.Marshal(rec)
	if err != nil {
		return refuse("a record that can be written", err.Error())
	}
	want, ok := writtenSubset(written, []byte(line))
	if !ok || !bytes.Equal(want, []byte(line)) {
		return refuse("a line in the form Append writes",
			"a line with a key twice, a key in another case or a value in another form")
	}
	return nil
}

// member is one member of a JSON object: its key and its value as written.
type member struct {
	key   string
	value json.RawMessage
}

// writtenSubset returns the members of the object written that the object line names, in the order
// line names them and in the bytes of written; an object value is cut the same way. ok is false when
// line is not an object or names a key written lacks, a key twice or keys out of the written order.
func writtenSubset(written, line []byte) ([]byte, bool) {
	writtenMembers, writtenOK := objectMembers(written)
	lineMembers, lineOK := objectMembers(line)
	if !writtenOK || !lineOK {
		return nil, false
	}
	parts := make([][]byte, 0, len(lineMembers))
	next := 0
	for _, m := range lineMembers {
		at := slices.IndexFunc(writtenMembers[next:], func(w member) bool { return w.key == m.key })
		if at < 0 {
			return nil, false
		}
		part, ok := writtenMember(writtenMembers[next+at], m)
		if !ok {
			return nil, false
		}
		parts = append(parts, part)
		next += at + 1
	}
	return slices.Concat([]byte{'{'}, bytes.Join(parts, []byte{','}), []byte{'}'}), true
}

// writtenMember returns the member w as Append writes it, cut to the keys of m when both values are
// objects.
func writtenMember(w, m member) ([]byte, bool) {
	value := []byte(w.value)
	if isObject(w.value) && isObject(m.value) {
		var ok bool
		if value, ok = writtenSubset(w.value, m.value); !ok {
			return nil, false
		}
	}
	return slices.Concat([]byte(strconv.Quote(w.key)), []byte{':'}, value), true
}

// isObject reports whether a JSON value is an object.
func isObject(value []byte) bool { return len(value) > 0 && value[0] == '{' }

// objectMembers returns the members of the JSON object obj in written order; ok is false when obj is
// not an object. A key is read unescaped, so a key written with an escape differs from the written
// form only in bytes, which sameAsWritten compares.
func objectMembers(obj []byte) ([]member, bool) {
	dec := json.NewDecoder(bytes.NewReader(obj))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return nil, false
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		key, isKey := tok.(string)
		var value json.RawMessage
		if err != nil || !isKey || dec.Decode(&value) != nil {
			return nil, false
		}
		out = append(out, member{key: key, value: value})
	}
	return out, true
}
