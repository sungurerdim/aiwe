package repocheck_test

import (
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// sourceRulesFile builds one Go source file from its lines, joined with LF.
func sourceRulesFile(path string, lines ...string) repocheck.SourceFile {
	return repocheck.SourceFile{Path: path, Body: []byte(strings.Join(lines, "\n") + "\n")}
}

func TestSourceRules(t *testing.T) {
	// The fixtures use a stand-in product so this file does not hold the real product name.
	cfg := repocheck.SourceRulesConfig{Product: "zentra", ProductFile: "internal/core/product.go"}

	t.Run("known-good sources pass", func(t *testing.T) {
		// Guards against a check so broad that the allowed forms (SPEC §5.4, DR-25) turn it red.
		good := []repocheck.SourceFile{
			sourceRulesFile("internal/core/product.go",
				"package core",
				`const Product = "zentra"`),
			sourceRulesFile("cmd/zentra/main.go",
				"package main",
				`import "github.com/example/zentra/internal/core"`,
				"var version string",
				"func main() { _ = core.Product; _ = version }"),
			sourceRulesFile("internal/core/patterns.go",
				"package core",
				`import ("errors"; "regexp")`,
				`var ErrGone = errors.New("gone")`,
				"var (",
				`	ErrStale = errors.New("stale")`,
				`	namePattern = regexp.MustCompile("^[a-z]+$")`,
				")"),
			sourceRulesFile("internal/core/remove.go",
				"package core",
				`import "os"`,
				"func removeDir(p string) error { return os.RemoveAll(p) }"),
			sourceRulesFile("internal/engine/clone.go",
				"package engine",
				`import "path/filepath"`,
				"func dir(home string) string {",
				"	var n int",
				"	_ = n",
				`	return filepath.Join(home, "internal", "core")`,
				"}"),
			sourceRulesFile("internal/core/output_test.go",
				"package core_test",
				`const wantVersion = "v1.4.2"`),
		}
		if got := repocheck.SourceRules(cfg, good); len(got) != 0 {
			t.Fatalf("SourceRules(known-good) = %+v, want no finding", got)
		}
	})

	bad := []struct {
		name string
		file repocheck.SourceFile
		rule string
		line int
	}{
		{"Windows home path with a Turkish user name",
			sourceRulesFile("internal/engine/cache.go", "package engine",
				`const cacheDir = "C:\\Users\\Ayşe\\AppData\\Local\\cache"`),
			"machine_path", 2},
		{"Linux home path",
			sourceRulesFile("internal/engine/cache.go", "package engine",
				`const src = "/home/kerem/src"`),
			"machine_path", 2},
		{"Linux root home path",
			sourceRulesFile("internal/engine/cache.go", "package engine",
				`const src = "/root/src"`),
			"machine_path", 2},
		{"macOS home path in a raw string",
			sourceRulesFile("internal/engine/cache.go", "package engine",
				"const caches = `/Users/mei/Library/Caches`"),
			"machine_path", 2},
		{"drive-letter path with forward slashes",
			sourceRulesFile("internal/engine/cache.go", "package engine",
				`const repo = "D:/GitHub/project"`),
			"machine_path", 2},
		{"product name in a message",
			sourceRulesFile("internal/engine/run.go", "package engine",
				`const msg = "zentra: run failed"`),
			"product_name", 2},
		{"product name as a path segment",
			sourceRulesFile("internal/engine/run.go", "package engine",
				`const state = ".zentra/state"`),
			"product_name", 2},
		{"product name in another case",
			sourceRulesFile("internal/engine/run.go", "package engine",
				`const title = "Zentra"`),
			"product_name", 2},
		{"recursive removal outside core/remove.go",
			sourceRulesFile("internal/engine/clone.go", "package engine", `import "os"`,
				"func drop(p string) error { return os.RemoveAll(p) }"),
			"recursive_removal", 3},
		{"recursive removal through an import alias",
			sourceRulesFile("internal/engine/clone.go", "package engine", `import fsys "os"`,
				"func drop(p string) error { return fsys.RemoveAll(p) }"),
			"recursive_removal", 3},
		{"recursive removal taken as a function value",
			sourceRulesFile("internal/engine/clone.go", "package engine", `import "os"`,
				"func drop() func(string) error { rm := os.RemoveAll; return rm }"),
			"recursive_removal", 3},
		{"recursive removal in a remove.go of another package",
			sourceRulesFile("internal/engine/remove.go", "package engine", `import "os"`,
				"func drop(p string) error { return os.RemoveAll(p) }"),
			"recursive_removal", 3},
		{"recursive removal through a dot import",
			sourceRulesFile("internal/engine/clone.go", "package engine", `import . "os"`,
				"func drop(p string) error { return RemoveAll(p) }"),
			"recursive_removal", 3},
		{"semantic version literal",
			sourceRulesFile("internal/engine/info.go", "package engine",
				`const Version = "1.2.3"`),
			"version_literal", 2},
		{"tag-style version literal",
			sourceRulesFile("internal/engine/info.go", "package engine",
				`func tag() string { return "v0.4.0" }`),
			"version_literal", 2},
		{"pre-release version literal",
			sourceRulesFile("internal/engine/info.go", "package engine",
				`const next = "v2.0.0-rc.1"`),
			"version_literal", 2},
		{"version literal in the build-injected variable",
			sourceRulesFile("cmd/zentra/main.go", "package main",
				`var version = "v1.0.0"`),
			"version_literal", 2},
		{"build-injected variable given a value",
			sourceRulesFile("cmd/zentra/main.go", "package main",
				`var version = "v1.0.0"`),
			"package_var", 2},
		{"package-level mutable map",
			sourceRulesFile("internal/engine/state.go", "package engine",
				"var cache = map[string]int{}"),
			"package_var", 2},
		{"package-level var in a group",
			sourceRulesFile("internal/engine/state.go", "package engine",
				"var (", "	count int", ")"),
			"package_var", 3},
		{"version variable outside cmd main.go",
			sourceRulesFile("internal/engine/state.go", "package engine",
				"var version string"),
			"package_var", 2},
		{"version variable in the main.go of another command",
			sourceRulesFile("cmd/other/main.go", "package main",
				"var version string"),
			"package_var", 2},
		{"CRLF source keeps its line numbers",
			repocheck.SourceFile{Path: "internal/engine/state.go",
				Body: []byte("package engine\r\n\r\nvar cache = map[string]int{}\r\n")},
			"package_var", 3},
		{"source with a byte order mark is still checked",
			repocheck.SourceFile{Path: "internal/engine/state.go",
				Body: []byte("\ufeffpackage engine\nvar cache = map[string]int{}\n")},
			"package_var", 2},
		{"unparsable source is a finding, not a pass",
			sourceRulesFile("internal/engine/broken.go", "package engine", "func ("),
			"parse_error", 0},
		{"invalid UTF-8 source is a finding, not a pass",
			repocheck.SourceFile{Path: "internal/engine/latin.go",
				Body: []byte("package engine\nconst s = \"\xdd\xfd\"\n")},
			"invalid_utf8", 0},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			// Guards against a known-bad source that passes repo/source-rules (DR-25, SPEC §5.4).
			got := repocheck.SourceRules(cfg, []repocheck.SourceFile{tc.file})
			for _, f := range got {
				// line 0 means the test does not pin the line, only the file and the rule.
				if f.Rule == tc.rule && f.Path == tc.file.Path && (tc.line == 0 || f.Line == tc.line) {
					return
				}
			}
			t.Fatalf("SourceRules(%s) = %+v, want a %q finding at %s line %d",
				tc.file.Path, got, tc.rule, tc.file.Path, tc.line)
		})
	}
}
