package gates

import "testing"

// cacheTestResult is a plausible stored result of the unit verify in keyTestInput.
func cacheTestResult(key string) CheckResult {
	return CheckResult{Key: key, StepID: "unit", Result: ResultGreen, ExitCode: 0, Tail: "ok"}
}

// TestCacheServesStoredResult catches a cache that runs a check again for a key it already holds, or
// serves a result for a key it never stored (DR-8, SPEC §7.8).
func TestCacheServesStoredResult(t *testing.T) {
	cache := NewCache()
	key := CheckKey(keyTestInput())
	cache.Store(cacheTestResult(key))
	got, ok := cache.Lookup(key)
	if !ok {
		t.Fatalf("catches a stored result that is not served for its own key")
	}
	if got.Key != key || got.StepID != "unit" || got.Result != ResultGreen || got.ExitCode != 0 || got.Tail != "ok" {
		t.Errorf("served result %+v, want the stored one", got)
	}
	other := keyTestInput()
	other.Argv = []string{"go", "test", "./internal/engine/..."}
	if _, ok := cache.Lookup(CheckKey(other)); ok {
		t.Errorf("catches a result served for a key that was never stored")
	}
}

// TestCacheNeverServesChangedTree catches a stored result served for a check whose tree content
// changed, so a changed tree is never reported with the result of the tree before it (DR-8, SPEC
// §7.8).
func TestCacheNeverServesChangedTree(t *testing.T) {
	cache := NewCache()
	before := keyTestInput()
	after := keyTestInput()
	after.InputsDigest = keyTestDigest("tree after the fix round")
	first, last := CheckKey(before), CheckKey(after)
	if first == last {
		t.Fatalf("catches a key that does not change with the tree content, so a changed tree reuses a stored result")
	}
	cache.Store(cacheTestResult(first))
	if _, ok := cache.Lookup(last); ok {
		t.Errorf("catches a stored result served for a changed tree: key %q served for key %q", first, last)
	}
}
