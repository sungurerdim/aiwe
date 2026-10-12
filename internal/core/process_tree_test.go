package core_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProcessTree(t *testing.T) {
	t.Run("stop ends a tree that obeys the ask", func(t *testing.T) {
		child, dir := startProcessTree(t, t.Context(), nil, false)
		stopProcessTree(t, child, 2*time.Second)
		requireProcessTreeGone(t, dir)
	})

	t.Run("stop kills a tree that ignores the ask after the grace", func(t *testing.T) {
		child, dir := startProcessTree(t, t.Context(), nil, true)
		stopProcessTree(t, child, 200*time.Millisecond)
		requireProcessTreeGone(t, dir)
	})

	t.Run("a time limit stops the whole tree", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		child, dir := startProcessTree(t, ctx, nil, true)
		if err := waitProcessChild(t, child, processOuterBound+processBound); err == nil {
			t.Error("Wait after the time limit = nil, want the error of a child stopped by its limit")
		}
		requireProcessTreeGone(t, dir)
	})

	t.Run("stop ends a grandchild left behind by a child that exited", func(t *testing.T) {
		dir := t.TempDir()
		child := startProcessChild(t, t.Context(), dir, "orphan")
		if err := waitProcessChild(t, child, processOuterBound); err != nil {
			t.Fatalf("Wait = %v, want nil: the child did not see its grandchild run", err)
		}
		stopProcessTree(t, child, 200*time.Millisecond)
		requireProcessBeatsStopped(t, filepath.Join(dir, "leaf.beat"))
	})

	t.Run("a time limit stops a grandchild left behind by a child that exited", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		dir := t.TempDir()
		child := startProcessChild(t, ctx, dir, "orphan")
		if err := waitProcessChild(t, child, processOuterBound); err != nil {
			t.Fatalf("Wait = %v, want nil: the child did not see its grandchild run before the limit", err)
		}
		<-ctx.Done()
		requireProcessBeatsStopped(t, filepath.Join(dir, "leaf.beat"))
	})

	t.Run("a grandchild started first thing cannot run outside the tree", func(t *testing.T) {
		beats := make([]string, 0, processRaceAttempts)
		for range processRaceAttempts {
			dir := t.TempDir()
			child := startProcessChild(t, t.Context(), dir, "tree")
			requireProcessFile(t, filepath.Join(dir, "leaf.pid"))
			stopProcessTree(t, child, 200*time.Millisecond)
			beats = append(beats, filepath.Join(dir, "leaf.beat"))
		}
		requireProcessBeatsStopped(t, beats...) // U22: a leaf that escaped the tree outlives its stop
	})

	if runtime.GOOS != "windows" {
		t.Run("stop ends a child that obeys the ask within the grace, without a kill", func(t *testing.T) {
			dir := t.TempDir()
			child := startProcessChild(t, t.Context(), dir, "term")
			requireProcessBeating(t, filepath.Join(dir, "child.beat"), time.Now().Add(processBound))
			grace := processBound
			start := time.Now()
			done := make(chan error, 1)
			go func() { done <- child.StopTree(grace) }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("StopTree(%v) = %v, want nil", grace, err)
				}
			case <-time.After(grace + processBound):
				t.Fatalf("StopTree(%v) did not return within %v", grace, grace+processBound)
			}
			if took := time.Since(start); took >= grace {
				t.Errorf("StopTree took %v, want less than the grace %v: the child that obeys SIGTERM was not asked", took, grace)
			}
			if err := waitProcessChild(t, child, processBound); err != nil {
				t.Errorf("Wait = %v, want nil: the child was killed instead of ending on SIGTERM", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "term")); err != nil {
				t.Errorf("the child recorded no SIGTERM: %v", err)
			}
		})
		return // the case below runs only on Windows
	}

	// DR-11: the job kills what it holds when its last handle closes, so a tree never outlives an
	// engine that exits, or crashes, without stopping it. The test starts the engine in a tree of
	// its own, so cleanup still ends a leaf that escaped.
	t.Run("a tree does not outlive the engine that started it", func(t *testing.T) {
		dir := t.TempDir()
		engine := startProcessChild(t, t.Context(), dir, "engine")
		if err := waitProcessChild(t, engine, processOuterBound); err != nil {
			t.Fatalf("engine Wait = %v, want nil: the engine did not see its tree run", err)
		}
		readProcessPids(t, dir, time.Now().Add(processBound))
		requireProcessBeatsStoppedWithin(t, 3*time.Second,
			filepath.Join(dir, "child.beat"), filepath.Join(dir, "leaf.beat"))
	})
}
