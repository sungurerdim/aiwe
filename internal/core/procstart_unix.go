//go:build unix && !darwin

package core

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// clockTicks is USER_HZ, the unit of the start time in /proc/<pid>/stat; Linux fixes it at 100 on
// every architecture Go supports.
const clockTicks = 100

// exactStartID reports whether this OS keeps an exact start identity (startID): Linux does.
const exactStartID = runtime.GOOS == "linux"

// startTolerance is the widest Duration: the Linux start time moves with the wall clock, so a
// ProcessID without an exact start identity, one built by hand, whose pid runs always counts as a
// live owner and nothing is removed (fail-closed, DR-25). CurrentProcess always sets the identity.
const startTolerance = time.Duration(math.MaxInt64)

// clockTick is one clock tick of /proc/<pid>/stat.
const clockTick = time.Second / clockTicks

// processStart returns the OS start time of pid and whether the process runs (U13). A pid that
// names no process, or a zombie, is not running; any other failure is an error, which the caller
// treats as a live owner (fail-closed). On Linux the time is the boot time of /proc/stat plus the
// start tick; it moves with every step of the wall clock, so liveness compares startID instead.
func processStart(pid int) (time.Time, bool, error) {
	if pid <= 0 {
		return time.Time{}, false, nil
	}
	if runtime.GOOS != "linux" {
		// No start time source on this OS yet; a pid that names no process is still dead.
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("start time of process %d: not readable on %s", pid, runtime.GOOS)
	}
	ticks, running, err := procStartTicks(pid)
	if err != nil || !running {
		return time.Time{}, false, err
	}
	boot, err := bootTime()
	if err != nil {
		return time.Time{}, false, fmt.Errorf("start time of process %d: %w", pid, err)
	}
	return boot.Add(time.Duration(ticks) * clockTick), true, nil
}

// bootTime reads the boot time from the btime line of /proc/stat.
func bootTime() (time.Time, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("boot time: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("boot time: %w", err)
			}
			return time.Unix(sec, 0), nil
		}
	}
	if err := sc.Err(); err != nil {
		return time.Time{}, fmt.Errorf("boot time: %w", err)
	}
	return time.Time{}, errors.New("boot time: no btime line in /proc/stat")
}

// startID returns the exact start identity of pid, the boot id and the start tick of
// /proc/<pid>/stat, and whether the process runs (U13). Neither moves with the wall clock, so it
// is compared exactly.
func startID(pid int) (string, bool, error) {
	if !exactStartID {
		return "", false, fmt.Errorf("start identity of process %d: not readable on %s", pid, runtime.GOOS)
	}
	ticks, running, err := procStartTicks(pid)
	if err != nil || !running {
		return "", false, err
	}
	boot, err := bootID()
	if err != nil {
		return "", false, fmt.Errorf("start identity of process %d: %w", pid, err)
	}
	return boot + "/" + strconv.FormatUint(ticks, 10), true, nil
}

// bootID reads the id of the running boot.
func bootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("boot id: %w", err)
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", errors.New("boot id: empty /proc/sys/kernel/random/boot_id")
	}
	return id, nil
}

// procStartTicks reads the start time of pid in clock ticks since boot, field 22 of
// /proc/<pid>/stat. A pid that names no process, or a zombie, is not running.
func procStartTicks(pid int) (uint64, bool, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("start time of process %d: %w", pid, err)
	}
	// The command name in parentheses may hold spaces and parentheses; the fields follow the last one.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0, false, fmt.Errorf("start time of process %d: malformed stat %q", pid, b)
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return 0, false, fmt.Errorf("start time of process %d: malformed stat %q", pid, b)
	}
	if f[0] == "Z" || f[0] == "X" {
		return 0, false, nil
	}
	ticks, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("start time of process %d: %w", pid, err)
	}
	return ticks, true, nil
}
