package ledger

import (
	"bufio"
	"bytes"
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

// Append writes rec as one line of the JSONL ledger in w, after every line written before it; it
// never rewrites or removes an earlier line (SPEC §4, §8.5). A record that breaks the schema is
// refused, and then nothing is written.
func Append(w io.Writer, rec Record) error {
	if err := validateRecord(rec); err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("ledger record: %w", err)
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("ledger append: %w", err)
	}
	return nil
}

// Read returns the records of the JSONL ledger in r, oldest first; an empty ledger holds no record
// (SPEC §8.5). A last line that was never finished is ErrTornLine, and a line that is not a record
// of the schema is refused: no line is ever dropped silently (SPEC §4).
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
	var head Header
	if err := json.Unmarshal([]byte(line), &head); err != nil {
		return nil, fmt.Errorf("ledger line %d: %w", n, refuse("one JSON object", err.Error()))
	}
	rec, err := decodeRecord(head.Kind, line)
	if err == nil {
		err = sameAsWritten(rec, line)
	}
	if err == nil {
		err = validateRecord(rec)
	}
	if err != nil {
		return nil, fmt.Errorf("ledger line %d: %w", n, err)
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
