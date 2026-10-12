package gates

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// streamLineLimit is the longest line the stream holds back while it waits for the newline; a
// longer line is written in pieces, so a step that prints no newline still streams (§7.8).
const streamLineLimit = 64 << 10

// stepStream is the writer a step's standard output and standard error stream to while the step
// runs (§7.8), so a killed step never erases what it reported. Each line reaches w
// escaped by core.TextEscape, so step output cannot drive the owner's terminal (#223); only the
// terminal stream is escaped, the runner keeps the raw bytes. The two streams write from two
// goroutines; mu keeps each write whole.
type stepStream struct {
	mu      sync.Mutex
	w       io.Writer
	pending []byte
	// scanned counts the bytes searched for a newline, so a test sees that held bytes are not.
	scanned int
}

// newStepStream returns the stream of step on w, after a header line that names the step. A nil w
// streams nowhere.
func newStepStream(w io.Writer, step GateStep) (*stepStream, error) {
	if w == nil {
		w = io.Discard
	}
	s := &stepStream{w: w}
	if _, err := fmt.Fprintf(s, "--- step %s\n", step.ID); err != nil {
		return nil, fmt.Errorf("stream header: %w", err)
	}
	return s, nil
}

// Write takes p into the stream and writes each complete line, escaped. Held bytes that reach
// streamLineLimit are written up to their last whole rune. Only p is scanned for a newline: the
// held bytes hold none, so a step that writes one byte at a time costs linear time.
func (s *stepStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	if i := bytes.LastIndexByte(p, '\n'); i >= 0 {
		n = len(s.pending) + i + 1
	}
	s.pending = append(s.pending, p...)
	if len(s.pending)-n >= streamLineLimit {
		n += wholeRunes(s.pending[n:])
	}
	if n == 0 {
		return len(p), nil
	}
	if err := s.emit(s.pending[:n]); err != nil {
		return 0, err
	}
	s.pending = append(s.pending[:0], s.pending[n:]...)
	return len(p), nil
}

// Close writes the last line the step left without a newline, escaped and ended by a newline, so
// the next step's header starts its own line.
func (s *stepStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	err := s.emit(append(s.pending, '\n'))
	s.pending = nil
	return err
}

// emit writes b to w through the core text escape.
func (s *stepStream) emit(b []byte) error {
	if _, err := io.WriteString(s.w, core.TextEscape(string(b))); err != nil {
		return fmt.Errorf("stream step output: %w", err)
	}
	return nil
}

// wholeRunes returns the length of the longest prefix of b that does not end inside a rune whose
// remaining bytes have not arrived yet.
func wholeRunes(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return len(b)
			}
			return i
		}
	}
	return len(b)
}
