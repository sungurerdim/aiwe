package roles

import (
	"reflect"
	"strings"
	"testing"
)

// validCard is a complete card of the SPEC §9.1 format. Red cases start here and
// remove one required part, so a failure is the missing part and not a bad fixture.
func validCard() Card {
	return Card{
		ID:      "engineer",
		Purpose: "Implements the unit inside the file set.",
		Trigger: "a unit whose work is code in the declared file set",
		Needs:   "the unit text, its file set and the recorded red result",
		Output:  "the change on the run branch",
		Tier:    "base",
		May:     []string{"edit inside the declared file set"},
		MayNot:  []Limit{{Text: "leave the file set", Gate: "DR-9"}},
		Body: "Red before only from a recorded result. A deliberate behaviour change " +
			"goes under Scope deviation. Three reds with no new cause stop.",
	}
}

// TestCardSchemaAcceptsCompleteCard proves a card that carries every SPEC §9.1 field
// passes validation, including each starting tier from §8.1.
func TestCardSchemaAcceptsCompleteCard(t *testing.T) {
	t.Parallel()
	for _, tier := range []string{"base", "raised", "top"} {
		t.Run(tier, func(t *testing.T) {
			t.Parallel()
			c := validCard()
			c.Tier = tier
			if err := c.Validate(); err != nil {
				t.Fatalf("complete card at tier %q: %v", tier, err)
			}
		})
	}
}

// TestCardSchemaRefusesInvalid proves the red case of the card schema: each row is a
// card missing one required part, and validation refuses it (SPEC §9.1).
func TestCardSchemaRefusesInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Card)
	}{
		{name: "empty card", mutate: func(c *Card) { *c = Card{} }},
		{name: "missing id", mutate: func(c *Card) { c.ID = "" }},
		{name: "missing purpose", mutate: func(c *Card) { c.Purpose = "" }},
		{name: "missing trigger", mutate: func(c *Card) { c.Trigger = "" }},
		{name: "missing needs", mutate: func(c *Card) { c.Needs = "" }},
		{name: "missing output", mutate: func(c *Card) { c.Output = "" }},
		{name: "missing tier", mutate: func(c *Card) { c.Tier = "" }},
		{name: "unknown tier", mutate: func(c *Card) { c.Tier = "bottom" }},
		{name: "missing may", mutate: func(c *Card) { c.May = nil }},
		{name: "blank may entry", mutate: func(c *Card) { c.May = []string{"  "} }},
		{name: "missing limits", mutate: func(c *Card) { c.MayNot = nil }},
		{name: "limit without text", mutate: func(c *Card) { c.MayNot = []Limit{{Gate: "D1"}} }},
		{name: "limit without gate", mutate: func(c *Card) {
			c.MayNot = []Limit{{Text: "leave the file set"}}
		}},
		{name: "missing method", mutate: func(c *Card) { c.Body = "" }},
		{name: "blank method", mutate: func(c *Card) { c.Body = " \n\t" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := validCard()
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("accepted a card with %s", tc.name)
			}
		})
	}
}

// skeletonCard is one shipped role: the purpose, method and limits SPEC §9.1 states
// for it, and the gate id of each limit.
type skeletonCard struct {
	id      string
	tier    string
	purpose string
	method  []string
	limits  []string
	gates   []string
}

func skeletonCards() []skeletonCard {
	return []skeletonCard{
		{
			id: "engineer", tier: "base", purpose: "inside the file set",
			method: []string{"recorded result", "removes no check", "Scope deviation", "three reds"},
			limits: []string{"test-author", "file set", "weaken", "secrets"},
			gates:  []string{"D1", "DR-9", "D2", "D4", "D7", "D9", "D15", "D16", "D17"},
		},
		{
			id: "test-author", tier: "base", purpose: "before code",
			method: []string{"Done criteria", "production entry point", "missing behaviour", "fixture error"},
			limits: []string{"production code", "skipped", "focused", "empty test"},
			gates:  []string{"D1", "D10", "D11"},
		},
		{
			id: "auditor", tier: "raised", purpose: "did not write",
			method: []string{"PASS or FAIL", "evidence line", "silent scope change", "never fixes"},
			limits: []string{"writer", "VERDICT"},
			gates:  []string{"DR-5", "D14", "verdict_in_branch"},
		},
	}
}

// TestShippedCardsStateMethodAndLimits proves each skeleton card is valid and states
// that role's method and limits (SPEC §9.1, Done criteria).
func TestShippedCardsStateMethodAndLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range skeletonCards() {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			c, ok := Lookup(tc.id)
			if !ok {
				t.Fatalf("card %q is not shipped", tc.id)
			}
			if c.ID != tc.id {
				t.Fatalf("card ID = %q, want %q", c.ID, tc.id)
			}
			if err := c.Validate(); err != nil {
				t.Fatalf("card %q is not valid: %v", tc.id, err)
			}
			if c.Tier != tc.tier {
				t.Fatalf("card %q tier = %q, want %q", tc.id, c.Tier, tc.tier)
			}
			if !strings.Contains(c.Purpose, tc.purpose) {
				t.Fatalf("card %q purpose %q does not state %q", tc.id, c.Purpose, tc.purpose)
			}
			for _, phrase := range tc.method {
				if !strings.Contains(c.Body, phrase) {
					t.Fatalf("card %q method does not state %q: %s", tc.id, phrase, c.Body)
				}
			}
			limits := limitText(c)
			for _, phrase := range tc.limits {
				if !strings.Contains(limits, phrase) {
					t.Fatalf("card %q limits do not state %q: %s", tc.id, phrase, limits)
				}
			}
			for _, gate := range tc.gates {
				if !hasGate(c, gate) {
					t.Fatalf("card %q has no limit gated by %s", tc.id, gate)
				}
			}
		})
	}
}

// TestCardsReturnsShippedSet proves the embedded set is the three skeleton cards and
// that Lookup serves the same card (SPEC §9.1).
func TestCardsReturnsShippedSet(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"engineer": false, "test-author": false, "auditor": false}
	seen := map[string]Card{}
	for _, c := range Cards() {
		if _, dup := seen[c.ID]; dup {
			t.Fatalf("Cards() returns %q twice", c.ID)
		}
		seen[c.ID] = c
		if _, ok := want[c.ID]; !ok {
			t.Fatalf("Cards() ships %q, which is outside the skeleton set", c.ID)
		}
		want[c.ID] = true
		got, ok := Lookup(c.ID)
		if !ok || !reflect.DeepEqual(got, c) {
			t.Fatalf("Lookup(%q) does not serve the Cards() entry", c.ID)
		}
	}
	for id, ok := range want {
		if !ok {
			t.Fatalf("Cards() does not ship %q", id)
		}
	}
}

// TestLookupMissesUnshippedRole proves a role without a shipped card is reported
// missing, including the roles this unit keeps out of scope (SPEC §9.1).
func TestLookupMissesUnshippedRole(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, id string }{
		{name: "empty id", id: ""},
		{name: "architect", id: "architect"},
		{name: "security-reviewer", id: "security-reviewer"},
		{name: "doc-writer", id: "doc-writer"},
		{name: "unknown role", id: "no-such-role"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := Lookup(tc.id); ok {
				t.Fatalf("Lookup(%q) reported a role without a shipped card", tc.id)
			}
		})
	}
}

func limitText(c Card) string {
	var b strings.Builder
	for _, m := range c.MayNot {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func hasGate(c Card, gate string) bool {
	for _, m := range c.MayNot {
		if m.Gate == gate {
			return true
		}
	}
	return false
}
