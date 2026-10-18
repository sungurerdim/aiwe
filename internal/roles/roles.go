package roles

import (
	"github.com/sungurerdim/aiwe/internal/adapters"
	"github.com/sungurerdim/aiwe/internal/core"
)

// Pin is one pinned (source, model, effort) entry of a role (SPEC §8.1).
type Pin struct {
	Source string
	Model  string
	Effort string
}

// Pins returns the role's pinned list. A role with no shipped card returns no entry. Each call
// builds the list again. The skeleton uses one entry for every shipped role until automatic choice
// replaces the list (SPEC §8.1 point 11).
func Pins(id string) []Pin {
	if _, ok := Lookup(id); !ok {
		return nil
	}
	return []Pin{{
		Source: harnessID(),
		Model:  adapters.DefaultModel,
		Effort: "high",
	}}
}

// harnessID returns the id of the harness the skeleton is pinned to: the one model-API provider of
// the compiled adapter list, read from its one owner, so roles names no vendor type and the id lives
// in one place (SPEC §5.1, §10.2, DR-17). A list without that provider is a programmer error, never
// an empty source.
func harnessID() string {
	for _, p := range adapters.All(nil) {
		if p.Capabilities().Kind == adapters.KindModelAPI {
			return p.ID()
		}
	}
	core.Assert(false, "the compiled adapter list holds no model-API harness")
	return ""
}
