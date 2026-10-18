package core

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The limits that can bind a worker count (DR-3).
const (
	boundCoresPerJob = "cores/jobs"
	boundMinimum     = "minimum"
)

// Capacity is a worker count chosen from measured cores and concurrent jobs (DR-3).
type Capacity struct {
	Cores Measured[int64]
	Jobs  int
	// JobsFrom names where the concurrent job count came from; empty prints nothing.
	JobsFrom string
	Workers  int
}

// CapacityFor returns workers = cores ÷ jobs, rounded down, minimum 1. Unmeasured cores or a job
// count below 1 give the minimum: an unknown core count is never read as a number.
func CapacityFor(cores Measured[int64], jobs int) Capacity {
	c := Capacity{Cores: cores, Jobs: jobs, Workers: 1}
	if cores.Known && jobs >= 1 && cores.Value/int64(jobs) > 1 {
		c.Workers = int(cores.Value / int64(jobs))
	}
	return c
}

// bound names the limit that set the worker count.
func (c Capacity) bound() string {
	if c.Cores.Known && c.Jobs >= 1 && c.Cores.Value/int64(c.Jobs) >= 1 {
		return boundCoresPerJob
	}
	return boundMinimum
}

// String prints the chosen worker count with its inputs and the limit that bound it (DR-3).
func (c Capacity) String() string {
	cores := "unknown"
	if c.Cores.Known {
		cores = fmt.Sprint(c.Cores.Value)
	}
	line := fmt.Sprintf("workers=%d cores=%s jobs=%d bound=%s", c.Workers, cores, c.Jobs, c.bound())
	if c.JobsFrom != "" {
		line += " jobs_from=" + c.JobsFrom
	}
	return line
}

// MeasureCores measures the cores this process may use at run time: the lower of the machine's
// usable CPUs and the scheduler's limit, which follows a cgroup CPU limit on Linux (SPEC §6.6). A
// count below 1 is unknown.
func MeasureCores() Measured[int64] {
	n := min(runtime.NumCPU(), runtime.GOMAXPROCS(0))
	if n < 1 {
		return Measured[int64]{}
	}
	return Measured[int64]{Value: int64(n), Known: true}
}

// Workers is the worker count for jobs concurrent jobs on the measured cores (DR-3).
func Workers(jobs int) int {
	return CapacityFor(MeasureCores(), jobs).Workers
}

// The default load thresholds of SPEC §6.6 and DR-6: a sample over a stop threshold stops parallel
// work, a sample over a wait threshold holds it back. GB is decimal, as the footprint ceilings use
// it.
const (
	loadStopCPUPercent  = 90.0
	loadWaitCPUPercent  = 80.0
	loadStopQueueFactor = 2.0
	loadStopP95Millis   = 15.0
	loadWaitP95Millis   = 5.0
	loadStopFreeBytes   = 1_000_000_000
	loadWaitFreeBytes   = 2_000_000_000

	// loadSteps is the number of readings one sample window takes.
	loadSteps = 16
	// loadDefaultWindow is the window MeasureLoad samples when the caller asks for none.
	loadDefaultWindow = 200 * time.Millisecond
)

// The reasons a load signal carries when nothing could read it (DR-6).
const (
	loadReasonUnreadable = "unreadable"
	loadReasonTimer      = "timer_unmeasured"
)

// Verdict is what a load sample tells the caller to do with parallel work (SPEC §6.6): start, hold
// back (wait) or stop.
type Verdict string

// The three load verdicts.
const (
	VerdictStart Verdict = "start"
	VerdictWait  Verdict = "wait"
	VerdictStop  Verdict = "stop"
)

// LoadAverage is the average and peak of one load signal over a sample window; both values are
// measured or both are unknown with the reason (DR-6).
type LoadAverage struct {
	Avg, Peak Measured[float64]
	Reason    string
}

// LoadLowest is one free-memory reading in bytes, the lowest of them at the sample level, or the
// reason it is unknown (DR-6).
type LoadLowest struct {
	Low    Measured[int64]
	Reason string
}

// LoadPercentiles is the timer latency of a sample window in milliseconds, at p50 and p95, or the
// reason it is unknown (DR-6).
type LoadPercentiles struct {
	P50, P95 Measured[float64]
	Reason   string
}

// LoadSample is one measurement of the machine load over a window (SPEC §6.6, DR-6): the CPU busy
// share of this machine in percent, its run queue in tasks, its lowest free memory in bytes, its
// timer latency in milliseconds and the cores this process may use. A signal this OS cannot read is
// unknown with a reason, never a measured 0.
type LoadSample struct {
	Cores        Measured[int64]
	CPU          LoadAverage
	RunQueue     LoadAverage
	FreeMemory   LoadLowest
	TimerLatency LoadPercentiles
}

// MeasureLoad takes one load sample of window length: the CPU busy average and peak, the run queue
// average and peak, the lowest free memory and the timer latency p50 and p95 of this machine, plus
// the verdict its default thresholds give (SPEC §6.6, DR-6). A signal this OS offers no source for
// is unknown with a reason, never a measured 0. It starts no child process.
func MeasureLoad(window time.Duration) LoadSample {
	if window <= 0 {
		window = loadDefaultWindow
	}
	step := window / loadSteps
	if step <= 0 {
		step = time.Microsecond
	}
	cores := MeasureCores()
	read := newLoadSource()
	var cpu, queue averageTracker
	free := LoadLowest{}
	latencies := make([]float64, 0, loadSteps)
	deadline := time.Now()
	for range loadSteps {
		r := read()
		cpu.add(r.CPU)
		queue.add(r.RunQueue)
		free = lowestMemory(free, r.FreeMemory)
		deadline = deadline.Add(step)
		latencies = append(latencies, sleepOvershoot(deadline))
	}
	return LoadSample{
		Cores:        cores,
		CPU:          cpu.average(),
		RunQueue:     queue.average(),
		FreeMemory:   free,
		TimerLatency: timerPercentiles(latencies),
	}
}

// Verdict is what the sample's default thresholds say about parallel work: stop for a hard stop
// signal, wait for a wait signal or for a signal nothing could measure, else start (SPEC §6.6,
// DR-6).
func (s LoadSample) Verdict() Verdict {
	if s.stopSignal() {
		return VerdictStop
	}
	if s.waitSignal() {
		return VerdictWait
	}
	return VerdictStart
}

// stopSignal reports whether the sample is over a hard stop threshold: CPU over 90 %, the run queue
// over twice the cores, the timer p95 over 15 ms or free memory under 1 GB. A measured stop wins
// over every wait signal.
func (s LoadSample) stopSignal() bool {
	if over(s.CPU.Avg, loadStopCPUPercent) || over(s.TimerLatency.P95, loadStopP95Millis) {
		return true
	}
	if s.Cores.Known && over(s.RunQueue.Avg, loadStopQueueFactor*float64(s.Cores.Value)) {
		return true
	}
	return s.FreeMemory.Low.Known && s.FreeMemory.Low.Value < loadStopFreeBytes
}

// waitSignal reports whether the sample should hold parallel work back: CPU over 80 %, the run
// queue over the cores, the timer p95 over 5 ms or free memory under 2 GB. A machine whose CPU busy
// and run queue are both unmeasured, or whose free memory is unmeasured, never starts work (DR-6).
func (s LoadSample) waitSignal() bool {
	if over(s.CPU.Avg, loadWaitCPUPercent) || over(s.TimerLatency.P95, loadWaitP95Millis) {
		return true
	}
	if s.Cores.Known && over(s.RunQueue.Avg, float64(s.Cores.Value)) {
		return true
	}
	if s.FreeMemory.Low.Known && s.FreeMemory.Low.Value < loadWaitFreeBytes {
		return true
	}
	if !s.CPU.Avg.Known && !s.RunQueue.Avg.Known {
		return true
	}
	return !s.FreeMemory.Low.Known
}

// String prints one line with every sample value, the reason of each unmeasured signal and the
// verdict (SPEC §6.6). An unmeasured signal prints as unknown with its reason, never as 0 (DR-6).
func (s LoadSample) String() string {
	var b strings.Builder
	b.WriteString("load")
	fmt.Fprintf(&b, " cpu_avg=%s cpu_peak=%s", pointText(s.CPU.Avg, s.CPU.Reason), pointText(s.CPU.Peak, s.CPU.Reason))
	fmt.Fprintf(&b, " run_queue_avg=%s run_queue_peak=%s", pointText(s.RunQueue.Avg, s.RunQueue.Reason), pointText(s.RunQueue.Peak, s.RunQueue.Reason))
	fmt.Fprintf(&b, " free_mem_low=%s", memoryText(s.FreeMemory))
	fmt.Fprintf(&b, " timer_p50=%s timer_p95=%s", pointText(s.TimerLatency.P50, s.TimerLatency.Reason), pointText(s.TimerLatency.P95, s.TimerLatency.Reason))
	fmt.Fprintf(&b, " cores=%s verdict=%s", coresText(s.Cores), s.Verdict())
	return b.String()
}

// loadPoint is one reading of a load signal: a value, or the reason it is unknown (DR-6).
type loadPoint struct {
	Value  Measured[float64]
	Reason string
}

// loadReading is one instant's reading of the machine load: the CPU busy share in percent, the run
// queue length and the free memory in bytes (SPEC §6.6).
type loadReading struct {
	CPU        loadPoint
	RunQueue   loadPoint
	FreeMemory LoadLowest
}

// loadSource reads the machine load once; one OS implements it in capacity_<os>_io.go. The first
// reading of a signal that needs two readings is unknown with a reason, never 0 (DR-6).
type loadSource func() loadReading

// averageTracker folds the readings of one signal over a sample window into its average, peak and
// the reason it stayed unknown, if it did (DR-6).
type averageTracker struct {
	sum    float64
	count  int
	peak   float64
	reason string
}

// add folds one reading into the tracker.
func (t *averageTracker) add(p loadPoint) {
	if !p.Value.Known {
		if t.reason == "" {
			t.reason = p.Reason
		}
		return
	}
	if t.count == 0 || p.Value.Value > t.peak {
		t.peak = p.Value.Value
	}
	t.sum += p.Value.Value
	t.count++
}

// average returns the average and peak of the folded readings; with none, both are unknown with the
// reason the readings gave (DR-6).
func (t averageTracker) average() LoadAverage {
	if t.count == 0 {
		return LoadAverage{Reason: reasonOr(t.reason, loadReasonUnreadable)}
	}
	return LoadAverage{Avg: measuredFloat(t.sum / float64(t.count)), Peak: measuredFloat(t.peak)}
}

// lowestMemory returns the lower of two free-memory readings; a reading nothing could take keeps its
// reason while no reading is measured (DR-6).
func lowestMemory(cur, next LoadLowest) LoadLowest {
	if !next.Low.Known {
		if cur.Low.Known || cur.Reason != "" {
			return cur
		}
		return LoadLowest{Reason: reasonOr(next.Reason, loadReasonUnreadable)}
	}
	if cur.Low.Known && cur.Low.Value <= next.Low.Value {
		return cur
	}
	return LoadLowest{Low: next.Low}
}

// sleepOvershoot sleeps until target and returns how many milliseconds past it the timer woke, never
// negative (SPEC §6.6).
func sleepOvershoot(target time.Time) float64 {
	if wait := time.Until(target); wait > 0 {
		time.Sleep(wait)
	}
	return max(0, float64(time.Since(target).Microseconds())/1000)
}

// timerPercentiles returns the p50 and p95 of the measured timer latencies in milliseconds; with no
// latency, both are unknown with a reason, never 0 (SPEC §6.6, DR-6).
func timerPercentiles(latencies []float64) LoadPercentiles {
	if len(latencies) == 0 {
		return LoadPercentiles{Reason: loadReasonTimer}
	}
	sorted := slices.Clone(latencies)
	slices.Sort(sorted)
	return LoadPercentiles{
		P50: measuredFloat(sorted[len(sorted)*50/100]),
		P95: measuredFloat(sorted[min(len(sorted)*95/100, len(sorted)-1)]),
	}
}

// over reports whether a measured value is above limit; an unmeasured one is never over it.
func over(m Measured[float64], limit float64) bool { return m.Known && m.Value > limit }

// reasonOr returns reason, or fallback when reason is empty: an unknown signal always carries one
// (DR-6).
func reasonOr(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}

// measuredFloat returns a measured float64 value.
func measuredFloat(v float64) Measured[float64] { return Measured[float64]{Value: v, Known: true} }

// measuredInt returns a measured int64 value.
func measuredInt(v int64) Measured[int64] { return Measured[int64]{Value: v, Known: true} }

// unknownText prints an unmeasured signal as unknown with its reason, never as a number (DR-6).
func unknownText(reason string) string {
	if reason == "" {
		return "unknown"
	}
	return "unknown(" + reason + ")"
}

// pointText prints a load value as a number, or as unknown with its reason (DR-6).
func pointText(v Measured[float64], reason string) string {
	if !v.Known {
		return unknownText(reason)
	}
	return strconv.FormatFloat(v.Value, 'g', -1, 64)
}

// memoryText prints a free-memory value as a number of bytes, or as unknown with its reason (DR-6).
func memoryText(v LoadLowest) string {
	if !v.Low.Known {
		return unknownText(v.Reason)
	}
	return strconv.FormatInt(v.Low.Value, 10)
}

// coresText prints a core count as a number, or as unknown (DR-6).
func coresText(v Measured[int64]) string {
	if !v.Known {
		return "unknown"
	}
	return strconv.FormatInt(v.Value, 10)
}
