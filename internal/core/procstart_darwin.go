package core

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// procZombie is SZOMB, the p_stat of a process that has exited and waits for its parent.
const procZombie = 5

// processStart returns the OS start time of pid and whether the process runs. A pid that
// names no process, or a zombie, is not running; any other failure is an error, which the caller
// treats as a live owner (fail-closed).
func processStart(pid int) (time.Time, bool, error) {
	if pid <= 0 {
		return time.Time{}, false, nil
	}
	if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
		return time.Time{}, false, nil
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// A pid that exits between the two calls returns no record, which SysctlKinfoProc reports
		// as EIO; only a confirmed ESRCH counts as dead.
		if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("start time of process %d: %w", pid, err)
	}
	if int(k.Proc.P_pid) != pid {
		return time.Time{}, false, fmt.Errorf("start time of process %d: sysctl returned pid %d", pid, k.Proc.P_pid)
	}
	if k.Proc.P_stat == procZombie {
		return time.Time{}, false, nil
	}
	sec, nsec := k.Proc.P_starttime.Unix()
	if sec <= 0 {
		return time.Time{}, false, fmt.Errorf("start time of process %d: no start time in the process record", pid)
	}
	return time.Unix(sec, nsec), true, nil
}

// exactStartID reports whether this OS keeps an exact start identity: the stored start time
// serves instead.
const exactStartID = false

// errNoStartID is the error of startID on an OS that keeps no exact start identity.
var errNoStartID = errors.New("start identity of a process: not kept on this OS")

// startID fails: this OS keeps no exact start identity. A record that carries one, written on
// another OS, therefore counts as a live owner (fail-closed).
func startID(int) (string, bool, error) {
	return "", false, errNoStartID
}
