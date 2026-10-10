//go:build unix

package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// tree is the process group the child leads; every process it starts joins the group.
type tree struct {
	pgid int  // 0 until the child has started
	gone bool // the group was seen empty; its id may since name another group
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
	if t.pgid == 0 || t.gone {
		return nil
	}
	for _, step := range []struct {
		sig   syscall.Signal
		bound time.Duration
	}{{syscall.SIGTERM, grace}, {syscall.SIGKILL, killBound}} {
		if err := syscall.Kill(-t.pgid, step.sig); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				t.gone = true
				return nil
			}
			return fmt.Errorf("signal %v to process group %d: %w", step.sig, t.pgid, err)
		}
		gone, err := waitGone(step.bound, t.empty)
		if err != nil {
			return err
		}
		if gone {
			t.gone = true
			return nil
		}
	}
	return fmt.Errorf("process group %d still runs %v after SIGKILL", t.pgid, killBound)
}

// empty reports whether no process of the group is left.
func (t *tree) empty() (bool, error) {
	err := syscall.Kill(-t.pgid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return false, nil
	case errors.Is(err, syscall.ESRCH):
		return true, nil
	default:
		return false, fmt.Errorf("probe process group %d: %w", t.pgid, err)
	}
}

// release has nothing to free: a process group ends with its last member.
func (t *tree) release() {}
