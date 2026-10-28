package gates

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// DeclaredDep is one row of the file-level declaredDeps of gate.json (SPEC L70, SPEC-contracts
// §8.3): non-code dependencies, such as the SPEC or data files, and the test packages a change to
// one of them selects. A row that selects no test says why in Reason; ParseDefinition refuses a row
// with neither.
type DeclaredDep struct {
	// Paths are the globs of the dependencies, in the syntax of a step's inputs: slash paths
	// relative to the repository root, path.Match per segment, and a `**` segment for any depth.
	Paths []string `json:"paths"`
	// Tests are the test packages the row selects, as the arguments `go test` takes.
	Tests []string `json:"tests,omitempty"`
	// Reason is why the row exists, or why it selects no test.
	Reason string `json:"reason,omitempty"`
}

// DeclaredSelection is one test package a change selects through declaredDeps, with the reason
// (SPEC L66). It has the Package and Reason fields of GoSelection; it has no Dir and no HasTests,
// because a row names its packages and no import graph is read.
type DeclaredSelection struct {
	// Package is the entry of a row's Tests, verbatim.
	Package string
	// Reason is "declared: <changed files> match <row paths>", followed by " (<row reason>)" when
	// the row has one. A package named by several rows carries each reason, joined by "; ", in the
	// order of the rows.
	Reason string
}

// DeclaredSelectResult is the outcome of SelectDeclared.
type DeclaredSelectResult struct {
	// Selected is sorted by package.
	Selected []DeclaredSelection
	// Unmapped lists the changed files that no row declares, sorted and free of duplicates, as
	// slash paths. The declaredDeps do not know them: the caller falls back to the suite of the
	// module and records a `map_gap` (SPEC L69). A file that a row declares with no tests is
	// mapped: the row says why it selects nothing.
	Unmapped []string
}

// SelectDeclared returns the packages whose tests a change of the files in changed (paths relative
// to the repository root) selects through rows, and the files no row declares. A file selects the
// tests of every row that has a glob matching it. It does no IO and reads no file.
func SelectDeclared(rows []DeclaredDep, changed []string) DeclaredSelectResult {
	var res DeclaredSelectResult
	files := cleanChanged(changed)
	reasons, known := declaredReasons(rows, files)
	for _, pkg := range slices.Sorted(maps.Keys(reasons)) {
		res.Selected = append(res.Selected, DeclaredSelection{Package: pkg, Reason: strings.Join(reasons[pkg], "; ")})
	}
	for _, f := range files {
		if !known[f] {
			res.Unmapped = append(res.Unmapped, f)
		}
	}
	return res
}

// declaredReasons returns, per package, the reason of each row that selects it, in row order, and
// the set of files some row declares.
func declaredReasons(rows []DeclaredDep, files []string) (reasons map[string][]string, known map[string]bool) {
	reasons, known = map[string][]string{}, map[string]bool{}
	for _, row := range rows {
		hit := slices.DeleteFunc(slices.Clone(files), func(f string) bool { return !rowDeclares(row, f) })
		reason := declaredReason(row, hit)
		for _, f := range hit {
			known[f] = true
		}
		for _, pkg := range row.Tests {
			if len(hit) > 0 && !slices.Contains(reasons[pkg], reason) {
				reasons[pkg] = append(reasons[pkg], reason)
			}
		}
	}
	return reasons, known
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
