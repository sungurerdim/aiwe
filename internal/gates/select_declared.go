package gates

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// DeclaredDep is one row of the file-level declaredDeps of gate.json (SPEC L70, SPEC-contracts
// §8.3): non-code dependencies, such as the SPEC or data files, the test packages a change to one
// of them selects, and why. Every row has a Reason. A row with no tests exempts its files from the
// suite of the module on purpose, and the Reason says why. ParseDefinition refuses a row without
// a path or a reason.
type DeclaredDep struct {
	// Paths are the globs of the dependencies, in the syntax of a step's inputs: slash paths
	// relative to the repository root, path.Match per segment, and a `**` segment for any depth.
	Paths []string `json:"paths"`
	// Tests are the test packages the row selects, each `./<dir>`: the clean slash directory of
	// the package relative to the repository root (`.` for the root package). ParseDefinition
	// refuses a pattern, an import path and any other spelling.
	Tests []string `json:"tests,omitempty"`
	// Reason is why the row exists, or why it selects no test.
	Reason string `json:"reason"`
}

// DeclaredSelection is one test package a change selects through declaredDeps, with the reason
// (SPEC L66). It has the Package and Reason fields of GoSelection; it has no Dir and no HasTests,
// because a row names its packages and no import graph is read.
type DeclaredSelection struct {
	// Package is the entry of a row's Tests, `./<dir>` or `.`. It is not an import path: #598
	// joins it on GoSelection.Dir, where path.Clean(Package) == Dir.
	Package string
	// Reason is "declared: <changed files> match <row paths>", followed by " (<row reason>)".
	// A package named by several rows carries each reason once, joined by "; ", in row order.
	Reason string
}

// DeclaredExempt is a changed file that a row without tests declares: the file selects no test on
// purpose, and the row says why.
type DeclaredExempt struct {
	File string
	// Reason is the reason of the row; several rows give theirs joined by "; ", in row order.
	Reason string
}

// DeclaredSelectResult is the outcome of SelectDeclared. Every changed file is in exactly one of
// Selected (a row with tests declares it), Exempt (only rows without tests declare it) and Unmapped
// (no row declares it).
type DeclaredSelectResult struct {
	// Selected is sorted by package.
	Selected []DeclaredSelection
	// Exempt is sorted by file.
	Exempt []DeclaredExempt
	// Unmapped lists the changed files that no row declares, sorted and free of duplicates, as
	// slash paths. The declaredDeps do not know them: the caller falls back to the suite of the
	// module and records a `map_gap` (SPEC L69).
	Unmapped []string
}

// declaredIndex is what the rows say about a change.
type declaredIndex struct {
	// reasons holds, per package, the reason of each row with tests that selects it, in row order.
	reasons map[string][]string
	// selected is the set of files some row with tests declares.
	selected map[string]bool
	// exempt holds, per file no row with tests declares, the reason of each row without tests.
	exempt map[string][]string
	// known is the set of files some row declares.
	known map[string]bool
}

// SelectDeclared returns the packages whose tests a change of the files in changed (paths relative
// to the repository root) selects through rows, the files a row without tests exempts, and the
// files no row declares. A file selects the tests of every row that has a glob matching it. It
// does no IO and reads no file.
func SelectDeclared(rows []DeclaredDep, changed []string) DeclaredSelectResult {
	var res DeclaredSelectResult
	files := cleanChanged(changed)
	idx := indexDeclared(rows, files)
	for _, pkg := range slices.Sorted(maps.Keys(idx.reasons)) {
		res.Selected = append(res.Selected, DeclaredSelection{Package: pkg, Reason: strings.Join(idx.reasons[pkg], "; ")})
	}
	for _, f := range files {
		switch {
		case idx.selected[f]: // already in Selected through its package
		case idx.known[f]:
			res.Exempt = append(res.Exempt, DeclaredExempt{File: f, Reason: strings.Join(idx.exempt[f], "; ")})
		default:
			res.Unmapped = append(res.Unmapped, f)
		}
	}
	return res
}

// indexDeclared reads the rows against the sorted changed files.
func indexDeclared(rows []DeclaredDep, files []string) declaredIndex {
	idx := declaredIndex{reasons: map[string][]string{}, selected: map[string]bool{},
		exempt: map[string][]string{}, known: map[string]bool{}}
	for _, row := range rows {
		hit := slices.DeleteFunc(slices.Clone(files), func(f string) bool { return !rowDeclares(row, f) })
		idx.addFiles(row, hit)
		idx.addPackages(row, hit)
	}
	return idx
}

// addFiles records the files hit that a row declares: selected by a row with tests, else exempt
// with the reason of the row.
func (idx declaredIndex) addFiles(row DeclaredDep, hit []string) {
	for _, f := range hit {
		idx.known[f] = true
		if len(row.Tests) > 0 {
			idx.selected[f] = true
		} else {
			idx.exempt[f] = append(idx.exempt[f], strings.TrimSpace(row.Reason))
		}
	}
}

// addPackages records the reason a row selects each of its packages after the files hit changed.
func (idx declaredIndex) addPackages(row DeclaredDep, hit []string) {
	if len(hit) == 0 {
		return
	}
	reason := declaredReason(row, hit)
	for _, pkg := range row.Tests {
		if !slices.Contains(idx.reasons[pkg], reason) {
			idx.reasons[pkg] = append(idx.reasons[pkg], reason)
		}
	}
}

// rowDeclares reports whether a glob of row matches file. A row with no glob declares nothing.
func rowDeclares(row DeclaredDep, file string) bool {
	return len(row.Paths) > 0 && matchesInputs(file, row.Paths)
}

// declaredReason is the reason a row gives for selecting its packages after the files in hit
// changed.
func declaredReason(row DeclaredDep, hit []string) string {
	reason := "declared: " + strings.Join(hit, ", ") + " match " + strings.Join(row.Paths, ", ")
	if why := strings.TrimSpace(row.Reason); why != "" {
		reason += " (" + why + ")"
	}
	return reason
}

// cleanChanged returns the changed files as clean slash paths, sorted and free of duplicates.
func cleanChanged(changed []string) []string {
	files := make([]string, len(changed))
	for i, f := range changed {
		files[i] = path.Clean(strings.ReplaceAll(f, `\`, `/`))
	}
	slices.Sort(files)
	return slices.Compact(files)
}
