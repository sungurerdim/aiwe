package roles

import (
	"reflect"
	"testing"

	"github.com/sungurerdim/aiwe/internal/adapters"
)

// TestPinnedListPerRole proves each skeleton role runs on a pinned (source, model,
// effort) list until automatic choice replaces it (SPEC §8.1 point 11): the list is
// non-empty, every entry fills all three fields, and a second read returns the same list.
func TestPinnedListPerRole(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"engineer", "test-author", "auditor", "security-reviewer"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			pins := Pins(id)
			if len(pins) == 0 {
				t.Fatalf("role %q has no pinned (source, model, effort) entry", id)
			}
			for i, p := range pins {
				if p.Source == "" || p.Model == "" || p.Effort == "" {
					t.Fatalf("role %q pin %d is not a (source, model, effort) triple: %+v", id, i, p)
				}
			}
			if again := Pins(id); !reflect.DeepEqual(pins, again) {
				t.Fatalf("role %q pinned list changed across reads: %+v then %+v", id, pins, again)
			}
		})
	}
}

// TestPinSourceIsTheCompiledModelAPI proves each role's pinned source is the id of the compiled
// model-API provider and its model the pinned default: roles reads both from adapters, their one
// owner, and names no vendor type of its own (SPEC §8.1, §5.1, §10.2, DR-17).
func TestPinSourceIsTheCompiledModelAPI(t *testing.T) {
	t.Parallel()
	want := ""
	for _, p := range adapters.All(nil) {
		if p.Capabilities().Kind == adapters.KindModelAPI {
			want = p.ID()
			break
		}
	}
	if want == "" {
		t.Fatal("the compiled adapter list holds no model-API provider")
	}
	for _, id := range []string{"engineer", "test-author", "auditor", "security-reviewer"} {
		pins := Pins(id)
		if len(pins) == 0 {
			t.Fatalf("role %q has no pinned entry", id)
		}
		if pins[0].Source != want {
			t.Fatalf("role %q pin source = %q, want the compiled harness id %q", id, pins[0].Source, want)
		}
		if pins[0].Model != adapters.DefaultModel {
			t.Fatalf("role %q pin model = %q, want %q", id, pins[0].Model, adapters.DefaultModel)
		}
	}
}

// TestPinnedListUnknownRole proves the red case: a role with no shipped card has no
// pinned entry (SPEC §8.1 point 11, §9.1).
func TestPinnedListUnknownRole(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, id string }{
		{name: "empty id", id: ""},
		{name: "architect", id: "architect"},
		{name: "doc-writer", id: "doc-writer"},
		{name: "unknown role", id: "no-such-role"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if pins := Pins(tc.id); len(pins) != 0 {
				t.Fatalf("role %q returned %d pinned entries, want none", tc.id, len(pins))
			}
		})
	}
}
