package repocheck_test

import (
	"slices"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// envReadLines returns the lines of the env_read findings of SourceRules on one file.
func envReadLines(t *testing.T, file repocheck.SourceFile) []int {
	t.Helper()
	cfg := repocheck.SourceRulesConfig{Product: "zentra", ProductFile: "internal/core/product.go"}
	var lines []int
	for _, f := range repocheck.SourceRules(cfg, []repocheck.SourceFile{file}) {
		if f.Rule == "env_read" {
			lines = append(lines, f.Line)
		}
	}
	return lines
}

// TestSourceRulesEnvRead guards SPEC §5.4: only internal/core/env.go reads the process environment,
// so a new os.Environ, os.Getenv or os.LookupEnv anywhere else is a finding.
func TestSourceRulesEnvRead(t *testing.T) {
	flagged := []struct {
		name  string
		file  repocheck.SourceFile
		lines []int
	}{
		{"os.Environ in a core file",
			sourceRulesFile("internal/core/git.go", "package core", `import "os"`,
				"func env() []string { return os.Environ() }"), []int{3}},
		{"os.Getenv in an OS variant of a core file",
			sourceRulesFile("internal/core/launcher_windows.go", "//go:build windows", "",
				"package core", `import "os"`,
				`func exts() string { return os.Getenv("PATHEXT") }`), []int{5}},
		{"os.LookupEnv in an *_io.go file",
			sourceRulesFile("internal/repocheck/doctor_io.go", "package repocheck", `import "os"`,
				`func home() (string, bool) { return os.LookupEnv("HOME") }`), []int{3}},
		{"os.Environ in core/env_windows.go, which is not env.go",
			sourceRulesFile("internal/core/env_windows.go", "package core", `import "os"`,
				"func env() []string { return os.Environ() }"), []int{3}},
		{"aliased os import",
			sourceRulesFile("internal/gates/roots_io.go", "package gates", `import sys "os"`,
				`func get() string { return sys.Getenv("PATH") }`), []int{3}},
		{"read taken as a value",
			sourceRulesFile("internal/core/config.go", "package core", `import "os"`,
				"func lookup() func(string) string {", "	return os.Getenv", "}"), []int{4}},
		{"dot import of os",
			sourceRulesFile("internal/core/process.go", "package core", `import . "os"`,
				"func env() []string {", "	_ = Getenv(\"PATH\")", "	return Environ()", "}"),
			[]int{4, 5}},
	}
	for _, tc := range flagged {
		t.Run(tc.name, func(t *testing.T) {
			if got := envReadLines(t, tc.file); !slices.Equal(got, tc.lines) {
				t.Fatalf("env_read lines of %s = %v, want %v", tc.file.Path, got, tc.lines)
			}
		})
	}

	allowed := []struct {
		name string
		file repocheck.SourceFile
	}{
		{"core/env.go, the one reader",
			sourceRulesFile("internal/core/env.go", "package core", `import "os"`,
				"func Environ() []string { return os.Environ() }",
				"func LookupEnv(name string) string { return os.Getenv(name) }")},
		{"a test file",
			sourceRulesFile("internal/core/git_test.go", "package core_test", `import "os"`,
				`func home() string { return os.Getenv("HOME") }`)},
		{"a read through core",
			sourceRulesFile("internal/core/git.go", "package core",
				`func exts() string { return LookupEnv("PATHEXT") + Environ()[0] }`)},
		{"Getenv selected on a value that is not the os package",
			sourceRulesFile("internal/engine/plan.go", "package engine",
				"type vars struct{}",
				"func (vars) Getenv(string) string { return \"\" }",
				`func get(v vars) string { return v.Getenv("PATH") }`)},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if got := envReadLines(t, tc.file); len(got) != 0 {
				t.Fatalf("env_read lines of %s = %v, want none", tc.file.Path, got)
			}
		})
	}
}
