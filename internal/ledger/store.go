package ledger

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if err != nil {
		return nil, fmt.Errorf("ledger line %d: %w", n, err)
	}
	if err := validateRecord(rec); err != nil {
		return nil, fmt.Errorf("ledger line %d: %w", n, err)
	}
	return rec, nil
}

// decodeRecord decodes line into the record of kind; a kind this package does not read is refused,
// never skipped (SPEC §4).
func decodeRecord(kind Kind, line string) (Record, error) {
	switch kind {
	case KindRun:
		var run Run
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			return nil, refuse("a run record", err.Error())
		}
		return run, nil
	case KindCheck:
		var check Check
		if err := json.Unmarshal([]byte(line), &check); err != nil {
			return nil, refuse("a check record", err.Error())
		}
		return check, nil
	case KindEvent:
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, refuse("an event record", err.Error())
		}
		return event, nil
	}
	return nil, refuse(fmt.Sprintf("kind %q or %q", KindRun, KindCheck), fmt.Sprintf("kind %q", kind))
}
