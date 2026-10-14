package trackertest

import (
	"testing"

	"github.com/sungurerdim/aiwe/internal/tracker"
)

// fixture is the repository state every implementation of the tracker port is checked against. The
// fake and the GitHub adapter both pass it to Run, so they pass one and the same contract.
func fixture() Fixture {
	return Fixture{
		Repo: tracker.RepoID{Owner: "sungurerdim", Name: "engine"},
		Units: []tracker.Unit{
			{Number: 25, Title: "Intake reads a unit issue", Body: "## Problem\nread the issue\n"},
			{Number: 26, Title: "Write the tracker port", Body: "## Problem\nwrite the port\n"},
		},
	}
}

func TestFakePassesContract(t *testing.T) {
	fx := fixture()
	Run(t, NewFake(fx), fx)
}
