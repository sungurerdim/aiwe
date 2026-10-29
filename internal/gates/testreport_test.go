package gates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/ledger"
)

// reportTestPkg is the Go package the report fixtures name their tests in.
const reportTestPkg = "example.com/x/p"

// reportTestEnvelope is the JSON result envelope of `aiwe report tests`; the report is its detail.
type reportTestEnvelope struct {
	Status  core.Status  `json:"status"`
	Outcome core.Outcome `json:"outcome"`
	Detail  string       `json:"detail"`
}

// reportTestTitles are the section titles of the report, in the order they print: the five of the
// record and the top failing tests.
func reportTestTitles() []string {
	return []string{"red class shares", "slowest 10 % of tests", "tests never red", "map gaps",
		"queue wait p50 and p90", "top failing tests"}
}

// reportTestRun runs `aiwe report tests` through the one command registry, as the binary does.
func reportTestRun(t *testing.T, args ...string) reportTestEnvelope {
	t.Helper()
	var out, stream bytes.Buffer
	argv := append([]string{"report", "tests"}, args...)
	argv = append(argv, "--format", "json")
	code := core.RunCLI(context.Background(), Commands(), argv, &out, &stream)
	var env reportTestEnvelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("%s printed no JSON envelope: %v\n%s", strings.Join(argv, " "), err, out.String())
	}
	if code != 0 || env.Status != core.StatusOK {
		t.Fatalf("%s exit %d, status %q, want 0 and ok: %s", strings.Join(argv, " "), code, env.Status, env.Detail)
	}
	return env
}

// reportTestSection returns the lines under the section title of detail, up to the next section.
func reportTestSection(t *testing.T, detail, title string) []string {
	t.Helper()
	var body []string
	in := false
	for _, line := range strings.Split(detail, "\n") {
		switch {
		case line == "## "+title:
			in = true
		case strings.HasPrefix(line, "## "):
			in = false
		case in:
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		t.Fatalf("section %q is absent or empty in:\n%s", title, detail)
	}
	return body
}

// reportTestCheck is a check record of repo with every test-run field unknown.
func reportTestCheck(repo, commit string, at time.Time) ledger.Check {
	return ledger.Check{Header: ledger.Header{FormatVersion: checkFormatVersion, Kind: ledger.KindCheck, At: at.UTC(),
		RepoID: repo, Goal: recordNone, Unit: recordNone, RunID: recordNone}, Key: "k", StepID: "test", Commit: commit}
}

// reportTestSlowest is a measured list of ten tests: T0 takes 900 ms and T1 to T9 10 ms each.
func reportTestSlowest() ledger.Field[[]ledger.Timed] {
	timed := []ledger.Timed{{ID: reportTestPkg + ".T0", DurationMS: 900}}
	for i := 1; i < 10; i++ {
		timed = append(timed, ledger.Timed{ID: fmt.Sprintf("%s.T%d", reportTestPkg, i), DurationMS: 10})
	}
	return ledger.Field[[]ledger.Timed]{Value: timed, Known: true}
}

// reportTestSelected is a measured list of the tests named, each run for an import.
func reportTestSelected(names ...string) ledger.Field[[]ledger.Selected] {
	sel := []ledger.Selected{}
	for _, name := range names {
		sel = append(sel, ledger.Selected{ID: reportTestPkg + "." + name, Reason: "import"})
	}
	return ledger.Field[[]ledger.Selected]{Value: sel, Known: true}
}

// reportTestAll names the ten tests of reportTestSlowest.
func reportTestAll() []string {
	names := []string{}
	for i := range 10 {
		names = append(names, fmt.Sprintf("T%d", i))
	}
	return names
}

// reportTestFail is the go test -json line of a failing test.
func reportTestFail(name string) string {
	return fmt.Sprintf(`{"Action":"fail","Package":%q,"Test":%q,"Elapsed":0.01}`, reportTestPkg, name)
}

// reportTestWrite writes c, and its tail when it has one, through the sink of the gate.
func reportTestWrite(t *testing.T, c ledger.Check, tail ...string) {
	t.Helper()
	sink, err := openCheckLedger()
	if err != nil {
		t.Fatalf("open the check ledger: %v", err)
	}
	if err := sink.WriteCheck(context.Background(), CheckRecord{Check: c, Tail: tail}); err != nil {
		t.Fatalf("write the check record: %v", err)
	}
}

// reportTestMeasured is a measured number.
func reportTestMeasured(v int64) core.Measured[int64] {
	return core.Measured[int64]{Value: v, Known: true}
}

// reportTestSeed writes four records of repo, all inside seven days: two red runs of class real_bug
// that fail T1 (and T3 once), a red run whose class and failing tests are unknown and that ran only
// T0, and a green run. Every run but the third ran T0 to T9.
func reportTestSeed(t *testing.T, repo string, now time.Time) {
	t.Helper()
	red := func(commit string, age time.Duration, wait int64) ledger.Check {
		c := reportTestCheck(repo, commit, now.Add(-age))
		c.ExitCode, c.Slowest = reportTestMeasured(1), reportTestSlowest()
		c.Selected = reportTestSelected(reportTestAll()...)
		c.RedClass = ledger.Field[string]{Value: "real_bug", Known: true}
		c.QueueWaitMS = ledger.Field[map[string]core.Measured[int64]]{
			Value: map[string]core.Measured[int64]{"db": reportTestMeasured(wait)}, Known: true}
		return c
	}
	r1 := red("c1c1c1c", 72*time.Hour, 100)
	r1.MapGaps = ledger.Field[[]string]{Value: []string{"a.go"}, Known: true}
	reportTestWrite(t, r1, reportTestFail("T1"))
	r2 := red("c2c2c2c", 48*time.Hour, 300)
	r2.MapGaps = ledger.Field[[]string]{Value: []string{"a.go", "b.go"}, Known: true}
	reportTestWrite(t, r2, reportTestFail("T1"), reportTestFail("T3"))
	r3 := reportTestCheck(repo, "c3c3c3c", now.Add(-24*time.Hour))
	r3.ExitCode, r3.Slowest = reportTestMeasured(1), reportTestSlowest()
	r3.Selected = reportTestSelected("T0")
	reportTestWrite(t, r3)
	r4 := reportTestCheck(repo, "c4c4c4c", now.Add(-time.Hour))
	r4.ExitCode, r4.Slowest = reportTestMeasured(0), reportTestSlowest()
	r4.Selected = reportTestSelected(reportTestAll()...)
	r4.MapGaps = ledger.Field[[]string]{Value: []string{}, Known: true}
	r4.QueueWaitMS = ledger.Field[map[string]core.Measured[int64]]{Value: map[string]core.Measured[int64]{
		"db": reportTestMeasured(200), "browser": {}}, Known: true}
	reportTestWrite(t, r4)
}

// TestReportTestsPrintsFiveSections pins that `aiwe report tests --since 7d` prints the five sections
// of the record, then the top failing tests, each with the figures the check records give.
func TestReportTestsPrintsFiveSections(t *testing.T) {
	commandsTestSetRoots(t)
	reportTestSeed(t, "r", time.Now())
	env := reportTestRun(t, "--since", "7d")
	var titles []string
	for _, line := range strings.Split(env.Detail, "\n") {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			titles = append(titles, title)
		}
	}
	if !slices.Equal(titles, reportTestTitles()) {
		t.Fatalf("section titles = %q, want %q", titles, reportTestTitles())
	}
	want := map[string][]string{
		"red class shares":       {"real_bug 2 (66.7 %)", "unknown 1 (33.3 %)"},
		"slowest 10 % of tests":  {"unknown: a record holds no test count, so its kept tests may not be all it ran"},
		"tests never red":        {"7 tests ran in 3 or more runs and were never red"},
		"map gaps":               {"a.go in 2 of 3 records", "b.go in 1 of 3 records"},
		"queue wait p50 and p90": {"browser p50 unknown p90 unknown", "db p50 200 ms p90 300 ms"},
	}
	for title, lines := range want {
		got := reportTestSection(t, env.Detail, title)
		if len(got) < len(lines) || !slices.Equal(got[:len(lines)], lines) {
			t.Errorf("section %q = %q, want it to start with %q", title, got, lines)
		}
	}
	never := reportTestSection(t, env.Detail, "tests never red")
	for _, id := range []string{"T2", "T9"} {
		if !slices.Contains(never, reportTestPkg+"."+id) {
			t.Errorf("tests never red misses %s: %q", id, never)
		}
	}
	// T0 ran in the third run, a red run whose failing tests are unknown: it is unknown, not never red.
	for _, line := range []string{reportTestPkg + ".T0 unknown", "note: red runs without a readable tail: 1"} {
		if !slices.Contains(never, line) {
			t.Errorf("tests never red misses %q: %q", line, never)
		}
	}
	for _, id := range []string{"T0", "T1", "T3"} {
		if slices.Contains(never, reportTestPkg+"."+id) {
			t.Errorf("tests never red lists %s, which failed: %q", id, never)
		}
	}
}

// TestReportTestsListsTopFailures pins that the top failing tests are listed most failing first, each
// with its subclass, its count and the commit of its last red run.
func TestReportTestsListsTopFailures(t *testing.T) {
	commandsTestSetRoots(t)
	reportTestSeed(t, "r", time.Now())
	got := reportTestSection(t, reportTestRun(t, "--since", "7d").Detail, "top failing tests")
	want := []string{
		reportTestPkg + ".T1 real_bug.assertion count 2 last red c2c2c2c",
		reportTestPkg + ".T3 real_bug.assertion count 1 last red c2c2c2c",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("top failing tests = %q, want %q", got, want)
	}
}

// TestReportTestsUnknownWhenUnmeasured pins that a value no record measured prints "unknown" and never
// 0, for a ledger that holds no record and for one whose only record measured nothing.
func TestReportTestsUnknownWhenUnmeasured(t *testing.T) {
	zero := regexp.MustCompile(`\b0\b`)
	cases := map[string]func(t *testing.T){
		"no ledger file": func(t *testing.T) {},
		"a record that measured nothing": func(t *testing.T) {
			reportTestWrite(t, reportTestCheck("r", "c1c1c1c", time.Now().Add(-time.Hour)))
		},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			commandsTestSetRoots(t)
			seed(t)
			env := reportTestRun(t, "--since", "7d")
			for _, title := range reportTestTitles() {
				got := reportTestSection(t, env.Detail, title)
				if !slices.Equal(got, []string{"unknown"}) {
					t.Errorf("section %q = %q, want [unknown]", title, got)
				}
				if zero.MatchString(strings.Join(got, "\n")) {
					t.Errorf("section %q prints 0 for a value that was not measured: %q", title, got)
				}
			}
		})
	}
}

// TestReportTestsFiltersByRepoAndSince pins that a record of another repo is not counted with --repo,
// and that a record older than --since is not counted.
func TestReportTestsFiltersByRepoAndSince(t *testing.T) {
	commandsTestSetRoots(t)
	repo, other := t.TempDir(), t.TempDir()
	now := time.Now()
	for _, r := range []struct {
		repo, class string
		age         time.Duration
	}{{repo, "real_bug", time.Hour}, {other, "environment", time.Hour}, {repo, "stale", 30 * 24 * time.Hour}} {
		c := reportTestCheck(r.repo, "c1c1c1c", now.Add(-r.age))
		c.ExitCode = reportTestMeasured(1)
		c.RedClass = ledger.Field[string]{Value: r.class, Known: true}
		reportTestWrite(t, c)
	}
	shares := func(args ...string) string {
		return strings.Join(reportTestSection(t, reportTestRun(t, args...).Detail, "red class shares"), "\n")
	}
	if got, want := shares("--repo", repo, "--since", "7d"), "real_bug 1 (100.0 %)"; got != want {
		t.Errorf("--repo and --since shares = %q, want %q", got, want)
	}
	if got := shares("--since", "7d"); !strings.Contains(got, "environment 1") || strings.Contains(got, "stale") {
		t.Errorf("--since 7d without --repo shares = %q, want the other repo counted and the old record not", got)
	}
	if got := shares("--repo", repo, "--since", "60d"); !strings.Contains(got, "stale 1") {
		t.Errorf("--since 60d shares = %q, want the 30-day-old record counted", got)
	}
}

// reportTestSnapshot maps each file under root to its content, and each directory to "".
func reportTestSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			snap[path] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		snap[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return snap
}

// TestReportTestsWritesNoFile pins that printing the report creates, changes and removes no file under
// the state, cache or temp root: the report is printed only on request and none is kept.
func TestReportTestsWritesNoFile(t *testing.T) {
	roots := commandsTestSetRoots(t)
	reportTestSeed(t, "r", time.Now())
	dirs := []string{roots.state, roots.cache, roots.temp}
	before := make([]map[string]string, len(dirs))
	for i, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
		before[i] = reportTestSnapshot(t, dir)
	}
	reportTestRun(t, "--since", "7d")
	for i, dir := range dirs {
		after := reportTestSnapshot(t, dir)
		if len(after) != len(before[i]) {
			t.Errorf("%s holds %d entries after the report, %d before", dir, len(after), len(before[i]))
		}
		for path, content := range after {
			if old, ok := before[i][path]; !ok || old != content {
				t.Errorf("%s was created or changed by the report", path)
			}
		}
	}
}

// TestReportTestsSlowestNeedsTheWholeRun pins that the slowest 10 % are summed only when each record
// keeps every test it ran: a run of more tests than its record keeps prints unknown with the reason,
// never a share of the kept ones.
func TestReportTestsSlowestNeedsTheWholeRun(t *testing.T) {
	cases := map[string]struct {
		counts map[string]int64
		want   []string
	}{
		"every test kept": {map[string]int64{"pass": 9, "fail": 1, "skip": 3},
			[]string{"1 of 10 tests take 90.9 % of the time", reportTestPkg + ".T0 900 ms"}},
		"more tests than kept": {map[string]int64{"pass": 40},
			[]string{"unknown: a record ran more tests than the 10 slowest it keeps"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			commandsTestSetRoots(t)
			c := reportTestCheck("r", "c1c1c1c", time.Now().Add(-time.Hour))
			c.ExitCode, c.Slowest = reportTestMeasured(0), reportTestSlowest()
			c.Counts = ledger.Field[map[string]int64]{Value: tc.counts, Known: true}
			reportTestWrite(t, c)
			got := reportTestSection(t, reportTestRun(t, "--since", "7d").Detail, "slowest 10 % of tests")
			if !slices.Equal(got, tc.want) {
				t.Errorf("slowest 10 %% of tests = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReportTestsRedRunNamingNoFailure pins that a red run whose tail names no failing test is no run
// a test survived: the tests it ran read unknown, the run is noted, and the top failing tests are
// unknown.
func TestReportTestsRedRunNamingNoFailure(t *testing.T) {
	commandsTestSetRoots(t)
	now := time.Now()
	for i, exit := range []int64{1, 0, 0} {
		c := reportTestCheck("r", fmt.Sprintf("c%dc%dc%dc", i, i, i), now.Add(-time.Duration(i+1)*time.Hour))
		c.ExitCode, c.Selected = reportTestMeasured(exit), reportTestSelected("T0")
		pass := fmt.Sprintf(`{"Action":"pass","Package":%q,"Test":"T0","Elapsed":0.01}`, reportTestPkg)
		reportTestWrite(t, c, pass)
	}
	detail := reportTestRun(t, "--since", "7d").Detail
	never := reportTestSection(t, detail, "tests never red")
	if slices.Contains(never, reportTestPkg+".T0") || !slices.Contains(never, reportTestPkg+".T0 unknown") {
		t.Errorf("tests never red = %q, want T0 unknown and not never red", never)
	}
	if !slices.Contains(never, "note: red runs whose tail names no failing test: 1") {
		t.Errorf("tests never red = %q, want the red run without a named failure noted", never)
	}
	if got := reportTestSection(t, detail, "top failing tests"); !slices.Equal(got, []string{"unknown"}) {
		t.Errorf("top failing tests = %q, want [unknown]", got)
	}
}

// TestParseSince pins the forms --since takes (Nd, Nh, a date) and the counts it refuses: zero, a
// negative count, a count over maxSinceUnits, no count, an unknown unit and a date that is no date.
func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	accepted := map[string]time.Time{
		"":           {},
		"7d":         now.Add(-7 * 24 * time.Hour),
		"12h":        now.Add(-12 * time.Hour),
		"36500d":     now.Add(-36500 * 24 * time.Hour),
		"2026-10-22": time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
	}
	for value, want := range accepted {
		if got, err := parseSince(value, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"0d", "-1d", "0h", "36501d", "d", "7", "7w", "7\u0434", "2026-13-01"} {
		_, err := parseSince(value, now)
		var refusal *core.Error
		if !errors.As(err, &refusal) || refusal.Outcome != core.OutcomeUsage {
			t.Errorf("parseSince(%q) error = %v, want a usage refusal", value, err)
		}
	}
}
