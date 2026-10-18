package core_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The load sample and its default thresholds (SPEC §6.6, DR-6). GB is decimal, as the footprint
// ceilings already use it.
const (
	loadGB        = int64(1_000_000_000)
	loadWindow    = 200 * time.Millisecond
	loadCalmCores = int64(8)
)

func floatMeasured(v float64) core.Measured[float64] {
	return core.Measured[float64]{Value: v, Known: true}
}

func intMeasured(v int64) core.Measured[int64] {
	return core.Measured[int64]{Value: v, Known: true}
}

// calmLoad is a sample under every default threshold on a machine of the given cores.
func calmLoad(cores int64) core.LoadSample {
	return core.LoadSample{
		Cores:        intMeasured(cores),
		CPU:          core.LoadAverage{Avg: floatMeasured(10), Peak: floatMeasured(20)},
		RunQueue:     core.LoadAverage{Avg: floatMeasured(0.5), Peak: floatMeasured(1)},
		FreeMemory:   core.LoadLowest{Low: intMeasured(4 * loadGB)},
		TimerLatency: core.LoadPercentiles{P50: floatMeasured(0.5), P95: floatMeasured(1)},
	}
}

// TestLoadSampleVerdictThresholds pins each default threshold row: stop at p95 > 15 ms, free
// memory < 1 GB, run queue > 2 × cores or CPU > 90 %; wait at CPU > 80 %, run queue > cores,
// p95 > 5 ms or free memory < 2 GB; else start. A measured stop wins over a wait signal.
func TestLoadSampleVerdictThresholds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*core.LoadSample)
		want   core.Verdict
	}{
		{"a calm machine starts", func(*core.LoadSample) {}, core.VerdictStart},
		{"cpu at 80% still starts", func(s *core.LoadSample) { s.CPU.Avg = floatMeasured(80) }, core.VerdictStart},
		{"cpu over 80% waits", func(s *core.LoadSample) { s.CPU.Avg = floatMeasured(80.1) }, core.VerdictWait},
		{"cpu at 90% waits, not stops", func(s *core.LoadSample) { s.CPU.Avg = floatMeasured(90) }, core.VerdictWait},
		{"cpu over 90% stops", func(s *core.LoadSample) { s.CPU.Avg = floatMeasured(90.1) }, core.VerdictStop},
		{"run queue at the cores still starts", func(s *core.LoadSample) { s.RunQueue.Avg = floatMeasured(8) }, core.VerdictStart},
		{"run queue over the cores waits", func(s *core.LoadSample) { s.RunQueue.Avg = floatMeasured(9) }, core.VerdictWait},
		{"run queue at twice the cores waits, not stops", func(s *core.LoadSample) { s.RunQueue.Avg = floatMeasured(16) }, core.VerdictWait},
		{"run queue over twice the cores stops", func(s *core.LoadSample) { s.RunQueue.Avg = floatMeasured(17) }, core.VerdictStop},
		{"timer p95 at 5ms still starts", func(s *core.LoadSample) { s.TimerLatency.P95 = floatMeasured(5) }, core.VerdictStart},
		{"timer p95 over 5ms waits", func(s *core.LoadSample) { s.TimerLatency.P95 = floatMeasured(5.1) }, core.VerdictWait},
		{"timer p95 at 15ms waits, not stops", func(s *core.LoadSample) { s.TimerLatency.P95 = floatMeasured(15) }, core.VerdictWait},
		{"timer p95 over 15ms stops", func(s *core.LoadSample) { s.TimerLatency.P95 = floatMeasured(15.1) }, core.VerdictStop},
		{"free memory at 2GB still starts", func(s *core.LoadSample) { s.FreeMemory.Low = intMeasured(2 * loadGB) }, core.VerdictStart},
		{"free memory under 2GB waits", func(s *core.LoadSample) { s.FreeMemory.Low = intMeasured(2*loadGB - 1) }, core.VerdictWait},
		{"free memory at 1GB waits, not stops", func(s *core.LoadSample) { s.FreeMemory.Low = intMeasured(loadGB) }, core.VerdictWait},
		{"free memory under 1GB stops", func(s *core.LoadSample) { s.FreeMemory.Low = intMeasured(loadGB - 1) }, core.VerdictStop},
		{"a hard stop wins over a wait signal", func(s *core.LoadSample) {
			s.CPU.Avg = floatMeasured(85)
			s.TimerLatency.P95 = floatMeasured(16)
		}, core.VerdictStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := calmLoad(loadCalmCores)
			tc.mutate(&s)
			if got := s.Verdict(); got != tc.want {
				t.Fatalf("Verdict() of %+v = %q, want %q", s, got, tc.want)
			}
		})
	}
}

// TestLoadSampleUnknownWaits pins DR-6 for admission: an unmeasured CPU and run queue, or an
// unmeasured free memory, never gives start; a measured stop still stops.
func TestLoadSampleUnknownWaits(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*core.LoadSample)
		want   core.Verdict
	}{
		{"neither cpu nor run queue measured waits", func(s *core.LoadSample) {
			s.CPU = core.LoadAverage{Reason: "cpu_unreadable"}
			s.RunQueue = core.LoadAverage{Reason: "run_queue_unreadable"}
		}, core.VerdictWait},
		{"free memory unmeasured waits", func(s *core.LoadSample) {
			s.FreeMemory = core.LoadLowest{Reason: "free_memory_unreadable"}
		}, core.VerdictWait},
		{"a measured stop still stops when free memory is unknown", func(s *core.LoadSample) {
			s.CPU.Avg = floatMeasured(95)
			s.FreeMemory = core.LoadLowest{Reason: "free_memory_unreadable"}
		}, core.VerdictStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := calmLoad(loadCalmCores)
			tc.mutate(&s)
			if got := s.Verdict(); got != tc.want {
				t.Fatalf("Verdict() of %+v = %q, want %q", s, got, tc.want)
			}
		})
	}
}

// TestLoadSampleUnknownIsNeverZero pins DR-6: an unmeasured signal is printed as unknown with its
// reason, never as 0, and an all-unknown sample waits.
func TestLoadSampleUnknownIsNeverZero(t *testing.T) {
	s := calmLoad(loadCalmCores)
	s.CPU = core.LoadAverage{Reason: "cpu_unreadable"}
	s.RunQueue = core.LoadAverage{Reason: "run_queue_unreadable"}
	s.FreeMemory = core.LoadLowest{Reason: "free_memory_unreadable"}
	s.TimerLatency = core.LoadPercentiles{Reason: "timer_unreadable"}

	line := s.String()
	for _, label := range []string{"cpu_avg", "cpu_peak", "run_queue_avg", "run_queue_peak", "free_mem_low", "timer_p50", "timer_p95"} {
		if !strings.Contains(line, label+"=unknown") {
			t.Errorf("printed sample %q does not print %s as unknown", line, label)
		}
		if strings.Contains(line, label+"=0") {
			t.Errorf("printed sample %q reads unmeasured %s as 0", line, label)
		}
	}
	for _, reason := range []string{"cpu_unreadable", "run_queue_unreadable", "free_memory_unreadable", "timer_unreadable"} {
		if !strings.Contains(line, reason) {
			t.Errorf("printed sample %q does not carry reason %q for its unknown signal", line, reason)
		}
	}
	if !strings.Contains(line, "verdict=wait") {
		t.Errorf("printed sample %q does not give the unknown rows the verdict wait", line)
	}
}

// TestLoadSampleStringPrintsEveryValue pins the one-line output: every sample value and the verdict
// appear in it.
func TestLoadSampleStringPrintsEveryValue(t *testing.T) {
	s := calmLoad(loadCalmCores)
	s.CPU = core.LoadAverage{Avg: floatMeasured(12.5), Peak: floatMeasured(41)}
	s.RunQueue = core.LoadAverage{Avg: floatMeasured(1.25), Peak: floatMeasured(3)}
	s.FreeMemory = core.LoadLowest{Low: intMeasured(3_750_000_000)}
	s.TimerLatency = core.LoadPercentiles{P50: floatMeasured(0.75), P95: floatMeasured(2.5)}

	line := s.String()
	if strings.Contains(line, "\n") {
		t.Fatalf("String() = %q, want one line", line)
	}
	for _, want := range []string{
		"cpu_avg=12.5", "cpu_peak=41",
		"run_queue_avg=1.25", "run_queue_peak=3",
		"free_mem_low=3750000000",
		"timer_p50=0.75", "timer_p95=2.5",
		"verdict=start",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("printed sample %q lacks %q", line, want)
		}
	}
}

// TestMeasureLoadSample proves one sample over a window gives CPU busy average and peak, run queue
// average and peak, lowest free memory, timer latency p50 and p95, and a verdict on this OS. A
// signal the OS cannot read is unknown with a reason, never a measured 0 (DR-6).
func TestMeasureLoadSample(t *testing.T) {
	start := time.Now()
	s := core.MeasureLoad(loadWindow)
	if elapsed := time.Since(start); elapsed < loadWindow {
		t.Fatalf("MeasureLoad(%v) returned after %v, before its window ended", loadWindow, elapsed)
	}
	if !s.Cores.Known || s.Cores.Value < 1 {
		t.Fatalf("MeasureLoad cores = %+v, want the measured cores of this machine", s.Cores)
	}

	measured := 0
	if loadAverageOK(t, "cpu", s.CPU, 100) {
		measured++
	}
	if loadAverageOK(t, "run queue", s.RunQueue, 1_000_000) {
		measured++
	}
	if loadLowestOK(t, "free memory", s.FreeMemory) {
		measured++
	}
	if loadPercentilesOK(t, "timer latency", s.TimerLatency) {
		measured++
	}
	// This OS measures every signal it offers a source for, and no fewer: a sample that measures fewer
	// has lost a source, which the one-line output would hide (DR-6).
	if want := loadSignalsMeasured(); measured != want {
		t.Fatalf("MeasureLoad(%v) = %+v: %d of the four load signals is measured, want %d on %s",
			loadWindow, s, measured, want, runtime.GOOS)
	}

	switch got := s.Verdict(); got {
	case core.VerdictStart, core.VerdictWait, core.VerdictStop:
	default:
		t.Fatalf("Verdict() = %q, want start, wait or stop", got)
	}
	if line := s.String(); strings.Contains(line, "\n") || !strings.Contains(line, "verdict=") {
		t.Fatalf("String() = %q, want one line carrying the verdict", line)
	}
}

// loadSignalsMeasured is the number of the four load signals this OS offers a source for: Linux
// measures every one, Windows all but the run queue, and macOS, which gives its CPU time and its run
// queue only through Mach calls, the free memory and the timer latency. Each OS has exactly this
// many, so a source that silently dies is red (DR-6).
func loadSignalsMeasured() int {
	switch runtime.GOOS {
	case "windows":
		return 3
	case "darwin":
		return 2
	default:
		return 4
	}
}

// loadAverageOK checks one average/peak signal: both are measured or both unknown with a reason,
// a 0..max range and peak at least avg. It reports whether the signal is measured.
func loadAverageOK(t *testing.T, name string, v core.LoadAverage, max float64) bool {
	t.Helper()
	if !v.Avg.Known && !v.Peak.Known {
		if v.Reason == "" {
			t.Errorf("%s is unknown without a reason (DR-6)", name)
		}
		return false
	}
	if !v.Avg.Known || !v.Peak.Known {
		t.Errorf("%s is partly measured: avg %+v peak %+v", name, v.Avg, v.Peak)
		return false
	}
	for _, got := range []struct {
		label string
		m     core.Measured[float64]
	}{{"avg", v.Avg}, {"peak", v.Peak}} {
		if got.m.Value < 0 || got.m.Value > max {
			t.Errorf("%s %s = %v, want 0..%v", name, got.label, got.m.Value, max)
		}
	}
	if v.Peak.Value < v.Avg.Value {
		t.Errorf("%s peak %v is below avg %v", name, v.Peak.Value, v.Avg.Value)
	}
	if v.Reason != "" {
		t.Errorf("%s is measured but carries reason %q", name, v.Reason)
	}
	return true
}

// loadLowestOK checks the lowest-free-memory signal, measured in bytes.
func loadLowestOK(t *testing.T, name string, v core.LoadLowest) bool {
	t.Helper()
	if !v.Low.Known {
		if v.Reason == "" {
			t.Errorf("%s is unknown without a reason (DR-6)", name)
		}
		return false
	}
	if v.Low.Value <= 0 {
		t.Errorf("%s = %d bytes, want a positive free memory", name, v.Low.Value)
	}
	if v.Reason != "" {
		t.Errorf("%s is measured but carries reason %q", name, v.Reason)
	}
	return true
}

// loadPercentilesOK checks the timer-latency p50 and p95 signal, measured in milliseconds.
func loadPercentilesOK(t *testing.T, name string, v core.LoadPercentiles) bool {
	t.Helper()
	if !v.P50.Known && !v.P95.Known {
		if v.Reason == "" {
			t.Errorf("%s is unknown without a reason (DR-6)", name)
		}
		return false
	}
	if !v.P50.Known || !v.P95.Known {
		t.Errorf("%s is partly measured: p50 %+v p95 %+v", name, v.P50, v.P95)
		return false
	}
	if v.P50.Value < 0 || v.P95.Value < 0 {
		t.Errorf("%s = p50 %v p95 %v, want non-negative milliseconds", name, v.P50.Value, v.P95.Value)
	}
	if v.P95.Value < v.P50.Value {
		t.Errorf("%s p95 %v is below p50 %v", name, v.P95.Value, v.P50.Value)
	}
	if v.Reason != "" {
		t.Errorf("%s is measured but carries reason %q", name, v.Reason)
	}
	return true
}

// loadSpawnCall reports whether a call of pkg.name can start a child process: os.StartProcess,
// exec.Command, syscall.ForkExec and the CreateProcess family of the Windows API.
func loadSpawnCall(pkg, name string) bool {
	switch pkg {
	case "os":
		return name == "StartProcess"
	case "exec":
		return name == "Command" || name == "CommandContext"
	case "syscall":
		return name == "ForkExec" || name == "StartProcess"
	case "windows":
		return name == "CreateProcess" || name == "CreateProcessAsUser" || name == "CreateProcessW"
	default:
		return false
	}
}

// loadProcAllowed reports whether the load sample may look up the system procedure proc: reading the
// machine CPU times and its memory status starts no child process, while a lazily loaded procedure
// such as kernel32!CreateProcessW would start one without an import the scan could see.
func loadProcAllowed(proc string) bool {
	return proc == "GetSystemTimes" || proc == "GlobalMemoryStatusEx"
}

// TestLoadSampleStartsNoChildProcess pins the sample to this process: no file that defines or
// samples the load imports os/exec, calls a function that starts a child process, or looks up a
// system procedure that is no load source, so it can start no child process (SPEC §6.6). It also
// demands that the sampler of this OS defines newLoadSource, so a sample without a source never
// passes here.
func TestLoadSampleStartsNoChildProcess(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the core package directory: %v", err)
	}
	osFile := "capacity_" + runtime.GOOS + "_io.go"
	sampler := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(src)
		if !strings.Contains(text, "loadSource") && !strings.Contains(text, "LoadSample") &&
			!strings.Contains(text, "MeasureLoad") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if name == osFile && definesFunc(file, "newLoadSource") {
			sampler = true
		}
		checkNoChildProcess(t, name, file)
	}
	if !sampler {
		t.Fatalf("%s does not define newLoadSource: the load sampler of %s is missing", osFile, runtime.GOOS)
	}
}

// checkNoChildProcess fails name for every import, call and lazily looked-up procedure through which
// the load sample of that file could start a child process.
func checkNoChildProcess(t *testing.T, name string, file *ast.File) {
	t.Helper()
	for _, imp := range file.Imports {
		if path := strings.Trim(imp.Path.Value, `"`); path == "os/exec" {
			t.Errorf("%s imports os/exec, so the load sample may start a child process", name)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		checkCallStartsNoProcess(t, name, n)
		return true
	})
}

// checkCallStartsNoProcess fails name for one node that calls a function which starts a child
// process, or that loads a system procedure the load sample may not call.
func checkCallStartsNoProcess(t *testing.T, name string, n ast.Node) {
	t.Helper()
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	if loadSpawnCall(pkg.Name, sel.Sel.Name) {
		t.Errorf("%s calls %s.%s, which starts a child process", name, pkg.Name, sel.Sel.Name)
	}
	proc, ok := loadedProc(call, sel)
	if ok && !loadProcAllowed(proc) {
		t.Errorf("%s loads the procedure %s, which is no load source and may start a child process",
			name, proc)
	}
}

// loadedProc returns the system procedure a lazy NewProc call looks up by name.
func loadedProc(call *ast.CallExpr, sel *ast.SelectorExpr) (string, bool) {
	if sel.Sel.Name != "NewProc" || len(call.Args) != 1 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, `"`), true
}

// definesFunc reports whether file declares a function of this name.
func definesFunc(file *ast.File, name string) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return true
		}
	}
	return false
}
