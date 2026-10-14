package adapters

import (
	"context"
	"slices"
	"testing"
)

// TestAllHoldsTheCompiledModelAPI catches a compiled adapter missing from the one list: the list is
// never empty, every id is stable and unique, and one entry offers the model the skeleton runs
// (SPEC §5.1, §10.2).
func TestAllHoldsTheCompiledModelAPI(t *testing.T) {
	log, _ := testLog(t)
	all := All(log)
	if len(all) == 0 {
		t.Fatal("All() is empty, so no role can run (SPEC §5.1)")
	}
	seen := map[string]bool{}
	offersDefault := false
	for _, p := range all {
		if p.ID() == "" {
			t.Errorf("All() holds a %T with an empty id (SPEC §5.1)", p)
		}
		if seen[p.ID()] {
			t.Errorf("All() holds %q twice; the list is the one copy of the compiled adapters (SPEC §5.1)", p.ID())
		}
		seen[p.ID()] = true
		if slices.Contains(p.Capabilities().Models, DefaultModel) {
			offersDefault = true
		}
	}
	if !offersDefault {
		t.Errorf("All() holds no provider that offers %s, the model the skeleton runs (SPEC §10.2)", DefaultModel)
	}
}

// TestAvailableFiltersByCredential catches a provider offered without its credential or hidden while
// it has one: Available filters the compiled list through Detect on the run's environment (SPEC §5.1).
func TestAvailableFiltersByCredential(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	if got := Available(context.Background(), All(log), ProcessEnv()); len(got) == 0 {
		t.Error("Available with the key set returned no provider, so no role can run (SPEC §5.1)")
	}
	t.Setenv(envKeyVar, "")
	if got := Available(context.Background(), All(log), ProcessEnv()); len(got) != 0 {
		t.Errorf("Available without the key returned %d providers, want none (SPEC §5.1)", len(got))
	}
}

// TestAvailableKeepsOnlyPresentProviders catches a filter that keeps an absent provider: of two fakes
// that differ only in the variable they read, only the present one survives (SPEC §5.1).
func TestAvailableKeepsOnlyPresentProviders(t *testing.T) {
	models := []string{DefaultModel}
	present := fakeProvider{id: "present", keyEnv: envKeyVar, models: models}
	absent := fakeProvider{id: "absent", keyEnv: envKeyVar + "_OTHER", models: models}
	env := fixtureEnv{envKeyVar + "=" + envSecret}
	got := Available(context.Background(), []Provider{present, absent}, env)
	if len(got) != 1 || got[0].ID() != present.id {
		t.Errorf("Available kept %v, want the provider whose credential is present (SPEC §5.1)", providerIDs(got))
	}
}

// providerIDs returns the ids of ps, for a failure message.
func providerIDs(ps []Provider) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID())
	}
	return out
}
