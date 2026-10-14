package trackertest

import (
	"context"
	"slices"

	"github.com/sungurerdim/aiwe/internal/tracker"
)

// Fake is the in-memory tracker the contract suite checks; it returns the fixture's units as they
// are (SPEC §5.13, §10.1).
type Fake struct {
	fx Fixture
}

// NewFake returns the fake over fx.
func NewFake(fx Fixture) *Fake { return &Fake{fx: fx} }

// ID is the fake's stable identifier (SPEC §5.1).
func (f *Fake) ID() string { return "fake" }

// ReadUnits returns the fixture's units, in order.
func (f *Fake) ReadUnits(context.Context, tracker.RepoID) ([]tracker.Unit, error) {
	return slices.Clone(f.fx.Units), nil
}
