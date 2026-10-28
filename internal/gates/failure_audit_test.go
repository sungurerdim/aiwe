package gates

import (
	"fmt"
	"strings"
	"testing"
)

const (
	pkgDep  = failureModule + "/internal/dep"
	pkgWeb  = failureModule + "/internal/web"
	xtestIm = failureModule + "/internal/calc_test [" + pkgCalc + ".test]"
)

// Jest report whose deepest frame is a library, then product code, then the test.
const junitLibraryDeepest = `<testsuite><testcase classname="app" name="app runs"><failure>Error: expect(received).toBe(expected)
    at deepLib (/repo/node_modules/dep/index.js:2:3)
    at app (/repo/src/app.js:4:5)
    at Object.&lt;anonymous&gt; (/repo/src/app.test.js:9:20)</failure></testcase></testsuite>`

func junitFailures(t *testing.T, xml string, scope RepoScope) []Failure {
	t.Helper()
	got, err := FailuresFromJUnit([]byte(xml), scope)
	if err != nil {
		t.Fatalf("FailuresFromJUnit: %v", err)
	}
	return got
}

func TestFailureReportThatReadsNothingIsAnError(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":     nil,
		"text only": []byte("FAIL\nsomething broke\n"),
		"no action": []byte("{\"Package\":\"p\"}\n"),
	} {
		if got, err := FailuresFromGoJSON(data, failureScope); err == nil {
			t.Errorf("go %s: want an error, got %+v", name, got)
		}
	}
	if got, err := FailuresFromJUnit([]byte("<html><body/></html>"), failureScope); err == nil {
		t.Errorf("a non-JUnit XML root must be an error, got %+v", got)
	}
	if got, err := FailuresFromPlaywright([]byte("{}"), failureScope); err == nil {
		t.Errorf("a JSON object without suites must be an error, got %+v", got)
	}
	// A readable report with no failure is a result, not an error.
	green := goJSON(t, fixEvent{Action: "pass", Package: pkgCalc})
	if got, err := FailuresFromGoJSON(green, failureScope); err != nil || len(got) != 0 {
		t.Errorf("green go stream = %+v, %v", got, err)
	}
	if got := junitFailures(t, `<testsuite><testcase name="a"/></testsuite>`, failureScope); len(got) != 0 {
		t.Errorf("green JUnit = %+v", got)
	}
	if got, err := FailuresFromPlaywright([]byte(`{"suites":[]}`), failureScope); err != nil || len(got) != 0 {
		t.Errorf("green Playwright = %+v, %v", got, err)
	}
}

func TestFailureBuildErrorGivesOneRecord(t *testing.T) {
	external := goJSON(t,
		fixEvent{Action: "build-output", ImportPath: xtestIm, Output: "internal/calc/x_test.go:4:2: undefined: nope\n"},
		fixEvent{Action: "build-fail", ImportPath: xtestIm},
		fixEvent{Action: "output", Package: pkgCalc, Output: "FAIL\t" + pkgCalc + " [build failed]\n"},
		fixEvent{Action: "fail", Package: pkgCalc, FailedBuild: xtestIm},
	)
	f := onlyFailure(t, goFailures(t, external, failureScope))
	if f.Kind != FailureCompile || f.Test != pkgCalc || f.TestLocation != "internal/calc/x_test.go:4:2" {
		t.Errorf("external test package build = %+v", f)
	}
	dependency := goJSON(t,
		fixEvent{Action: "build-output", ImportPath: pkgDep, Output: "internal/dep/dep.go:3:1: syntax error: unexpected }\n"},
		fixEvent{Action: "build-fail", ImportPath: pkgDep},
		fixEvent{Action: "fail", Package: pkgCalc, FailedBuild: pkgDep},
		fixEvent{Action: "fail", Package: pkgWeb, FailedBuild: pkgDep},
	)
	f = onlyFailure(t, goFailures(t, dependency, failureScope))
	if f.Kind != FailureCompile || f.Test != pkgDep {
		t.Errorf("a dependency that does not build must give its own record only: %+v", f)
	}
}

func TestFailureLogLineBeforeTheAssertionIsNotTheLocation(t *testing.T) {
	cases := map[string][]string{
		"with got and want": {"    rows_test.go:8: case 3 of 5\n", "    rows_test.go:9: Rows(3): got 2, want 3\n"},
		"without them":      {"    rows_test.go:8: case 3 of 5\n", "    rows_test.go:9: boom\n"},
	}
	for name, lines := range cases {
		evs := []fixEvent{{Action: "run", Package: pkgCalc, Test: "TestRows"}}
		for _, l := range lines {
			evs = append(evs, testOut("TestRows", l))
		}
		evs = append(evs, fixEvent{Action: "fail", Package: pkgCalc, Test: "TestRows"})
		f := onlyFailure(t, goFailures(t, goJSON(t, evs...), failureScope))
		if f.TestLocation != "internal/calc/rows_test.go:9" || strings.Contains(f.Message, "case 3") {
			t.Errorf("%s: location/message = %q/%q", name, f.TestLocation, f.Message)
		}
	}
}

func TestFailureParentWithItsOwnEvidenceKeepsItsRecord(t *testing.T) {
	data := goJSON(t,
		testOut("TestP/kid", "        p_test.go:9: got 1, want 2\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestP/kid"},
		testOut("TestP", "    p_test.go:15: parent check failed\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestP"},
	)
	if got := goFailures(t, data, failureScope); len(got) != 2 {
		t.Fatalf("want the child and the parent, got %+v", got)
	}
}

func TestFailureRecordHoldsPackageOutputDurationAndFrames(t *testing.T) {
	var b strings.Builder
	b.WriteString("panic: deep\n\ngoroutine 7 [running]:\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "%s.f%d(0xc000)\n\tD:/GitHub/aiwe/internal/calc/deep.go:%d +0x1c\n", pkgCalc, i, i)
	}
	evs := goStackEvents("TestDeep", b.String())
	evs = append(evs, testOut("TestOther", "    o_test.go:1: other-only\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestDeep", Elapsed: 0.25})
	f := onlyFailure(t, goFailures(t, goJSON(t, evs...), failureScope))
	if f.Package != pkgCalc || f.DurationMs != 250 {
		t.Errorf("package/duration = %q/%d", f.Package, f.DurationMs)
	}
	if len(f.Frames) != 10 || f.Frames[0].Location != "internal/calc/deep.go:1" || f.Frames[9].Location != "internal/calc/deep.go:10" {
		t.Errorf("want the 10 deepest repository frames, deepest first: %+v", f.Frames)
	}
	out := strings.Join(f.Output, "\n")
	if !strings.Contains(out, "panic: deep") || strings.Contains(out, "other-only") {
		t.Errorf("own output = %q", out)
	}
}

func TestFailureOwnOutputIsCappedAtTwoHundredLines(t *testing.T) {
	var evs []fixEvent
	for i := 0; i < 300; i++ {
		evs = append(evs, testOut("TestChatty", fmt.Sprintf("    chatty_test.go:3: line %d\n", i)))
	}
	evs = append(evs, testOut("TestOther", "    o_test.go:1: other-only\n"),
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestChatty"})
	f := onlyFailure(t, goFailures(t, goJSON(t, evs...), failureScope))
	if len(f.Output) != 200 {
		t.Fatalf("Output has %d lines, want the 200 nearest the failure", len(f.Output))
	}
	if !strings.Contains(f.Output[0], "line 100") || !strings.Contains(f.Output[199], "line 299") {
		t.Errorf("kept the wrong lines: %q ... %q", f.Output[0], f.Output[199])
	}
	if strings.Contains(strings.Join(f.Output, "\n"), "other-only") {
		t.Error("another test's output is in the record")
	}
}

func TestFailureDurationAndOutputComeFromEachReport(t *testing.T) {
	j := junitFailures(t, `<testsuite><testcase classname="c" name="n" time="1.5"><failure message="assert 1 == 2">E  assert 1 == 2</failure></testcase></testsuite>`, failureScope)
	if len(j) != 1 || j[0].DurationMs != 1500 || len(j[0].Output) != 1 {
		t.Errorf("junit = %+v", j)
	}
	const pw = `{"config":{},"suites":[{"title":"a.spec.ts","file":"a.spec.ts","specs":[{"title":"t","file":"a.spec.ts","line":3,"tests":[{"status":"unexpected","results":[{"status":"failed","duration":2000,"error":{"message":"Error: expect(x)"},"stdout":[{"text":"hello\n"}]}]}]}]}]}`
	p, err := FailuresFromPlaywright([]byte(pw), failureScope)
	if err != nil || len(p) != 1 || p[0].DurationMs != 2000 || len(p[0].Output) != 1 || p[0].Output[0] != "hello" {
		t.Errorf("playwright = %+v, %v", p, err)
	}
	if p[0].TestLocation != "" {
		t.Errorf("a spec file without the report rootDir is not a repository path: %q", p[0].TestLocation)
	}
}

func TestFailureLibraryFrameAtTheDeepestPositionIsRemoved(t *testing.T) {
	f := onlyFailure(t, junitFailures(t, junitLibraryDeepest, failureScope))
	if f.SourceLocation != "src/app.js:4" || f.TestLocation != "src/app.test.js:9" {
		t.Errorf("js locations = %q/%q", f.SourceLocation, f.TestLocation)
	}
	for _, fr := range f.Frames {
		if strings.Contains(fr.Location, "node_modules") {
			t.Errorf("a library frame is in the frame list: %+v", f.Frames)
		}
	}
	py := `<testsuite><testcase classname="tests.test_calc" name="test_total"><failure message="ValueError: bad">tests/test_calc.py:12: in test_total
src/calc.py:20: in total
.venv/lib/python3.12/site-packages/dep/core.py:3: in call
.venv/lib/python3.12/site-packages/dep/core.py:3: ValueError</failure></testcase></testsuite>`
	p := onlyFailure(t, junitFailures(t, py, failureScope))
	if p.SourceLocation != "src/calc.py:20" || p.Kind != "exception:ValueError" {
		t.Errorf("python source/kind = %q/%q", p.SourceLocation, p.Kind)
	}
}

func TestFailureAbsolutePathWithoutRootIsNotARepoFrame(t *testing.T) {
	f := onlyFailure(t, junitFailures(t, junitLibraryDeepest, RepoScope{}))
	if f.SourceLocation != "" || f.TestLocation != "" || len(f.Frames) != 0 {
		t.Errorf("without a Root an absolute path is no repository frame: %+v", f)
	}
}

func TestFailureDoesNotInventWhatTheReportOmits(t *testing.T) {
	e := onlyFailure(t, junitFailures(t, `<testsuite><testcase classname="c" name="n"><error message="boom"/></testcase></testsuite>`, failureScope))
	if e.Kind != "exception:unknown" {
		t.Errorf("an unknown exception type must not be invented: %q", e.Kind)
	}
	const pw = `{"suites":[{"title":"a.spec.ts","file":"a.spec.ts","specs":[{"title":"t","file":"a.spec.ts","line":3,"tests":[{"status":"unexpected","results":[{"status":"failed","error":{"message":"net down"}}]}]}]}]}`
	p, err := FailuresFromPlaywright([]byte(pw), failureScope)
	if err != nil || len(p) != 1 || p[0].Kind != "exception:unknown" {
		t.Errorf("playwright kind = %+v, %v", p, err)
	}
	one := onlyFailure(t, junitFailures(t, `<testsuite><testcase classname="c" name="n"><failure>Error: expect(x)
Expected: 4</failure></testcase></testsuite>`, failureScope))
	if !strings.Contains(one.Message, "want 4") || strings.Contains(one.Message, "got") {
		t.Errorf("a side that is missing must not be shown: %q", one.Message)
	}
}

func TestFailurePytestLineAttributeIsZeroBased(t *testing.T) {
	// pytest junitxml writes location[1] from reportinfo(), which counts lines from 0.
	f := onlyFailure(t, junitFailures(t, `<testsuite><testcase classname="tests.test_a" name="test_x" file="tests/test_a.py" line="11"><failure message="assert 1 == 2"/></testcase></testsuite>`, failureScope))
	if f.TestLocation != "tests/test_a.py:12" {
		t.Errorf("TestLocation = %q", f.TestLocation)
	}
}

func TestFailureESMFileURLFramesAreRead(t *testing.T) {
	esm := strings.ReplaceAll(junitLibraryDeepest, "(/repo/", "(file:///repo/")
	f := onlyFailure(t, junitFailures(t, esm, failureScope))
	if f.SourceLocation != "src/app.js:4" || f.TestLocation != "src/app.test.js:9" {
		t.Errorf("esm locations = %q/%q", f.SourceLocation, f.TestLocation)
	}
}

func TestFailureGoFullpathLocationStaysInTheRepo(t *testing.T) {
	cases := map[string]string{
		"    /repo/internal/calc/add_test.go:14: got 3, want 4\n": "internal/calc/add_test.go:14",
		"    C:/other/work/calc/add_test.go:14: got 3, want 4\n":  "internal/calc/add_test.go:14",
	}
	for line, want := range cases {
		data := goJSON(t, testOut("TestAdd", line), fixEvent{Action: "fail", Package: pkgCalc, Test: "TestAdd"})
		if f := onlyFailure(t, goFailures(t, data, failureScope)); f.TestLocation != want {
			t.Errorf("%q: TestLocation = %q, want %q", line, f.TestLocation, want)
		}
	}
}
