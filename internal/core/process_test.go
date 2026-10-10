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
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The test binary runs itself as the child and the grandchild of a process tree. The role is set
// through the environment; init answers it before the test framework starts.
const (
	processRoleEnv       = "AIWE_PROCESS_TEST_ROLE"
	processDirEnv        = "AIWE_PROCESS_TEST_DIR"
	processIgnoreTermEnv = "AIWE_PROCESS_TEST_IGNORE_TERM"

	// processHelperLifetime bounds every helper process, so a failed test cannot leave one
	// running for longer than this.
	processHelperLifetime = 30 * time.Second
	// processBound bounds each wait of the test: setup, a stop, a time limit, a quiet check.
	processBound = 5 * time.Second
	// processQuiet is how long a heartbeat must stay unchanged to count as stopped.
	processQuiet     = 300 * time.Millisecond
	processBeatEvery = 10 * time.Millisecond
	processPollEvery = 25 * time.Millisecond
)

func init() {
	role := os.Getenv(processRoleEnv)
	if role == "" {
		return
	}
	os.Exit(runProcessHelper(role))
}

// runProcessHelper is the body of a helper process. tree starts a leaf as its first action
// (U22: a grandchild started before the child joins its job would escape), records both pids and
// beats; leaf beats; argv records the arguments it received.
func runProcessHelper(role string) int {
	dir := os.Getenv(processDirEnv)
	if os.Getenv(processIgnoreTermEnv) == "1" {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	}
	switch role {
	case "tree":
		leaf := exec.Command(os.Args[0])
		leaf.Env = append(os.Environ(), processRoleEnv+"=leaf")
		if err := leaf.Start(); err != nil {
			return 10
		}
		pids := fmt.Sprintf("%d %d", os.Getpid(), leaf.Process.Pid)
		if err := writeProcessFile(filepath.Join(dir, "pids"), []byte(pids)); err != nil {
			return 11
		}
		beatProcessFile(filepath.Join(dir, "child.beat"))
	case "leaf":
		beatProcessFile(filepath.Join(dir, "leaf.beat"))
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
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		child, dir := startProcessTree(t, ctx, nil, true)
		if err := waitProcessChild(t, child, 3*time.Second+processBound); err == nil {
			t.Error("Wait after the time limit = nil, want the error of a child stopped by its limit")
		}
		requireProcessTreeGone(t, dir)
	})

	if runtime.GOOS != "windows" {
		return // batch launchers exist only on Windows (SPEC §5.20)
	}

	t.Run("a time limit stops a tree started by a batch launcher", func(t *testing.T) {
		launcher := writeProcessLauncher(t, t.TempDir())
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		child, dir := startProcessTree(t, ctx, []string{launcher}, true)
		if err := waitProcessChild(t, child, 3*time.Second+processBound); err == nil {
			t.Error("Wait after the time limit = nil, want the error of a launcher stopped by its limit")
		}
		requireProcessTreeGone(t, dir)
	})

	t.Run("a batch launcher argument arrives intact or is refused", func(t *testing.T) {
		launcherDir := t.TempDir()
		launcher := writeProcessLauncher(t, launcherDir)
		injected := filepath.Join(launcherDir, "injected")
		tests := []struct {
			name string
			arg  string
			// quotable: cmd.exe takes the argument literally inside quotes, so refusing it
			// turns every run of a harness red.
			quotable bool
		}{
			{"plain word", "unit", true},
			{"space", "two words", true},
			{"empty", "", true},
			{"ampersand", "build&test", true},
			{"pipe", "a|b", true},
			{"redirect", "a<b>c", true},
			{"parentheses", "(group)", true},
			{"caret", "a^b", true},
			{"comma semicolon equals", "a,b;c=d", true},
			{"directory with trailing backslash", `build\out dir\`, true},
			{"command injection", "x & type nul > " + injected, true},
			{"percent expansion", "%PATH%", false},
			{"double quote", `say "hi"`, false},
			{"newline", "line one\nline two", false},
			{"unicode", "çalışma ağacı", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), processBound)
				defer cancel()
				child, err := core.Start(ctx, core.ChildSpec{
					Argv: []string{launcher, tt.arg},
					Dir:  dir,
					Env:  processEnv(dir, "argv", false),
				})
				if err != nil {
					if tt.quotable {
						t.Fatalf("Start refused %q: %v; want it quoted and passed", tt.arg, err)
					}
					if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
						t.Errorf("Start refused %q with %v, but the launcher ran", tt.arg, err)
					}
					return
				}
				if err := waitProcessChild(t, child, processBound); err != nil {
					t.Fatalf("launcher with %q: Wait = %v, want nil", tt.arg, err)
				}
				data, err := os.ReadFile(filepath.Join(dir, "argv.json"))
				if err != nil {
					t.Fatalf("launcher with %q did not reach the program: %v", tt.arg, err)
				}
				var got []string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("argv record %q: %v", data, err)
				}
				if want := []string{tt.arg}; !slices.Equal(got, want) {
					t.Errorf("launcher passed %q, program received %q", want, got)
				}
				if _, err := os.Stat(injected); err == nil {
					t.Errorf("argument %q ran as a command: %s exists", tt.arg, injected)
				}
			})
		}
	})
}

// writeProcessLauncher writes a batch launcher that hands every argument to the test binary.
func writeProcessLauncher(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "launch.cmd")
	body := "@\"" + os.Args[0] + "\" %*\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	return path
}

func processEnv(dir, role string, ignoreTerm bool) []string {
	env := append(os.Environ(), processRoleEnv+"="+role, processDirEnv+"="+dir)
	if ignoreTerm {
		env = append(env, processIgnoreTermEnv+"=1")
	}
	return env
}

// startProcessTree starts a child that starts a grandchild, and returns once both beat. argv
// nil runs the test binary itself as the child.
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
	readProcessPids(t, dir)
	requireProcessBeating(t, filepath.Join(dir, "child.beat"))
	requireProcessBeating(t, filepath.Join(dir, "leaf.beat"))
	return child, dir
}

func readProcessPids(t *testing.T, dir string) []int {
	t.Helper()
	end := time.Now().Add(processBound)
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
			t.Fatalf("the child recorded no pids within %v: %v", processBound, err)
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

// requireProcessBeating proves the process behind path runs, so a later quiet beat means stopped.
func requireProcessBeating(t *testing.T, path string) {
	t.Helper()
	end := time.Now().Add(processBound)
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
	t.Fatalf("%s did not beat within %v: the helper process is not running", filepath.Base(path), processBound)
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
	waitProcessChild(t, child, processBound)
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
