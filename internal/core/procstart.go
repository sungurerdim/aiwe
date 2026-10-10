package core

import (
	"fmt"
	"os"
	"time"
)

// ProcessID identifies a process across pid reuse: the pid and the OS start time (U13). Where the OS
// keeps an exact start identity (exactStartID, Linux), id holds it and liveness compares it in
// place of Start. CurrentProcess sets id. A ProcessID built by hand has none, so on Linux its pid
// counts as alive while it runs, whatever Start says, and nothing of its owner is removed (DR-25).
type ProcessID struct {
	PID   int
	Start time.Time
	id    string
}

// CurrentProcess returns the identity of the running process.
func CurrentProcess() (ProcessID, error) {
	pid := os.Getpid()
	start, running, err := processStart(pid)
	if err != nil {
		return ProcessID{}, fmt.Errorf("current process: %w", err)
	}
	if !running {
		return ProcessID{}, fmt.Errorf("current process %d: not found among running processes", pid)
	}
	p := ProcessID{PID: pid, Start: start}
	if exactStartID {
		if p.id, _, err = startID(pid); err != nil {
			return ProcessID{}, fmt.Errorf("current process: %w", err)
		}
	}
	return p, nil
}
