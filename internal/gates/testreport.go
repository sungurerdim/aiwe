package gates

import (
	"cmp"
	"fmt"
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
	// neverRedRuns is how many runs a test must appear in before it counts as never red (L68).
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

// slowestShare is how many tests the slowest 10 % are and how much of the time they take, then the
// slowest of them. A record keeps only its slowest tests, so the figure is over those.
func slowestShare(checks []ledger.Check) []string {
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

// failures are the failing tests read from the tail files of the red runs; read and unread count
// the red runs whose tail was read and was not.
type failures struct {
	tests        map[string]*testFailure
	read, unread int
}

// redClassOf is the red class c names, or unknown when no rule matched.
func redClassOf(c ledger.Check) string {
	if c.RedClass.Known {
		return c.RedClass.Value
	}
	return unknownText
}

// addTail counts each failing test the tail data of the red run c holds.
func (f failures) addTail(data []byte, c ledger.Check) {
	for _, e := range readEntries(data, nil, RepoScope{}) {
		if e.Test != "" && e.Kind != findingKind {
			f.add(e.Test, redClassOf(c)+"."+e.Kind, c)
		}
	}
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

// unreadNote counts the red runs that have no readable tail, or is empty when all have one.
func (f failures) unreadNote() []string {
	if f.unread == 0 {
		return nil
	}
	return []string{fmt.Sprintf("note: red runs without a readable tail: %d", f.unread)}
}

// neverRed lists the tests that ran in at least neverRedRuns records and failed in none of the red
// runs whose tail was read. The runs a test is known to have run in are the records that name it in
// their slowest list.
func neverRed(checks []ledger.Check, f failures) []string {
	runs := map[string]int{}
	known := false
	for _, c := range checks {
		known = known || c.Slowest.Known
		for _, t := range c.Slowest.Value {
			runs[t.ID]++
		}
	}
	if !known {
		return unknownLines()
	}
	var ids []string
	for id, n := range runs {
		if _, red := f.tests[id]; n >= neverRedRuns && !red {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	head := fmt.Sprintf("%d tests ran in %d or more runs and were never red", len(ids), neverRedRuns)
	if len(ids) == 0 {
		head = fmt.Sprintf("no test ran in %d or more runs without a red", neverRedRuns)
	}
	rows := append([]string{head}, ids[:min(len(ids), reportRows)]...)
	return append(rows, f.unreadNote()...)
}

// topFailing lists the most failing tests with subclass, count and the commit of their last red run.
func topFailing(f failures) []string {
	if f.read == 0 {
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
	if len(rows) == 0 {
		rows = append(rows, "no failing test in the red runs read")
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
