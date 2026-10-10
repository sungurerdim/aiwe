//go:build unix && !darwin

package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// onlyZombies reports whether every process of group pgid is a zombie. It reads /proc.
func onlyZombies(pgid int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, fmt.Errorf("list processes: %w", err)
	}
	for _, e := range entries {
		live, err := liveMember(e.Name(), pgid)
		if err != nil {
			return false, err
		}
		if live {
			return false, nil
		}
	}
	return true, nil
}

// liveMember reports whether the /proc entry name is a process of group pgid that is not a
// zombie. An entry that is not a process directory, or whose process ended after the listing,
// is not a live member.
func liveMember(name string, pgid int) (bool, error) {
	if _, err := strconv.Atoi(name); err != nil {
		return false, nil // not a process directory
	}
	stat, err := os.ReadFile(filepath.Join("/proc", name, "stat"))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil // the process ended after the listing
	}
	if err != nil {
		return false, fmt.Errorf("read status of process %s: %w", name, err)
	}
	state, group, err := procStateGroup(stat)
	if err != nil {
		return false, fmt.Errorf("read status of process %s: %w", name, err)
	}
	return group == pgid && state != 'Z' && state != 'X', nil
}

// procStateGroup returns the state, field 3, and the process group, field 5, of a
// /proc/<pid>/stat file.
func procStateGroup(stat []byte) (byte, int, error) {
	fields, err := procStatFields(stat) // state, ppid, pgrp, ...
	if err != nil {
		return 0, 0, err
	}
	if len(fields[0]) != 1 {
		return 0, 0, fmt.Errorf("unexpected format %q", stat)
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, fmt.Errorf("process group %q: %w", fields[2], err)
	}
	return fields[0][0], group, nil
}
