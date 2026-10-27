package gates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// selectGoFixture is the shape `go list -deps -test -json ./...` prints for the module
// example.com/m at /repo: a imports nothing in the module, b imports a, d imports b, c stands
// alone, and the external test of a imports c. It holds the variants the test flag adds
// ("a [a.test]", "a_test [a.test]", "a.test") and one standard package that -deps adds.
const selectGoFixture = `{"Dir":"/goroot/src/fmt","ImportPath":"fmt","Name":"fmt","Standard":true,"DepOnly":true,"GoFiles":["print.go"],"Imports":["os"]}
{"Dir":"/repo/a","ImportPath":"example.com/m/a","Name":"a","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["a.go"],"TestGoFiles":["a_test.go"],"XTestGoFiles":["a_x_test.go"],"EmbedFiles":["data/rules.txt"],"Imports":["fmt"],"TestImports":["testing"],"XTestImports":["example.com/m/c","testing"]}
{"Dir":"/repo/b","ImportPath":"example.com/m/b","Name":"b","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["b.go"],"Imports":["example.com/m/a","fmt"]}
{"Dir":"/repo/c","ImportPath":"example.com/m/c","Name":"c","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["c.go"],"TestGoFiles":["c_test.go"],"Imports":["fmt"],"TestImports":["testing"]}
{"Dir":"/repo/d","ImportPath":"example.com/m/d","Name":"d","Module":{"Path":"example.com/m","Main":true,"Dir":"/repo"},"GoFiles":["d.go"],"Imports":["example.com/m/b"]}
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

func TestGoImportsSelectsDependentPackages(t *testing.T) {
	const m = "example.com/m/"
	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{"a changed selects a and its importer b and b's importer d, not c",
			[]string{"a/a.go"}, []string{m + "a", m + "b", m + "d"}},
		{"b changed selects b and d only, never its dependency a",
			[]string{"b/b.go"}, []string{m + "b", m + "d"}},
		{"c changed selects c and a, whose external test imports c, but not b or d",
			[]string{"c/c.go"}, []string{m + "a", m + "c"}},
		{"d changed selects d alone",
			[]string{"d/d.go"}, []string{m + "d"}},
		{"two changed files select the union once",
			[]string{"a/a.go", "b/b.go"}, []string{m + "a", m + "b", m + "d"}},
		{"a deleted go file still selects its package",
			[]string{"c/gone.go"}, []string{m + "a", m + "c"}},
		{"an embedded file selects the package that embeds it",
			[]string{"a/data/rules.txt"}, []string{m + "a", m + "b", m + "d"}},
	}
	g := selectGoGraph(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := g.Select(tc.changed)
			if got := selectedPackages(res); !slices.Equal(got, tc.want) {
				t.Errorf("Select(%v) packages = %v, want %v", tc.changed, got, tc.want)
			}
			if len(res.Unmapped) != 0 {
				t.Errorf("Select(%v) Unmapped = %v, want none", tc.changed, res.Unmapped)
			}
		})
	}
}

func TestGoImportsChangedTestFileSelectsItsPackage(t *testing.T) {
	const m = "example.com/m/"
	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{"an in-package test file selects its package and no importer",
			[]string{"a/a_test.go"}, []string{m + "a"}},
		{"an external test file selects its package and no importer",
			[]string{"a/a_x_test.go"}, []string{m + "a"}},
		{"a test file of c selects c and not a, which imports c only in its own test",
			[]string{"c/c_test.go"}, []string{m + "c"}},
		{"a test file and a source file of one package select it once with the importers",
			[]string{"a/a_test.go", "a/a.go"}, []string{m + "a", m + "b", m + "d"}},
	}
	g := selectGoGraph(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := g.Select(tc.changed)
			if got := selectedPackages(res); !slices.Equal(got, tc.want) {
				t.Errorf("Select(%v) packages = %v, want %v", tc.changed, got, tc.want)
			}
		})
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
	want := map[string]bool{"example.com/m/a": true, "example.com/m/b": false, "example.com/m/d": false}
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

func TestSelectGoPackagesReadsATempModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool on PATH")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":      "module example.com/tmp\n\ngo 1.21\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b.go":      "package b\n\nimport \"example.com/tmp/a\"\n\nfunc B() int { return a.A() }\n",
		"c/c.go":      "package c\n",
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
