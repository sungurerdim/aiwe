package gates

import (
	"fmt"
	"io"
	"sync"
)

// stepStream is the writer a step's standard output and standard error stream to while the step
// runs (DR-38, SPEC §7.8), so a killed step never erases what it reported. The two streams write
// from two goroutines; mu keeps each write whole.
type stepStream struct {
	mu sync.Mutex
	w  io.Writer
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

// Write writes p to the stream as one piece.
func (s *stepStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
