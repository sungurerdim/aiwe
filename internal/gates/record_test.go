package gates

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/ledger"
)

// recordTestEnv makes this test binary a helper step: it prints the file the variable names to its
// stdout and exits with the code that recordTestExitEnv holds.
func recordTestEnv() string { return gatesTestEnv("RECORD") }

// recordTestExitEnv holds the exit code of the helper step of recordTestEnv.
func recordTestExitEnv() string { return gatesTestEnv("RECORD_EXIT") }

func init() {
	path := os.Getenv(recordTestEnv())
	if path == "" {
		return
	}
	out, err := os.ReadFile(path)
	if err != nil {
		os.Exit(3)
	}
	_, _ = os.Stdout.Write(out)
	code, err := strconv.Atoi(os.Getenv(recordTestExitEnv()))
	if err != nil {
		os.Exit(4)
	}
	os.Exit(code)
}

// recordTestPkg is the Go package the go test -json fixtures report on.
const recordTestPkg = durationsTestModule + "/internal/x"

// recordTestStep returns a step with id that prints output to its stdout and exits with code.
func recordTestStep(t *testing.T, id, output string, code int) GateStep {
	t.Helper()
	path := filepath.Join(t.TempDir(), id+".out")
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		t.Fatalf("write the output of step %s: %v", id, err)
	}
	t.Setenv(recordTestEnv(), path)
	t.Setenv(recordTestExitEnv(), strconv.Itoa(code))
	return GateStep{ID: id, Command: []string{os.Args[0], "-test.run=^$"}, Required: true}
}

// recordTestSink keeps every record a runner hands it, in order.
type recordTestSink struct {
	mu   sync.Mutex
	recs []CheckRecord
}

// WriteCheck keeps rec.
func (s *recordTestSink) WriteCheck(_ context.Context, rec CheckRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
	return nil
}

// recordTestRun runs steps as one gate whose records go to a sink, and returns the records.
func recordTestRun(t *testing.T, steps ...GateStep) []CheckRecord {
	t.Helper()
	r := runnerTestRunner(t)
	sink := &recordTestSink{}
	r.Records = sink
	r.Facts = RunFacts{Commit: ledger.CommitUncommitted, Toolchain: "go1.25.0 " + runtime.GOOS + " " + runtime.GOARCH,
		OS: runtime.GOOS + "/" + runtime.GOARCH, Module: durationsTestModule}
	if _, err := r.Run(t.Context(), Definition{Steps: steps}, t.TempDir(), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return sink.recs
}

// recordTestLedger returns the check records of the ledger in the state root, by step id.
func recordTestLedger(t *testing.T, state string) map[string][]ledger.Check {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("catches a gate that writes no ledger: %v", err)
	}
	defer func() { _ = f.Close() }()
	recs, err := ledger.Read(f)
	if err != nil {
		t.Fatalf("catches a check record the ledger cannot read back: %v", err)
	}
	byStep := map[string][]ledger.Check{}
	for _, rec := range recs {
		if c, ok := rec.(ledger.Check); ok {
			byStep[c.StepID] = append(byStep[c.StepID], c)
		}
	}
	return byStep
}

// recordTestOne returns the one record of step; any other count fails the test.
func recordTestOne(t *testing.T, byStep map[string][]ledger.Check, step string) ledger.Check {
	t.Helper()
	if n := len(byStep[step]); n != 1 {
		t.Fatalf("catches a gate step that writes %d check records, want 1: step %s", n, step)
	}
	return byStep[step][0]
}

// TestGateStepWritesOneCheckRecord pins SPEC-contracts §8.5: a gate run writes one check record per
// step into the ledger of the state root, each with the tree, toolchain and OS it ran on, its local
// duration and its exit code.
func TestGateStepWritesOneCheckRecord(t *testing.T) {
	goBin := runnerTestGo(t)
	steps := []GateStep{
		{ID: "format", Command: []string{goBin, "version"}, Required: true},
		{ID: "vuln", Command: []string{goBin, "help", core.Product + "-no-such-help-topic"}, Required: true},
	}
	def, err := json.Marshal(Definition{FormatVersion: formatVersion, Steps: steps})
	if err != nil {
		t.Fatalf("encode gate.json: %v", err)
	}
	repo := cacheTestRepo(t, map[string]string{"gate.json": string(def)})
	roots, err := core.UserRoots()
	if err != nil {
		t.Fatalf("UserRoots: %v", err)
	}
	head := cacheTestGit(t, repo, "", "rev-parse", "HEAD")
	tree := cacheTestGit(t, repo, "", "rev-parse", "HEAD^{tree}")
	if _, env, out := commandsTestRun(t, "--repo", repo); env.Data.Result != ResultRed {
		t.Fatalf("the gate result is %q, want red from the vuln step\n%s", env.Data.Result, out)
	}
	byStep := recordTestLedger(t, roots.State)
	if n := len(byStep); n != len(steps) {
		t.Fatalf("catches a gate that writes records for %d steps, want %d: %v", n, len(steps), byStep)
	}
	for step, exit := range map[string]int64{"format": 0, "vuln": 2} {
		c := recordTestOne(t, byStep, step)
		for _, row := range []struct {
			catches string
			ok      bool
		}{
			{"a record bound to no commit, or to another one", c.Commit == head},
			{"a record with no tree hash, or not the tree it tested", c.TreeHash.Known && c.TreeHash.Value == tree},
			{"a record with no toolchain", c.Toolchain.Known && strings.TrimSpace(c.Toolchain.Value) != ""},
			{"a record with no OS, or another one", c.OS.Known && c.OS.Value == runtime.GOOS+"/"+runtime.GOARCH},
			{"a record with no local duration", c.DurationMS.Known && c.DurationMS.Value >= 0},
			{"a record with no exit code, or another step's", c.ExitCode.Known && c.ExitCode.Value == exit},
		} {
			if !row.ok {
				t.Errorf("step %s: catches %s: %+v", step, row.catches, c)
			}
		}
	}
}

// TestRedRecordNamesItsTailFile pins the tail rule of SPEC-contracts §8.5: a red step whose output
// no parser understood writes its last 50 lines to a file that its record names, with the red
// class of the rule table, and a green step writes no file.
func TestRedRecordNamesItsTailFile(t *testing.T) {
	roots := commandsTestSetRoots(t)
	var lines []string
	for i := 1; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("step output line %02d", i))
	}
	lines = append(lines, "dial tcp 127.0.0.1:5432: connect: connection refused")
	red := recordTestStep(t, "probe", strings.Join(lines, "\n")+"\n", 1)
	green := GateStep{ID: "format", Command: []string{runnerTestGo(t), "version"}, Required: true}
	repo := commandsTestRepo(t, []GateStep{green, red})
	if _, env, out := commandsTestRun(t, "--repo", repo); env.Data.Result != ResultRed {
		t.Fatalf("the gate result is %q, want red from the probe step\n%s", env.Data.Result, out)
	}
	byStep := recordTestLedger(t, roots.state)
	redRec, greenRec := recordTestOne(t, byStep, "probe"), recordTestOne(t, byStep, "format")
	if !redRec.TailPath.Known {
		t.Fatalf("catches a red record that names no tail file: %+v", redRec)
	}
	data, err := os.ReadFile(redRec.TailPath.Value)
	if err != nil {
		t.Fatalf("catches a tail path that names no file: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	entries, err := os.ReadDir(filepath.Dir(redRec.TailPath.Value))
	if err != nil {
		t.Fatalf("read the tail directory: %v", err)
	}
	for _, row := range []struct {
		catches string
		ok      bool
	}{
		{"a tail that is not the last 50 lines of the output", slices.Equal(got, lines[len(lines)-50:])},
		{"a red record with no red class, or not the rule table's", redRec.RedClass.Known && redRec.RedClass.Value == string(RedEnvironment)},
		{"a green record that names a tail file", !greenRec.TailPath.Known},
		{"a green run that writes a tail file", len(entries) == 1},
	} {
		if !row.ok {
			t.Errorf("catches %s: red %+v, green %+v, %d tail files, tail %q", row.catches, redRec, greenRec, len(entries), got)
		}
	}
}

// recordTestCountsJSON is the go test -json stream of one package whose tests pass, fail and skip
// once each.
const recordTestCountsJSON = `{"Action":"run","Package":"` + recordTestPkg + `","Test":"TestSlow"}
{"Action":"pass","Package":"` + recordTestPkg + `","Test":"TestSlow","Elapsed":2.5}
{"Action":"run","Package":"` + recordTestPkg + `","Test":"TestFail"}
{"Action":"output","Package":"` + recordTestPkg + `","Test":"TestFail","Output":"    x_test.go:9: got 3, want 4\n"}
{"Action":"fail","Package":"` + recordTestPkg + `","Test":"TestFail","Elapsed":0.25}
{"Action":"run","Package":"` + recordTestPkg + `","Test":"TestSkip"}
{"Action":"skip","Package":"` + recordTestPkg + `","Test":"TestSkip","Elapsed":0}
{"Action":"fail","Package":"` + recordTestPkg + `","Elapsed":2.9}
`

// TestTestStepRecordsCounts pins the test-run fields of L67: the record of a test step counts its
// tests by result and keeps the slowest of them with their durations, slowest first.
func TestTestStepRecordsCounts(t *testing.T) {
	recs := recordTestRun(t, recordTestStep(t, testStepID, recordTestCountsJSON, 1))
	if len(recs) != 1 {
		t.Fatalf("catches a test step that writes %d records, want 1", len(recs))
	}
	c := recs[0].Check
	wantSlowest := []ledger.Timed{{ID: recordTestPkg + ".TestSlow", DurationMS: 2500}, {ID: recordTestPkg + ".TestFail", DurationMS: 250}}
	for _, row := range []struct {
		catches string
		ok      bool
	}{
		{"a test step with no counts, or counts that miss a result", c.Counts.Known &&
			len(c.Counts.Value) == 3 && c.Counts.Value["pass"] == 1 && c.Counts.Value["fail"] == 1 && c.Counts.Value["skip"] == 1},
		{"a test step with no slowest tests, or not slowest first", c.Slowest.Known && slices.Equal(c.Slowest.Value, wantSlowest)},
	} {
		if !row.ok {
			t.Errorf("catches %s: counts %+v, slowest %+v", row.catches, c.Counts, c.Slowest)
		}
	}
}

// recordTestFailJSON is the go test -json stream of one test that fails on an assertion.
const recordTestFailJSON = `{"Action":"run","Package":"` + recordTestPkg + `","Test":"TestX"}
{"Action":"output","Package":"` + recordTestPkg + `","Test":"TestX","Output":"=== RUN   TestX\n"}
{"Action":"output","Package":"` + recordTestPkg + `","Test":"TestX","Output":"    x_test.go:12: got 1, want 2\n"}
{"Action":"output","Package":"` + recordTestPkg + `","Test":"TestX","Output":"--- FAIL: TestX (0.01s)\n"}
{"Action":"fail","Package":"` + recordTestPkg + `","Test":"TestX","Elapsed":0.01}
{"Action":"fail","Package":"` + recordTestPkg + `","Elapsed":0.02}
`

// recordTestVetJSON is the go vet -json output of one printf finding.
const recordTestVetJSON = `{"` + recordTestPkg + `":{"printf":[{"posn":"internal/x/x.go:7:2","message":"fmt.Sprintf format %d has arg s of wrong type string"}]}}
`

// TestCheckRecordHoldsParsedFailures pins the failure record of L76: a red test step's record holds
// failures[] from the test parser with file:line, and a red lint step's record holds its static
// findings in the same entry shape; a parsed red run writes no tail.
func TestCheckRecordHoldsParsedFailures(t *testing.T) {
	cases := []struct {
		name, catches, step, output, wantLoc, wantKind string
	}{
		{"red test step", "a red test step whose record drops the parsed failure or its file:line",
			testStepID, recordTestFailJSON, "internal/x/x_test.go:12", FailureAssertion},
		{"red lint step", "a red lint step whose static finding is not in the record, or in another shape",
			"lint", recordTestVetJSON, "internal/x/x.go:7:2", "finding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs := recordTestRun(t, recordTestStep(t, tc.step, tc.output, 1))
			if len(recs) != 1 || len(recs[0].Failures) != 1 {
				t.Fatalf("catches %s: %+v", tc.catches, recs)
			}
			e := recs[0].Failures[0]
			if e.TestLocation != tc.wantLoc || e.Kind != tc.wantKind || e.Message == "" {
				t.Errorf("catches %s: entry %+v, want location %q and kind %q", tc.catches, e, tc.wantLoc, tc.wantKind)
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("encode the entry: %v", err)
			}
			var keys map[string]any
			if err := json.Unmarshal(data, &keys); err != nil {
				t.Fatalf("decode the entry: %v", err)
			}
			for _, k := range []string{"testLocation", "kind", "message"} {
				if _, ok := keys[k]; !ok {
					t.Errorf("catches an entry shape that misses %q: %s", k, data)
				}
			}
			if len(recs[0].Tail) != 0 {
				t.Errorf("catches a parsed red run that still keeps a tail: %d lines", len(recs[0].Tail))
			}
		})
	}
}
