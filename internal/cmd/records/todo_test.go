package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

const wantTodo = `# TODO

15 open issues. Regenerate: ` + "`go run ./internal/cmd/records --write todo`" + `

## #438 G0 root goal

### #440 G1 work store

- #455 Item writer
- #456 Item reader
- #454 Item schema (blocked by #456)
- #457 Item migration (blocked by #454)
- #460 Reader cache

### #441 G2 unattended sessions (blocked by #440)

- #475 End record
- #477 Ready check (blocked by #456)

### #439 Spec part two (blocked by #441)

- #449 Spec rewrite

## Legacy (to triage, tasima/issue-eylem.md)

- #7 Old bug
- #12 Old idea
- #300 Old follow-up (blocked by #7)
`

// TestTodoOrder catches a TODO that lists work in number order instead of blocker order, loses
// the blocked marker, or reads only one of the two blockedBy sources.
func TestTodoOrder(t *testing.T) {
	t.Run("goals and units come after their blockers, ties by number, with the blocked marker", func(t *testing.T) {
		w := newWorld()
		res, err := generate(context.Background(), w.toolDeps(stamp), "todo")
		if err != nil {
			t.Fatalf("generate todo: %v", err)
		}
		got := body(t, res.recs, "TODO.md")
		wantFirst := "<!-- generated 2026-10-25T10:00:00Z by go run ./internal/cmd/records from " +
			"gh issue list --state open, sub-issues and dependencies; do not edit by hand -->"
		if first := strings.SplitN(got, "\n", 2)[0]; first != wantFirst {
			t.Errorf("first line = %q, want %q", first, wantFirst)
		}
		if text := withoutStamp(got); text != wantTodo {
			t.Errorf("TODO.md body differs\n got:\n%s\nwant:\n%s", text, wantTodo)
		}
	})

	t.Run("every open issue appears exactly once and no closed one does", func(t *testing.T) {
		w := newWorld()
		res, err := generate(context.Background(), w.toolDeps(stamp), "todo")
		if err != nil {
			t.Fatalf("generate todo: %v", err)
		}
		got := body(t, res.recs, "TODO.md")
		for _, is := range w.open {
			marker := "#" + strconv.Itoa(is.Number) + " "
			if n := strings.Count(got, marker); n != 1 {
				t.Errorf("issue %d appears %d times, want 1", is.Number, n)
			}
		}
		for _, is := range w.closed {
			if strings.Contains(got, "#"+strconv.Itoa(is.Number)+" ") {
				t.Errorf("closed issue %d is in TODO.md", is.Number)
			}
		}
	})

	t.Run("a failing dependencies endpoint falls back to links.blockedBy of the body", func(t *testing.T) {
		w := newWorld()
		w.depErr = map[int]bool{454: true}
		w.open[5].Body = withLinks(456) // #454 names its blocker only in its body now
		res, err := generate(context.Background(), w.toolDeps(stamp), "todo")
		if err != nil {
			t.Fatalf("generate todo: %v", err)
		}
		if got := withoutStamp(body(t, res.recs, "TODO.md")); got != wantTodo {
			t.Errorf("fallback changed the order or marker\n got:\n%s", got)
		}
		if len(res.warnings) != 0 {
			t.Errorf("warnings = %v, want none: the body held the blocker", res.warnings)
		}
	})

	t.Run("an issue whose blockedBy cannot be read is named in a warning, not hidden", func(t *testing.T) {
		w := newWorld()
		w.depErr = map[int]bool{300: true}
		w.open[14].Body = "no record block here"
		res, err := generate(context.Background(), w.toolDeps(stamp), "todo")
		if err != nil {
			t.Fatalf("generate todo: %v", err)
		}
		if len(res.warnings) != 1 || !strings.Contains(res.warnings[0], "#300") {
			t.Fatalf("warnings = %v, want one naming #300", res.warnings)
		}
		if strings.Contains(body(t, res.recs, "TODO.md"), "#300 Old follow-up (blocked") {
			t.Errorf("an unreadable blocker must not be invented")
		}
	})

	t.Run("a blocking cycle terminates and still lists every member once", func(t *testing.T) {
		w := newWorld()
		w.open[5].Body = withLinks(457) // 454 waits for 457 and 456, 457 waits for 454
		res, err := generate(context.Background(), w.toolDeps(stamp), "todo")
		if err != nil {
			t.Fatalf("generate todo: %v", err)
		}
		got := body(t, res.recs, "TODO.md")
		for _, n := range []string{"#454 ", "#455 ", "#456 ", "#457 "} {
			if c := strings.Count(got, n); c != 1 {
				t.Errorf("%s appears %d times in a cyclic goal, want 1", n, c)
			}
		}
	})
}
