package gates

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzTestCounts catches a reader of the untrusted `go test -json` stream that panics or builds
// counts that break the invariant: a count is never negative, the slowest list holds at most
// slowestTests tests with no negative duration, and the two fields are known together.
func FuzzTestCounts(f *testing.F) {
	f.Add(goJSON(f,
		fixEvent{Action: "start", Package: pkgCalc},
		fixEvent{Action: "run", Package: pkgCalc, Test: "TestAdd"},
		testOut("TestAdd", "=== RUN   TestAdd\n"),
		fixEvent{Action: "pass", Package: pkgCalc, Test: "TestAdd", Elapsed: 0.25},
		fixEvent{Action: "run", Package: pkgCalc, Test: "TestSub"},
		fixEvent{Action: "fail", Package: pkgCalc, Test: "TestSub", Elapsed: 1.5},
		fixEvent{Action: "skip", Package: pkgCalc, Test: "TestMul"},
		fixEvent{Action: "fail", Package: pkgCalc, Elapsed: 1.8},
	))
	f.Add(goJSON(f,
		fixEvent{Action: "build-output", ImportPath: buildImport, Output: "a.go:1:2: x\n"},
		fixEvent{Action: "build-fail", ImportPath: buildImport},
		fixEvent{Action: "fail", Package: pkgCalc, FailedBuild: buildImport},
	))
	f.Add([]byte("{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"T\",\"Elapsed\":-1}\nnot json\n{\"Action\":\"pass\",\"Test\":"))
	f.Add(passStream(f, slowestTests+1))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		counts, slowest := testCounts(data)
		if counts.Known != slowest.Known {
			t.Fatalf("counts known %v but slowest known %v", counts.Known, slowest.Known)
		}
		for result, n := range counts.Value {
			if n < 0 {
				t.Fatalf("count of %q = %d, want >= 0", result, n)
			}
		}
		if len(slowest.Value) > slowestTests {
			t.Fatalf("slowest holds %d tests, bound is %d", len(slowest.Value), slowestTests)
		}
		for _, tm := range slowest.Value {
			if tm.DurationMS < 0 {
				t.Fatalf("slowest test %q has duration %d, want >= 0", tm.ID, tm.DurationMS)
			}
		}
	})
}

// FuzzGoModulePath catches a reader of go.mod that panics or returns a path that breaks the
// invariant: the path holds no quote, no backtick, no line break and no surrounding space.
func FuzzGoModulePath(f *testing.F) {
	f.Add([]byte("module github.com/acme/calc\n\ngo 1.22\n\nrequire golang.org/x/text v0.14.0\n"))
	f.Add([]byte("// calc module\r\nmodule \"github.com/acme/calc\"\r\n\r\ngo 1.22\r\n"))
	f.Add([]byte("go 1.22\n\nrequire golang.org/x/text v0.14.0\n"))
	f.Add([]byte("module\nmodule \t example.com/m // comment\n"))
	f.Add([]byte("module a\"b\n"))
	f.Add([]byte("module \"\\\"a\"\n"))
	f.Add([]byte("module `x`\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		got := goModulePath(data)
		if strings.ContainsAny(got, "\"`\r\n") {
			t.Fatalf("module path %q holds a quote, a backtick or a line break", got)
		}
		if strings.TrimSpace(got) != got {
			t.Fatalf("module path %q has surrounding space", got)
		}
	})
}

// TestGoModulePath pins the module path of a bare token, a Go string literal and text that is no path.
func TestGoModulePath(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare", "module github.com/acme/calc\n", "github.com/acme/calc"},
		{"quoted", "module \"github.com/acme/calc\"\r\n", "github.com/acme/calc"},
		{"backtick", "module `github.com/acme/calc`\n", "github.com/acme/calc"},
		{"missing", "go 1.22\n", ""},
		{"inner quote", "module a\"b\n", ""},
		{"escaped quote", "module \"\\\"a\"\n", ""},
		{"backtick only", "module `x\n", ""},
		{"inner backtick", "module a`b\n", ""},
		{"escaped line break", "module \"a\\nb\"\n", ""},
		{"unterminated", "module \"a\n", ""},
	}
	for _, c := range cases {
		if got := goModulePath([]byte(c.in)); got != c.want {
			t.Errorf("%s: goModulePath(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// passStream is a test2json stream of n passed tests, test i taking i+1 seconds.
func passStream(tb testing.TB, n int) []byte {
	tb.Helper()
	evs := make([]fixEvent, n)
	for i := range evs {
		evs[i] = fixEvent{Action: "pass", Package: pkgCalc, Test: fmt.Sprintf("Test%02d", i), Elapsed: float64(i + 1)}
	}
	return goJSON(tb, evs...)
}

// TestTestCountsSlowestCap pins that a stream of more timed tests than the bound keeps exactly the
// bound, the slowest first.
func TestTestCountsSlowestCap(t *testing.T) {
	n := slowestTests + 1
	counts, slowest := testCounts(passStream(t, n))
	if got := counts.Value["pass"]; got != int64(n) {
		t.Fatalf("pass count = %d, want %d", got, n)
	}
	if len(slowest.Value) != slowestTests {
		t.Fatalf("slowest holds %d tests, want %d", len(slowest.Value), slowestTests)
	}
	for i, tm := range slowest.Value {
		wantMS := int64(n-i) * 1000
		wantID := fmt.Sprintf("%s.Test%02d", pkgCalc, n-1-i)
		if tm.DurationMS != wantMS || tm.ID != wantID {
			t.Errorf("slowest[%d] = %q %d ms, want %q %d ms", i, tm.ID, tm.DurationMS, wantID, wantMS)
		}
	}
}
