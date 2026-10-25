package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

const wantAuto = `<!-- auto:start -->
<!-- generated 2026-10-25T10:00:00Z by go run ./internal/cmd/records from git, gh issue list and gh run list; do not edit by hand -->
- HEAD: e1bb4291f0a3 docs: make the SPEC provider-agnostic
- Branch: main, ahead 2, behind 1 of the upstream
- Issues: 15 open, 4 closed
- CI (main, workflow ci): failure, 2026-10-24T08:02:31Z, sha 527ba9aa3105
<!-- auto:end -->
`

const handStatus = `# Status

Intro written by hand.

<!-- auto:start -->
STALE LINE
<!-- auto:end -->

## Broken

- parser fails on a nested table

## Live configuration

- cache dir = the default
`

// TestStatusKeepsHandText catches a regeneration that overwrites the hand-kept part of STATUS.md,
// or that seeds a new file without the two hand headings.
func TestStatusKeepsHandText(t *testing.T) {
	t.Run("text outside the markers survives byte for byte", func(t *testing.T) {
		w := newWorld()
		w.files["STATUS.md"] = handStatus
		res, err := generate(context.Background(), w.toolDeps(stamp), "status")
		if err != nil {
			t.Fatalf("generate status: %v", err)
		}
		got := body(t, res.recs, "STATUS.md")
		wantPrefix := "# Status\n\nIntro written by hand.\n\n"
		wantSuffix := "\n## Broken\n\n- parser fails on a nested table\n\n## Live configuration\n\n- cache dir = the default\n"
		if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, wantSuffix) {
			t.Errorf("hand text changed:\n%s", got)
		}
		if strings.Contains(got, "STALE LINE") {
			t.Errorf("the old auto section was kept")
		}
		if !strings.Contains(got, wantAuto) {
			t.Errorf("auto section missing or wrong:\n%s\nwant it to contain:\n%s", got, wantAuto)
		}
	})

	t.Run("a regeneration over its own output changes only the stamp", func(t *testing.T) {
		w := newWorld()
		w.files["STATUS.md"] = handStatus
		first, err := generate(context.Background(), w.toolDeps(stamp), "status")
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		w.files["STATUS.md"] = body(t, first.recs, "STATUS.md")
		second, err := generate(context.Background(), w.toolDeps(stamp.Add(time.Hour)), "status")
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		a, b := body(t, first.recs, "STATUS.md"), body(t, second.recs, "STATUS.md")
		if a == b {
			t.Errorf("the stamp did not move with the clock")
		}
		if withoutStamp(a) != withoutStamp(b) {
			t.Errorf("regeneration is not idempotent\nfirst:\n%s\nsecond:\n%s", a, b)
		}
	})

	t.Run("a new file is seeded with the two empty hand headings", func(t *testing.T) {
		w := newWorld()
		res, err := generate(context.Background(), w.toolDeps(stamp), "status")
		if err != nil {
			t.Fatalf("generate status: %v", err)
		}
		want := "# Status\n\n" + wantAuto + "\n## Broken\n\n## Live configuration\n"
		if got := body(t, res.recs, "STATUS.md"); got != want {
			t.Errorf("new STATUS.md =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("an existing file without both markers is refused, never overwritten", func(t *testing.T) {
		for name, text := range map[string]string{
			"no markers": "# Status\n\nhand text\n",
			"only start": "# Status\n<!-- auto:start -->\nx\n",
			"reversed":   "<!-- auto:end -->\nx\n<!-- auto:start -->\n",
		} {
			w := newWorld()
			w.files["STATUS.md"] = text
			if _, err := generate(context.Background(), w.toolDeps(stamp), "status"); err == nil ||
				!strings.Contains(err.Error(), "STATUS.md") {
				t.Errorf("%s: err = %v, want an error naming STATUS.md", name, err)
			}
		}
	})

	t.Run("unknown ahead and behind is said, never zero", func(t *testing.T) {
		w := newWorld()
		w.fail["git rev-list"] = errNoUpstream
		res, err := generate(context.Background(), w.toolDeps(stamp), "status")
		if err != nil {
			t.Fatalf("generate status: %v", err)
		}
		if got := body(t, res.recs, "STATUS.md"); !strings.Contains(got, "- Branch: main, ahead/behind unknown (no upstream)") {
			t.Errorf("STATUS.md = %s", got)
		}
	})
}
