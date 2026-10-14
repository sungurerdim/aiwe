package gates

import "sync"

// CheckResult is the stored result of one check key (DR-8, SPEC §7.8).
type CheckResult struct {
	Key      string
	StepID   string
	Result   Result
	ExitCode int
	Tail     string
}

// Cache stores one result per check key. A lookup of a key that was stored returns that result;
// any other key is a miss, including a key whose tree content changed (DR-8, SPEC §7.8). A cache
// is safe for concurrent callers: callers that share a key run the check once, and callers with
// different keys never wait for each other (DR-3).
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

// Store records r under r.Key, replacing a result already stored for that key.
func (c *Cache) Store(r CheckResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.results == nil {
		c.results = make(map[string]CheckResult)
	}
	c.results[r.Key] = r
}

// Lookup returns the result stored for key. A key that was never stored is a miss.
func (c *Cache) Lookup(key string) (CheckResult, bool) {
	if c == nil {
		return CheckResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.results[key]
	return r, ok
}

// Once returns the result stored for key, or runs build once and stores its result. The cache lock
// is held only around the map, never while build runs: two callers that share a key run build once
// and two different keys run at the same time (DR-3, DR-8). An error from build reaches every
// caller of that flight and stores nothing, so a later caller runs build again.
func (c *Cache) Once(key string, build func() (CheckResult, error)) (CheckResult, error) {
	if c == nil {
		return build()
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
