package core

import (
	"fmt"
	"runtime"
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
