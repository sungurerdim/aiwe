package core

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process that has not exited.
const stillActive = 259

// startTolerance absorbs rounding only: the creation time is a stored FILETIME, read the same way
// at every call.
const startTolerance = time.Second

// processStart returns the OS start time of pid and whether the process runs (U13). A pid that
// names no process is not running; any other failure is an error, which the caller treats as a
// live owner (fail-closed).
func processStart(pid int) (time.Time, bool, error) {
	if pid <= 0 || pid > int(^uint32(0)) {
		return time.Time{}, false, nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return time.Time{}, false, fmt.Errorf("exit code of process %d: %w", pid, err)
	}
	if code != stillActive {
		return time.Time{}, false, nil
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, false, fmt.Errorf("times of process %d: %w", pid, err)
	}
	return time.Unix(0, created.Nanoseconds()), true, nil
}

// realPath returns the final path of an existing p: symlinks and junctions resolved, letter case
// and short names normalised, as the file system reports it for an open handle.
func realPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", fmt.Errorf("real path of %q: %w", p, err)
	}
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", fmt.Errorf("real path of %q: %w", p, err)
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	for {
		// Flags 0: FILE_NAME_NORMALIZED with VOLUME_NAME_DOS, a drive-letter or UNC path.
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", fmt.Errorf("real path of %q: %w", p, err)
		}
		if int(n) < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]uint16, n+1)
	}
	s := windows.UTF16ToString(buf)
	if rest, ok := strings.CutPrefix(s, `\\?\UNC\`); ok {
		return `\\` + rest, nil
	}
	return strings.TrimPrefix(s, `\\?\`), nil
}

// exactStartID reports whether this OS keeps an exact start identity: the stored start time
// serves instead.
const exactStartID = false

// startID fails: this OS keeps no exact start identity. A record that carries one, written on
// another OS, therefore counts as a live owner (fail-closed).
func startID(pid int) (string, bool, error) {
	return "", false, fmt.Errorf("start identity of process %d: not kept on this OS", pid)
}
