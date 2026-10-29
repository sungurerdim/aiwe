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
	coverFormatVersion = 1
	// coverMaxAge is how long an index selects by coverage; an older index is stale (SPEC L66).
	coverMaxAge = 7 * 24 * time.Hour
	// coverKind and staleKind are the reasons of SPEC-contracts §8.2 that this selector gives.
	coverKind = "coverage"
	staleKind = "stale"
)

// CoverIndex is the coverage reverse index of SPEC L66: for each file of the module, the test
// packages whose tests ran one of its statements, written by a green full run. Built is when the
// run ended. A zero Built is an index that was never built.
type CoverIndex struct {
	FormatVersion int       `json:"formatVersion"`
	Built         time.Time `json:"built"`
	// Files maps a slash file path relative to the repository root to its test packages, each
	// `./<dir>` or `.`, sorted and free of duplicates (the spelling of declaredDeps tests).
	Files map[string][]string `json:"files"`
}

// CoverProfiles holds the cover profile text of each test package, keyed by the slash directory of
// the package relative to the repository root.
type CoverProfiles map[string][]byte

// CoverCollection is what one coverage run measured: the profiles and the directory of each import
// path of the module, which turns a profile's file names into repository paths.
type CoverCollection struct {
	Profiles CoverProfiles
	DirOf    map[string]string
}

// CoverSelection is one test package a change selects through the index. Kind is "coverage" or
// "stale", the reason word of SPEC-contracts §8.2, and Reason says which files and why.
type CoverSelection struct {
	Package string
	Kind    string
	Reason  string
}

// CoverSelectResult is the outcome of CoverIndex.Select. Selected is sorted by package, then kind.
// Unmapped lists the changed files a fresh index does not know, sorted: the caller falls back to
// the suite of the module and records a `map_gap` (SPEC L66, L69).
type CoverSelectResult struct {
	Selected []CoverSelection
	Unmapped []string
}

// BuildCoverIndex inverts the profiles: a file that a profile counts at least once under a block
// that ran maps to that profile's package. A profile that is not a cover profile is an error, so an
// unread profile never builds an index that selects nothing (DR-6). An import path that dirOf does
// not know is not part of the module and is left out.
func BuildCoverIndex(profiles CoverProfiles, dirOf map[string]string, built time.Time) (CoverIndex, error) {
	idx := CoverIndex{FormatVersion: coverFormatVersion, Built: built.UTC(), Files: map[string][]string{}}
	for _, dir := range slices.Sorted(maps.Keys(profiles)) {
		files, err := coveredFiles(profiles[dir], dirOf)
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

// stale reports whether the index is too old to select by coverage at now. An index built exactly
// coverMaxAge ago is still fresh.
func (idx CoverIndex) stale(now time.Time) bool {
	return idx.Built.IsZero() || now.Sub(idx.Built) > coverMaxAge
}

// Select returns the test packages the changed files, slash paths relative to the repository root,
// affect. A fresh index gives each file the packages that covered it. A stale index says "stale"
// and gives each file the suite of its module, the directory of the file (SPEC L66, L69). It does
// no IO.
func (idx CoverIndex) Select(changed []string, now time.Time) CoverSelectResult {
	files := cleanChanged(changed)
	var res CoverSelectResult
	byKey := map[[2]string][]string{}
	for _, f := range files {
		switch pkgs := idx.Files[f]; {
		case idx.stale(now):
			key := [2]string{testDir(path.Dir(f)), staleKind}
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
	if key[1] == staleKind {
		return fmt.Sprintf("stale: %s (index built %s, older than %s)", list,
			idx.Built.Format(time.RFC3339), coverMaxAge)
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
