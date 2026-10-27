package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// GoSelection is one Go package whose tests a change selects, with the reason (SPEC L66).
type GoSelection struct {
	// Package is the import path, the argument `go test` takes.
	Package string
	// Dir is the package directory relative to the repository root, "." for the root.
	Dir string
	// Reason is "changed: <files>", "import: <changed dir> ← … ← <dir>", or the same import
	// chain followed by " (test)" for a package whose own test imports the changed one.
	Reason string
	// HasTests is false for a package with no test file; the caller decides whether a changed
	// package with no test is red (`untested_change`).
	HasTests bool
}

// GoSelectResult is the outcome of GoGraph.Select.
type GoSelectResult struct {
	// Selected is sorted by import path.
	Selected []GoSelection
	// Unmapped lists the changed files that no Go package owns (a go.mod, a document, a file in a
	// directory that holds no package). The import graph does not know them: the caller falls back
	// to the suite of the module and records a `map_gap` (SPEC L66, L69).
	Unmapped []string
}

// GoGraph is the import graph of the packages of one module under a repository root, read from the
// `go list -deps -test -json` records. A test variant, the generated test main, a standard package
// and a package of another module are not nodes.
type GoGraph struct {
	nodes map[string]*goNode
	// byDir maps a slash directory relative to the root to the package in it.
	byDir map[string]string
	// byFile maps the slash path of a non-Go file a package owns (an embedded file, a C file) to it.
	byFile map[string]goOwner
	// rev maps a package to the packages whose production code imports it.
	rev map[string][]string
	// testRev maps a package to the packages whose own tests import it.
	testRev map[string][]string
}

// goNode is one package of the graph.
type goNode struct {
	importPath string
	dir        string
	hasTests   bool
}

// goOwner is the package that owns a file and whether the file is a test file.
type goOwner struct {
	pkg  string
	test bool
}

// goListRecord is the part of the JSON object of `go list -json` the graph reads.
type goListRecord struct {
	ImportPath string
	Dir        string
	ForTest    string
	Standard   bool
	DepOnly    bool
	Error      *struct{ Err string }

	GoFiles, CgoFiles, CFiles, CXXFiles, HFiles, SFiles, SysoFiles, IgnoredGoFiles []string
	TestGoFiles, XTestGoFiles                                                      []string
	EmbedFiles, TestEmbedFiles, XTestEmbedFiles                                    []string

	Imports, TestImports, XTestImports []string
}

// ParseGoList reads the concatenated JSON objects that `go list -deps -test -json ./...` prints
// and builds the graph of the packages under root, the repository root. The plain record of a
// package holds its own files, its imports and the imports of its tests, so the test variants add
// nothing and are left out. Output without a package of the module, a record that carries an
// error, or text that is not JSON is an error, so a selection is never taken from an unread graph
// (DR-6).
func ParseGoList(data []byte, root string) (*GoGraph, error) {
	g := &GoGraph{nodes: map[string]*goNode{}, byDir: map[string]string{}, byFile: map[string]goOwner{}}
	recs, err := decodeGoList(data)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if !graphRecord(rec) {
			continue
		}
		dir, ok := relativeDir(root, rec.Dir)
		if !ok {
			continue
		}
		if rec.Error != nil {
			return nil, goListError("a package record without an error", rec.ImportPath+": "+rec.Error.Err, nil)
		}
		g.add(rec, dir)
	}
	if len(g.nodes) == 0 {
		return nil, goListError("at least one package of the module under "+root, "no package", nil)
	}
	g.link(recs)
	return g, nil
}

// decodeGoList decodes every JSON object of the stream.
func decodeGoList(data []byte) ([]goListRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var recs []goListRecord
	for {
		var rec goListRecord
		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			return recs, nil
		}
		if err != nil {
			return nil, goListError("the JSON objects of `go list -json`", err.Error(), err)
		}
		recs = append(recs, rec)
	}
}

// graphRecord reports whether rec is the plain record of a package of the module: not a standard
// package, not a dependency only, not a test variant ("p [p.test]"), not the generated test main.
func graphRecord(rec goListRecord) bool {
	return !rec.Standard && !rec.DepOnly && rec.ForTest == "" &&
		!strings.Contains(rec.ImportPath, " [") && !strings.HasSuffix(rec.ImportPath, ".test")
}

// relativeDir returns dir as a slash path relative to root, and false for a directory outside it.
func relativeDir(root, dir string) (string, bool) {
	rel, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(dir))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// add records the package of rec and the files it owns.
func (g *GoGraph) add(rec goListRecord, dir string) {
	g.nodes[rec.ImportPath] = &goNode{importPath: rec.ImportPath, dir: dir,
		hasTests: len(rec.TestGoFiles)+len(rec.XTestGoFiles) > 0}
	g.byDir[dir] = rec.ImportPath
	own := func(test bool, lists ...[]string) {
		for _, list := range lists {
			for _, name := range list {
				g.byFile[path.Join(dir, name)] = goOwner{pkg: rec.ImportPath, test: test}
			}
		}
	}
	own(false, rec.GoFiles, rec.CgoFiles, rec.CFiles, rec.CXXFiles, rec.HFiles, rec.SFiles,
		rec.SysoFiles, rec.IgnoredGoFiles, rec.EmbedFiles)
	own(true, rec.TestGoFiles, rec.XTestGoFiles, rec.TestEmbedFiles, rec.XTestEmbedFiles)
}

// link fills the reverse edges once every package is known, so an import of a standard or
// foreign package is dropped. Both lists are sorted and free of duplicates.
func (g *GoGraph) link(recs []goListRecord) {
	g.rev, g.testRev = map[string][]string{}, map[string][]string{}
	for _, rec := range recs {
		if g.nodes[rec.ImportPath] != nil && graphRecord(rec) {
			g.addEdges(g.rev, rec.ImportPath, rec.Imports)
			g.addEdges(g.testRev, rec.ImportPath, rec.TestImports, rec.XTestImports)
		}
	}
	sortEdges(g.rev)
	sortEdges(g.testRev)
}

// addEdges records from as an importer, in into, of each package of the lists that the graph holds.
func (g *GoGraph) addEdges(into map[string][]string, from string, lists ...[]string) {
	for _, list := range lists {
		for _, dep := range list {
			if g.nodes[dep] != nil {
				into[dep] = append(into[dep], from)
			}
		}
	}
}

// sortEdges sorts each list of edges and drops the duplicates.
func sortEdges(edges map[string][]string) {
	for dep, list := range edges {
		slices.Sort(list)
		edges[dep] = slices.Compact(list)
	}
}

// Select maps the changed files, slash paths relative to the repository root, to packages and
// returns each package whose tests the change can affect, with the reason: the package of a
// changed file; every package that imports a package whose production code changed, through any
// number of imports; and every package whose own test imports one of those. A changed test file
// selects its package only, because no other package imports a test file. An import that only a
// test makes does not carry the change on to the importers of that package.
func (g *GoGraph) Select(changed []string) GoSelectResult {
	direct, seeds, unmapped := g.mapFiles(changed)
	parent := g.importers(seeds)
	reasons := map[string]string{}
	for pkg, files := range direct {
		reasons[pkg] = "changed: " + strings.Join(files, ", ")
	}
	for pkg := range parent {
		if _, ok := reasons[pkg]; !ok {
			reasons[pkg] = "import: " + g.chain(parent, pkg, "")
		}
	}
	g.addTesters(reasons, parent)
	return GoSelectResult{Selected: g.selections(reasons), Unmapped: unmapped}
}

// addTesters selects every package whose own test imports a package that parent reached and that
// has no reason yet.
func (g *GoGraph) addTesters(reasons map[string]string, parent map[string]string) {
	for _, pkg := range sortedKeys(parent) {
		for _, tester := range g.testRev[pkg] {
			if _, ok := reasons[tester]; !ok {
				reasons[tester] = "import: " + g.chain(parent, pkg, tester) + " (test)"
			}
		}
	}
}

// mapFiles assigns each changed file to its package: direct holds the sorted files per package,
// seeds the packages with a changed non-test file, and unmapped the files no package owns.
func (g *GoGraph) mapFiles(changed []string) (direct map[string][]string, seeds, unmapped []string) {
	direct = map[string][]string{}
	prod := map[string]bool{}
	for _, file := range changed {
		file = path.Clean(filepath.ToSlash(file))
		owner, ok := g.owner(file)
		if !ok {
			unmapped = append(unmapped, file)
			continue
		}
		direct[owner.pkg] = append(direct[owner.pkg], file)
		prod[owner.pkg] = prod[owner.pkg] || !owner.test
	}
	for pkg, files := range direct {
		slices.Sort(files)
		direct[pkg] = slices.Compact(files)
	}
	slices.Sort(unmapped)
	return direct, sortedKeysIf(prod), slices.Compact(unmapped)
}

// owner returns the package that owns file: any .go file of a package directory, else a file the
// package lists (an embedded file, a C file).
func (g *GoGraph) owner(file string) (goOwner, bool) {
	if strings.HasSuffix(file, ".go") {
		pkg, ok := g.byDir[path.Dir(file)]
		return goOwner{pkg: pkg, test: strings.HasSuffix(file, "_test.go")}, ok
	}
	owner, ok := g.byFile[file]
	return owner, ok
}

// importers walks the reverse production imports from seeds, breadth first, and returns for each
// reached package the package it was reached from; a seed maps to "".
func (g *GoGraph) importers(seeds []string) map[string]string {
	parent := map[string]string{}
	queue := slices.Clone(seeds)
	for _, s := range seeds {
		parent[s] = ""
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range g.rev[cur] {
			if _, seen := parent[next]; !seen {
				parent[next] = cur
				queue = append(queue, next)
			}
		}
	}
	return parent
}

// chain renders the import chain from the changed seed down to pkg, then tester when it is not
// empty, as directories joined by " ← ".
func (g *GoGraph) chain(parent map[string]string, pkg, tester string) string {
	var dirs []string
	if tester != "" {
		dirs = append(dirs, g.nodes[tester].dir)
	}
	for cur := pkg; cur != ""; cur = parent[cur] {
		dirs = append(dirs, g.nodes[cur].dir)
	}
	slices.Reverse(dirs)
	return strings.Join(dirs, " ← ")
}

// selections lists reasons as GoSelection values sorted by import path.
func (g *GoGraph) selections(reasons map[string]string) []GoSelection {
	var out []GoSelection
	for _, pkg := range sortedKeys(reasons) {
		n := g.nodes[pkg]
		out = append(out, GoSelection{Package: pkg, Dir: n.dir, Reason: reasons[pkg], HasTests: n.hasTests})
	}
	return out
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedKeysIf returns the keys of m whose value is true, in order.
func sortedKeysIf(m map[string]bool) []string {
	var keys []string
	for _, k := range sortedKeys(m) {
		if m[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

// goListError is the refusal of go list output that cannot be read.
func goListError(expected, found string, err error) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found,
		Next: "run `go list -deps -test -json ./...` in the module and fix what it reports, then select again",
		Err:  err}
}
