package gates

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// streamHelperEnv makes this test binary a helper step: it prints streamOutLine to its standard
// output and streamErrLine to its standard error, then waits up to streamWait for the file the
// variable names. It exits 0 once the file exists and 5 when the wait ends without it. Only a
// runner that streams the output while the step runs lets the test create the file in time. It
// opens no window.
const (
	streamHelperEnv = "AIWE_GATES_TEST_STREAM"
	streamOutLine   = "aiwe-stream-stdout-line"
	streamErrLine   = "aiwe-stream-stderr-line"
	streamWait      = 10 * time.Second
)

func init() {
	release := os.Getenv(streamHelperEnv)
	if release == "" {
		return
	}
	fmt.Fprintln(os.Stdout, streamOutLine)
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
	t.Setenv(streamHelperEnv, release)
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
