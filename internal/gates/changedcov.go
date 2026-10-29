package gates

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
)

// FileRange names a run of lines of one file as `file:startLine-endLine`.
type FileRange struct {
	File       string
	Start, End int
}

// String returns the name `file:startLine-endLine` a report carries.
func (r FileRange) String() string {
	return fmt.Sprintf("%s:%d-%d", r.File, r.Start, r.End)
}

// ChangedCoverage is the intersection of the changed lines of a diff with a coverage profile.
// Every changed line is in exactly one of the three classes.
type ChangedCoverage struct {
	// Uncovered are the changed lines inside a block that no test executed (count 0), merged into
	// runs, sorted by file and line.
	Uncovered []FileRange
	// NotMeasured are the changed lines that lie in no profile block, or in a file the profile does
	// not name (DR-6). They are neither covered nor uncovered.
	NotMeasured []FileRange
	// Covered counts the changed lines inside a block that a test executed.
	Covered int
}

// lineState is the class of a run of changed lines.
type lineState int

const (
	stateNotMeasured lineState = iota
	stateUncovered
	stateCovered
)

// segment is a run of changed lines of one class.
type segment struct {
	start, end int
	state      lineState
}

// ChangedLinesUncovered intersects the added lines of a diff with the blocks of a coverage profile.
// A changed line in a block that a test executed is covered; a changed line whose blocks all have
// count 0 is uncovered; a changed line in no block is not measured. A line that several blocks
// share (one block reported by two test binaries) is covered when any of them was executed. The
// work follows the number of blocks, never the length of a range.
func ChangedLinesUncovered(hunks DiffHunks, profile CoverProfile) ChangedCoverage {
	var res ChangedCoverage
	for _, file := range slices.Sorted(maps.Keys(hunks)) {
		ranges := slices.SortedFunc(slices.Values(hunks[file]), func(a, b LineRange) int {
			return cmp.Compare(a.Start, b.Start)
		})
		for _, r := range ranges {
			for _, seg := range classifyRange(r, profile[file]) {
				res.add(file, seg)
			}
		}
	}
	return res
}

// add files one segment under its class.
func (c *ChangedCoverage) add(file string, seg segment) {
	named := FileRange{File: file, Start: seg.start, End: seg.end}
	switch seg.state {
	case stateCovered:
		c.Covered += seg.end - seg.start + 1
	case stateUncovered:
		c.Uncovered = append(c.Uncovered, named)
	case stateNotMeasured:
		c.NotMeasured = append(c.NotMeasured, named)
	}
}

// edge is the start (+1) or the end (-1) of a block along the lines of a range.
type edge struct {
	at, measured, hit int
}

// classifyRange splits one changed range into runs of one class, in line order, by a sweep over the
// edges of the blocks that touch the range.
func classifyRange(r LineRange, blocks []CoverBlock) []segment {
	var edges []edge
	for _, b := range blocks {
		if b.End < r.Start || b.Start > r.End {
			continue
		}
		hit := 0
		if b.Count > 0 {
			hit = 1
		}
		edges = append(edges, edge{at: max(b.Start, r.Start), measured: 1, hit: hit},
			edge{at: min(b.End, r.End) + 1, measured: -1, hit: -hit})
	}
	slices.SortFunc(edges, func(a, b edge) int { return cmp.Compare(a.at, b.at) })
	var segs []segment
	pos, measured, hit := r.Start, 0, 0
	for i := 0; i < len(edges); {
		at := edges[i].at
		segs = appendSegment(segs, pos, at-1, stateOf(measured, hit))
		for ; i < len(edges) && edges[i].at == at; i++ {
			measured += edges[i].measured
			hit += edges[i].hit
		}
		pos = at
	}
	return appendSegment(segs, pos, r.End, stateOf(measured, hit))
}

// stateOf returns the class of a line that lies in measured blocks, hit of them executed.
func stateOf(measured, hit int) lineState {
	switch {
	case hit > 0:
		return stateCovered
	case measured > 0:
		return stateUncovered
	default:
		return stateNotMeasured
	}
}

// appendSegment adds the lines start..end, extending the last segment when it ends just before and
// has the same class. An empty run adds nothing.
func appendSegment(segs []segment, start, end int, state lineState) []segment {
	if end < start {
		return segs
	}
	if last := len(segs) - 1; last >= 0 && segs[last].state == state && segs[last].end == start-1 {
		segs[last].end = end
		return segs
	}
	return append(segs, segment{start: start, end: end, state: state})
}
