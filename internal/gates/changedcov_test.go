package gates

import (
	"reflect"
	"slices"
	"testing"
)

// changedcovTestRanges renders ranges as the file:startLine-endLine names a report carries.
func changedcovTestRanges(ranges []FileRange) []string {
	names := make([]string, 0, len(ranges))
	for _, r := range ranges {
		names = append(names, r.String())
	}
	return names
}

// TestChangedLinesUncovered catches an intersection that reports a covered line, misses a changed
// line in a count 0 block, splits one run of lines in two, or names a range without its file and
// both ends.
func TestChangedLinesUncovered(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hunks       DiffHunks
		profile     CoverProfile
		uncovered   []string
		covered     int
		notMeasured []string
	}{
		{"changed lines in a count 0 block are named by file and lines",
			DiffHunks{"a.go": {{Start: 11, End: 14}}},
			CoverProfile{"a.go": {{Start: 10, End: 12, Count: 1}, {Start: 13, End: 15, Count: 0}}},
			[]string{"a.go:13-14"}, 2, nil},
		{"changed lines that every test executed",
			DiffHunks{"a.go": {{Start: 11, End: 12}}},
			CoverProfile{"a.go": {{Start: 10, End: 12, Count: 3}}},
			nil, 2, nil},
		{"a count 0 block next to a count 0 block is one range",
			DiffHunks{"a.go": {{Start: 5, End: 8}}},
			CoverProfile{"a.go": {{Start: 5, End: 6, Count: 0}, {Start: 7, End: 8, Count: 0}}},
			[]string{"a.go:5-8"}, 0, nil},
		{"a block that one test binary missed and another executed is covered",
			DiffHunks{"a.go": {{Start: 4, End: 6}}},
			CoverProfile{"a.go": {{Start: 4, End: 6, Count: 0}, {Start: 4, End: 6, Count: 2}}},
			nil, 3, nil},
		{"only the changed lines of a wide unexecuted block are named",
			DiffHunks{"a.go": {{Start: 20, End: 20}}},
			CoverProfile{"a.go": {{Start: 1, End: 100, Count: 0}}},
			[]string{"a.go:20-20"}, 0, nil},
		{"files and ranges come out sorted",
			DiffHunks{"b.go": {{Start: 3, End: 3}}, "a.go": {{Start: 9, End: 9}, {Start: 2, End: 2}}},
			CoverProfile{"a.go": {{Start: 1, End: 10, Count: 0}}, "b.go": {{Start: 3, End: 3, Count: 0}}},
			[]string{"a.go:2-2", "a.go:9-9", "b.go:3-3"}, 0, nil},
		{"a diff with no hunk", DiffHunks{}, CoverProfile{"a.go": {{Start: 1, End: 2, Count: 0}}}, nil, 0, nil},
		{"a range that spans the whole line space is not walked line by line",
			DiffHunks{"a.go": {{Start: 1, End: 1 << 30}}},
			CoverProfile{"a.go": {{Start: 5, End: 6, Count: 0}}},
			[]string{"a.go:5-6"}, 0, []string{"a.go:1-4", "a.go:7-1073741824"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ChangedLinesUncovered(tc.hunks, tc.profile)
			if names := changedcovTestRanges(got.Uncovered); !slices.Equal(names, tc.uncovered) {
				t.Errorf("uncovered = %v, want %v", names, tc.uncovered)
			}
			if got.Covered != tc.covered {
				t.Errorf("covered lines = %d, want %d", got.Covered, tc.covered)
			}
			if names := changedcovTestRanges(got.NotMeasured); !slices.Equal(names, tc.notMeasured) {
				t.Errorf("not measured = %v, want %v", names, tc.notMeasured)
			}
		})
	}
}

// TestLineOutsideProfileIsNotMeasured catches an intersection that reports a changed line in no
// block as covered or as uncovered (DR-6): it is not_measured and counts as neither.
func TestLineOutsideProfileIsNotMeasured(t *testing.T) {
	hunks := DiffHunks{"a.go": {{Start: 1, End: 6}}, "new.go": {{Start: 1, End: 2}}}
	profile := CoverProfile{"a.go": {{Start: 3, End: 4, Count: 0}, {Start: 5, End: 5, Count: 1}}}

	got := ChangedLinesUncovered(hunks, profile)
	if want := []string{"a.go:3-4"}; !reflect.DeepEqual(changedcovTestRanges(got.Uncovered), want) {
		t.Errorf("uncovered = %v, want %v", changedcovTestRanges(got.Uncovered), want)
	}
	if got.Covered != 1 {
		t.Errorf("covered lines = %d, want 1 (line 5 only)", got.Covered)
	}
	want := []string{"a.go:1-2", "a.go:6-6", "new.go:1-2"}
	if names := changedcovTestRanges(got.NotMeasured); !reflect.DeepEqual(names, want) {
		t.Errorf("not measured = %v, want %v (a line outside every block, and a file the profile lacks)", names, want)
	}
	if line := got.NotMeasured[0].String(); line != "a.go:1-2" {
		t.Errorf("range name = %q, want file:startLine-endLine", line)
	}
}

// FuzzChangedLinesUncovered catches an intersection that panics, loops over the line space, or lets
// the three classes overlap: every changed line is covered, uncovered or not measured, once (STD-19).
func FuzzChangedLinesUncovered(f *testing.F) {
	f.Add(1, 10, 3, 5, 0)
	f.Add(1, 1<<30, 5, 6, 1)
	f.Add(7, 7, 7, 7, 0)
	f.Fuzz(func(t *testing.T, hs, he, bs, be, count int) {
		if hs < 1 || he < hs || bs < 1 || be < bs || count < 0 || he > 1<<30 || be > 1<<30 {
			return
		}
		got := ChangedLinesUncovered(DiffHunks{"a.go": {{Start: hs, End: he}}},
			CoverProfile{"a.go": {{Start: bs, End: be, Count: count}}})
		total := got.Covered
		for _, r := range slices.Concat(got.Uncovered, got.NotMeasured) {
			if r.File != "a.go" || r.Start < hs || r.End > he || r.End < r.Start {
				t.Fatalf("range %v lies outside the hunk %d-%d", r, hs, he)
			}
			total += r.End - r.Start + 1
		}
		if total != he-hs+1 {
			t.Fatalf("classes cover %d lines, want %d", total, he-hs+1)
		}
	})
}
