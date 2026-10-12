package gates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// streamHelperEnv makes this test binary a helper step: it prints streamOutLine to its standard
// output and streamErrLine to its standard error, then waits up to streamWait for the file the
// variable names. It exits 0 once the file exists, 5 when the wait ends without it and 6 when it
// cannot print to its standard output. Only a
// runner that streams the output while the step runs lets the test create the file in time. It
// opens no window.
func streamHelperEnv() string { return gatesTestEnv("STREAM") }

const (
	streamOutLine = core.Product + "-stream-stdout-line"
	streamErrLine = core.Product + "-stream-stderr-line"
	streamWait    = 10 * time.Second
)

func init() {
	release := os.Getenv(streamHelperEnv())
	if release == "" {
		return
	}
	if _, err := fmt.Fprintln(os.Stdout, streamOutLine); err != nil {
		os.Exit(6)
	}
	fmt.Fprintln(os.Stderr, streamErrLine)
	for deadline := time.Now().Add(streamWait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(release); err == nil {
			os.Exit(0)
		}
	}
	os.Exit(5)
}

// streamTestWriter records what the runner streams. Once both helper lines have arrived it
// creates the release file, which ends the helper step green.
type streamTestWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	release  string
	released bool
	err      error
}

func (w *streamTestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	got := w.buf.Bytes()
	if !w.released && bytes.Contains(got, []byte(streamOutLine)) && bytes.Contains(got, []byte(streamErrLine)) {
		w.released = true
		w.err = os.WriteFile(w.release, []byte("go\n"), 0o600)
	}
	return len(p), nil
}

// TestStream catches step output held back until the step ends instead of streamed while it runs
// (DR-38, SPEC §7.8): the helper step ends green only when both its standard output and its
// standard error reach the runner's Output while it still runs, under a header that names the step.
func TestStream(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv(streamHelperEnv(), release)
	w := &streamTestWriter{release: release}
	step := GateStep{ID: "stream", Command: []string{os.Args[0], "-test.run=^$"}, Required: true}
	def := Definition{FormatVersion: 1, Steps: []GateStep{step}}

	start := time.Now()
	report, err := (&Runner{Output: w, Owner: rootsTestOwner(t)}).Run(context.Background(), def, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		t.Fatalf("create the release file: %v", w.err)
	}
	if !w.released || report.Steps[0].Result != ResultGreen || report.Steps[0].ExitCode != 0 {
		t.Fatalf("catches output held back until the step ends: released=%v after %v, step %+v, streamed %q",
			w.released, time.Since(start).Round(time.Millisecond), report.Steps[0], w.buf.String())
	}
	if !bytes.Contains(w.buf.Bytes(), []byte("step stream")) {
		t.Errorf("catches streamed output that does not name its step: streamed %q", w.buf.String())
	}
}

// streamEscapeEnv makes this test binary a helper step that prints streamEscapeOut, a line of
// terminal controls, then streamEscapeTail, a last line with no newline, and exits 0.
func streamEscapeEnv() string { return gatesTestEnv("STREAM_ESCAPE") }

const (
	streamEscapeOut  = "esc a\x1b[2Jb osc \x1b]0;PWN\x07c c1 \u009b2Jd bidi evil\u202etxt.exe\n"
	streamEscapeTail = "tail \x1b[0m"
)

func init() {
	if os.Getenv(streamEscapeEnv()) != "1" {
		return
	}
	if _, err := fmt.Fprint(os.Stdout, streamEscapeOut, streamEscapeTail); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}

// TestStepStreamEscapes catches a step whose output carries ESC, CSI, OSC, C1 or a bidi override
// and reaches the runner's Output raw, so a repo test drives the owner's terminal during a gate
// (#223). A last line with no newline must still reach the Output, escaped.
func TestStepStreamEscapes(t *testing.T) {
	t.Setenv(streamEscapeEnv(), "1")
	var w bytes.Buffer
	step := GateStep{ID: "escape", Command: []string{os.Args[0], "-test.run=^$"}, Required: true}
	def := Definition{FormatVersion: 1, Steps: []GateStep{step}}
	report, err := (&Runner{Output: &w, Owner: rootsTestOwner(t)}).Run(context.Background(), def, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Steps[0].Result != ResultGreen {
		t.Fatalf("helper step = %+v, want green", report.Steps[0])
	}
	got := w.String()
	if strings.ContainsAny(got, "\x1b\x07\u009b\u202e") {
		t.Fatalf("catches step output that reaches the terminal raw: streamed %q", got)
	}
	want := `esc a\x1b[2Jb osc \x1b]0;PWN\x07c c1 \u009b2Jd bidi evil\u202etxt.exe` + "\n" + `tail \x1b[0m` + "\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("catches escaped step output that differs or drops its last line: streamed %q, want suffix %q", got, want)
	}
}

// TestStepStreamRunes catches a stream that escapes a rune split across two writes as invalid
// bytes, or holds back a long line with no newline until the step ends (DR-38).
func TestStepStreamRunes(t *testing.T) {
	var w bytes.Buffer
	s, err := newStepStream(&w, GateStep{ID: "runes"})
	if err != nil {
		t.Fatalf("newStepStream: %v", err)
	}
	for _, p := range []string{"İstanbul \xc3", "\xa9t\xc3", "\xa9 仓库\n"} {
		if _, err := s.Write([]byte(p)); err != nil {
			t.Fatalf("Write(%q): %v", p, err)
		}
	}
	if want := "--- step runes\nİstanbul été 仓库\n"; w.String() != want {
		t.Fatalf("catches a split rune escaped as invalid bytes: streamed %q, want %q", w.String(), want)
	}
	w.Reset()
	long := strings.Repeat("é", 40000)
	if _, err := s.Write([]byte(long[:len(long)-1])); err != nil {
		t.Fatalf("Write(long line): %v", err)
	}
	if w.Len() == 0 || strings.Contains(w.String(), `\x`) {
		t.Fatalf("catches a long line held back or cut inside a rune: streamed %d bytes, escapes %v",
			w.Len(), strings.Contains(w.String(), `\x`))
	}
}

// failTailWriter accepts every write except one that carries the helper's last line, as a
// terminal that fails while the step's held tail is written at its end. It has no WriteString, so
// every write reaches Write.
type failTailWriter struct{ buf bytes.Buffer }

func (w *failTailWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("tail ")) {
		return 0, errors.New("terminal closed")
	}
	return w.buf.Write(p)
}

// TestStepStreamTailWriteFails catches a failed terminal write of the held last line that turns a
// measured step into not_measured: the exit code was measured and the runner keeps the raw output
// (DR-13), so the step keeps its result and the detail notes the failed write (#223).
func TestStepStreamTailWriteFails(t *testing.T) {
	t.Setenv(streamEscapeEnv(), "1")
	for _, tc := range []struct {
		name     string
		wantExit int
		want     Result
	}{
		{name: "green step", wantExit: 0, want: ResultGreen},
		{name: "red step", wantExit: 1, want: ResultRed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := GateStep{ID: "tail", Command: []string{os.Args[0], "-test.run=^$"}, Required: true,
				Expect: Expect{ExitCode: tc.wantExit}}
			def := Definition{FormatVersion: 1, Steps: []GateStep{step}}
			report, err := (&Runner{Output: &failTailWriter{}, Owner: rootsTestOwner(t)}).Run(context.Background(), def, t.TempDir(), nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := report.Steps[0]
			if got.Result != tc.want || got.ExitCode != 0 || !strings.Contains(got.Detail, "terminal closed") {
				t.Fatalf("step = %+v, want result %s, exit code 0 and a detail that notes the failed write", got, tc.want)
			}
		})
	}
}

// TestStepStreamHeldBytes catches a write whose complete line is streamed while the long rest of
// it, past streamLineLimit, is held back until the step ends (DR-38).
func TestStepStreamHeldBytes(t *testing.T) {
	var w bytes.Buffer
	s, err := newStepStream(&w, GateStep{ID: "held"})
	if err != nil {
		t.Fatalf("newStepStream: %v", err)
	}
	rest := strings.Repeat("x", 70000)
	if _, err := s.Write([]byte("a\n" + rest)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := "--- step held\na\n" + rest; w.String() != want {
		t.Fatalf("catches held bytes past the line limit: streamed %d bytes, want %d", w.Len(), len(want))
	}
}

// TestStepStreamByteWrites catches a stream that scans every held byte on each write: a step that
// writes one byte at a time onto a held line would then cost time quadratic in the line length.
func TestStepStreamByteWrites(t *testing.T) {
	s, err := newStepStream(io.Discard, GateStep{ID: "bytes"})
	if err != nil {
		t.Fatalf("newStepStream: %v", err)
	}
	writes := [][]byte{bytes.Repeat([]byte("x"), 60000)}
	for range 2000 {
		writes = append(writes, []byte("x"))
	}
	written := 0
	for _, p := range append(writes, []byte("\n")) {
		if _, err := s.Write(p); err != nil {
			t.Fatalf("Write: %v", err)
		}
		written += len(p)
	}
	if s.scanned > written {
		t.Fatalf("catches a write that scans the held line: scanned %d bytes for %d written", s.scanned, written)
	}
}
