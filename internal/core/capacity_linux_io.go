//go:build linux

package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// The kernel files the Linux load signals live in (SPEC §5.6).
const (
	procStatPath    = "/proc/stat"
	procMeminfoPath = "/proc/meminfo"
)

// procReadBufSize and procReadMax bound one /proc read: /proc/stat grows by one line per core and
// stays far below the ceiling on a machine of a few thousand cores.
const (
	procReadBufSize = 16 << 10
	procReadMax     = 1 << 18
)

// The reasons Linux cannot read a load signal (DR-6). proc_stat_no_interval is a second reading
// that carries no CPU interval: the counters did not advance, or one of them went backwards.
const (
	linuxReasonStatFirst = "proc_stat_need_two_readings"
	linuxReasonStatNone  = "proc_stat_no_interval"
	linuxReasonStatRead  = "proc_stat_unreadable"
	linuxReasonRunQueue  = "run_queue_missing"
	linuxReasonMemory    = "free_memory_unreadable"
)

// linuxLoadSource samples the Linux load signals: the machine CPU times and the run queue of
// /proc/stat and the memory left of /proc/meminfo. It starts no child process (SPEC §6.6).
type linuxLoadSource struct {
	prev   linuxCPUTimes
	primed bool
}

// newLoadSource returns the load sample source of this OS (SPEC §6.6).
func newLoadSource() loadSource {
	return (&linuxLoadSource{}).read
}

// read reads the Linux load signals once.
func (s *linuxLoadSource) read() loadReading {
	cpu, queue := s.stat()
	return loadReading{CPU: cpu, RunQueue: queue, FreeMemory: linuxFreeMemory()}
}

// stat reads /proc/stat once and returns the CPU busy share since the previous reading and the run
// queue length; a signal the read cannot give is unknown with a reason, never 0 (DR-6).
func (s *linuxLoadSource) stat() (cpu, queue loadPoint) {
	data, err := procRead(procStatPath)
	if err != nil {
		return loadPoint{Reason: linuxReasonStatRead}, loadPoint{Reason: linuxReasonRunQueue}
	}
	times, running, err := parseLoadStat(data)
	if err != nil {
		return loadPoint{Reason: linuxReasonStatRead}, loadPoint{Reason: linuxReasonRunQueue}
	}
	queue = loadPoint{Value: measuredFloat(float64(running))}
	prev, primed := s.prev, s.primed
	s.prev, s.primed = times, true
	if !primed {
		return loadPoint{Reason: linuxReasonStatFirst}, queue
	}
	busy, ok := linuxBusyPercent(prev, times)
	if !ok {
		return loadPoint{Reason: linuxReasonStatNone}, queue
	}
	return loadPoint{Value: measuredFloat(busy)}, queue
}

// linuxCPUTimes is the machine CPU time in clock ticks: the total ticks and the ticks the machine
// was idle, waiting for IO included.
type linuxCPUTimes struct{ total, idle uint64 }

// parseLoadStat returns the CPU times and the run queue of the contents of /proc/stat: the totals of
// the cpu line and the procs_running line. A missing line is an error, never a 0.
func parseLoadStat(data []byte) (linuxCPUTimes, uint64, error) {
	var stat loadStat
	for _, line := range strings.Split(string(data), "\n") {
		if err := stat.addLine(line); err != nil {
			return linuxCPUTimes{}, 0, err
		}
	}
	if !stat.seen {
		return linuxCPUTimes{}, 0, errors.New("no cpu line in /proc/stat")
	}
	if !stat.seenQueue {
		return linuxCPUTimes{}, 0, errors.New("no procs_running line in /proc/stat")
	}
	return stat.times, stat.queue, nil
}

// loadStat holds the two load signals a /proc/stat file carries.
type loadStat struct {
	times     linuxCPUTimes
	queue     uint64
	seen      bool
	seenQueue bool
}

// addLine folds one line of /proc/stat into the signals it carries; a line it does not hold is
// ignored. The per-core cpu0, cpu1, ... lines are ignored: the cpu line is every core together.
func (s *loadStat) addLine(line string) error {
	name, rest, ok := strings.Cut(line, " ")
	if !ok {
		return nil
	}
	if name == "cpu" {
		times, err := parseCPUTicks(strings.Fields(rest))
		if err != nil {
			return err
		}
		s.times, s.seen = times, true
		return nil
	}
	if name != "procs_running" {
		return nil
	}
	value := strings.TrimSpace(rest)
	queue, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return fmt.Errorf("procs_running %q: %w", value, err)
	}
	s.queue, s.seenQueue = queue, true
	return nil
}

// parseCPUTicks returns the total and idle ticks of the fields that follow "cpu" in /proc/stat:
// every field is busy time except idle, field 4, and the time waiting for IO, field 5. The guest
// fields, 9 and 10, are left out because user and nice already count them.
func parseCPUTicks(fields []string) (linuxCPUTimes, error) {
	if len(fields) < 4 {
		return linuxCPUTimes{}, fmt.Errorf("cpu line: %d fields, want at least 4", len(fields))
	}
	var times linuxCPUTimes
	for i, f := range fields[:min(len(fields), 8)] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return linuxCPUTimes{}, fmt.Errorf("cpu field %d %q: %w", i+1, f, err)
		}
		times.total += v
		if i == 3 || i == 4 {
			times.idle += v
		}
	}
	return times, nil
}

// linuxBusyPercent returns the share of the CPU time between two readings that was not idle, in
// percent 0..100. Two readings without a new tick carry no interval. A counter that went
// backwards (iowait may decrease, so idle can fall while the total still rises), or an idle delta
// larger than the total (a non-idle tick counter went backwards), leaves the interval unmeasured,
// so the subtraction can neither underflow nor report that interval as a busy share of 0 (DR-6).
func linuxBusyPercent(prev, now linuxCPUTimes) (float64, bool) {
	if now.total <= prev.total || now.idle < prev.idle {
		return 0, false
	}
	total := now.total - prev.total
	idle := now.idle - prev.idle
	if idle > total {
		return 0, false
	}
	return float64(total-idle) / float64(total) * 100, true
}

// linuxFreeMemory returns the memory Linux can hand out, in bytes, from the MemAvailable line of
// /proc/meminfo, or from the MemFree line on a kernel before 3.14. An unreadable or negative value
// is unknown with a reason. A reported 0 is zero bytes left, a measurement, so the 1 GB stop can
// see it (DR-6).
func linuxFreeMemory() LoadLowest {
	data, err := procRead(procMeminfoPath)
	if err != nil {
		return LoadLowest{Reason: linuxReasonMemory}
	}
	free, err := parseMeminfo(data)
	if err != nil {
		return LoadLowest{Reason: linuxReasonMemory}
	}
	return LoadLowest{Low: measuredInt(free)}
}

// parseMeminfo returns the memory /proc/meminfo reports as available, in bytes: MemAvailable when
// the kernel has it, else MemFree. A file without either line is an error, never a 0.
func parseMeminfo(data []byte) (int64, error) {
	free := int64(-1)
	for _, line := range strings.Split(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name == "MemAvailable" {
			return meminfoKiB(rest)
		}
		if name == "MemFree" {
			kb, err := meminfoKiB(rest)
			if err != nil {
				return 0, err
			}
			free = kb
		}
	}
	if free < 0 {
		return 0, errors.New("no MemAvailable or MemFree line in /proc/meminfo")
	}
	return free, nil
}

// meminfoKiB returns the bytes of the value of a /proc/meminfo line, which counts kibibytes. A
// missing or negative value is an error. Zero kibibytes is zero bytes, a measurement, so the 1 GB
// stop can see it; the error path's 0 is not a measurement (DR-6).
func meminfoKiB(rest string) (int64, error) {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, errors.New("no value")
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a memory size: %w", fields[0], err)
	}
	if kb < 0 {
		return 0, fmt.Errorf("%q is a negative memory size", fields[0])
	}
	return kb * 1024, nil
}

// procRead reads one fixed /proc file. unix.Open and unix.Read keep os, whose path reads are
// reserved for the files SPEC §5.6 lists (rule root_read), out of this read: /proc is a kernel file
// system, not a repository or state root, and the path is a constant of this file, so no link can
// leave a root.
func procRead(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	data := make([]byte, 0, procReadBufSize)
	buf := make([]byte, procReadBufSize)
	for {
		n, err := unix.Read(fd, buf)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return data, nil
		}
		data = append(data, buf[:n]...)
		if len(data) > procReadMax {
			return nil, fmt.Errorf("a /proc file larger than %d bytes", procReadMax)
		}
	}
}
