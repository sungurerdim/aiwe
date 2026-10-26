package core

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"time"
)

// ProcessID identifies a process across pid reuse: the pid and the OS start time. Where the OS
// keeps an exact start identity (exactStartID, Linux), id holds it and liveness compares it in
// place of Start. CurrentProcess sets id. A ProcessID built by hand has none, so on Linux its pid
// counts as alive while it runs, whatever Start says, and nothing of its owner is removed (DR-11).
type ProcessID struct {
	PID   int
	Start time.Time
	id    string
}

// StartID returns the exact start identity of p, or "" when p has none. A caller that stores p
// stores StartID() as it came from CurrentProcess and hands it back to ProcessFrom unchanged.
func (p ProcessID) StartID() string { return p.id }

// ProcessFrom rebuilds a ProcessID from its stored fields; an empty startID means none. The identity
// is kept as given on every OS. On an OS without exact identities, OwnerAlive counts a ProcessID
// that holds one alive (fail-closed), so a value stored elsewhere never removes a live owner.
func ProcessFrom(pid int, start time.Time, startID string) ProcessID {
	return ProcessID{PID: pid, Start: start, id: startID}
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

// OwnerAlive reports whether p still runs: the pid runs and its start matches. A p with an exact
// start identity is compared by startID; any other by the OS start time within startTolerance, which
// on Linux has no limit: a p built by hand counts as alive while its pid runs. When the start cannot
// be read, the owner counts as alive, so nothing is removed (fail-closed). A process that exited
// does not run, even while an open handle keeps the OS naming its pid: on Windows the exit code
// decides, not whether the pid opens. The engine's claim table judges the holder of a claim with it.
func OwnerAlive(p ProcessID) bool {
	if p.id != "" {
		id, running, err := startID(p.PID)
		return err != nil || running && id == p.id
	}
	start, running, err := processStart(p.PID)
	if err != nil {
		return true
	}
	if !running {
		return false
	}
	d := start.Sub(p.Start)
	if d < 0 {
		d = -d
	}
	return d <= startTolerance()
}

// startTolerance is how far the OS start time of a p without an exact start identity may lie from
// p.Start. On Windows and macOS the start is a stored value, read the same way at every call, so the
// tolerance absorbs rounding only. Elsewhere it is read from /proc and moves with the wall clock, so
// it is the widest Duration: a p built by hand whose pid runs always counts as a live owner and
// nothing is removed (fail-closed, DR-11). On Linux CurrentProcess sets the exact identity, so the
// tolerance applies only to a p built by hand.
func startTolerance() time.Duration {
	switch runtime.GOOS {
	case "windows", "darwin":
		return time.Second
	}
	return time.Duration(math.MaxInt64)
}
