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
	// guardPollEvery is how often the tree a child left behind is checked for being empty.
	guardPollEvery = 100 * time.Millisecond
)

// ChildSpec describes one child process: its argv, working directory, environment, input and
// output. A nil Stdout or Stderr sends that stream to the null device.
type ChildSpec struct {
	Argv   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Child is a started child process together with every process it starts: its tree. The tree is
// a process group on Unix and a Job Object on Windows. Wait returns when the child exits; the tree
// is held until it is empty, StopTree stops it or the context of Start ends (DR-15).
type Child struct {
	cmd *exec.Cmd
	// mu serialises the start, the stops and the release of the tree, so a stop never runs
	// between the start of the child and its joining the tree.
	mu   sync.Mutex
	tree tree
	// held reports that the tree may still run a process; it turns false, and ended is closed,
	// once the tree is stopped or seen empty, so its id is never used again.
	held  bool
	ended chan struct{}
	done  chan struct{}
	err   error
}

// Start starts spec as a child in its own process group or job. When ctx ends, the whole tree is
// stopped with a grace of cancelGrace, also after the child itself has exited.
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
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.WaitDelay = waitDelay
	c := &Child{cmd: cmd, ended: make(chan struct{}), done: make(chan struct{})}
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
	c.held = true
	attachErr := c.tree.attach(cmd.Process)
	c.mu.Unlock()

	go c.reap(ctx)
	if attachErr != nil {
		_ = cmd.Process.Kill() // the child never ran outside its tree; reap ends it
		<-c.done
		c.mu.Lock()
		c.endTree() // the child was the only process of the tree; this also ends reap
		c.mu.Unlock()
		return nil, fmt.Errorf("start %s: %w", spec.Argv[0], attachErr)
	}
	return c, nil
}

// reap waits for the child and publishes the result to Wait, then guards the tree the child left
// behind. Start owns it; it ends once the tree is let go, which a stop, the context or the last
// process of the tree ending forces.
func (c *Child) reap(ctx context.Context) {
	c.err = c.cmd.Wait()
	close(c.done)
	tick := time.NewTicker(guardPollEvery)
	defer tick.Stop()
	limit := ctx.Done()
	for {
		select {
		case <-c.ended:
			return
		case <-limit:
			limit = nil
			// A failed stop keeps the tree held: a tick still lets it go once it is empty, and
			// the owner's StopTree tries again and returns the error.
			_ = c.StopTree(cancelGrace)
		case <-tick.C:
			if c.endIfEmpty() {
				return
			}
		}
	}
}

// endIfEmpty lets the tree go once no process of it is left; it reports whether the tree is let
// go. A tree that cannot be probed stays held, so a stop can still end it.
func (c *Child) endIfEmpty() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.held {
		return true
	}
	if empty, err := c.tree.empty(); err != nil || !empty {
		return false
	}
	c.endTree()
	return true
}

// endTree releases a held tree and closes ended. The caller holds mu.
func (c *Child) endTree() {
	if !c.held {
		return
	}
	c.held = false
	c.tree.release()
	close(c.ended)
}

// StopTree asks the tree to end, waits grace, then kills it. It returns nil once the tree is
// gone, also when it was gone before.
func (c *Child) StopTree(grace time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.held {
		return nil
	}
	if err := c.tree.stop(grace); err != nil {
		return fmt.Errorf("stop tree of %s: %w", c.cmd.Path, err)
	}
	c.endTree()
	return nil
}

// Wait waits for the child to exit and returns its error; a child stopped by its context returns
// a non-nil error. A process the child left behind may still run; StopTree or the context ends it.
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
