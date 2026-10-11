package gates

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// workersArgvEnv makes this test binary a fake `go` tool: it writes the arguments it was started
// with, one per line, to the file the variable names, prints the `go test -json` output of one
// package that ran 2.4 s, far under half of the 60 s floor limit, and exits 0. It opens no window.
const workersArgvEnv = "AIWE_GATES_TEST_ARGV"

func init() {
	path := os.Getenv(workersArgvEnv)
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
		os.Exit(3)
	}
	if _, err := os.Stdout.WriteString(durationsTestPackage("github.com/sungurerdim/aiwe/internal/core", 2.4)); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

// TestWorkers pins DR-7 for the test step's argv: one `-p=N` goes into a `go test` command and
// nowhere else; the test binary's own flags after `-args` stay as declared; no `-p`, `--p` or
// `--p=` of the declared command survives to override N.
func TestWorkers(t *testing.T) {
	goBin := filepath.Join("usr", "local", "go", "bin", "go")
	cases := []struct {
		name    string
		argv    []string
		workers int
		want    []string
	}{
		{"one worker goes right after test", []string{"go", "test", "-json", "./..."}, 1, []string{"go", "test", "-p=1", "-json", "./..."}},
		{"eight workers go right after test", []string{"go", "test", "-json", "./..."}, 8, []string{"go", "test", "-p=8", "-json", "./..."}},
		{"sixteen workers go right after test", []string{"go", "test", "-json", "./..."}, 16, []string{"go", "test", "-p=16", "-json", "./..."}},
		{"a go tool named by its path is still go test", []string{goBin, "test", "./..."}, 4, []string{goBin, "test", "-p=4", "./..."}},
		{"a shell command is not go test and stays as declared", []string{"sh", "-c", "go test ./..."}, 4, []string{"sh", "-c", "go test ./..."}},
		{"go vet is not go test and stays as declared", []string{"go", "vet", "./..."}, 4, []string{"go", "vet", "./..."}},
		{"a test word that is not the go subcommand stays as declared", []string{"make", "test"}, 4, []string{"make", "test"}},
		{"a declared -p=1 at the end is replaced, not doubled", []string{"go", "test", "./...", "-p=1"}, 4, []string{"go", "test", "-p=4", "./..."}},
		{"a declared -p 2 and its value are replaced", []string{"go", "test", "-p", "2", "-json", "./..."}, 4, []string{"go", "test", "-p=4", "-json", "./..."}},
		{"a declared --p=2 cannot override the measured count", []string{"go", "test", "--p=2", "./..."}, 4, []string{"go", "test", "-p=4", "./..."}},
		{"a declared --p 2 and its value cannot override the measured count", []string{"go", "test", "./...", "--p", "2"}, 4, []string{"go", "test", "-p=4", "./..."}},
		{"the test binary's -p 3 after -args is kept", []string{"go", "test", "./...", "-args", "-p", "3"}, 4, []string{"go", "test", "-p=4", "./...", "-args", "-p", "3"}},
		{"the test binary's -p=3 after -args is kept", []string{"go", "test", "./...", "-args", "-p=3"}, 4, []string{"go", "test", "-p=4", "./...", "-args", "-p=3"}},
		{"a -p before -args goes and the one after -args stays", []string{"go", "test", "-p", "2", "./...", "-args", "-p", "3"}, 4, []string{"go", "test", "-p=4", "./...", "-args", "-p", "3"}},
		{"a -args with no -p after it keeps its flags", []string{"go", "test", "./...", "-args", "-v"}, 4, []string{"go", "test", "-p=4", "./...", "-args", "-v"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared := slices.Clone(tc.argv)
			got := withParallelism(tc.argv, tc.workers)
			if !slices.Equal(got, tc.want) {
				t.Errorf("withParallelism(%q, %d) = %q, want %q", declared, tc.workers, got, tc.want)
			}
			if !slices.Equal(tc.argv, declared) {
				t.Errorf("declared argv changed to %q; want it untouched", tc.argv)
			}
		})
	}
}

// workersFakeGo copies this test binary to a file named like the go tool, so a gate step that
// starts it is a `go test` command whose argv the test can read back.
func workersFakeGo(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("the test binary has no path: %v", err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(t.TempDir(), name)
	src, err := os.Open(self)
	if err != nil {
		t.Fatalf("open the test binary: %v", err)
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		t.Fatalf("create the fake go tool: %v", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		t.Fatalf("copy the fake go tool: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close the fake go tool: %v", err)
	}
	return dst
}

// TestRunnerWorkers catches the gate's test step run without the measured worker count, or run
// without printing that count with its inputs (DR-7). The declared gate stays untouched.
func TestRunnerWorkers(t *testing.T) {
	goBin := workersFakeGo(t)
	cases := []struct {
		name     string
		capacity core.Capacity
		wantArgv []string
	}{
		{"measured cores split by jobs set -p", core.CapacityFor(core.Measured[int64]{Value: 8, Known: true}, 2), []string{"test", "-p=4", "./..."}},
		{"unmeasured cores run at the minimum and print unknown", core.CapacityFor(core.Measured[int64]{}, 2), []string{"test", "-p=1", "./..."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argvFile := filepath.Join(t.TempDir(), "argv.txt")
			t.Setenv(workersArgvEnv, argvFile)
			declared := []string{goBin, "test", "-p", "1", "./..."}
			def := Definition{FormatVersion: 1, Steps: []GateStep{{ID: "test", Command: slices.Clone(declared), Required: true}}}
			report, err := (&Runner{Capacity: tc.capacity}).Run(context.Background(), def, t.TempDir(), nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if report.Result != ResultGreen {
				t.Fatalf("report = %+v, want a green gate from the fake go tool", report)
			}
			raw, err := os.ReadFile(argvFile)
			if err != nil {
				t.Fatalf("the fake go tool left no argv: %v", err)
			}
			if got := strings.Split(string(raw), "\n"); !slices.Equal(got, tc.wantArgv) {
				t.Errorf("the test step ran with %q, want %q", got, tc.wantArgv)
			}
			if want := tc.capacity.String(); !strings.Contains(report.Steps[0].Detail, want) {
				t.Errorf("test step detail %q, want the worker count %q", report.Steps[0].Detail, want)
			}
			if !slices.Equal(def.Steps[0].Command, declared) {
				t.Errorf("declared command changed to %q; want it untouched", def.Steps[0].Command)
			}
		})
	}
}

// TestRunnerWorkersRequired catches a test step run with an assumed worker count: a Runner with no
// chosen capacity leaves the step not_measured, the gate not green, and the fake go tool never
// starts (DR-7, DR-13).
func TestRunnerWorkersRequired(t *testing.T) {
	goBin := workersFakeGo(t)
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv(workersArgvEnv, argvFile)
	def := Definition{FormatVersion: 1, Steps: []GateStep{{ID: "test", Command: []string{goBin, "test", "./..."}, Required: true}}}
	report, err := (&Runner{}).Run(context.Background(), def, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s := report.Steps[0]; s.Result != ResultNotMeasured || s.ExitCode != exitUnknown {
		t.Errorf("test step = %+v, want %q with exit code %d", s, ResultNotMeasured, exitUnknown)
	}
	if report.Result == ResultGreen {
		t.Errorf("gate result = %q, want it not green", report.Result)
	}
	if _, err := os.Stat(argvFile); err == nil {
		t.Errorf("the test step started; want it never started")
	}
}
