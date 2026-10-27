package gates

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestRedClassRuleTable pins the rule table of SPEC decision L64: each baseline red signature
// (docs/evidence/2026-10-27-test-baseline.md section 4) falls in its class and subclass, and a
// signature that no rule matches is unknown. Rows are ordered like the table: the first rule
// that matches a line decides.
func TestRedClassRuleTable(t *testing.T) {
	tests := []struct {
		name string
		exit int
		out  []string
		want string
	}{
		{"deadcode finding of the gate", 1, []string{"adapters/all.go:10 unreachable func: All"}, "real_bug.finding"},
		{"linter finding ends with its rule name", 1, []string{"core/x.go:12:5: Error return value is not checked (errcheck)"}, "real_bug.finding"},
		{"go test failure line", 1, []string{"=== RUN   TestOutcomes", "--- FAIL: TestOutcomes/outcome_string_used_in_code_missing (0.00s)", "FAIL"}, "real_bug.assertion"},
		{"go test failure on a subtest", 1, []string{"--- FAIL: TestRepoIONotRegular/file_at_the_cap_refused (0.01s)"}, "real_bug.assertion"},
		{"pytest assertion", 1, []string{"E   AssertionError: assert 1 == 2"}, "real_bug.assertion"},
		{"pytest summary line", 1, []string{"FAILED tests/test_x.py::test_y - assert 0"}, "real_bug.assertion"},
		{"go panic", 2, []string{"panic: runtime error: index out of range [3] with length 3", "--- FAIL: TestX"}, "real_bug.panic"},
		{"go test timeout is not a plain panic", 2, []string{"panic: test timed out after 10m0s", "running tests:"}, "real_bug.timeout"},
		{"data race", 66, []string{"WARNING: DATA RACE", "Write at 0x00c000 by goroutine 7:"}, "real_bug.race"},
		{"compile error names file line col", 1, []string{"core/x.go:14:9: undefined: Frobnicate", "FAIL\tgithub.com/x/core [build failed]"}, "real_bug.compile"},
		{"syntax error is a compile error", 1, []string{"core/x.go:3:1: syntax error: unexpected }"}, "real_bug.compile"},
		{"build failed with no compiler line", 1, []string{"FAIL\tgithub.com/x/core [build failed]"}, "real_bug.build"},
		{"setup failed", 1, []string{"FAIL\tgithub.com/x/core [setup failed]"}, "real_bug.setup"},
		{"pytest collection error", 2, []string{"ERROR collecting tests/test_x.py"}, "real_bug.setup"},
		{"no tests ran", 1, []string{"testing: warning: no tests to run"}, "real_bug.no_tests"},
		{"pytest collected nothing", 5, []string{"collected 0 items"}, "real_bug.no_tests"},
		{"wrong working directory", 1, []string{"go: go.mod file not found in current directory or any parent directory; see 'go help modules'"}, "environment.workdir"},
		{"custom linter without its config", 3, []string{"custom-gcl: configuration file not found"}, "environment.workdir"},
		{"windows ACL read", 1, []string{"read the ACL of C" + `:\Users\RUNNER~1\AppData\Local\Temp\x: exit status 1`}, "environment.permission"},
		{"permission denied", 1, []string{"open /var/x: permission denied"}, "environment.permission"},
		{"dns failure", 1, []string{"dial tcp: lookup proxy.golang.org: no such host"}, "environment.network"},
		{"connection refused", 1, []string{"dial tcp 127.0.0.1:5432: connect: connection refused"}, "environment.network"},
		{"missing tool by name", 1, []string{`exec: "golangci-lint": executable file not found in $PATH`}, "environment.missing_tool"},
		{"shell exit 127 with no log", 127, nil, "environment.missing_tool"},
		{"disk full", 1, []string{"write /tmp/x: no space left on device"}, "environment.disk"},
		{"lock held", 1, []string{"fatal: Unable to create '/r/.git/index.lock': File exists."}, "environment.lock_wait"},
		{"database absent", 1, []string{`pq: database "t_3" does not exist`}, "environment.db-missing"},
		{"container daemon absent", 1, []string{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock."}, "environment.container-missing"},
		{"port busy", 1, []string{"listen tcp :8080: bind: address already in use"}, "environment.port-busy"},
		{"out of memory", 137, []string{"fatal error: runtime: out of memory"}, "environment.memory"},
		{"declared variable unset", 1, []string{"environment variable DATABASE_URL is not set"}, "environment.missing-env:DATABASE_URL"},
		{"stale build cache", 1, []string{"go: build cache is corrupt: stale object"}, "stale.build_cache"},
		{"stale generated file", 1, []string{"generated files are out of date; run go generate"}, "stale.generated"},
		{"toolchain below the pin", 1, []string{"go: go.mod requires go >= 1.27.3 (running go 1.26.1)"}, "stale.toolchain"},
		{"build output missing", 1, []string{"Error: Could not find a production build in the '.next' directory."}, "stale.build-output"},
		{"stale pyc", 1, []string{"RuntimeError: bad magic number in 'x'"}, "stale.cache"},
		{"environment wins over a later assertion", 1, []string{"dial tcp: lookup x: no such host", "--- FAIL: TestFetch (0.00s)"}, "environment.network"},
		{"toolchain pin beats a later setup failure", 1, []string{"go: go.mod requires go >= 1.27.3 (running go 1.26.1)", "FAIL\tgithub.com/x/core [setup failed]"}, "stale.toolchain"},
		{"stale build cache beats a later build failure", 1, []string{"go: build cache is corrupt: stale object", "FAIL\tgithub.com/x/core [build failed]"}, "stale.build_cache"},
		{"permission beats a later assertion", 1, []string{"open /var/x: permission denied", "--- FAIL: TestWrite (0.00s)"}, "environment.permission"},
		{"disk full beats a later panic", 2, []string{"write /tmp/x: no space left on device", "panic: write failed"}, "environment.disk"},
		{"a name before the phrase is not a variable name", 1, []string{"HOME environment variable is not set"}, "unknown"},
		{"a lower-case word after the phrase is not a variable name", 1, []string{"the environment variable is required"}, "unknown"},
		{"signature inside the 4096-byte scan limit", 1, []string{strings.Repeat("x", 4000) + " no space left on device"}, "environment.disk"},
		{"signature past the 4096-byte scan limit is not read", 1, []string{strings.Repeat("x", 5000) + " no space left on device"}, "unknown"},
		{"green summary has no rule", 1, []string{"ok  \tgithub.com/x/core\t0.012s"}, "unknown"},
		{"repocheck finding has no rule", 1, []string{"missing_repo_file ci.yml", "exit status 1"}, "unknown"},
		{"no output at all", 1, nil, "unknown"},
		{"test source quoted in a log is not a signature", 1, []string{"timeout := 5 * time.Second", "// (cached) result"}, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyRed(RedRun{Step: "test", Exit: tc.exit, Output: tc.out})
			if got.Subclass != tc.want {
				t.Fatalf("%s: subclass = %q (line %q), want %q", tc.name, got.Subclass, got.Line, tc.want)
			}
			wantClass := RedClass(strings.SplitN(tc.want, ".", 2)[0])
			if tc.want == "unknown" {
				wantClass = RedUnknown
			}
			if got.Class != wantClass {
				t.Fatalf("%s: class = %q, want %q", tc.name, got.Class, wantClass)
			}
			if got.Step != "test" {
				t.Fatalf("%s: step = %q, want the failing step id echoed", tc.name, got.Step)
			}
		})
	}
}

// TestRedSubclassNamesTheMatchedLine pins that a verdict carries the output line that matched,
// trimmed and shortened, and that the line is the first one the winning rule matches.
func TestRedSubclassNamesTheMatchedLine(t *testing.T) {
	long := "--- FAIL: TestLong " + strings.Repeat("x", 500)
	tests := []struct {
		name     string
		out      []string
		wantLine string
		maxLen   int
	}{
		{"line is trimmed", []string{"ok  pkg 0.1s", "   --- FAIL: TestA (0.00s)   ", "--- FAIL: TestB (0.00s)"}, "--- FAIL: TestA (0.00s)", 0},
		{"first match of the winning rule", []string{"--- FAIL: TestFirst (0s)", "--- FAIL: TestSecond (0s)"}, "--- FAIL: TestFirst (0s)", 0},
		{"rule order beats line order", []string{"--- FAIL: TestX (0s)", "panic: boom"}, "panic: boom", 0},
		{"long line is shortened", []string{long}, "--- FAIL: TestLong ", redShowMax},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyRed(RedRun{Step: "test", Exit: 1, Output: tc.out})
			if tc.maxLen == 0 {
				if got.Line != tc.wantLine {
					t.Fatalf("%s: line = %q, want %q", tc.name, got.Line, tc.wantLine)
				}
				return
			}
			if len([]rune(got.Line)) > tc.maxLen || !strings.HasPrefix(got.Line, tc.wantLine) {
				t.Fatalf("%s: line = %q (%d runes), want prefix %q within %d runes", tc.name, got.Line, len([]rune(got.Line)), tc.wantLine, tc.maxLen)
			}
		})
	}
	t.Run("no match names no line", func(t *testing.T) {
		got := ClassifyRed(RedRun{Step: "test", Exit: 1, Output: []string{"nothing here"}})
		if got.Line != "" || got.Subclass != "unknown" {
			t.Fatalf("unmatched run = %+v, want unknown with an empty line", got)
		}
	})
	t.Run("a captured name appears in the subclass", func(t *testing.T) {
		got := ClassifyRed(RedRun{Step: "test", Exit: 1, Output: []string{"environment variable API_KEY is required"}})
		if got.Subclass != "environment.missing-env:API_KEY" || !strings.Contains(got.Hint, "API_KEY") {
			t.Fatalf("verdict = %+v, want the variable name in subclass and hint", got)
		}
	})
}

// TestEverySubclassCarriesAFixHint pins SPEC-contracts 8.5: every subclass of the table has a
// one-line fix hint, a verdict carries the hint of its subclass, and the hints are unique per
// subclass so a hint names its fault.
func TestEverySubclassCarriesAFixHint(t *testing.T) {
	subs := RedSubclasses()
	if len(subs) < 20 {
		t.Fatalf("RedSubclasses() = %d rows, want the full L64 set (at least 20)", len(subs))
	}
	seenSub := map[string]bool{}
	seenHint := map[string]string{}
	for _, s := range subs {
		if seenSub[s] {
			t.Errorf("subclass %q listed twice", s)
		}
		seenSub[s] = true
		hint, ok := RedFixHint(s)
		if !ok || strings.TrimSpace(hint) == "" {
			t.Errorf("subclass %q has no fix hint", s)
			continue
		}
		if strings.ContainsAny(hint, "\r\n") {
			t.Errorf("hint of %q is not one line: %q", s, hint)
		}
		if other, dup := seenHint[hint]; dup {
			t.Errorf("subclasses %q and %q share the hint %q", other, s, hint)
		}
		seenHint[hint] = s
		class, _, _ := strings.Cut(s, ".")
		if !slices.Contains([]string{"real_bug", "environment", "stale"}, class) {
			t.Errorf("subclass %q is in class %q, which a rule table row may not use", s, class)
		}
	}
	for _, want := range []string{"real_bug.assertion", "real_bug.timeout", "environment.network", "environment.db-missing", "stale.toolchain"} {
		if !seenSub[want] {
			t.Errorf("L64 subclass %q is missing from the table", want)
		}
	}
	if _, ok := RedFixHint("environment.nope"); ok {
		t.Error("an unknown subclass has a hint")
	}
	if hint, ok := RedFixHint("environment.missing-env:PORT"); !ok || !strings.Contains(hint, "PORT") {
		t.Errorf("RedFixHint(missing-env:PORT) = %q, %v, want a hint naming PORT", hint, ok)
	}
	for _, bad := range []string{"environment.missing-env:A\nB", "environment.missing-env:A\x00B", "environment.missing-env:port", "environment.missing-env:", "environment.missing-env:A B"} {
		if hint, ok := RedFixHint(bad); ok {
			t.Errorf("RedFixHint(%q) = %q, want a refusal: the name is not an environment variable name", bad, hint)
		}
	}
	got := ClassifyRed(RedRun{Step: "test", Exit: 1, Output: []string{"panic: x"}})
	if want, _ := RedFixHint("real_bug.panic"); got.Hint != want || want == "" {
		t.Errorf("verdict hint = %q, want the hint of real_bug.panic %q", got.Hint, want)
	}
	if un := ClassifyRed(RedRun{Step: "test", Exit: 1}); strings.TrimSpace(un.Hint) == "" || strings.ContainsAny(un.Hint, "\r\n") {
		t.Errorf("unknown verdict hint = %q, want one non-empty line", un.Hint)
	}
}

// TestFlakyFromHistory pins that flaky is read from history and never from a rerun: the same
// test green and red on the same tree hash. A green on one tree and a red on another is a
// change, not flakiness.
func TestFlakyFromHistory(t *testing.T) {
	r := func(tree, id string, passed bool) TestRun { return TestRun{TreeHash: tree, TestID: id, Passed: passed} }
	tests := []struct {
		name string
		hist []TestRun
		want []string
	}{
		{"green and red on one tree", []TestRun{r("t1", "pkg::TestA", true), r("t1", "pkg::TestA", false)}, []string{"pkg::TestA"}},
		{"red then green on one tree", []TestRun{r("t1", "pkg::TestA", false), r("t1", "pkg::TestA", true)}, []string{"pkg::TestA"}},
		{"green on one tree and red on another", []TestRun{r("t1", "pkg::TestA", true), r("t2", "pkg::TestA", false)}, nil},
		{"red only", []TestRun{r("t1", "pkg::TestA", false), r("t1", "pkg::TestA", false)}, nil},
		{"green only", []TestRun{r("t1", "pkg::TestA", true), r("t1", "pkg::TestA", true)}, nil},
		{"two tests on one tree", []TestRun{r("t1", "pkg::TestA", true), r("t1", "pkg::TestB", false)}, nil},
		{"flaky on a later tree only", []TestRun{r("t1", "pkg::TestA", true), r("t2", "pkg::TestA", true), r("t2", "pkg::TestA", false)}, []string{"pkg::TestA"}},
		{"unknown tree proves nothing", []TestRun{r("", "pkg::TestA", true), r("", "pkg::TestA", false)}, nil},
		{"unnamed test proves nothing", []TestRun{r("t1", "", true), r("t1", "", false)}, nil},
		{"result is sorted and unique", []TestRun{
			r("t1", "pkg::TestB", true), r("t1", "pkg::TestB", false), r("t2", "pkg::TestB", true), r("t2", "pkg::TestB", false),
			r("t1", "pkg::TestA", true), r("t1", "pkg::TestA", false),
		}, []string{"pkg::TestA", "pkg::TestB"}},
		{"empty history", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FlakyTests(tc.hist)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s: FlakyTests = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
	t.Run("the input is not reordered", func(t *testing.T) {
		hist := []TestRun{r("t2", "b", false), r("t1", "a", true), r("t2", "b", true)}
		before := slices.Clone(hist)
		FlakyTests(hist)
		if !slices.Equal(hist, before) {
			t.Fatalf("history changed: %v, was %v", hist, before)
		}
	})
}

// FuzzClassifyRed catches a classifier of untrusted output that panics, or returns a verdict
// that breaks the contract: a known class, a subclass in that class, one fix-hint line, a
// matched line that is one short line and present unless the verdict is unknown.
func FuzzClassifyRed(f *testing.F) {
	f.Add(1, "--- FAIL: TestA (0.00s)\nFAIL")
	f.Add(2, "panic: test timed out after 10m0s")
	f.Add(127, "")
	f.Add(1, "environment variable \xff\xfe is not set")
	f.Add(1, "core/x.go:14:9: undefined: Frobnicate\r\nFAIL\tx [build failed]")
	f.Add(0, strings.Repeat("a", 10000))
	f.Fuzz(func(t *testing.T, exit int, out string) {
		got := ClassifyRed(RedRun{Step: "test", Exit: exit, Output: strings.Split(out, "\n")})
		valid := []RedClass{RedRealBug, RedEnvironment, RedStale, RedUnknown}
		if !slices.Contains(valid, got.Class) {
			t.Fatalf("class %q is not one a rule table returns", got.Class)
		}
		if got.Class == RedUnknown {
			if got.Subclass != "unknown" || got.Line != "" {
				t.Fatalf("unknown verdict = %+v", got)
			}
		} else if !strings.HasPrefix(got.Subclass, string(got.Class)+".") || got.Line == "" {
			t.Fatalf("verdict %+v: subclass outside its class or no matched line", got)
		}
		if strings.TrimSpace(got.Hint) == "" || strings.ContainsAny(got.Hint+got.Line, "\r\n") {
			t.Fatalf("verdict %+v: hint empty or a line holds a line break", got)
		}
		if len([]rune(got.Line)) > redShowMax {
			t.Fatalf("matched line has %d runes, want at most %d", len([]rune(got.Line)), redShowMax)
		}
	})
}

// TestRedHintsMatchContracts pins that the rule table and the red-class table of
// docs/SPEC-contracts.md 8.5 hold the same subclasses with the same hints. A subclass or a hint
// that is only in one of them, or that differs, fails.
func TestRedHintsMatchContracts(t *testing.T) {
	raw, err := os.ReadFile("../../docs/SPEC-contracts.md")
	if err != nil {
		t.Fatalf("read SPEC-contracts: %v", err)
	}
	spec := contractHints(string(raw))
	if len(spec) == 0 {
		t.Fatal("no subclass rows found under the heading \"Subclasses and default fix hints (L64):\"")
	}
	code := map[string]string{}
	for _, s := range RedSubclasses() {
		hint, _ := RedFixHint(s)
		code[s] = hint
	}
	for s, hint := range code {
		got, ok := spec[s]
		switch {
		case !ok:
			t.Errorf("subclass %q is in the rule table and not in SPEC-contracts 8.5", s)
		case got != hint:
			t.Errorf("hint of %q differs: SPEC-contracts %q, rule table %q", s, got, hint)
		}
	}
	for s := range spec {
		if _, ok := code[s]; !ok {
			t.Errorf("subclass %q is in SPEC-contracts 8.5 and not in the rule table", s)
		}
	}
}

// TestContractHintsParse pins the reader of the contract table: the rows after the heading,
// and nothing past the table.
func TestContractHintsParse(t *testing.T) {
	doc := "x\nSubclasses and default fix hints (L64):\n\n| Class and subclass | Default fix hint |\n|---|---|\n" +
		"| `a.b` | Do it with `c`. |\n| `d.e:<NAME>` | Set `<NAME>`. |\n\nafter\n| `z.z` | not a row |\n"
	got := contractHints(doc)
	want := map[string]string{"a.b": "Do it with `c`.", "d.e:<NAME>": "Set `<NAME>`."}
	if len(got) != len(want) || got["a.b"] != want["a.b"] || got["d.e:<NAME>"] != want["d.e:<NAME>"] {
		t.Fatalf("contractHints = %v, want %v", got, want)
	}
}

// contractHints reads the subclass rows (| `subclass` | hint |) that follow the heading of the
// L64 table, up to the first line that is not a table line after the rows began.
func contractHints(doc string) map[string]string {
	rows := map[string]string{}
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "Subclasses and default fix hints (L64)") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			if len(rows) > 0 {
				break
			}
			continue
		}
		rest, ok := strings.CutPrefix(line, "| `")
		if !ok {
			continue
		}
		sub, hint, ok := strings.Cut(rest, "` | ")
		if !ok {
			continue
		}
		rows[sub] = strings.TrimSuffix(strings.TrimSpace(hint), " |")
	}
	return rows
}
