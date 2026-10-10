package repocheck_test

import (
	"fmt"
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
	lines := []string{"package core", "", "func long() {"}
	for i := 0; i < n-2; i++ {
		lines = append(lines, "\t_ = 0")
	}
	lines = append(lines, "}")
	return sourceRulesFile(path, lines...)
}

func TestSize(t *testing.T) {
	t.Run("files and functions at the limits pass", func(t *testing.T) {
		// Guards against an off-by-one: SPEC §5.9 limits are inclusive.
		good := []repocheck.SourceFile{
			linesFile("internal/core/text.go", 400),
			linesFile("internal/core/text_test.go", 500),
			funcFile("internal/core/flow.go", 60),
			funcFile("internal/core/flow_test.go", 60),
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
		{"function over 60 lines in a test file", funcFile("internal/core/flow_test.go", 61), "func_too_long", 3},
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

	t.Run("a file that does not parse is a finding, not a pass", func(t *testing.T) {
		// Guards against a function count that silently skips a file it cannot read.
		f := sourceRulesFile("internal/core/broken.go", "package core", "func long() {")
		got := repocheck.Size([]repocheck.SourceFile{f})
		if !hasFinding(got, "parse_error", f.Path) {
			t.Fatalf("Size(unparsable) = %+v, want a parse_error finding", got)
		}
	})
}
