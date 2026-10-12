package repocheck

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// The rule names of check repo/boundaries (DR-17, SPEC §6.1).
const (
	rulePrivateImport  = "private_import"
	ruleUndeclaredEdge = "undeclared_edge"
)

// Finding is one broken repository rule; Path is repository-relative and slash-separated, or a
// module path for check repo/deps. Line is 1-based, or 0 when the rule has no position.
type Finding struct {
	Rule string
	Path string
	Line int
}

// listedModule is the Module object of one `go list -json` package.
type listedModule struct {
	Path string
	Main bool
}

// listedPackage is the subset of one `go list -deps -json` package that the checks read.
type listedPackage struct {
	ImportPath string
	Standard   bool
	Module     *listedModule
	Imports    []string
}

// own reports whether p belongs to the main module.
func (p listedPackage) own() bool { return p.Module != nil && p.Module.Main }

// decodeGoList reads the back-to-back JSON objects of `go list -deps -json`. No package listed is
// an error: absence is not evidence that anything was checked (DR-6).
func decodeGoList(out []byte) ([]listedPackage, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []listedPackage
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		return nil, errors.New("read go list output: no package listed")
	}
	return pkgs, nil
}

// mainModule returns the path of the main module, or an error when no listed package is part of it.
func mainModule(pkgs []listedPackage) (string, error) {
	for _, p := range pkgs {
		if p.own() {
			return p.Module.Path, nil
		}
	}
	return "", errors.New("read go list output: no package of the main module listed")
}

// Boundaries compares the imports of the main module's packages, as `go list -deps -json ./...`
// prints them, with the declared module edges: edges maps each module directory to the module
// directories it may import (SPEC §6.1). A module may import its own packages freely; another
// module only through its root package (private_import) and only over a declared edge
// (undeclared_edge). A package outside every declared module may import no own package.
func Boundaries(edges map[string][]string, goList []byte) ([]Finding, error) {
	pkgs, err := decodeGoList(goList)
	if err != nil {
		return nil, err
	}
	modPath, err := mainModule(pkgs)
	if err != nil {
		return nil, err
	}
	b := boundaryCheck{edges: edges, prefix: modPath + "/"}
	var found []Finding
	for _, p := range pkgs {
		if p.own() {
			found = append(found, b.checkPackage(p)...)
		}
	}
	sortFindings(found)
	return found, nil
}

// boundaryCheck holds the declared edges and the import-path prefix of the main module.
type boundaryCheck struct {
	edges  map[string][]string
	prefix string
}

// checkPackage returns the findings of each import of p that reaches another module.
func (b boundaryCheck) checkPackage(p listedPackage) []Finding {
	dir := strings.TrimPrefix(p.ImportPath, b.prefix)
	from := b.moduleOf(dir)
	var found []Finding
	for _, imp := range p.Imports {
		target, ok := strings.CutPrefix(imp, b.prefix)
		if !ok {
			continue
		}
		to := b.moduleOf(target)
		if from != "" && to == from {
			continue
		}
		if to != "" && target != to {
			found = append(found, Finding{Rule: rulePrivateImport, Path: dir})
		}
		if to == "" || from == "" || !slices.Contains(b.edges[from], to) {
			found = append(found, Finding{Rule: ruleUndeclaredEdge, Path: dir})
		}
	}
	return found
}

// moduleOf returns the declared module that holds the package directory dir: the longest module
// directory that is dir or one of its parents, or "" when no declared module holds it.
func (b boundaryCheck) moduleOf(dir string) string {
	best := ""
	for m := range b.edges {
		if (dir == m || strings.HasPrefix(dir, m+"/")) && len(m) > len(best) {
			best = m
		}
	}
	return best
}

// sortFindings orders findings by path, line and rule, so a report is the same on every run.
func sortFindings(found []Finding) {
	slices.SortStableFunc(found, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Rule, b.Rule))
	})
}
