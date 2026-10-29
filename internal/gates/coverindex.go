package gates

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

const (
	// coverFormatVersion is the only format of the coverage index file this engine reads.
	coverFormatVersion = 2
	// coverKind and staleKind are the reasons of SPEC-contracts §8.2 that this selector gives.
	coverKind = SelectorCoverage
	staleKind = "stale"
	// coverFullSelection is the full test selection the test step runs without the index: every
	// package of the module, the suite of the module (SPEC L69). isFullGoTest reads the same one.
	coverFullSelection = "./..."
)

// CoverIndex is the coverage reverse index of SPEC L66: for each file of the module, the test
// packages whose tests ran one of its statements, written by a green full run. Built is when the
// run ended; it is a note, never a staleness rule (DR-2).
type CoverIndex struct {
	FormatVersion int       `json:"formatVersion"`
	Built         time.Time `json:"built"`
	// Hashes maps the slash directory of each package of the module to the content hash of its Go
	// files when the index was built. An entry is fresh while the hash of its package is unchanged.
	Hashes map[string]string `json:"hashes"`
	// NotMeasured lists the test packages, spelled as Files spells them, whose coverage run wrote no
	// profile. Such a package may cover any file, so the index selects nothing by coverage (DR-6).
	NotMeasured []string `json:"notMeasured,omitempty"`
	// Files maps a slash file path relative to the repository root to its test packages, each
	// `./<dir>` or `.`, sorted and free of duplicates (the spelling of declaredDeps tests).
	Files map[string][]string `json:"files"`
}

// CoverProfiles holds the cover profile text of each test package, keyed by the slash directory of
// the package relative to the repository root.
type CoverProfiles map[string][]byte

// CoverCollection is what one coverage run measured: the profiles, the directory of each import
// path of the module, which turns a profile's file names into repository paths, the content hash of
// the Go files of each package (HashGoDirs), and the slash directories of the test packages whose
// run wrote no profile.
type CoverCollection struct {
	Profiles    CoverProfiles
	DirOf       map[string]string
	Hashes      map[string]string
	NotMeasured []string
}

// CoverSelection is one test package a change selects through the index. Kind is "coverage" or
// "stale", the reason word of SPEC-contracts §8.2, and Reason says which files and why.
type CoverSelection struct {
	Package string
	Kind    string
	Reason  string
}

// CoverSelectResult is the outcome of CoverIndex.Select. Selected is sorted by package, then kind.
// Unmapped lists the changed files the index does not know, sorted: a file of a fresh package that
// no test covered, or a file in no Go package. The caller falls back to the suite of the module and
// records a `map_gap` (SPEC L66, L69).
type CoverSelectResult struct {
	Selected []CoverSelection
	Unmapped []string
}

// BuildCoverIndex inverts the profiles of c: a file that a profile counts at least once under a
// block that ran maps to that profile's package. A profile that is not a cover profile is an error,
// so an unread profile never builds an index that selects nothing (DR-6). An import path that DirOf
// does not know is not part of the module and is left out. The package hashes and the packages
// that were not measured are kept.
func BuildCoverIndex(c CoverCollection, built time.Time) (CoverIndex, error) {
	idx := CoverIndex{FormatVersion: coverFormatVersion, Built: built.UTC(), Hashes: maps.Clone(c.Hashes),
		Files: map[string][]string{}}
	for _, dir := range c.NotMeasured {
		idx.NotMeasured = append(idx.NotMeasured, testDir(dir))
	}
	slices.Sort(idx.NotMeasured)
	idx.NotMeasured = slices.Compact(idx.NotMeasured)
	for _, dir := range slices.Sorted(maps.Keys(c.Profiles)) {
		files, err := coveredFiles(c.Profiles[dir], c.DirOf)
		if err != nil {
			return CoverIndex{}, coverError("a cover profile of "+dir, err.Error(), err)
		}
		for _, f := range files {
			idx.Files[f] = append(idx.Files[f], testDir(dir))
		}
	}
	for f, pkgs := range idx.Files {
		slices.Sort(pkgs)
		idx.Files[f] = slices.Compact(pkgs)
	}
	return idx, nil
}

// coveredFiles returns the sorted repository paths of the files whose blocks ran in profile.
func coveredFiles(profile []byte, dirOf map[string]string) ([]string, error) {
	sc := bufio.NewScanner(bytes.NewReader(profile))
	if !sc.Scan() || !strings.HasPrefix(sc.Text(), "mode: ") {
		return nil, errors.New("no `mode:` line first")
	}
	seen := map[string]bool{}
	for sc.Scan() {
		name, count, err := profileBlock(strings.TrimRight(sc.Text(), "\r"))
		if err != nil {
			return nil, err
		}
		dir, ok := dirOf[path.Dir(name)]
		if ok && count > 0 {
			seen[path.Join(dir, path.Base(name))] = true
		}
	}
	return slices.Sorted(maps.Keys(seen)), sc.Err()
}

// profileBlock reads one block line, `<import path>/<file>:<from>,<to> <statements> <count>`, and
// returns the file name and the run count.
func profileBlock(line string) (name string, count int, err error) {
	fields := strings.Fields(line)
	colon := strings.LastIndex(line, ":")
	if len(fields) != 3 || colon < 1 {
		return "", 0, fmt.Errorf("line %q is not a block", line)
	}
	if _, convErr := strconv.Atoi(fields[1]); convErr != nil {
		return "", 0, fmt.Errorf("line %q: statements: %w", line, convErr)
	}
	count, err = strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, fmt.Errorf("line %q: count: %w", line, err)
	}
	return line[:colon], count, nil
}

// stale reports whether the entry of file f is stale against current, the content hash of the Go
// files of each package now: its package's hash differs from the one stored with the index, or one
// side has no package there. A file in a directory that is a Go package on neither side has no
// entry and is not stale (DR-2).
func (idx CoverIndex) stale(f string, current map[string]string) bool {
	dir := path.Dir(f)
	now, isPkg := current[dir]
	then, wasPkg := idx.Hashes[dir]
	return isPkg != wasPkg || now != then
}

// Select returns the test packages the changed files, slash paths relative to the repository root,
// affect; current is the content hash of the Go files of each package now (HashGoDirs). A fresh
// entry gives a file the packages that covered it. A stale entry, and any file of a package while a
// test package is not measured, says "stale" and takes the full selection the test step runs
// without the index; it never selects a directory, which may be no Go package (SPEC L66, L69,
// DR-6). It does no IO.
func (idx CoverIndex) Select(changed []string, current map[string]string) CoverSelectResult {
	var res CoverSelectResult
	byKey := map[[2]string][]string{}
	for _, f := range cleanChanged(changed) {
		_, inPkg := current[path.Dir(f)]
		switch pkgs := idx.Files[f]; {
		case idx.stale(f, current) || (inPkg && len(idx.NotMeasured) > 0):
			key := [2]string{coverFullSelection, staleKind}
			byKey[key] = append(byKey[key], f)
		case len(pkgs) == 0:
			res.Unmapped = append(res.Unmapped, f)
		default:
			for _, pkg := range pkgs {
				key := [2]string{pkg, coverKind}
				byKey[key] = append(byKey[key], f)
			}
		}
	}
	keys := slices.SortedFunc(maps.Keys(byKey), func(a, b [2]string) int {
		return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1])
	})
	for _, k := range keys {
		res.Selected = append(res.Selected, CoverSelection{Package: k[0], Kind: k[1],
			Reason: idx.reason(k, byKey[k])})
	}
	return res
}

// reason is the text of a selection of package key[0] by the files that changed.
func (idx CoverIndex) reason(key [2]string, files []string) string {
	list := strings.Join(files, ", ")
	switch {
	case key[1] == staleKind && len(idx.NotMeasured) > 0:
		return fmt.Sprintf("stale: %s (coverage of %s not_measured)", list, strings.Join(idx.NotMeasured, ", "))
	case key[1] == staleKind:
		return "stale: " + list + " (the Go files of their package differ from the index)"
	}
	return "coverage: " + list + " covered by " + key[0]
}

// testDir spells the slash directory dir as a tests entry: `.` or `./<dir>`.
func testDir(dir string) string {
	if dir == "." || dir == "" {
		return "."
	}
	return "./" + dir
}

// coverError is the refusal of a coverage index or profile that cannot be read.
func coverError(expected, found string, err error) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found,
		Next: "run the gate on a green tree to rebuild the coverage index", Err: err}
}
