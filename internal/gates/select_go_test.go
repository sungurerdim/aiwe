package gates

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// selectGoFixture is the shape `go list -deps -test -json ./...` prints for the module
// example.com/m at /repo: a imports nothing in the module, b imports a, d imports b, c stands
// alone, the external test of a imports c, and the in-package test of d imports c. It holds the variants the test flag adds
// ("a [a.test]", "a_test [a.test]", "a.test") and one standard package that -deps adds.
const selectGoFixture = `{"Dir":"/goroot/src/fmt","ImportPath":"fmt","Name":"fmt","Standard":true,"DepOnly":true,"GoFiles":["print.go"],"Imports":["os"]}
{"Dir":"/repo/a","ImportPath":"example.com/m/a","Name":"a","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["a.go"],"TestGoFiles":["a_test.go"],"XTestGoFiles":["a_x_test.go"],"EmbedFiles":["data/rules.txt"],"Imports":["fmt"],"TestImports":["testing"],"XTestImports":["example.com/m/c","testing"]}
{"Dir":"/repo/b","ImportPath":"example.com/m/b","Name":"b","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["b.go"],"Imports":["example.com/m/a","fmt"]}
{"Dir":"/repo/c","ImportPath":"example.com/m/c","Name":"c","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["c.go"],"TestGoFiles":["c_test.go"],"Imports":["fmt"],"TestImports":["testing"]}
{"Dir":"/repo/d","ImportPath":"example.com/m/d","Name":"d","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["d.go"],"TestGoFiles":["d_test.go"],"Imports":["example.com/m/b"],"TestImports":["example.com/m/c","testing"]}
{"Dir":"/repo/a","ImportPath":"example.com/m/a [example.com/m/a.test]","ForTest":"example.com/m/a","Name":"a","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["a.go","a_test.go"],"Imports":["fmt","testing"]}
{"Dir":"/repo/a","ImportPath":"example.com/m/a_test [example.com/m/a.test]","ForTest":"example.com/m/a","Name":"a_test","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["a_x_test.go"],"Imports":["example.com/m/a [example.com/m/a.test]","example.com/m/c","testing"]}
{"Dir":"/repo/a","ImportPath":"example.com/m/a.test","Name":"main","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["_testmain.go"],"Imports":["example.com/m/a [example.com/m/a.test]","example.com/m/a_test [example.com/m/a.test]","testing"]}
`

// selectGoGraph parses the fixture with /repo as the repository root.
func selectGoGraph(t *testing.T) *GoGraph {
	t.Helper()
	g, err := ParseGoList([]byte(selectGoFixture), "/repo")
	if err != nil {
		t.Fatalf("ParseGoList(fixture) error = %v, want nil", err)
	}
	return g
}

// selectedPackages returns the import paths of a selection, in the order returned.
func selectedPackages(res GoSelectResult) []string {
	var out []string
	for _, s := range res.Selected {
		out = append(out, s.Package)
	}
	return out
}

// checkSelection fails unless res selects exactly the packages of want, keyed by directory, each
// with its reason, in import-path order.
func checkSelection(t *testing.T, changed []string, res GoSelectResult, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, s := range res.Selected {
		got[s.Dir] = s.Reason
	}
	if !maps.Equal(got, want) {
		t.Errorf("Select(%v) dir → reason = %v, want %v", changed, got, want)
	}
	if pkgs := selectedPackages(res); !slices.IsSorted(pkgs) {
		t.Errorf("Select(%v) packages = %v, want them sorted by import path", changed, pkgs)
	}
	if len(res.Unmapped) != 0 {
		t.Errorf("Select(%v) Unmapped = %v, want none", changed, res.Unmapped)
	}
}

func TestGoImportsSelectsDependentPackages(t *testing.T) {
	tests := []struct {
		name    string
		changed []string
		want    map[string]string
	}{
		{"a changed selects a, its importer b and the importer d of b, not c",
			[]string{"a/a.go"}, map[string]string{"a": "changed: a/a.go", "b": "import: a ← b", "d": "import: a ← b ← d"}},
		{"b changed selects b and d only, never its dependency a",
			[]string{"b/b.go"}, map[string]string{"b": "changed: b/b.go", "d": "import: b ← d"}},
		{"c changed selects c, a and d whose tests import it, but not b, which imports d",
			[]string{"c/c.go"}, map[string]string{"c": "changed: c/c.go", "a": "import: c ← a (test)", "d": "import: c ← d (test)"}},
		{"d changed selects d alone",
			[]string{"d/d.go"}, map[string]string{"d": "changed: d/d.go"}},
		{"two changed files select the union once, b by its own file",
			[]string{"a/a.go", "b/b.go"}, map[string]string{"a": "changed: a/a.go", "b": "changed: b/b.go", "d": "import: b ← d"}},
		{"a deleted go file still selects its package and the tests that import it",
			[]string{"c/gone.go"}, map[string]string{"c": "changed: c/gone.go", "a": "import: c ← a (test)", "d": "import: c ← d (test)"}},
		{"an embedded file selects the package that embeds it and its importers",
			[]string{"a/data/rules.txt"}, map[string]string{"a": "changed: a/data/rules.txt", "b": "import: a ← b", "d": "import: a ← b ← d"}},
	}
	g := selectGoGraph(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkSelection(t, tc.changed, g.Select(tc.changed), tc.want)
		})
	}
}

func TestGoImportsChangedTestFileSelectsItsPackage(t *testing.T) {
	tests := []struct {
		name    string
		changed []string
		want    map[string]string
	}{
		{"an in-package test file selects its package and no importer",
			[]string{"a/a_test.go"}, map[string]string{"a": "changed: a/a_test.go"}},
		{"an external test file selects its package and no importer",
			[]string{"a/a_x_test.go"}, map[string]string{"a": "changed: a/a_x_test.go"}},
		{"a test file of c selects c and not a or d, which import c only in their own tests",
			[]string{"c/c_test.go"}, map[string]string{"c": "changed: c/c_test.go"}},
		{"a test file of d selects d alone",
			[]string{"d/d_test.go"}, map[string]string{"d": "changed: d/d_test.go"}},
		{"a test file and a source file of one package select it once with the importers",
			[]string{"a/a_test.go", "a/a.go"},
			map[string]string{"a": "changed: a/a.go, a/a_test.go", "b": "import: a ← b", "d": "import: a ← b ← d"}},
	}
	g := selectGoGraph(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkSelection(t, tc.changed, g.Select(tc.changed), tc.want)
		})
	}
}

// TestGoImportsInPackageTestImportSelectsTheTester pins the TestImports branch: d imports c only in
// its in-package test, so a change to c selects d, and b, which imports d, stays out.
func TestGoImportsInPackageTestImportSelectsTheTester(t *testing.T) {
	res := selectGoGraph(t).Select([]string{"c/c.go"})
	i := slices.IndexFunc(res.Selected, func(s GoSelection) bool { return s.Package == "example.com/m/d" })
	if i < 0 {
		t.Fatalf("Select(c/c.go) did not select d: %v", selectedPackages(res))
	}
	if got, want := res.Selected[i].Reason, "import: c ← d (test)"; got != want {
		t.Errorf("Select(c/c.go) reason of d = %q, want %q", got, want)
	}
	if slices.Contains(selectedPackages(res), "example.com/m/b") {
		t.Errorf("Select(c/c.go) = %v, want b out: it imports d, not c", selectedPackages(res))
	}
}

func TestGoImportsReasonNamesTheChain(t *testing.T) {
	tests := []struct {
		name    string
		changed []string
		pkg     string
		want    string
	}{
		{"a changed file is its own reason", []string{"a/a.go"}, "example.com/m/a", "changed: a/a.go"},
		{"a direct importer names the pair", []string{"a/a.go"}, "example.com/m/b", "import: a ← b"},
		{"a transitive importer names the whole chain", []string{"a/a.go"}, "example.com/m/d", "import: a ← b ← d"},
		{"a package that only tests the change says so", []string{"c/c.go"}, "example.com/m/a", "import: c ← a (test)"},
		{"an importer of a changed importer names the chain through it", []string{"b/b.go"}, "example.com/m/d", "import: b ← d"},
		{"several changed files are all named", []string{"a/a_test.go", "a/a.go"}, "example.com/m/a",
			"changed: a/a.go, a/a_test.go"},
	}
	g := selectGoGraph(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := g.Select(tc.changed)
			i := slices.IndexFunc(res.Selected, func(s GoSelection) bool { return s.Package == tc.pkg })
			if i < 0 {
				t.Fatalf("Select(%v) did not select %s: %v", tc.changed, tc.pkg, selectedPackages(res))
			}
			if got := res.Selected[i].Reason; got != tc.want {
				t.Errorf("Select(%v) reason of %s = %q, want %q", tc.changed, tc.pkg, got, tc.want)
			}
		})
	}
}

func TestGoImportsUnmappedFilesAreReturned(t *testing.T) {
	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{"a file outside every package is unmapped", []string{"docs/SPEC.md"}, []string{"docs/SPEC.md"}},
		{"a non-go file no package embeds is unmapped", []string{"a/README.md"}, []string{"a/README.md"}},
		{"go.mod is unmapped for the module-wide fallback", []string{"go.mod"}, []string{"go.mod"}},
		{"a mapped and an unmapped file split", []string{"b/b.go", "Makefile"}, []string{"Makefile"}},
	}
	g := selectGoGraph(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.Select(tc.changed).Unmapped; !slices.Equal(got, tc.want) {
				t.Errorf("Select(%v) Unmapped = %v, want %v", tc.changed, got, tc.want)
			}
		})
	}
}

func TestGoImportsSelectionKeepsHasTests(t *testing.T) {
	res := selectGoGraph(t).Select([]string{"a/a.go"})
	want := map[string]bool{"example.com/m/a": true, "example.com/m/b": false, "example.com/m/d": true}
	for _, s := range res.Selected {
		if s.HasTests != want[s.Package] {
			t.Errorf("HasTests of %s = %v, want %v", s.Package, s.HasTests, want[s.Package])
		}
	}
}

func TestParseGoListRefusesUnusableOutput(t *testing.T) {
	tests := []struct {
		name string
		data string
		root string
	}{
		{"empty output holds no package", "", "/repo"},
		{"output that is not json", "go: cannot find main module", "/repo"},
		{"only standard packages", `{"ImportPath":"fmt","Standard":true,"DepOnly":true,"Dir":"/goroot/src/fmt"}`, "/repo"},
		{"packages all outside the root", selectGoFixture, "/elsewhere"},
		{"a package record with an error", `{"Dir":"/repo/a","ImportPath":"example.com/m/a","Error":{"Err":"no Go files"}}`, "/repo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if g, err := ParseGoList([]byte(tc.data), tc.root); err == nil {
				t.Errorf("ParseGoList(%q) = %v, nil error, want an error", tc.data, g)
			}
		})
	}
}

// selectGoWriteModule writes files, keyed by slash path, under a fresh temporary directory with
// symlinks resolved, and returns the directory.
func selectGoWriteModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSelectGoPackagesReadsATempModule(t *testing.T) {
	runnerTestGo(t)
	dir := selectGoWriteModule(t, map[string]string{
		"go.mod":      "module example.com/tmp\n\ngo 1.21\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b.go":      "package b\n\nimport \"example.com/tmp/a\"\n\nfunc B() int { return a.A() }\n",
		"c/c.go":      "package c\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := SelectGoPackages(ctx, dir, []string{"a/a.go"})
	if err != nil {
		t.Fatalf("SelectGoPackages(temp module) error = %v, want nil", err)
	}
	if got, want := strings.Join(selectedPackages(res), " "), "example.com/tmp/a example.com/tmp/b"; got != want {
		t.Errorf("SelectGoPackages(a/a.go) = %q, want %q", got, want)
	}
}

// TestSelectGoPackagesRefusesAFailedGoList: a go.mod go cannot read makes `go list` exit non-zero,
// which is an error that names go's complaint and selects nothing, never an empty green selection.
func TestSelectGoPackagesRefusesAFailedGoList(t *testing.T) {
	runnerTestGo(t)
	dir := selectGoWriteModule(t, map[string]string{
		"go.mod": "this is not a go.mod\n",
		"a/a.go": "package a\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := SelectGoPackages(ctx, dir, []string{"a/a.go"})
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("SelectGoPackages(broken go.mod) error = %v, want a *core.Error", err)
	}
	if !strings.Contains(ce.Found, "go.mod") {
		t.Errorf("error Found = %q, want go's stderr naming go.mod", ce.Found)
	}
	if len(res.Selected) != 0 {
		t.Errorf("SelectGoPackages(broken go.mod) selected %v, want nothing", selectedPackages(res))
	}
}

func TestOfflineGoEnvFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		parent []string
		want   []string
	}{
		{"a proxy is replaced and -mod=mod becomes readonly", []string{"PATH=/bin", "GOPROXY=https://proxy.example", "GOFLAGS=-mod=mod"},
			[]string{"PATH=/bin", "GOPROXY=off", "GOFLAGS=-mod=readonly"}},
		{"-mod=vendor is kept, so a vendored module still lists", []string{"GOFLAGS=-mod=vendor"},
			[]string{"GOPROXY=off", "GOFLAGS=-mod=vendor"}},
		{"other flags are kept and only -mod=mod is rewritten", []string{"GOFLAGS=-tags=x -mod=mod -v"},
			[]string{"GOPROXY=off", "GOFLAGS=-tags=x -mod=readonly -v"}},
		{"no GOFLAGS gets no mod flag", []string{"PATH=/bin"}, []string{"PATH=/bin", "GOPROXY=off"}},
		{"the names match without case, as Windows does", []string{"goproxy=direct"}, []string{"GOPROXY=off"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := offlineGoEnv(tc.parent); !slices.Equal(got, tc.want) {
				t.Errorf("offlineGoEnv(%v) = %v, want %v", tc.parent, got, tc.want)
			}
		})
	}
}
