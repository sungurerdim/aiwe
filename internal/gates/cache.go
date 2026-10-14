package gates

// CheckResult is the stored result of one check key (DR-8, SPEC §7.8).
type CheckResult struct {
	Key      string
	StepID   string
	Result   Result
	ExitCode int
	Tail     string
}

// Cache stores one result per check key. A lookup of a key that was stored returns that result;
// any other key is a miss, including a key whose tree content changed (DR-8, SPEC §7.8).
type Cache struct {
	results map[string]CheckResult
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{results: make(map[string]CheckResult)}
}

// Store records r under r.Key, replacing a result already stored for that key.
func (c *Cache) Store(r CheckResult) {
	if c.results == nil {
		c.results = make(map[string]CheckResult)
	}
	c.results[r.Key] = r
}

// Lookup returns the result stored for key. A key that was never stored is a miss.
func (c *Cache) Lookup(key string) (CheckResult, bool) {
	if c == nil || c.results == nil {
		return CheckResult{}, false
	}
	r, ok := c.results[key]
	return r, ok
}
