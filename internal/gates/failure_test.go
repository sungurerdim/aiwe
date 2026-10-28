package gates

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	failureModule = "github.com/sungurerdim/aiwe"
	pkgCalc       = failureModule + "/internal/calc"
	buildImport   = pkgCalc + " [" + pkgCalc + ".test]"
)

var failureScope = RepoScope{Module: failureModule, Root: "/repo"}

// fixEvent is one test2json event as `go test -json` writes it.
type fixEvent struct {
	Action      string  `json:"Action"`
	Package     string  `json:"Package,omitempty"`
	Test        string  `json:"Test,omitempty"`
	Output      string  `json:"Output,omitempty"`
	ImportPath  string  `json:"ImportPath,omitempty"`
	FailedBuild string  `json:"FailedBuild,omitempty"`
	Elapsed     float64 `json:"Elapsed,omitempty"`
}

// goJSON renders events as the line-per-event stream of `go test -json`.
func goJSON(tb testing.TB, evs ...fixEvent) []byte {
	tb.Helper()
	var b bytes.Buffer
	for _, e := range evs {
		line, err := json.Marshal(e)
		if err != nil {
			tb.Fatalf("marshal fixture event: %v", err)
		}
		b.Write(append(line, '\n'))
	}
	return b.Bytes()
}

// testOut is an output event of one test of pkgCalc.
func testOut(test, text string) fixEvent {
	return fixEvent{Action: "output", Package: pkgCalc, Test: test, Output: text}
}

// goStackEvents turns a panic or race text into one output event per line.
func goStackEvents(test, text string) []fixEvent {
	var evs []fixEvent
	for _, line := range strings.SplitAfter(text, "\n") {
		if line != "" {
			evs = append(evs, testOut(test, line))
		}
	}
	return evs
}

// goFailures parses a go test stream that must be readable.
func goFailures(t *testing.T, data []byte, scope RepoScope) []Failure {
	t.Helper()
	got, err := FailuresFromGoJSON(data, scope)
	if err != nil {
		t.Fatalf("FailuresFromGoJSON: %v", err)
	}
	return got
}

func onlyFailure(t *testing.T, got []Failure) Failure {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 failure, got %d: %+v", len(got), got)
	}
	return got[0]
}

func attachmentText(f Failure) string {
	var b strings.Builder
	for _, a := range f.Attachments {
		b.WriteString(a.Text + "\n" + a.Path + "\n")
	}
	return b.String()
}

func TestFailureFromGoJSONNamesFileLineGotWant(t *testing.T) {
	data := goJSON(t,
		fixEvent{Action: "start", Package: pkgCalc},
		fixEvent{Action: "run", Package: pkgCalc, Test: "TestAdd"},
		testOut("TestAdd", "=== RUN   TestAdd\n"),
		testOut("TestAdd", "    add_test.go:14: Add(1, 2): got 3, want 4\n"),
		testOut("TestAdd", "--- FAIL: TestAdd (0.00s)\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestAdd"},
		testOut("TestSub", "=== RUN   TestSub\n"),
		testOut("TestSub", "--- PASS: TestSub (0.00s)\n"),
		fixEvent{Action: "pass", Package: pkgCalc, Test: "TestSub"},
		fixEvent{Action: "output", Package: pkgCalc, Output: "FAIL\n"},
		fixEvent{Action: "output", Package: pkgCalc, Output: "FAIL\t" + pkgCalc + "\t0.003s\n"},
		fixEvent{Action: "fail", Package: pkgCalc},
	)
	f := onlyFailure(t, goFailures(t, data, failureScope))
	if f.Test != pkgCalc+".TestAdd" || f.Kind != FailureAssertion {
		t.Fatalf("test/kind = %q/%q", f.Test, f.Kind)
	}
	if f.TestLocation != "internal/calc/add_test.go:14" {
		t.Fatalf("TestLocation = %q", f.TestLocation)
	}
	if f.Got != "3" || f.Want != "4" || !strings.Contains(f.Message, "got 3, want 4") {
		t.Fatalf("got/want/message = %q/%q/%q", f.Got, f.Want, f.Message)
	}
	if f.Rerun != "aiwe check verify --test "+pkgCalc+".TestAdd" {
		t.Fatalf("Rerun = %q", f.Rerun)
	}
}

func TestFailureKeepsOnlyTheTestsOwnOutput(t *testing.T) {
	data := goJSON(t,
		testOut("TestA", "=== RUN   TestA\n"),
		testOut("TestA", "=== PAUSE TestA\n"),
		testOut("TestB", "=== RUN   TestB\n"),
		testOut("TestB", "=== PAUSE TestB\n"),
		testOut("TestC", "=== RUN   TestC\n"),
		testOut("TestA", "=== CONT  TestA\n"),
		testOut("TestB", "=== CONT  TestB\n"),
		testOut("TestB", "    b_test.go:20: b-only crash\n"),
		testOut("TestC", "    c_test.go:5: c-noise from a passing sibling\n"),
		testOut("TestA", "    a_test.go:10: a-only boom\n"),
		testOut("TestA", "--- FAIL: TestA (0.00s)\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestA"},
		testOut("TestB", "--- FAIL: TestB (0.00s)\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestB"},
		fixEvent{Action: "pass", Package: pkgCalc, Test: "TestC"},
		fixEvent{Action: "fail", Package: pkgCalc},
	)
	got := goFailures(t, data, failureScope)
	if len(got) != 2 {
		t.Fatalf("want 2 failures, got %+v", got)
	}
	for _, f := range got {
		all := f.Message + "\n" + attachmentText(f)
		own, other := "a-only", "b-only"
		if strings.HasSuffix(f.Test, ".TestB") {
			own, other = other, own
		}
		if !strings.Contains(all, own) {
			t.Errorf("%s lost its own output %q: %q", f.Test, own, all)
		}
		if strings.Contains(all, other) || strings.Contains(all, "c-noise") {
			t.Errorf("%s holds a sibling's output: %q", f.Test, all)
		}
	}
}

func TestFailureSubtestReportsChildOnly(t *testing.T) {
	data := goJSON(t,
		testOut("TestTable", "=== RUN   TestTable\n"),
		testOut("TestTable/neg", "=== RUN   TestTable/neg\n"),
		testOut("TestTable/neg", "        table_test.go:9: got -1, want 1\n"),
		testOut("TestTable/neg", "    --- FAIL: TestTable/neg (0.00s)\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestTable/neg"},
		testOut("TestTable", "--- FAIL: TestTable (0.00s)\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestTable"},
		fixEvent{Action: "fail", Package: pkgCalc},
	)
	f := onlyFailure(t, goFailures(t, data, failureScope))
	if f.Test != pkgCalc+".TestTable/neg" || f.Got != "-1" || f.Want != "1" {
		t.Fatalf("failure = %+v", f)
	}
}

const goPanicText = `=== RUN   TestCompute
panic: runtime error: index out of range [5] with length 3 [recovered]
	panic: runtime error: index out of range [5] with length 3

goroutine 7 [running]:
testing.tRunner.func1.2({0x7ff6a1, 0xc00001})
	C:/Program Files/Go/src/testing/testing.go:1631 +0x24a
testing.tRunner.func1()
	C:/Program Files/Go/src/testing/testing.go:1634 +0x3b6
panic({0x7ff6a1, 0xc00001})
	C:/Program Files/Go/src/runtime/panic.go:787 +0x132
github.com/sungurerdim/aiwe/internal/calc.pick(...)
	D:/GitHub/aiwe/internal/calc/calc.go:42
github.com/sungurerdim/aiwe/internal/calc.Compute(0xc00002)
	D:/GitHub/aiwe/internal/calc/calc.go:58 +0x1c
github.com/sungurerdim/aiwe/internal/calc.TestCompute(0xc000)
	D:/GitHub/aiwe/internal/calc/calc_test.go:20 +0x65
testing.tRunner(0xc000, 0x7ff6b2)
	C:/Program Files/Go/src/testing/testing.go:1689 +0xfb
created by testing.(*T).Run in goroutine 1
	C:/Program Files/Go/src/testing/testing.go:1742 +0x390
`

func goPanicJSON(tb testing.TB, test, text string) []byte {
	evs := goStackEvents(test, text)
	evs = append(evs, fixEvent{Action: "fail", Package: pkgCalc, Test: test},
		fixEvent{Action: "fail", Package: pkgCalc})
	return goJSON(tb, evs...)
}

func TestPanicFailureKeepsRepoFramesOnly(t *testing.T) {
	f := onlyFailure(t, goFailures(t, goPanicJSON(t, "TestCompute", goPanicText), failureScope))
	if f.Kind != FailurePanic {
		t.Fatalf("Kind = %q", f.Kind)
	}
	if !strings.HasPrefix(f.Message, "panic: runtime error: index out of range [5] with length 3") {
		t.Fatalf("Message = %q", f.Message)
	}
	if f.SourceLocation != "internal/calc/calc.go:42" || f.TestLocation != "internal/calc/calc_test.go:20" {
		t.Fatalf("source/test location = %q/%q", f.SourceLocation, f.TestLocation)
	}
	log := attachmentText(f)
	if !strings.Contains(log, "internal/calc/calc.go:58") {
		t.Errorf("repository frame missing from %q", log)
	}
	for _, lib := range []string{"runtime/panic.go", "testing.go", "tRunner"} {
		if strings.Contains(log+f.Message, lib) {
			t.Errorf("library frame %q kept: %q", lib, log)
		}
	}
}

func TestCompileErrorNamesFileLineCol(t *testing.T) {
	built := goJSON(t,
		fixEvent{Action: "build-output", ImportPath: buildImport, Output: "# " + buildImport + "\n"},
		fixEvent{Action: "build-output", ImportPath: buildImport, Output: "internal/calc/calc.go:7:9: undefined: missing\n"},
		fixEvent{Action: "build-output", ImportPath: buildImport, Output: "internal/calc/calc.go:9:2: declared and not used: x\n"},
		fixEvent{Action: "build-fail", ImportPath: buildImport},
		fixEvent{Action: "output", Package: pkgCalc, Output: "FAIL\t" + pkgCalc + " [build failed]\n"},
		fixEvent{Action: "fail", Package: pkgCalc, FailedBuild: buildImport},
	)
	old := goJSON(t,
		fixEvent{Action: "output", Package: pkgCalc, Output: "# " + pkgCalc + "\n"},
		fixEvent{Action: "output", Package: pkgCalc, Output: "./calc.go:7:9: undefined: missing\n"},
		fixEvent{Action: "output", Package: pkgCalc, Output: "FAIL\t" + pkgCalc + " [build failed]\n"},
		fixEvent{Action: "fail", Package: pkgCalc, FailedBuild: pkgCalc},
	)
	for name, data := range map[string][]byte{"build events": built, "package output": old} {
		f := onlyFailure(t, goFailures(t, data, failureScope))
		if f.Kind != FailureCompile || f.Test != pkgCalc {
			t.Errorf("%s: kind/test = %q/%q", name, f.Kind, f.Test)
		}
		if f.TestLocation != "internal/calc/calc.go:7:9" || f.Message != "undefined: missing" {
			t.Errorf("%s: location/message = %q/%q", name, f.TestLocation, f.Message)
		}
	}
}

const junitPytest = `<?xml version="1.0" encoding="utf-8"?>
<testsuites name="pytest tests"><testsuite name="pytest" errors="1" failures="1" skipped="1" tests="4">
<testcase classname="tests.test_calc" name="test_ok" time="0.001"/>
<testcase classname="tests.test_calc" name="test_add" time="0.002"><failure message="assert 3 == 4">def test_add():
&gt;       assert add(1, 2) == 4
E       assert 3 == 4
E        +  where 3 = add(1, 2)

tests/test_calc.py:12: AssertionError</failure></testcase>
<testcase classname="tests.test_calc" name="test_skip"><skipped type="pytest.skip" message="later"/></testcase>
<testcase classname="" name="tests/test_bad.py"><error message="collection failure">ImportError while importing test module '/repo/tests/test_bad.py'.
E   ModuleNotFoundError: No module named 'nothere'</error></testcase>
</testsuite></testsuites>`

const junitJest = `<testsuites><testsuite name="math" tests="1" failures="1">
<testcase classname="math adds" name="math adds" time="0.01"><failure>Error: expect(received).toBe(expected) // Object.is equality

Expected: 4
Received: 3
    at add (/repo/src/math.js:5:11)
    at Object.&lt;anonymous&gt; (/repo/src/math.test.js:9:20)
    at run (/repo/node_modules/jest-circus/build/run.js:100:5)</failure></testcase>
</testsuite></testsuites>`

func TestFailureFromJUnitXML(t *testing.T) {
	got, err := FailuresFromJUnit([]byte(junitPytest), failureScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 failures, got %+v", got)
	}
	a, b := got[0], got[1]
	if a.Test != "tests.test_calc.test_add" || a.Kind != FailureAssertion || a.TestLocation != "tests/test_calc.py:12" {
		t.Errorf("assertion = %+v", a)
	}
	if a.Got != "3" || a.Want != "4" || !strings.Contains(a.Message, "assert 3 == 4") {
		t.Errorf("got/want/message = %q/%q/%q", a.Got, a.Want, a.Message)
	}
	if b.Test != "tests/test_bad.py" || b.Kind != FailureImport {
		t.Errorf("import error = %+v", b)
	}
	js, err := FailuresFromJUnit([]byte(junitJest), failureScope)
	if err != nil {
		t.Fatal(err)
	}
	j := onlyFailure(t, js)
	if j.Test != "math adds" || j.Kind != FailureAssertion || j.Got != "3" || j.Want != "4" {
		t.Errorf("jest failure = %+v", j)
	}
	if j.TestLocation != "src/math.test.js:9" || j.SourceLocation != "src/math.js:5" {
		t.Errorf("jest locations = %q/%q", j.TestLocation, j.SourceLocation)
	}
	if _, err := FailuresFromJUnit([]byte("<testsuite><testcase"), failureScope); err == nil {
		t.Error("a truncated report must be an error, not an empty result")
	}
}

func TestFailureSourceLocationIsTheDeepestRepoFrame(t *testing.T) {
	goStack := `panic: boom

goroutine 7 [running]:
github.com/sungurerdim/aiwe/internal/calc.mul(...)
	D:/GitHub/aiwe/internal/calc/calc.go:30
github.com/other/lib.Do(0xc000)
	C:/Users/me/go/pkg/mod/github.com/other/lib@v1.2.0/do.go:9 +0x20
github.com/sungurerdim/aiwe/internal/calc.Compute(0xc000)
	D:/GitHub/aiwe/internal/calc/calc.go:50 +0x1c
github.com/sungurerdim/aiwe/internal/calc.helper(0xc000)
	D:/GitHub/aiwe/internal/calc/calc_test.go:8 +0x1c
github.com/sungurerdim/aiwe/internal/calc.TestX(0xc000)
	D:/GitHub/aiwe/internal/calc/calc_test.go:20 +0x65
`
	f := onlyFailure(t, goFailures(t, goPanicJSON(t, "TestX", goStack), failureScope))
	if f.SourceLocation != "internal/calc/calc.go:30" || f.TestLocation != "internal/calc/calc_test.go:8" {
		t.Errorf("go locations = %q/%q", f.SourceLocation, f.TestLocation)
	}
	py := `<testsuite><testcase classname="tests.test_calc" name="test_total"><failure message="IndexError: list index out of range">tests/test_calc.py:12: in test_total
    assert total([1]) == 2
src/calc.py:20: in total
    return helper(xs)
.venv/lib/python3.12/site-packages/pluggy/_callers.py:10: in call
    res = hook()
src/util.py:7: in helper
    return xs[1]
src/util.py:7: IndexError</failure></testcase></testsuite>`
	got, err := FailuresFromJUnit([]byte(py), failureScope)
	if err != nil {
		t.Fatal(err)
	}
	p := onlyFailure(t, got)
	if p.SourceLocation != "src/util.py:7" || p.TestLocation != "tests/test_calc.py:12" || p.Kind != "exception:IndexError" {
		t.Errorf("python failure = %+v", p)
	}
	other := RepoScope{Module: "github.com/someone/else"}
	g := onlyFailure(t, goFailures(t, goPanicJSON(t, "TestX", goStack), other))
	if g.SourceLocation != "" || g.TestLocation != "" {
		t.Errorf("frames of another module must not count: %q/%q", g.SourceLocation, g.TestLocation)
	}
}

func TestFailureRerunRunsOnlyThisTest(t *testing.T) {
	want := func(id string) []string { return []string{"aiwe", "check", "verify", "--test", id} }
	for _, id := range []string{"pkg.TestA", "a b", "it's \"x\"", "x; rm -rf /", "--all", "login.spec > one"} {
		if got := RerunCommand(id); !slices.Equal(got, want(id)) {
			t.Errorf("RerunCommand(%q) = %q, want %q", id, got, want(id))
		}
	}
	f := onlyFailure(t, goFailures(t, goPanicJSON(t, "TestCompute", goPanicText), failureScope))
	if !slices.Equal(f.Rerun, want(f.Test)) {
		t.Errorf("Rerun = %q for %q", f.Rerun, f.Test)
	}
	pw, err := FailuresFromPlaywright([]byte(playwrightReport), failureScope)
	if err != nil || len(pw) == 0 {
		t.Fatalf("playwright fixture: %v %v", pw, err)
	}
	if !slices.Equal(pw[0].Rerun, want("login.spec.ts > shows dashboard [chromium]")) {
		t.Errorf("an id with spaces and brackets must stay one argument: %q", pw[0].Rerun)
	}
}

const playwrightReport = `{"config":{"rootDir":"/repo/tests"},"suites":[{"title":"login.spec.ts","file":"login.spec.ts","specs":[
{"title":"shows dashboard","ok":false,"file":"login.spec.ts","line":10,"column":5,"tests":[{"projectName":"chromium","status":"unexpected","results":[
{"status":"failed","error":{"message":"\u001b[31mError: \u001b[39mexpect(locator).toBeVisible()\n\nLocator: getByText('Dashboard')\nExpected: visible\nReceived: hidden","stack":"Error: expect(locator).toBeVisible()\n    at /repo/tests/login.spec.ts:14:42","location":{"file":"/repo/tests/login.spec.ts","line":14,"column":42}},
"attachments":[{"name":"screenshot","contentType":"image/png","path":"/repo/test-results/login/test-failed-1.png"},{"name":"trace","contentType":"application/zip","path":"/repo/test-results/login/trace.zip"},{"name":"note","contentType":"text/plain","body":"aGk="}]}]}]},
{"title":"logs out","ok":true,"file":"login.spec.ts","line":30,"column":5,"tests":[{"projectName":"chromium","status":"expected","results":[{"status":"passed"}]}]},
{"title":"slow page","ok":false,"file":"login.spec.ts","line":40,"column":5,"tests":[{"projectName":"chromium","status":"unexpected","results":[{"status":"timedOut","error":{"message":"Test timeout of 30000ms exceeded."},"attachments":[]}]}]}
]}]}`

func TestFailureFromPlaywrightJSONKeepsAttachments(t *testing.T) {
	got, err := FailuresFromPlaywright([]byte(playwrightReport), failureScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 failures (the passing test is absent), got %+v", got)
	}
	f := got[0]
	if f.Test != "login.spec.ts > shows dashboard [chromium]" || f.Kind != FailureAssertion {
		t.Errorf("test/kind = %q/%q", f.Test, f.Kind)
	}
	if f.TestLocation != "tests/login.spec.ts:14" {
		t.Errorf("TestLocation = %q (error.location, repo relative)", f.TestLocation)
	}
	if f.Got != "hidden" || f.Want != "visible" || strings.Contains(f.Message, "\x1b") ||
		!strings.HasPrefix(f.Message, "Error: expect(locator).toBeVisible()") {
		t.Errorf("message/got/want = %q/%q/%q", f.Message, f.Got, f.Want)
	}
	want := []Attachment{
		{Kind: "screenshot", Path: "/repo/test-results/login/test-failed-1.png"},
		{Kind: "trace", Path: "/repo/test-results/login/trace.zip"},
	}
	if !slices.Equal(f.Attachments, want) {
		t.Errorf("Attachments = %+v, want %+v", f.Attachments, want)
	}
	if got[1].Kind != FailureTimeout || got[1].TestLocation != "tests/login.spec.ts:40" {
		t.Errorf("timed out test = %+v", got[1])
	}
	if _, err := FailuresFromPlaywright([]byte(`{"suites":`), failureScope); err == nil {
		t.Error("a truncated report must be an error")
	}
}

func TestFailureMessageIsShortened(t *testing.T) {
	long := strings.Repeat("é", 1000)
	data := goJSON(t,
		testOut("TestLong", "    long_test.go:3: "+long+": got "+long+", want "+long+"\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestLong"},
	)
	f := onlyFailure(t, goFailures(t, data, failureScope))
	checkFailures(t, []Failure{f})
	if utf8.RuneCountInString(f.Got) > failureValueMax || utf8.RuneCountInString(f.Want) > failureValueMax {
		t.Errorf("got/want not shortened: %d/%d runes", utf8.RuneCountInString(f.Got), utf8.RuneCountInString(f.Want))
	}
}

// checkFailures holds the invariants every parsed record keeps whatever the input.
func checkFailures(t *testing.T, fs []Failure) {
	t.Helper()
	for _, f := range fs {
		if f.Test == "" || f.Kind == "" {
			t.Fatalf("record without test or kind: %+v", f)
		}
		if !slices.Equal(f.Rerun, RerunCommand(f.Test)) {
			t.Fatalf("Rerun %q does not run only %q", f.Rerun, f.Test)
		}
		if len(f.Output) > 200 || len(f.Frames) > 10 {
			t.Fatalf("output %d lines, frames %d: over the caps", len(f.Output), len(f.Frames))
		}
		if !utf8.ValidString(f.Message) || utf8.RuneCountInString(f.Message) > failureMessageMax {
			t.Fatalf("message not valid or not shortened: %q", f.Message)
		}
	}
}

// FuzzFailuresFromGoJSON catches a reader of the untrusted test2json stream that panics or builds
// a record that breaks the invariants.
func FuzzFailuresFromGoJSON(f *testing.F) {
	f.Add(goPanicJSON(f, "TestCompute", goPanicText))
	f.Add(goJSON(f, testOut("TestAdd", "    add_test.go:14: got 3, want 4\n"), fixEvent{Action: "fail", Package: pkgCalc, Test: "TestAdd"}))
	f.Add(goJSON(f, fixEvent{Action: "build-output", ImportPath: buildImport, Output: "a.go:1:2: x\n"}, fixEvent{Action: "build-fail", ImportPath: buildImport}))
	f.Add([]byte("{\"Action\":\"fail\"}\nnot json\n{"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if got, err := FailuresFromGoJSON(data, failureScope); err == nil {
			checkFailures(t, got)
		}
	})
}

// FuzzFailuresFromJUnit catches a reader of the untrusted JUnit XML that panics or builds a record
// that breaks the invariants.
func FuzzFailuresFromJUnit(f *testing.F) {
	f.Add([]byte(junitPytest))
	f.Add([]byte(junitJest))
	f.Add([]byte(`<testcase name="a"><failure/></testcase>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := FailuresFromJUnit(data, failureScope)
		if err == nil {
			checkFailures(t, got)
		}
	})
}

// FuzzFailuresFromPlaywright catches a reader of the untrusted Playwright JSON that panics or
// builds a record that breaks the invariants.
func FuzzFailuresFromPlaywright(f *testing.F) {
	f.Add([]byte(playwrightReport))
	f.Add([]byte(`{"suites":[{"specs":[{"tests":[{"status":"unexpected","results":[{}]}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := FailuresFromPlaywright(data, failureScope)
		if err == nil {
			checkFailures(t, got)
		}
	})
}
