package gates

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/ledger"
)

const (
	// unknownText is what the report prints for a value no record measured, never 0 (DR-6, L68).
	unknownText = "unknown"
	// reportRows bounds the rows of a section that lists tests, files or resources.
	reportRows = 10
	// slowestPercent is the share of the tests, slowest first, that the slowest section sums.
	slowestPercent = 10
	// neverRedRuns is how many runs a test must be known to have survived before it counts as never
	// red; set by #595, which the owner may change.
	neverRedRuns = 3
)

// ReportSection is one titled block of the test report: its lines, or the one line unknown.
type ReportSection struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
}

// TestReport is what `aiwe report tests` prints (L68): the sections of the check records of one
// period, in print order. It is built when asked for and never kept.
type TestReport struct {
	Since    string          `json:"since"`
	Sections []ReportSection `json:"sections"`
}

// text renders the report: a heading line, then each section under its "## " title.
func (r TestReport) text() string {
	var b strings.Builder
	b.WriteString("test report " + r.Since)
	for _, s := range r.Sections {
		b.WriteString("\n## " + s.Title + "\n" + strings.Join(s.Lines, "\n"))
	}
	return b.String()
}

// buildTestReport builds the sections of checks and of the failing tests their tails named; since is
// the --since value as given, empty for every record.
func buildTestReport(checks []ledger.Check, failed failures, since string) TestReport {
	heading := "of all records"
	if since != "" {
		heading = "since " + since
	}
	return TestReport{Since: heading, Sections: []ReportSection{
		{Title: "red class shares", Lines: redShares(checks)},
		{Title: "slowest 10 % of tests", Lines: slowestShare(checks)},
		{Title: "tests never red", Lines: neverRed(checks, failed)},
		{Title: "map gaps", Lines: mapGapLines(checks)},
		{Title: "queue wait p50 and p90", Lines: queueWaitLines(checks)},
		{Title: "top failing tests", Lines: topFailing(failed)},
	}}
}

// unknownLines is the body of a section no record measured.
func unknownLines() []string { return []string{unknownText} }

// isRed reports whether c is a run that ended with a non-zero exit code. A record whose exit code
// was not measured is not known to be red.
func isRed(c ledger.Check) bool { return c.ExitCode.Known && c.ExitCode.Value != 0 }

// share is n of total as a percent with one decimal.
func share(n, total int64) string {
	return fmt.Sprintf("%.1f %%", 100*float64(n)/float64(total))
}

// countRows prints the keys of counts, most first and then by name, as "key n (share)".
func countRows(counts map[string]int64, total int64) []string {
	keys := sortedByCount(counts)
	rows := make([]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, fmt.Sprintf("%s %d (%s)", k, counts[k], share(counts[k], total)))
	}
	return rows
}

// sortedByCount returns the keys of counts, highest count first and then by name.
func sortedByCount(counts map[string]int64) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b)) })
	return keys
}

// redShares is the share of each red class among the red runs; a red run with no known class counts
// as unknown.
func redShares(checks []ledger.Check) []string {
	counts := map[string]int64{}
	var total int64
	for _, c := range checks {
		if !isRed(c) {
			continue
		}
		counts[redClassOf(c)]++
		total++
	}
	if total == 0 {
		return unknownLines()
	}
	return countRows(counts, total)
}

// timedTest is a test and its mean duration over the records that name it.
type timedTest struct {
	id     string
	meanMS int64
}

// meanDurations returns the tests the slowest lists name, slowest mean first.
func meanDurations(checks []ledger.Check) []timedTest {
	sums, runs := map[string]int64{}, map[string]int64{}
	for _, c := range checks {
		for _, t := range c.Slowest.Value {
			sums[t.ID] += t.DurationMS
			runs[t.ID]++
		}
	}
	tests := make([]timedTest, 0, len(sums))
	for id, sum := range sums {
		tests = append(tests, timedTest{id: id, meanMS: sum / runs[id]})
	}
	slices.SortFunc(tests, func(a, b timedTest) int { return cmp.Or(cmp.Compare(b.meanMS, a.meanMS), cmp.Compare(a.id, b.id)) })
	return tests
}

// slowestGap is why the slowest lists cannot give the slowest 10 % of the tests, or empty when they
// can: a record keeps only its slowestTests slowest tests, so the share is over every test a run
// ran only when its record holds its test count and kept all of them (DR-6). Skipped tests are not
// timed and so not counted.
func slowestGap(checks []ledger.Check) string {
	for _, c := range checks {
		if !c.Slowest.Known {
			continue
		}
		if !c.Counts.Known {
			return "a record holds no test count, so its kept tests may not be all it ran"
		}
		if c.Counts.Value["pass"]+c.Counts.Value["fail"] > int64(len(c.Slowest.Value)) {
			return fmt.Sprintf("a record ran more tests than the %d slowest it keeps", slowestTests)
		}
	}
	return ""
}

// slowestShare is how many tests the slowest 10 % are and how much of the time they take, then the
// slowest of them. It is unknown, with the reason, when a record did not keep every test it ran.
func slowestShare(checks []ledger.Check) []string {
	if gap := slowestGap(checks); gap != "" {
		return []string{unknownText + ": " + gap}
	}
	tests := meanDurations(checks)
	var all int64
	for _, t := range tests {
		all += t.meanMS
	}
	if all == 0 {
		return unknownLines()
	}
	top := max(1, (len(tests)*slowestPercent+99)/100)
	var sum int64
	rows := []string{""}
	for i, t := range tests[:top] {
		sum += t.meanMS
		if i < reportRows {
			rows = append(rows, fmt.Sprintf("%s %d ms", t.id, t.meanMS))
		}
	}
	rows[0] = fmt.Sprintf("%d of %d tests take %s of the time", top, len(tests), share(sum, all))
	return rows
}

// testFailure is a failing test as the red runs show it: how often, and the subclass and commit of
// its last red run.
type testFailure struct {
	count    int64
	at       time.Time
	commit   string
	subclass string
}

// failures are the failing tests read from the tail files of the red runs. named holds the index in
// the checks of each red run whose tail names a failing test; unread counts the red runs with no
// readable tail and unnamed those whose tail names no failing test.
type failures struct {
	tests           map[string]*testFailure
	named           map[int]bool
	unread, unnamed int
}

// newFailures is an empty failures.
func newFailures() failures {
	return failures{tests: map[string]*testFailure{}, named: map[int]bool{}}
}

// redClassOf is the red class c names, or unknown when no rule matched.
func redClassOf(c ledger.Check) string {
	if c.RedClass.Known {
		return c.RedClass.Value
	}
	return unknownText
}

// addTail counts each failing test the tail data of the red run c holds, and returns how many it
// named.
func (f failures) addTail(data []byte, c ledger.Check) int {
	n := 0
	for _, e := range readEntries(data, nil, RepoScope{}) {
		if e.Test != "" && e.Kind != findingKind {
			f.add(e.Test, redClassOf(c)+"."+e.Kind, c)
			n++
		}
	}
	return n
}

// add counts one red run of test; the subclass and commit are those of the latest run.
func (f failures) add(test, subclass string, c ledger.Check) {
	t, ok := f.tests[test]
	if !ok {
		t = &testFailure{}
		f.tests[test] = t
	}
	t.count++
	if !c.At.Before(t.at) {
		t.at, t.commit, t.subclass = c.At, c.Commit, subclass
	}
}

// unreadNote counts the red runs that have no readable tail and those whose tail names no failing
// test, or is empty when every red run names one.
func (f failures) unreadNote() []string {
	var notes []string
	if f.unread > 0 {
		notes = append(notes, fmt.Sprintf("note: red runs without a readable tail: %d", f.unread))
	}
	if f.unnamed > 0 {
		notes = append(notes, fmt.Sprintf("note: red runs whose tail names no failing test: %d", f.unnamed))
	}
	return notes
}

// selectedRuns counts, for each test the selected lists of checks name, the runs it is known to have
// survived (a green run, or a red run whose tail names its failing tests) and the runs whose outcome
// for it is unknown (a red run that names no failing test, or a run with no exit code). known is
// whether any record measured its selected list.
func selectedRuns(checks []ledger.Check, f failures) (survived, unsure map[string]int, known bool) {
	survived, unsure = map[string]int{}, map[string]int{}
	for i, c := range checks {
		if !c.Selected.Known {
			continue
		}
		known = true
		counted := unsure
		if (c.ExitCode.Known && c.ExitCode.Value == 0) || f.named[i] {
			counted = survived
		}
		for _, s := range c.Selected.Value {
			counted[s.ID]++
		}
	}
	return survived, unsure, known
}

// neverRed lists the tests that ran in at least neverRedRuns records and failed in none. A test that
// ran in a run whose failing tests are unknown is listed as unknown, not never red (DR-6). The runs a
// test ran in are the records that name it in their selected list.
func neverRed(checks []ledger.Check, f failures) []string {
	survived, unsure, known := selectedRuns(checks, f)
	if !known {
		return unknownLines()
	}
	ran := maps.Clone(survived)
	for id, n := range unsure {
		ran[id] += n
	}
	var ids, unknownIDs []string
	for _, id := range slices.Sorted(maps.Keys(ran)) {
		if _, red := f.tests[id]; red || ran[id] < neverRedRuns {
			continue
		}
		if unsure[id] > 0 {
			unknownIDs = append(unknownIDs, id+" "+unknownText)
			continue
		}
		ids = append(ids, id)
	}
	head := fmt.Sprintf("%d tests ran in %d or more runs and were never red", len(ids), neverRedRuns)
	if len(ids) == 0 {
		head = fmt.Sprintf("no test ran in %d or more runs without a red", neverRedRuns)
	}
	rows := append([]string{head}, ids[:min(len(ids), reportRows)]...)
	if len(unknownIDs) > 0 {
		rows = append(rows, fmt.Sprintf("%s for %d tests that ran in a run whose failing tests are not known",
			unknownText, len(unknownIDs)))
		rows = append(rows, unknownIDs[:min(len(unknownIDs), reportRows)]...)
	}
	return append(rows, f.unreadNote()...)
}

// topFailing lists the most failing tests with subclass, count and the commit of their last red run.
// It is unknown when no red run's tail names a failing test.
func topFailing(f failures) []string {
	if len(f.named) == 0 {
		return unknownLines()
	}
	counts := make(map[string]int64, len(f.tests))
	for id, t := range f.tests {
		counts[id] = t.count
	}
	rows := []string{}
	for _, id := range sortedByCount(counts)[:min(len(counts), reportRows)] {
		t := f.tests[id]
		rows = append(rows, fmt.Sprintf("%s %s count %d last red %s", id, t.subclass, t.count, t.commit))
	}
	return rows
}

// mapGapLines lists the files no source knows with the records that name each, over the records
// that measured their map gaps.
func mapGapLines(checks []ledger.Check) []string {
	counts := map[string]int64{}
	var measured int64
	for _, c := range checks {
		if !c.MapGaps.Known {
			continue
		}
		measured++
		for _, file := range slices.Compact(slices.Sorted(slices.Values(c.MapGaps.Value))) {
			counts[file]++
		}
	}
	if measured == 0 {
		return unknownLines()
	}
	if len(counts) == 0 {
		return []string{"no file without a source"}
	}
	rows := []string{}
	for _, file := range sortedByCount(counts)[:min(len(counts), reportRows)] {
		rows = append(rows, fmt.Sprintf("%s in %d of %d records", file, counts[file], measured))
	}
	return rows
}

// queueWaitLines is the p50 and p90 of the queue wait of each resource, nearest rank. A resource
// none of whose waits was measured reads unknown.
func queueWaitLines(checks []ledger.Check) []string {
	waits := map[string][]int64{}
	for _, c := range checks {
		for name, w := range c.QueueWaitMS.Value {
			if w.Known {
				waits[name] = append(waits[name], w.Value)
			} else if _, seen := waits[name]; !seen {
				waits[name] = nil
			}
		}
	}
	if len(waits) == 0 {
		return unknownLines()
	}
	names := make([]string, 0, len(waits))
	for name := range waits {
		names = append(names, name)
	}
	slices.Sort(names)
	rows := make([]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, fmt.Sprintf("%s p50 %s p90 %s", name, percentile(waits[name], 50), percentile(waits[name], 90)))
	}
	return rows
}

// percentile is the nearest-rank p-th percentile of values in ms, or unknown when there is none.
func percentile(values []int64, p int) string {
	if len(values) == 0 {
		return unknownText
	}
	sorted := slices.Sorted(slices.Values(values))
	return strconv.FormatInt(sorted[(p*len(sorted)+99)/100-1], 10) + " ms"
}
