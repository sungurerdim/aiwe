package gates

import (
	"sync"

	"github.com/sungurerdim/aiwe/internal/core"
)

// CheckResult is the stored result of one check key (DR-8, SPEC §7.8).
type CheckResult struct {
	Result   Result
	ExitCode int
	Tail     string
}

// Cache stores one result per check key and serves it through Once; Uncached runs a check whose
// key does not name what it runs (DR-8, SPEC §7.8). A nil cache is refused and runs nothing: it
// cannot keep the once-per-key rule. Callers that share a
// key run the check once, and callers with different keys never wait for each other (DR-3).
type Cache struct {
	mu      sync.Mutex
	results map[string]CheckResult
	flights map[string]*flight
}

// flight is the one check a key has in flight. Its owner runs the check; a caller that finds the
// flight running waits for done and takes the same result instead of running the check again.
type flight struct {
	done   chan struct{}
	result CheckResult
	err    error
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{results: make(map[string]CheckResult)}
}

// Once returns the result stored for key, or runs build once and stores its result. A nil cache
// is refused and build does not run: the once-per-key rule has no other owner (DR-8). The cache
// lock is held only around the map, never while build runs: two callers that share a key run
// build once and two different keys run at the same time (DR-3, DR-8). An error from build
// reaches every caller of that flight and stores nothing, so a later caller runs build again.
func (c *Cache) Once(key string, build func() (CheckResult, error)) (CheckResult, error) {
	if c == nil {
		return CheckResult{}, nilCacheError()
	}
	c.mu.Lock()
	if stored, ok := c.results[key]; ok {
		c.mu.Unlock()
		return stored, nil
	}
	if inFlight := c.flights[key]; inFlight != nil {
		c.mu.Unlock()
		<-inFlight.done
		return inFlight.result, inFlight.err
	}
	f := &flight{done: make(chan struct{})}
	if c.flights == nil {
		c.flights = make(map[string]*flight)
	}
	c.flights[key] = f
	c.mu.Unlock()

	r, err := build()
	c.mu.Lock()
	if err == nil {
		if c.results == nil {
			c.results = make(map[string]CheckResult)
		}
		c.results[key] = r
	}
	if c.flights[key] == f {
		delete(c.flights, key)
	}
	c.mu.Unlock()
	f.result, f.err = r, err
	close(f.done)
	return r, err
}

// Uncached runs build and returns its result without serving or storing one: the check has no key
// that names what it runs, such as a work tree that differs from HEAD under the step's declared
// inputs (SPEC §7.8). A nil cache is refused here too and build does not run, so the nil rule keeps
// one owner (DR-8).
func (c *Cache) Uncached(build func() (CheckResult, error)) (CheckResult, error) {
	if c == nil {
		return CheckResult{}, nilCacheError()
	}
	return build()
}

// nilCacheError is the refusal of a nil cache.
func nilCacheError() error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "a cache that runs one key once", Found: "a nil cache",
		Next: "pass a cache; a nil cache cannot keep the once-per-key rule"}
}
