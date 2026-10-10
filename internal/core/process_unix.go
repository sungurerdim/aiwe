//go:build unix

package core

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tree is the process group the child leads; every process it starts joins the group.
type tree struct {
	pgid int // 0 until the child has started
}

func newTree(cmd *exec.Cmd) (tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return tree{}, nil
}

// attach records the group: Setpgid made the child its leader before it ran, so no grandchild
// can start outside it.
func (t *tree) attach(p *os.Process) error {
	t.pgid = p.Pid
	return nil
}

// stop sends SIGTERM to the group, waits grace for it to be gone, then sends SIGKILL.
func (t *tree) stop(grace time.Duration) error {
	for _, step := range []struct {
		sig   syscall.Signal
		bound time.Duration
	}{{syscall.SIGTERM, grace}, {syscall.SIGKILL, killBound}} {
		if err := syscall.Kill(-t.pgid, step.sig); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return fmt.Errorf("signal %v to process group %d: %w", step.sig, t.pgid, err)
		}
		gone, err := waitGone(step.bound, t.empty)
		if err != nil {
			return err
		}
		if gone {
			return nil
		}
	}
	return fmt.Errorf("process group %d still runs %v after SIGKILL", t.pgid, killBound)
}

// empty reports whether no live process of the group is left. A zombie runs nothing and holds no
// resource a stop can free, so it does not count as live: in a container whose PID 1 does not reap,
// an orphan killed by a stop stays a zombie until the container ends.
func (t *tree) empty() (bool, error) {
	err := syscall.Kill(-t.pgid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return onlyZombies(t.pgid)
	case errors.Is(err, syscall.ESRCH):
		return true, nil
	default:
		return false, fmt.Errorf("probe process group %d: %w", t.pgid, err)
	}
}

// onlyZombies reports whether every process of group pgid is a zombie. It reads /proc, which only
// Linux has in this format; elsewhere a process of the group counts as live.
func onlyZombies(pgid int) (bool, error) {
	if runtime.GOOS != "linux" {
		return false, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, fmt.Errorf("list processes: %w", err)
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue // not a process directory
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue // the process ended after the listing
		}
		if err != nil {
			return false, fmt.Errorf("read status of process %s: %w", e.Name(), err)
		}
		state, group, err := parseProcStat(stat)
		if err != nil {
			return false, fmt.Errorf("read status of process %s: %w", e.Name(), err)
		}
		if group == pgid && state != 'Z' && state != 'X' {
			return false, nil
		}
	}
	return true, nil
}

// parseProcStat returns the state and the process group from /proc/<pid>/stat. The command name
// in parentheses may hold spaces and parentheses, so the fields are read after its last ")".
func parseProcStat(stat []byte) (byte, int, error) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, 0, errors.New("no command name")
	}
	fields := strings.Fields(string(stat[end+1:])) // state, ppid, pgrp, ...
	if len(fields) < 3 || len(fields[0]) != 1 {
		return 0, 0, fmt.Errorf("unexpected format %q", stat)
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, fmt.Errorf("process group %q: %w", fields[2], err)
	}
	return fields[0][0], group, nil
}

// release has nothing to free: a process group ends with its last member. A released group is
// never signalled again, because its id may since name another group.
func (t *tree) release() {}
