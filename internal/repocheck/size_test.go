package repocheck_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// linesFile builds a valid Go file of exactly n LF-terminated lines: a package clause, then
// comment lines.
func linesFile(path string, n int) repocheck.SourceFile {
	lines := []string{"package core"}
	for i := 2; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("// line %d", i))
	}
	return sourceRulesFile(path, lines...)
}

// funcFile builds a Go file whose function long starts on line 3 and spans n lines, counted from
// the func keyword line through the closing brace, both included.
func funcFile(path string, n int) repocheck.SourceFile {
	return declFile(path, "func long() {", n)
}

// testFuncFile builds a Go test file whose test function TestLong starts on line 3 and spans n
// lines, counted the same way as funcFile.
func testFuncFile(path string, n int) repocheck.SourceFile {
	return declFile(path, "func TestLong(t *testing.T) {", n)
}

// declFile builds a Go file whose function opened by header starts on line 3 and spans n lines.
func declFile(path, header string, n int) repocheck.SourceFile {
	lines := []string{"package core", "", header}
	for i := 0; i < n-2; i++ {
		lines = append(lines, "\t_ = 0")
	}
	lines = append(lines, "}")
	return sourceRulesFile(path, lines...)
}

func TestSize(t *testing.T) {
	t.Run("files and functions at the limits pass", func(t *testing.T) {
		// Guards against an off-by-one: SPEC §5.13 limits are inclusive.
		good := []repocheck.SourceFile{
			linesFile("internal/core/text.go", 400),
			linesFile("internal/core/text_test.go", 500),
			funcFile("internal/core/flow.go", 60),
		}
		if got := repocheck.Size(good); len(got) != 0 {
			t.Fatalf("Size(at limits) = %+v, want no finding", got)
		}
	})

	bad := []struct {
		name string
		file repocheck.SourceFile
		rule string
		line int
	}{
		{"source file over 400 lines", linesFile("internal/core/text.go", 401), "file_too_long", 0},
		{"test file over 500 lines", linesFile("internal/core/text_test.go", 501), "file_too_long", 0},
		{"function over 60 lines", funcFile("internal/core/flow.go", 61), "func_too_long", 3},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			got := repocheck.Size([]repocheck.SourceFile{tc.file})
			for _, f := range got {
				if f.Rule == tc.rule && f.Path == tc.file.Path && (tc.line == 0 || f.Line == tc.line) {
					return
				}
			}
			t.Fatalf("Size(%s) = %+v, want a %q finding at line %d", tc.file.Path, got, tc.rule, tc.line)
		})
	}

	t.Run("a test function has no line limit", func(t *testing.T) {
		// Guards against the 60-line limit reaching test functions: SPEC §5.13 and Q23 limit only
		// non-test functions, and a test file is bounded by its 500-line file limit alone.
		for _, n := range []int{61, 400} {
			f := testFuncFile("internal/core/flow_test.go", n)
			if got := repocheck.Size([]repocheck.SourceFile{f}); len(got) != 0 {
				t.Fatalf("Size(TestLong of %d lines) = %+v, want no finding", n, got)
			}
		}
	})

	t.Run("a test file over 500 lines is a finding even when no function is too long", func(t *testing.T) {
		// Guards against the test-function exemption also lifting the test file limit.
		f := testFuncFile("internal/core/flow_test.go", 499)
		if got := repocheck.Size([]repocheck.SourceFile{f}); !hasFinding(got, "file_too_long", f.Path) {
			t.Fatalf("Size(test file of 501 lines) = %+v, want a file_too_long finding", got)
		}
	})

	t.Run("a file that does not parse is a finding, not a pass", func(t *testing.T) {
		// Guards against a function count that silently skips a file it cannot read.
		f := sourceRulesFile("internal/core/broken.go", "package core", "func long() {")
		got := repocheck.Size([]repocheck.SourceFile{f})
		if !hasFinding(got, "parse_error", f.Path) {
			t.Fatalf("Size(unparsable) = %+v, want a parse_error finding", got)
		}
	})
}

// ruleLines returns the lines of the findings of rule that SourceRules reports for files.
func ruleLines(files []repocheck.SourceFile, rule string) []int {
	cfg := repocheck.SourceRulesConfig{Product: "zentra", ProductFile: "internal/core/product.go"}
	var lines []int
	for _, f := range repocheck.SourceRules(cfg, files) {
		if f.Rule == rule {
			lines = append(lines, f.Line)
		}
	}
	return lines
}

// skipFile builds a test file whose only test body is the given call, on line 6.
func skipFile(path, call string) repocheck.SourceFile {
	return sourceRulesFile(path,
		"package core",
		"",
		`import "testing"`,
		"",
		"func TestSkip(t *testing.T) {",
		"\t"+call,
		"}")
}

// The repository gate runs source-rules over every tracked file, so L1 and the tree half of DR-12
// are enforced there and not only in Size, which sees Go files alone (#309).
func TestSourceRulesSpecCapAndSkips(t *testing.T) {
	t.Run("SPEC at the L1 cap passes and one line over it is a finding", func(t *testing.T) {
		// Guards the L1 cap at its exact boundary: 900 lines is the cap, 901 is red.
		if got := ruleLines([]repocheck.SourceFile{linesFile("docs/SPEC.md", 900)}, "doc_too_long"); len(got) != 0 {
			t.Fatalf("SourceRules(SPEC of 900 lines) doc_too_long lines = %v, want none", got)
		}
		got := ruleLines([]repocheck.SourceFile{linesFile("docs/SPEC.md", 901)}, "doc_too_long")
		if len(got) != 1 || got[0] != 901 {
			t.Fatalf("SourceRules(SPEC of 901 lines) doc_too_long lines = %v, want [901]", got)
		}
	})

	t.Run("the L1 cap covers both SPEC files and no other document", func(t *testing.T) {
		// Guards L1 (docs/SPEC.md §3): the cap holds docs/SPEC.md and docs/SPEC-contracts.md alike,
		// and does not block every long document.
		for _, c := range []struct {
			path string
			n    int
			want []int
		}{
			{"docs/SPEC-contracts.md", 900, nil},
			{"docs/SPEC-contracts.md", 901, []int{901}},
			{"docs/standards.md", 950, nil},
		} {
			got := ruleLines([]repocheck.SourceFile{linesFile(c.path, c.n)}, "doc_too_long")
			if !slices.Equal(got, c.want) {
				t.Errorf("SourceRules(%s of %d lines) doc_too_long lines = %v, want %v", c.path, c.n, got, c.want)
			}
		}
	})

	t.Run("SPEC prose does not reach the Go parser", func(t *testing.T) {
		// Guards against SPEC prose reaching the Go parser: a long SPEC is one doc_too_long finding
		// and no parse_error.
		over := repocheck.SourceFile{Path: "docs/SPEC.md", Body: []byte(strings.Repeat("prose line\n", 901))}
		if got := ruleLines([]repocheck.SourceFile{over}, "parse_error"); len(got) != 0 {
			t.Fatalf("SourceRules(SPEC prose) parse_error lines = %v, want none", got)
		}
		if got := ruleLines([]repocheck.SourceFile{over}, "doc_too_long"); len(got) != 1 {
			t.Fatalf("SourceRules(SPEC prose of 901 lines) doc_too_long lines = %v, want one", got)
		}
	})

	t.Run("a bare skip is a finding", func(t *testing.T) {
		// Guards the tree half of DR-12: a skip is a behaviour nobody checks, so it says why. A nil,
		// a non-string literal and a constant with no visible character state nothing.
		for _, call := range []string{
			"t.SkipNow()", "t.Skip()", `t.Skip("")`, `t.Skip("  ")`, `t.Skipf("")`,
			"t.Skip(nil)",
			"t.Skip(0)",
			"t.Skip(1.5)",
			"t.Skip('x')",
			`t.Skip("" + "")`,
			`t.Skip(("") + (" "))`,
			`t.Skip("\u200b")`,
			`t.Skip("\u00a0\t")`,
			`t.Skip(nil, "")`,
		} {
			got := ruleLines([]repocheck.SourceFile{skipFile("internal/core/flow_test.go", call)}, "bare_skip")
			if len(got) != 1 || got[0] != 6 {
				t.Errorf("SourceRules(%s) bare_skip lines = %v, want [6]", call, got)
			}
		}
	})

	t.Run("a skip that states its reason passes", func(t *testing.T) {
		// Guards against a rule that demands an issue number: the reasons this tree already gives name
		// what the test itself checked, and a referenced skip passes with them. A call or a variable
		// is read as a reason, because the check cannot fold it.
		for _, call := range []string{
			`t.Skip("a FIFO is a Unix file type")`,
			`t.Skipf("no /proc/self/stat on %s", runtime.GOOS)`,
			`t.Skip("junctions are Windows-only")`,
			`t.Skip("blocked by #412")`,
			`t.Skip("tracked in issues/412")`,
			`t.Skip(reason)`,
			`t.Skip(err.Error())`,
			`t.Skip(fmt.Sprint(why))`,
			`t.Skip("" + reason)`,
			`t.Skip("", reason)`,
			`t.Skip("a" + "b")`,
		} {
			if got := ruleLines([]repocheck.SourceFile{skipFile("internal/core/flow_test.go", call)}, "bare_skip"); len(got) != 0 {
				t.Errorf("SourceRules(%s) bare_skip lines = %v, want none", call, got)
			}
		}
	})

	t.Run("a skip on a testing receiver is a finding wherever the file declares it", func(t *testing.T) {
		// Guards against the rule reading only a parameter named t of *testing.T: b, f, testing.TB, an
		// aliased import and a closure parameter are the testing receiver as well.
		for _, c := range []struct {
			name string
			imp  string
			decl []string
		}{
			{"benchmark", `import "testing"`, []string{"func BenchmarkX(b *testing.B) {", "\tb.Skip()", "}"}},
			{"fuzz", `import "testing"`, []string{"func FuzzX(f *testing.F) {", "\tf.Skip()", "}"}},
			{"interface", `import "testing"`, []string{"func helper(tb testing.TB) {", "\ttb.SkipNow()", "}"}},
			{"aliased import", `import tt "testing"`, []string{"func TestX(t *tt.T) {", "\tt.Skip()", "}"}},
			{"closure", `import "testing"`, []string{
				"func TestX(t *testing.T) {", `	t.Run("a", func(u *testing.T) {`, "\t\tu.Skip()", "\t})", "}"}},
		} {
			lines := append([]string{"package core", "", c.imp, ""}, c.decl...)
			f := sourceRulesFile("internal/core/flow_test.go", lines...)
			if got := ruleLines([]repocheck.SourceFile{f}, "bare_skip"); len(got) != 1 {
				t.Errorf("SourceRules(%s) bare_skip lines = %v, want one", c.name, got)
			}
		}
	})

	t.Run("a skip inside a non-test file is a finding too", func(t *testing.T) {
		// Guards against the rule reading only _test.go paths: a helper in a non-test file that takes
		// the testing receiver skips as well, so the call is what counts, not the file name.
		f := sourceRulesFile("internal/core/flow.go",
			"package core",
			"",
			`import "testing"`,
			"",
			"func skip(t testing.TB) {",
			"\tt.Skip()",
			"}")
		if got := ruleLines([]repocheck.SourceFile{f}, "bare_skip"); len(got) != 1 || got[0] != 6 {
			t.Fatalf("SourceRules(a non-test file calling Skip) bare_skip lines = %v, want [6]", got)
		}
	})

	t.Run("a Skip method that is not on a testing receiver is not a finding", func(t *testing.T) {
		// Guards against a name match on Skip alone: a reader's Skip() is not a skipped test, whether
		// its receiver is a parameter of another type or is not declared as a parameter at all.
		for _, c := range []struct {
			name string
			decl []string
		}{
			{"another type", []string{"func drop(r interface{ Skip() }) {", "\tr.Skip()", "}"}},
			{"no testing parameter", []string{"func drop(t *Reader) {", "\tt.Skip()", "}"}},
			{"a struct field", []string{"func (s *Stream) drop() {", "\ts.r.Skip()", "}"}},
		} {
			f := sourceRulesFile("internal/core/reader.go", append([]string{"package core", ""}, c.decl...)...)
			if got := ruleLines([]repocheck.SourceFile{f}, "bare_skip"); len(got) != 0 {
				t.Errorf("SourceRules(%s) bare_skip lines = %v, want none", c.name, got)
			}
		}
	})

	t.Run("a receiver typed by a package that is not testing is not a finding", func(t *testing.T) {
		// Guards the testing-import check: T, B, F and TB alone do not make a testing receiver, the
		// package must be the one the file imports as testing. On a3ec189 this row already passes.
		f := sourceRulesFile("internal/core/other.go",
			"package core",
			"",
			`import other "example.com/other"`,
			"",
			"func drop(t *other.T) {",
			"	t.Skip()",
			"}")
		if got := ruleLines([]repocheck.SourceFile{f}, "bare_skip"); len(got) != 0 {
			t.Errorf("SourceRules(a receiver of another package) bare_skip lines = %v, want none", got)
		}
	})

	t.Run("a skip in a function literal outside a function declaration is a finding", func(t *testing.T) {
		// Guards against walking function declarations alone: a package-level literal that takes the
		// testing receiver skips as well.
		for _, c := range []struct {
			name string
			decl []string
		}{
			{"var literal", []string{"var skip = func(t *testing.T) {", "	t.Skip()", "}"}},
			{"blank var literal", []string{"var _ = func(tb testing.TB) {", "	tb.SkipNow()", "}"}},
			{"grouped var literal", []string{"var (", "	skip = func(t *testing.T) {", "		t.Skipf(\"\")", "	}", ")"}},
		} {
			lines := append([]string{"package core", "", `import "testing"`, ""}, c.decl...)
			f := sourceRulesFile("internal/core/flow_test.go", lines...)
			if got := ruleLines([]repocheck.SourceFile{f}, "bare_skip"); len(got) != 1 {
				t.Errorf("SourceRules(%s) bare_skip lines = %v, want one", c.name, got)
			}
		}
	})
}
