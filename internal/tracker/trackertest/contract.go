// Package trackertest holds the tracker port contract suite and its fake; it is never shipped.
// The fake and every adapter run the same suite, so they pass one and the same contract
// (SPEC §5.13, §10.1).
package trackertest

import (
	"testing"

	"github.com/sungurerdim/aiwe/internal/tracker"
)

// Fixture is the repository state the contract suite runs every tracker implementation against.
type Fixture struct {
	Repo  tracker.RepoID
	Units []tracker.Unit
}

// Run checks one tracker implementation against the port contract: ReadUnits returns exactly the
// fixture's units, in order, with their numbers, titles and bodies.
func Run(t *testing.T, tr tracker.Tracker, fx Fixture) {
	t.Helper()
	if tr.ID() == "" {
		t.Fatal("tracker ID is empty")
	}
	got, err := tr.ReadUnits(t.Context(), fx.Repo)
	if err != nil {
		t.Fatalf("ReadUnits(%s): %v", fx.Repo.Owner+"/"+fx.Repo.Name, err)
	}
	if len(got) != len(fx.Units) {
		t.Fatalf("ReadUnits returned %d units, want %d", len(got), len(fx.Units))
	}
	for i, want := range fx.Units {
		if got[i] != want {
			t.Errorf("unit %d = %+v, want %+v", i, got[i], want)
		}
	}
}
