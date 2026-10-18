//go:build windows

package core

import (
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The kernel32 procedures the Windows load signals come from.
const (
	winKernel32          = "kernel32.dll"
	winGetSystemTimes    = "GetSystemTimes"
	winGlobalMemoryState = "GlobalMemoryStatusEx"
)

// The reasons Windows cannot read a load signal (DR-6).
const (
	winReasonCPUFirst = "cpu_times_need_two_readings"
	winReasonCPURead  = "cpu_times_unreadable"
	winReasonCPUPair  = "cpu_times_unusable"
	winReasonRunQueue = "run_queue_not_measured"
	winReasonMemory   = "memory_status_unreadable"
)

// winLoadSource samples the Windows load signals: the machine CPU times of
// kernel32!GetSystemTimes and the physical memory left of kernel32!GlobalMemoryStatusEx. Windows
// reports no run queue without a child process or an undocumented query, so that signal stays
// unknown. The sample starts no child process (SPEC §5.6, §6.6) and keeps no package-level state.
type winLoadSource struct {
	getSystemTimes   *windows.LazyProc
	globalMemoryStat *windows.LazyProc
	prev             winCPUTimes
	primed           bool
}

// newLoadSource returns the load sample source of this OS (SPEC §6.6). kernel32 is loaded from the
// Windows system directory only, so no library of another directory can stand in for it; a
// procedure that is not there is found empty on each read and leaves its signal unknown, never a
// panic.
func newLoadSource() loadSource {
	dll := windows.NewLazySystemDLL(winKernel32)
	s := &winLoadSource{
		getSystemTimes:   dll.NewProc(winGetSystemTimes),
		globalMemoryStat: dll.NewProc(winGlobalMemoryState),
	}
	return s.read
}

// read reads the Windows load signals once; the first reading only lays down the CPU-time baseline.
func (s *winLoadSource) read() loadReading {
	return loadReading{
		CPU:        s.busy(),
		RunQueue:   loadPoint{Reason: winReasonRunQueue},
		FreeMemory: s.freeMemory(),
	}
}

// busy returns the share of the CPU time the machine was not idle since the previous reading. The
// first reading, which has no interval yet, a pair of equal readings, which carries no interval at
// all, and a pair whose counters went backwards, which carries no interval this OS measured, are
// unknown with a reason, never 0 (DR-6).
func (s *winLoadSource) busy() loadPoint {
	now, err := s.cpuTimes()
	if err != nil {
		return loadPoint{Reason: winReasonCPURead}
	}
	prev, primed := s.prev, s.primed
	s.prev, s.primed = now, true
	if !primed {
		return loadPoint{Reason: winReasonCPUFirst}
	}
	busy, ok := winBusyPercent(prev, now)
	if !ok {
		return loadPoint{Reason: winReasonCPUPair}
	}
	return loadPoint{Value: measuredFloat(busy)}
}

// winCPUTimes is the machine CPU time in 100 ns units: the idle time and the kernel and user time,
// of which the kernel time includes the idle time.
type winCPUTimes struct{ idle, kernel, user uint64 }

// winFiletime is a FILETIME, split into its two 32-bit halves.
type winFiletime struct{ low, high uint32 }

// ticks returns the file time in 100 ns units.
func (f winFiletime) ticks() uint64 { return uint64(f.high)<<32 | uint64(f.low) }

// cpuTimes reads the idle, kernel and user CPU time of the machine through GetSystemTimes; the call
// starts no child process.
func (s *winLoadSource) cpuTimes() (winCPUTimes, error) {
	if err := s.getSystemTimes.Find(); err != nil {
		return winCPUTimes{}, err
	}
	var idle, kernel, user winFiletime
	ok, _, err := s.getSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return winCPUTimes{}, fmt.Errorf("GetSystemTimes: %w", err)
	}
	return winCPUTimes{idle: idle.ticks(), kernel: kernel.ticks(), user: user.ticks()}, nil
}

// winBusyPercent returns the share of the CPU time between two readings that was not idle, in
// percent 0..100. Two readings without a tick between them carry no interval; one of their counters
// that went backwards, an idle time above the kernel time that includes it, or an idle delta larger
// than the interval, leaves the interval unmeasured, so the subtraction can neither underflow nor
// report that interval as a busy share of 0 (DR-6). A fully idle interval, whose idle delta equals
// the total, is a measured 0.
func winBusyPercent(prev, now winCPUTimes) (float64, bool) {
	if now.idle < prev.idle || now.kernel < prev.kernel || now.user < prev.user ||
		now.idle > now.kernel || prev.idle > prev.kernel {
		return 0, false
	}
	total := (now.kernel - prev.kernel) + (now.user - prev.user)
	if total == 0 {
		return 0, false
	}
	idle := now.idle - prev.idle
	if idle > total {
		return 0, false
	}
	return float64(total-idle) / float64(total) * 100, true
}

// winMemoryStatus is MEMORYSTATUSEX, which GlobalMemoryStatusEx fills in: the length of the
// structure, the memory load percentage and then the physical, page-file and virtual memory in
// bytes.
type winMemoryStatus struct {
	length, memoryLoad uint32
	totalPhys          uint64
	availPhys          uint64
	totalPageFile      uint64
	availPageFile      uint64
	totalVirtual       uint64
	availVirtual       uint64
	availExtVirtual    uint64
}

// freeMemory returns the physical memory Windows can still hand out, in bytes, or the reason it is
// unknown (DR-6). A reported 0 is zero bytes left, a measurement, so the 1 GB stop can see it. A
// byte count Windows reports wider than an int64 can hold is unknown, so the conversion cannot wrap.
func (s *winLoadSource) freeMemory() LoadLowest {
	if err := s.globalMemoryStat.Find(); err != nil {
		return LoadLowest{Reason: winReasonMemory}
	}
	status := &winMemoryStatus{}
	status.length = uint32(unsafe.Sizeof(*status))
	ok, _, _ := s.globalMemoryStat.Call(uintptr(unsafe.Pointer(status)))
	if ok == 0 || status.availPhys > math.MaxInt64 {
		return LoadLowest{Reason: winReasonMemory}
	}
	return LoadLowest{Low: measuredInt(int64(status.availPhys))}
}
