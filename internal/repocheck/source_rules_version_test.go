package repocheck_test

import (
	"slices"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// versionLiteralLines returns the lines of the version_literal findings of SourceRules on one file.
func versionLiteralLines(cfg repocheck.SourceRulesConfig, file repocheck.SourceFile) []int {
	var lines []int
	for _, f := range repocheck.SourceRules(cfg, []repocheck.SourceFile{file}) {
		if f.Rule == "version_literal" {
			lines = append(lines, f.Line)
		}
	}
	return lines
}

// TestSourceRulesVersionOwner guards DR-17 and STD-6: the one declared file owns the format versions of
// standards, so a version literal there is not flagged; every other non-test file is still flagged.
func TestSourceRulesVersionOwner(t *testing.T) {
	owner := "internal/core/standards.go"
	cfg := repocheck.SourceRulesConfig{Product: "zentra", ProductFile: "internal/core/product.go", VersionFile: owner}

	t.Run("a tool version in another file is still flagged", func(t *testing.T) {
		file := sourceRulesFile("internal/gates/tool.go", "package gates", `const tool = "1.2.3"`)
		if got := versionLiteralLines(cfg, file); !slices.Equal(got, []int{2}) {
			t.Fatalf("version_literal lines of %s = %v, want [2]", file.Path, got)
		}
	})
	t.Run("a standard version in another file is still flagged", func(t *testing.T) {
		file := sourceRulesFile("internal/gates/sarif.go", "package gates", `const sarifVersion = "2.1.0"`)
		if got := versionLiteralLines(cfg, file); !slices.Equal(got, []int{2}) {
			t.Fatalf("version_literal lines of %s = %v, want [2]", file.Path, got)
		}
	})
	t.Run("a standard version in the owner file is not flagged", func(t *testing.T) {
		file := sourceRulesFile(owner, "package core", `const SarifVersion = "2.1.0"`)
		if got := versionLiteralLines(cfg, file); len(got) != 0 {
			t.Fatalf("version_literal lines of %s = %v, want none", file.Path, got)
		}
	})
	t.Run("a tool version in the owner file is not flagged", func(t *testing.T) {
		file := sourceRulesFile(owner, "package core", `const tool = "1.2.3"`)
		if got := versionLiteralLines(cfg, file); len(got) != 0 {
			t.Fatalf("version_literal lines of %s = %v, want none", file.Path, got)
		}
	})
	t.Run("a file beside the owner is not exempt", func(t *testing.T) {
		file := sourceRulesFile("internal/core/standards_extra.go", "package core", `const SarifVersion = "2.1.0"`)
		if got := versionLiteralLines(cfg, file); !slices.Equal(got, []int{2}) {
			t.Fatalf("version_literal lines of %s = %v, want [2]", file.Path, got)
		}
	})
	t.Run("an unset owner exempts nothing", func(t *testing.T) {
		unset := repocheck.SourceRulesConfig{Product: "zentra", ProductFile: "internal/core/product.go"}
		file := sourceRulesFile(owner, "package core", `const SarifVersion = "2.1.0"`)
		if got := versionLiteralLines(unset, file); !slices.Equal(got, []int{2}) {
			t.Fatalf("version_literal lines of %s with no owner = %v, want [2]", file.Path, got)
		}
	})
}
