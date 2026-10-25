package roles

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// engineSteps are steps the engine owns. The last one is the boundary a shell
// list misses: a body can tell the role to mutate code so a test fails without
// naming a command (SPEC §9.1 card format).
func engineSteps() []string {
	return []string{
		"go test", "go build", "go run", "git ", "rm -rf",
		"shell", "$(", "&&", "comment out", "delete the code", "revert the",
		"break code", "break the code", "breaking code", "breaking the code",
		"to prove a test", "mutate the code so the test fails",
	}
}

// cardText joins every field a card shows the role, including purpose, trigger,
// needs, output and the limit gate. A step hidden in any of those still counts.
func cardText(c Card) string {
	parts := []string{c.Purpose, c.Trigger, c.Needs, c.Output, c.Body}
	parts = append(parts, c.May...)
	for _, m := range c.MayNot {
		parts = append(parts, m.Text, m.Gate)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// engineStep reports the first engine-owned step in the card, if any.
func engineStep(c Card) (string, bool) {
	text := cardText(c)
	for _, step := range engineSteps() {
		if strings.Contains(text, step) {
			return step, true
		}
	}
	return "", false
}

// specMethodBodies reads the SPEC-contracts §9.1 method sentences. The oracle is that
// paragraph, so a second copy cannot keep a drifted body green.
func specMethodBodies(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "SPEC-contracts.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected to read %s; got %v. Run the test from the roles package so the path reaches docs/SPEC-contracts.md.", path, err)
	}
	bodies, err := methodBodies(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return bodies
}

// methodBodies parses the "Method per role" paragraph into role id → sentence.
// SPEC writes an identifier in markdown code marks. The §9.1 card format forbids
// a quoted span in the body, so the sentence is stored without those marks.
func methodBodies(spec string) (map[string]string, error) {
	const marker = "Method per role (the card body):"
	spec = strings.ReplaceAll(spec, "\r\n", "\n")
	spec = strings.ReplaceAll(spec, "\r", "\n")
	i := strings.Index(spec, marker)
	if i < 0 {
		return nil, fmt.Errorf("expected SPEC §9.1 to contain %q; found no method paragraph. Restore that paragraph.", marker)
	}
	rest := spec[i+len(marker):]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	rest = strings.Join(strings.Fields(rest), " ")
	out := map[string]string{}
	for {
		start := strings.Index(rest, "**")
		if start < 0 {
			break
		}
		rest = rest[start+2:]
		end := strings.Index(rest, "**:")
		if end < 0 {
			return nil, fmt.Errorf("expected a **role**: marker in the SPEC §9.1 method paragraph; found %q. Keep each method as **id**: sentence.", cut(rest))
		}
		id := rest[:end]
		if !roleID(id) {
			return nil, fmt.Errorf("expected a role id of lowercase words; found %q. Stop the method paragraph before the next heading.", id)
		}
		rest = strings.TrimSpace(rest[end+3:])
		next := strings.Index(rest, "**")
		body := rest
		if next >= 0 {
			body = rest[:next]
			rest = rest[next:]
		} else {
			rest = ""
		}
		body = strings.TrimSpace(strings.ReplaceAll(body, "`", ""))
		if body == "" {
			return nil, fmt.Errorf("expected a method sentence for role %q; found none. Write the sentence after **%s**:.", id, id)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("expected one method sentence for role %q; found two. Keep a single **%s**: marker.", id, id)
		}
		out[id] = body
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("expected **role**: sentences after %q; found none.", marker)
	}
	return out, nil
}

func roleID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r != '-' && (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func cut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// TestCardBodiesMatchSpec91 proves each shipped card's method is the sentence
// SPEC §9.1 writes for that role. A missing sentence or a blank body fails.
func TestCardBodiesMatchSpec91(t *testing.T) {
	t.Parallel()
	want := specMethodBodies(t)
	cards := Cards()
	if len(cards) == 0 {
		t.Fatal("Cards() shipped no card, so a body comparison has nothing to fail")
	}
	for _, c := range cards {
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			body, ok := want[c.ID]
			if !ok {
				t.Fatalf("SPEC §9.1 has no method sentence for shipped card %q", c.ID)
			}
			if strings.TrimSpace(c.Body) == "" {
				t.Fatalf("card %q has a blank body", c.ID)
			}
			if c.Body != body {
				t.Fatalf("card %q body =\n%s\nwant SPEC §9.1\n%s", c.ID, c.Body, body)
			}
			got, found := Lookup(c.ID)
			if !found || got.Body != c.Body {
				t.Fatalf("Lookup(%q) does not serve the shipped body", c.ID)
			}
		})
	}
}

// TestMethodBodiesRejectsAMissingParagraph proves other §9.1 text is not a card body.
func TestMethodBodiesRejectsAMissingParagraph(t *testing.T) {
	t.Parallel()
	if _, err := methodBodies("### 9.1 Role cards\nA role exists only with its own kind of work.\n"); err == nil {
		t.Fatal("expected an error when the method paragraph is absent; got nil. The parser must not treat other §9.1 text as card bodies.")
	}
}

// TestMethodBodiesUnwrapsWrappedSentences proves a wrapped paragraph yields one
// sentence per role and stops before the next section.
func TestMethodBodiesUnwrapsWrappedSentences(t *testing.T) {
	t.Parallel()
	const spec = "Method per role (the card body): **test-author**: tests check the Done\n" +
		"criteria. **auditor**: the writer's return is a claim; it never\n" +
		"fixes.\n\n**Card format**: id, purpose\n"
	got, err := methodBodies(spec)
	if err != nil {
		t.Fatalf("expected the wrapped paragraph to parse; got %v", err)
	}
	if got["test-author"] != "tests check the Done criteria." {
		t.Fatalf("test-author = %q, want the unwrapped sentence", got["test-author"])
	}
	if got["auditor"] != "the writer's return is a claim; it never fixes." {
		t.Fatalf("auditor = %q, want the unwrapped sentence", got["auditor"])
	}
}

// TestMethodBodiesDropsMarkdownCodeMarks proves a SPEC code span is method prose.
// A card that keeps the marks fails validation: the body rule reads them as a command.
func TestMethodBodiesDropsMarkdownCodeMarks(t *testing.T) {
	t.Parallel()
	const spec = "Method per role (the card body): **security-reviewer**: unreproduced → `not_measured`.\n\n"
	got, err := methodBodies(spec)
	if err != nil {
		t.Fatalf("expected the marked paragraph to parse; got %v", err)
	}
	if got["security-reviewer"] != "unreproduced → not_measured." {
		t.Fatalf("security-reviewer = %q, want the sentence without code marks", got["security-reviewer"])
	}
}

// TestAuditorLimitKeepsDeferredTrigger proves the §15 rule stays on the auditor
// card as a limit. The SPEC §9.1 method sentence does not carry it.
func TestAuditorLimitKeepsDeferredTrigger(t *testing.T) {
	t.Parallel()
	c, ok := Lookup("auditor")
	if !ok {
		t.Fatal("expected Lookup(\"auditor\") to serve the shipped card; it was missing")
	}
	const want = "propose a §15 deferred item unless new evidence meets its trigger"
	for _, m := range c.MayNot {
		if m.Text == want {
			if strings.TrimSpace(m.Gate) == "" {
				t.Fatal("expected a gate on the §15 limit; found a blank gate. Name the section that holds the trigger.")
			}
			return
		}
	}
	t.Fatalf("expected auditor MayNot to contain %q; found none of those limits", want)
}

// TestCardsHoldNoEngineMechanicalStep proves no shipped card hands the role a
// step the engine owns. An empty set fails, and so does a card Validate refuses.
func TestCardsHoldNoEngineMechanicalStep(t *testing.T) {
	t.Parallel()
	cards := Cards()
	if len(cards) == 0 {
		t.Fatal("Cards() shipped no card, so a phrase scan would pass")
	}
	for _, c := range cards {
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			if err := c.Validate(); err != nil {
				t.Fatalf("card %q is not a complete card: %v", c.ID, err)
			}
			if step, ok := engineStep(c); ok {
				t.Fatalf("card %q hands the engine step %q to the role", c.ID, step)
			}
		})
	}
}

// TestEngineStepScanCoversEveryCardField proves an engine step hidden in purpose,
// trigger, needs, output, body, may, limit text or gate is refused. The body row
// is the boundary "mutate the code so the test fails", which names no command.
func TestEngineStepScanCoversEveryCardField(t *testing.T) {
	t.Parallel()
	const phrase = "mutate the code so the test fails"
	base := func() Card {
		return Card{
			ID:      "engineer",
			Purpose: "Implements the unit inside the file set.",
			Trigger: "a unit whose work is code in the declared file set",
			Needs:   "the unit text, its file set and the recorded red result",
			Output:  "the change on the run branch",
			Tier:    "base",
			May:     []string{"edit inside the declared file set"},
			MayNot:  []Limit{{Text: "leave the file set", Gate: "DR-9"}},
			Body:    "A recorded result stays in the ledger.",
		}
	}
	tests := []struct {
		name   string
		mutate func(*Card)
	}{
		{name: "purpose", mutate: func(c *Card) { c.Purpose = phrase }},
		{name: "trigger", mutate: func(c *Card) { c.Trigger = phrase }},
		{name: "needs", mutate: func(c *Card) { c.Needs = phrase }},
		{name: "output", mutate: func(c *Card) { c.Output = phrase }},
		{name: "body", mutate: func(c *Card) { c.Body = phrase }},
		{name: "may", mutate: func(c *Card) { c.May = []string{phrase} }},
		{name: "limit text", mutate: func(c *Card) { c.MayNot[0].Text = phrase }},
		{name: "gate", mutate: func(c *Card) { c.MayNot[0].Gate = phrase }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := base()
			tc.mutate(&c)
			step, ok := engineStep(c)
			if !ok {
				t.Fatalf("field %s holding an engine step was accepted", tc.name)
			}
			if step != phrase {
				t.Fatalf("field %s matched %q, want %q", tc.name, step, phrase)
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
