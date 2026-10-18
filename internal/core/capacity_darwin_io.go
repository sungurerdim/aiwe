//go:build darwin

package core

import (
	"encoding/binary"
	"math"

	"golang.org/x/sys/unix"
)

// The sysctls the macOS load signal lives in (SPEC §5.6): the free, inactive and speculative page
// counts, whose sum is the memory a new process can still take.
const (
	darwinFreePageName        = "vm.page_free_count"
	darwinInactivePageName    = "vm.page_inactive_count"
	darwinSpeculativePageName = "vm.page_speculative_count"
)

// The reasons macOS cannot read a load signal (DR-6). macOS reports its CPU time and its run queue
// only through Mach calls, which golang.org/x/sys does not wrap, and its only load average decays
// over a minute, so every reading of a sample window would repeat one value instead of measuring the
// window: both signals stay unknown there, never a number.
const (
	darwinReasonCPU   = "cpu_not_measured"
	darwinReasonQueue = "run_queue_not_measured"
	darwinReasonPages = "free_page_count_unreadable"
)

// newLoadSource returns the load sample source of this OS (SPEC §6.6). It starts no child process.
func newLoadSource() loadSource {
	return darwinRead
}

// darwinRead reads the macOS load signals once: the memory left, and the two signals this OS offers
// no source for, each unknown with its reason (DR-6).
func darwinRead() loadReading {
	return loadReading{
		CPU:        loadPoint{Reason: darwinReasonCPU},
		RunQueue:   loadPoint{Reason: darwinReasonQueue},
		FreeMemory: darwinFreeMemory(),
	}
}

// darwinFreeMemory returns the physical memory macOS can still hand out, in bytes, or the reason it
// is unknown (DR-6). macOS names no single available-memory sysctl, and its free pages
// alone are far fewer than the memory a new process can still take, so the free, inactive and
// speculative page counts are read together, the magnitude Linux reports as MemAvailable: reading
// the free pages alone would drive the 1 GB and 2 GB thresholds of SPEC §6.6 into stop or wait on a
// machine with memory to spare. A page count macOS reports wider than an int64 can hold is unknown,
// so the conversion to int64 cannot wrap. A reported 0 is zero bytes left, a measurement, so the
// 1 GB stop can see it.
func darwinFreeMemory() LoadLowest {
	pages, ok := darwinAvailablePages()
	pageSize := int64(unix.Getpagesize())
	if !ok || pageSize <= 0 || pages > math.MaxInt64/pageSize {
		return LoadLowest{Reason: darwinReasonPages}
	}
	return LoadLowest{Low: measuredInt(pages * pageSize)}
}

// darwinAvailablePages returns the pages macOS can still give a new process: the free page count and
// the inactive and speculative page counts beside it, of which a count the kernel does not report
// adds nothing. A free page count nothing could read is no measurement. A count of 0 is zero pages,
// a measurement; a sum that no int64 holds is no measurement (DR-6).
func darwinAvailablePages() (int64, bool) {
	pages, ok := darwinPageCount(darwinFreePageName)
	if !ok {
		return 0, false
	}
	for _, name := range [...]string{darwinInactivePageName, darwinSpeculativePageName} {
		extra, ok := darwinPageCount(name)
		if !ok || extra > math.MaxInt64-pages {
			continue
		}
		pages += extra
	}
	return pages, true
}

// darwinPageCount returns the page count of one vm.page_*_count sysctl; an unreadable sysctl or one
// of another width than the kernel reports is no measurement.
func darwinPageCount(name string) (int64, bool) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0, false
	}
	return darwinPages(raw)
}

// darwinPages returns the page count of a raw vm.page_*_count reading, which the kernel reports four
// or eight bytes wide. A count of another width, or one no int64 holds, is no measurement. A count
// of 0 is zero pages, a measurement, so the 1 GB stop can see it (DR-6).
func darwinPages(raw []byte) (int64, bool) {
	var pages uint64
	switch len(raw) {
	case 4:
		pages = uint64(binary.LittleEndian.Uint32(raw))
	case 8:
		pages = binary.LittleEndian.Uint64(raw)
	default:
		return 0, false
	}
	if pages > math.MaxInt64 {
		return 0, false
	}
	return int64(pages), true
}
