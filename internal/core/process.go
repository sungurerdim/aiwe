package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const (
	// cancelGrace is how long a child stopped by its context gets to end before it is killed.
	cancelGrace = 2 * time.Second
	// waitDelay bounds Wait once the child is stopped or has exited, so a grandchild that holds a
	// pipe open cannot hang it (SPEC §5.20).
	waitDelay = 2 * time.Second
	// killBound bounds the wait for a killed tree to be gone.
	killBound = 3 * time.Second
	// treePollEvery is how often a stop checks whether the tree is gone.
	treePollEvery = 10 * time.Millisecond
)

// ChildSpec describes one child process: its argv, working directory, environment and input.
type ChildSpec struct {
	Argv  []string
	Dir   string
	Env   []string
	Stdin io.Reader
}

// Child is a started child process together with every process it starts: its tree. The tree is
// a process group on Unix and a Job Object on Windows.
type Child struct {
	cmd *exec.Cmd
	// mu serialises the start, the stops and the release of the tree, so a stop never runs
	// between the start of the child and its joining the tree.
	mu   sync.Mutex
	tree tree
	done chan struct{}
	err  error
}

// Start starts spec as a child in its own process group or job. When ctx ends, the whole tree is
// stopped with a grace of cancelGrace.
func Start(ctx context.Context, spec ChildSpec) (*Child, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("start child: empty argv")
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	if cmd.Err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Argv[0], cmd.Err)
	}
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdin = spec.Stdin
	cmd.WaitDelay = waitDelay
	c := &Child{cmd: cmd, done: make(chan struct{})}
	cmd.Cancel = func() error { return c.StopTree(cancelGrace) }

	t, err := newTree(cmd)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Argv[0], err)
	}
	c.mu.Lock()
	c.tree = t
	if err := cmd.Start(); err != nil {
		c.tree.release()
		c.mu.Unlock()
		return nil, fmt.Errorf("start %s: %w", spec.Argv[0], err)
	}
	attachErr := c.tree.attach(cmd.Process)
	c.mu.Unlock()

	go c.reap()
	if attachErr != nil {
		_ = cmd.Process.Kill() // the child never ran outside its tree; reap ends it
		<-c.done
		return nil, fmt.Errorf("start %s: %w", spec.Argv[0], attachErr)
	}
	return c, nil
}

// reap waits for the child, releases the tree and publishes the result to Wait. Start owns it;
// it ends when the child ends, which a stop or the context forces.
func (c *Child) reap() {
	err := c.cmd.Wait()
	c.mu.Lock()
	c.tree.release()
	c.mu.Unlock()
	c.err = err
	close(c.done)
}

// StopTree asks the tree to end, waits grace, then kills it. It returns nil once the tree is
// gone.
func (c *Child) StopTree(grace time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.tree.stop(grace); err != nil {
		return fmt.Errorf("stop tree of %s: %w", c.cmd.Path, err)
	}
	return nil
}

// Wait waits for the child to exit and returns its error; a child stopped by its context returns
// a non-nil error.
func (c *Child) Wait() error {
	<-c.done
	return c.err
}

// waitGone polls gone until it reports true or bound passes; it reports whether the tree is gone.
func waitGone(bound time.Duration, gone func() (bool, error)) (bool, error) {
	end := time.Now().Add(bound)
	for {
		ok, err := gone()
		if err != nil || ok {
			return ok, err
		}
		if time.Now().After(end) {
			return false, nil
		}
		time.Sleep(treePollEvery)
	}
}
