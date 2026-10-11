package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The test binary runs itself as the child and the grandchild of a process tree. The role is set
// through the environment; init answers it before the test framework starts.
// processTestEnv returns the env variable name of suffix (ROLE, DIR, IGNORE_TERM), derived from
// the product constant (DR-25).
func processTestEnv(suffix string) string {
	return strings.ToUpper(core.Product) + "_PROCESS_TEST_" + suffix
}

const (
	// processHelperLifetime bounds every helper process, so a failed test cannot leave one
	// running for longer than this.
	processHelperLifetime = 30 * time.Second
	// processBound bounds each wait of the test: setup, a stop, a quiet check, a helper's wait.
	processBound = 5 * time.Second
	// processOuterBound bounds what holds a wait of processBound: a time limit over a setup or a
	// waiting helper, a wait on a waiting helper. Its margin of processBound covers start-up.
	processOuterBound = 2 * processBound
	// processQuiet is how long a heartbeat must stay unchanged to count as stopped.
	processQuiet     = 300 * time.Millisecond
	processBeatEvery = 10 * time.Millisecond
	processPollEvery = 25 * time.Millisecond
	// processRaceAttempts is how many trees the U22 case starts: each is a chance for a grandchild
	// started first thing to run before its child joins the tree.
	processRaceAttempts = 20
)

func init() {
	role := os.Getenv(processTestEnv("ROLE"))
	if role == "" {
		return
	}
	os.Exit(runProcessHelper(role))
}

// runProcessHelper is the body of a helper process. tree starts a leaf as its first action
// (U22: a grandchild started before the child joins its job would escape), records both pids and
// beats; orphan starts a leaf, waits for its first beat and exits 0, leaving it behind; engine
// starts a tree through core.Start, waits for its leaf to beat and exits 0 without stopping it;
// term beats
// until SIGTERM, records it and exits 0; leaf records its pid and beats; argv records the arguments
// it received.
func runProcessHelper(role string) int {
	dir := os.Getenv(processTestEnv("DIR"))
	if os.Getenv(processTestEnv("IGNORE_TERM")) == "1" {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	}
	switch role {
	case "tree":
		leaf := exec.CommandContext(context.Background(), os.Args[0])
		leaf.Env = append(os.Environ(), processTestEnv("ROLE")+"=leaf")
		if err := leaf.Start(); err != nil {
			return 10
		}
		pids := fmt.Sprintf("%d %d", os.Getpid(), leaf.Process.Pid)
		if err := writeProcessFile(filepath.Join(dir, "pids"), []byte(pids)); err != nil {
			return 11
		}
		beatProcessFile(filepath.Join(dir, "child.beat"))
	case "orphan":
		leaf := exec.CommandContext(context.Background(), os.Args[0])
		leaf.Env = append(os.Environ(), processTestEnv("ROLE")+"=leaf")
		if err := leaf.Start(); err != nil {
			return 10
		}
		pids := fmt.Sprintf("%d %d", os.Getpid(), leaf.Process.Pid)
		if err := writeProcessFile(filepath.Join(dir, "pids"), []byte(pids)); err != nil {
			return 11
		}
		end := time.Now().Add(processBound)
		for {
			if _, ok := readProcessBeat(filepath.Join(dir, "leaf.beat")); ok {
				return 0
			}
			if time.Now().After(end) {
				return 15
			}
			time.Sleep(processBeatEvery)
		}
	case "term":
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		if err := writeProcessFile(filepath.Join(dir, "pids"), []byte(strconv.Itoa(os.Getpid()))); err != nil {
			return 11
		}
		beat := filepath.Join(dir, "child.beat")
		end := time.Now().Add(processHelperLifetime)
		for n := 1; time.Now().Before(end); n++ {
			select {
			case <-term:
				if err := writeProcessFile(filepath.Join(dir, "term"), []byte("SIGTERM")); err != nil {
					return 16
				}
				return 0
			case <-time.After(processBeatEvery):
				_ = writeProcessFile(beat, []byte(strconv.Itoa(n))) // a write lost to a reader is retried
			}
		}
	case "leaf":
		if err := writeProcessFile(filepath.Join(dir, "leaf.pid"), []byte(strconv.Itoa(os.Getpid()))); err != nil {
			return 11
		}
		beatProcessFile(filepath.Join(dir, "leaf.beat"))
	case "engine":
		// The engine starts a tree through core.Start and exits without stopping it; DR-15 says
		// the tree does not outlive the engine.
		child, err := core.Start(context.Background(), core.ChildSpec{
			Argv: []string{os.Args[0]},
			Dir:  dir,
			Env:  processEnv(dir, "tree", false),
		})
		if err != nil {
			return 17
		}
		_ = child // never stopped: the engine exits with the tree running
		end := time.Now().Add(processBound)
		for {
			if _, ok := readProcessBeat(filepath.Join(dir, "leaf.beat")); ok {
				return 0
			}
			if time.Now().After(end) {
				return 15
			}
			time.Sleep(processBeatEvery)
		}
	case "argv":
		data, err := json.Marshal(os.Args[1:])
		if err != nil {
			return 13
		}
		if err := writeProcessFile(filepath.Join(dir, "argv.json"), data); err != nil {
			return 14
		}
	default:
		return 12
	}
	return 0
}

// writeProcessFile writes through a rename, so a reader never sees a partial file.
func writeProcessFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func beatProcessFile(path string) {
	end := time.Now().Add(processHelperLifetime)
	for n := 1; time.Now().Before(end); n++ {
		_ = writeProcessFile(path, []byte(strconv.Itoa(n))) // a write lost to a reader is retried
		time.Sleep(processBeatEvery)
	}
}

func TestProcessTree(t *testing.T) {
	t.Run("stop ends a tree that obeys the ask", func(t *testing.T) {
		child, dir := startProcessTree(t, t.Context(), nil, false)
		stopProcessTree(t, child, 2*time.Second)
		requireProcessTreeGone(t, dir)
	})

	t.Run("stop kills a tree that ignores the ask after the grace", func(t *testing.T) {
		child, dir := startProcessTree(t, t.Context(), nil, true)
		stopProcessTree(t, child, 200*time.Millisecond)
		requireProcessTreeGone(t, dir)
	})

	t.Run("a time limit stops the whole tree", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		child, dir := startProcessTree(t, ctx, nil, true)
		if err := waitProcessChild(t, child, processOuterBound+processBound); err == nil {
			t.Error("Wait after the time limit = nil, want the error of a child stopped by its limit")
		}
		requireProcessTreeGone(t, dir)
	})

	t.Run("stop ends a grandchild left behind by a child that exited", func(t *testing.T) {
		dir := t.TempDir()
		child := startProcessChild(t, t.Context(), dir, "orphan")
		if err := waitProcessChild(t, child, processOuterBound); err != nil {
			t.Fatalf("Wait = %v, want nil: the child did not see its grandchild run", err)
		}
		stopProcessTree(t, child, 200*time.Millisecond)
		requireProcessBeatsStopped(t, filepath.Join(dir, "leaf.beat"))
	})

	t.Run("a time limit stops a grandchild left behind by a child that exited", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		dir := t.TempDir()
		child := startProcessChild(t, ctx, dir, "orphan")
		if err := waitProcessChild(t, child, processOuterBound); err != nil {
			t.Fatalf("Wait = %v, want nil: the child did not see its grandchild run before the limit", err)
		}
		<-ctx.Done()
		requireProcessBeatsStopped(t, filepath.Join(dir, "leaf.beat"))
	})

	t.Run("a grandchild started first thing cannot run outside the tree", func(t *testing.T) {
		beats := make([]string, 0, processRaceAttempts)
		for range processRaceAttempts {
			dir := t.TempDir()
			child := startProcessChild(t, t.Context(), dir, "tree")
			requireProcessFile(t, filepath.Join(dir, "leaf.pid"))
			stopProcessTree(t, child, 200*time.Millisecond)
			beats = append(beats, filepath.Join(dir, "leaf.beat"))
		}
		requireProcessBeatsStopped(t, beats...) // U22: a leaf that escaped the tree outlives its stop
	})

	if runtime.GOOS != "windows" {
		t.Run("stop ends a child that obeys the ask within the grace, without a kill", func(t *testing.T) {
			dir := t.TempDir()
			child := startProcessChild(t, t.Context(), dir, "term")
			requireProcessBeating(t, filepath.Join(dir, "child.beat"), time.Now().Add(processBound))
			grace := processBound
			start := time.Now()
			done := make(chan error, 1)
			go func() { done <- child.StopTree(grace) }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("StopTree(%v) = %v, want nil", grace, err)
				}
			case <-time.After(grace + processBound):
				t.Fatalf("StopTree(%v) did not return within %v", grace, grace+processBound)
			}
			if took := time.Since(start); took >= grace {
				t.Errorf("StopTree took %v, want less than the grace %v: the child that obeys SIGTERM was not asked", took, grace)
			}
			if err := waitProcessChild(t, child, processBound); err != nil {
				t.Errorf("Wait = %v, want nil: the child was killed instead of ending on SIGTERM", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "term")); err != nil {
				t.Errorf("the child recorded no SIGTERM: %v", err)
			}
		})
		return // the case below runs only on Windows
	}

	// DR-15: the job kills what it holds when its last handle closes, so a tree never outlives an
	// engine that exits, or crashes, without stopping it. The test starts the engine in a tree of
	// its own, so cleanup still ends a leaf that escaped.
	t.Run("a tree does not outlive the engine that started it", func(t *testing.T) {
		dir := t.TempDir()
		engine := startProcessChild(t, t.Context(), dir, "engine")
		if err := waitProcessChild(t, engine, processOuterBound); err != nil {
			t.Fatalf("engine Wait = %v, want nil: the engine did not see its tree run", err)
		}
		readProcessPids(t, dir, time.Now().Add(processBound))
		requireProcessBeatsStoppedWithin(t, 3*time.Second,
			filepath.Join(dir, "child.beat"), filepath.Join(dir, "leaf.beat"))
	})
}

func processEnv(dir, role string, ignoreTerm bool) []string {
	env := append(os.Environ(), processTestEnv("ROLE")+"="+role, processTestEnv("DIR")+"="+dir)
	if ignoreTerm {
		env = append(env, processTestEnv("IGNORE_TERM")+"=1")
	}
	return env
}

// startProcessTree starts a child that starts a grandchild, and returns once both beat. The setup
// is one wait of processBound. argv nil runs the test binary itself as the child.
func startProcessTree(t *testing.T, ctx context.Context, argv []string, ignoreTerm bool) (*core.Child, string) {
	t.Helper()
	dir := t.TempDir()
	if argv == nil {
		argv = []string{os.Args[0]}
	}
	child, err := core.Start(ctx, core.ChildSpec{Argv: argv, Dir: dir, Env: processEnv(dir, "tree", ignoreTerm)})
	if err != nil {
		t.Fatalf("Start(%q) = %v, want a running child", argv, err)
	}
	t.Cleanup(func() { killProcessTreeAfterFailure(t, dir) })
	end := time.Now().Add(processBound)
	readProcessPids(t, dir, end)
	requireProcessBeating(t, filepath.Join(dir, "child.beat"), end)
	requireProcessBeating(t, filepath.Join(dir, "leaf.beat"), end)
	return child, dir
}

// startProcessChild starts the test binary in role; cleanup stops its tree and kills what a failed
// test left.
func startProcessChild(t *testing.T, ctx context.Context, dir, role string) *core.Child {
	t.Helper()
	child, err := core.Start(ctx, core.ChildSpec{Argv: []string{os.Args[0]}, Dir: dir, Env: processEnv(dir, role, false)})
	if err != nil {
		t.Fatalf("Start(%s) = %v, want a running child", role, err)
	}
	t.Cleanup(func() {
		_ = child.StopTree(200 * time.Millisecond) // best effort: a passed test has proven the tree gone
		killProcessTreeAfterFailure(t, dir)
	})
	return child
}

// requireProcessFile waits for path to exist.
func requireProcessFile(t *testing.T, path string) {
	t.Helper()
	end := time.Now().Add(processBound)
	for {
		_, err := os.Stat(path)
		if err == nil {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("%s did not appear within %v: %v", filepath.Base(path), processBound, err)
		}
		time.Sleep(processPollEvery)
	}
}

// readProcessPids waits until end for the pids the child records.
func readProcessPids(t *testing.T, dir string, end time.Time) []int {
	t.Helper()
	for {
		data, err := os.ReadFile(filepath.Join(dir, "pids"))
		if err == nil {
			var pids []int
			for _, f := range strings.Fields(string(data)) {
				pid, convErr := strconv.Atoi(f)
				if convErr != nil {
					t.Fatalf("pids record %q: %v", data, convErr)
				}
				pids = append(pids, pid)
			}
			return pids
		}
		if time.Now().After(end) {
			t.Fatalf("the child recorded no pids by the deadline: %v", err)
		}
		time.Sleep(processPollEvery)
	}
}

// killProcessTreeAfterFailure kills what a failed test may have left; a passed test has proven
// the tree gone, so it kills nothing.
func killProcessTreeAfterFailure(t *testing.T, dir string) {
	if !t.Failed() {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "pids"))
	if err != nil {
		return
	}
	for _, f := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill() // best effort: the process may already be gone
		}
	}
}

func readProcessBeat(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return string(data), true
}

// requireProcessBeating proves by end that the process behind path runs, so a later quiet beat
// means stopped.
func requireProcessBeating(t *testing.T, path string, end time.Time) {
	t.Helper()
	first, ok := readProcessBeat(path)
	for time.Now().Before(end) {
		time.Sleep(processPollEvery)
		now, nowOK := readProcessBeat(path)
		if ok && nowOK && now != first {
			return
		}
		if !ok {
			first, ok = now, nowOK
		}
	}
	t.Fatalf("%s did not beat by the deadline: the helper process is not running", filepath.Base(path))
}

// requireProcessTreeGone fails when the child or the grandchild still beats.
func requireProcessTreeGone(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"child.beat", "leaf.beat"} {
		path := filepath.Join(dir, name)
		end := time.Now().Add(processBound)
		for {
			before, beforeOK := readProcessBeat(path)
			time.Sleep(processQuiet)
			after, afterOK := readProcessBeat(path)
			if beforeOK && afterOK && before == after {
				break
			}
			if time.Now().After(end) {
				t.Errorf("%s still beats %v after the stop: a process of the tree outlived it", name, processBound)
				break
			}
		}
	}
}

// requireProcessBeatsStopped fails when a beat at any path still changes after processBound; a
// beat that stays the same, or stays absent, for processQuiet counts as stopped.
func requireProcessBeatsStopped(t *testing.T, paths ...string) {
	t.Helper()
	requireProcessBeatsStoppedWithin(t, processBound, paths...)
}

// requireProcessBeatsStoppedWithin is requireProcessBeatsStopped with its own bound.
func requireProcessBeatsStoppedWithin(t *testing.T, bound time.Duration, paths ...string) {
	t.Helper()
	end := time.Now().Add(bound)
	pending := paths
	for {
		before := make(map[string]string, len(pending))
		for _, p := range pending {
			before[p], _ = readProcessBeat(p)
		}
		time.Sleep(processQuiet)
		var still []string
		for _, p := range pending {
			if now, _ := readProcessBeat(p); now != before[p] {
				still = append(still, p)
			}
		}
		if len(still) == 0 {
			return
		}
		if time.Now().After(end) {
			for _, p := range still {
				t.Errorf("%s still beats %v after the stop: a process of the tree outlived it", p, bound)
			}
			return
		}
		pending = still
	}
}

func stopProcessTree(t *testing.T, child *core.Child, grace time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- child.StopTree(grace) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StopTree(%v) = %v, want nil", grace, err)
		}
	case <-time.After(grace + processBound):
		t.Fatalf("StopTree(%v) did not return within %v", grace, grace+processBound)
	}
	_ = waitProcessChild(t, child, processBound) // a stopped child ends with a kill status, not nil
}

// waitProcessChild returns the error of Wait, failing the test when Wait does not return in bound.
func waitProcessChild(t *testing.T, child *core.Child, bound time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("Wait did not return within %v", bound)
		return errors.New("unreachable")
	}
}
