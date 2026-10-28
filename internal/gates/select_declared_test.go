package gates

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// declaredRows are the rows the selection tests share: a SPEC row with a reason, a data row with a
// deep glob and two packages, a second row that names a package the data row also names, and a
// row that selects no test on purpose.
func declaredRows() []DeclaredDep {
	return []DeclaredDep{
		{Paths: []string{"docs/SPEC.md", "docs/standards.md"}, Tests: []string{"./internal/repocheck"},
			Reason: "the registry test reads both"},
		{Paths: []string{"testdata/**/*.json"}, Tests: []string{"./internal/item", "./internal/core"}},
		{Paths: []string{"schema/*.json"}, Tests: []string{"./internal/core"}},
		{Paths: []string{"docs/guide.md"}, Reason: "read by no test"},
	}
}

// TestDeclaredRowSelectsItsPackages catches a declared non-code dependency that selects no test,
// the wrong rows' tests, a package twice, an unsorted answer, a selection without the row as its
// reason, or a changed file that no row knows and is not reported (SPEC L69, L70).
func TestDeclaredRowSelectsItsPackages(t *testing.T) {
	cases := []struct {
		name         string
		changed      []string
		want         []DeclaredSelection
		wantUnmapped []string
	}{
		{"a SPEC.md change selects the registry package with the row and its reason as reason",
			[]string{"docs/SPEC.md"},
			[]DeclaredSelection{{Package: "./internal/repocheck",
				Reason: "declared: docs/SPEC.md match docs/SPEC.md, docs/standards.md (the registry test reads both)"}},
			nil},
		{"a change of the second path of a row selects the same row",
			[]string{"docs/standards.md"},
			[]DeclaredSelection{{Package: "./internal/repocheck",
				Reason: "declared: docs/standards.md match docs/SPEC.md, docs/standards.md (the registry test reads both)"}},
			nil},
		{"a deep data file matches a ** glob and selects both packages of the row, sorted",
			[]string{"testdata/a/b/rules.json"},
			[]DeclaredSelection{
				{Package: "./internal/core", Reason: "declared: testdata/a/b/rules.json match testdata/**/*.json"},
				{Package: "./internal/item", Reason: "declared: testdata/a/b/rules.json match testdata/**/*.json"}},
			nil},
		{"a ** glob also matches a file at the top level of its directory",
			[]string{"testdata/rules.json"},
			[]DeclaredSelection{
				{Package: "./internal/core", Reason: "declared: testdata/rules.json match testdata/**/*.json"},
				{Package: "./internal/item", Reason: "declared: testdata/rules.json match testdata/**/*.json"}},
			nil},
		{"a single * glob does not reach a file in a deeper directory, so the file is unmapped",
			[]string{"schema/deep/x.json"}, nil, []string{"schema/deep/x.json"}},
		{"a package named by two rows is selected once, with both rows as reason",
			[]string{"schema/s.json", "testdata/t.json"},
			[]DeclaredSelection{
				{Package: "./internal/core",
					Reason: "declared: testdata/t.json match testdata/**/*.json; declared: schema/s.json match schema/*.json"},
				{Package: "./internal/item", Reason: "declared: testdata/t.json match testdata/**/*.json"}},
			nil},
		{"a file no row declares is unmapped, never selected and never dropped",
			[]string{"README.md"}, nil, []string{"README.md"}},
		{"a matched file selects its row while an unknown file beside it stays unmapped, sorted",
			[]string{"z.bin", "docs/SPEC.md", "a.bin"},
			[]DeclaredSelection{{Package: "./internal/repocheck",
				Reason: "declared: docs/SPEC.md match docs/SPEC.md, docs/standards.md (the registry test reads both)"}},
			[]string{"a.bin", "z.bin"}},
		{"a row with a reason and no tests selects nothing and the file is mapped, not a gap",
			[]string{"docs/guide.md"}, nil, nil},
		{"a Windows separator and a dot segment still match the row",
			[]string{`.\docs\SPEC.md`},
			[]DeclaredSelection{{Package: "./internal/repocheck",
				Reason: "declared: docs/SPEC.md match docs/SPEC.md, docs/standards.md (the registry test reads both)"}},
			nil},
		{"the same file listed twice is one reason and one selection",
			[]string{"docs/SPEC.md", "docs/SPEC.md"},
			[]DeclaredSelection{{Package: "./internal/repocheck",
				Reason: "declared: docs/SPEC.md match docs/SPEC.md, docs/standards.md (the registry test reads both)"}},
			nil},
		{"no changed file selects nothing and reports no gap", nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectDeclared(declaredRows(), tc.changed)
			if !reflect.DeepEqual(got.Selected, tc.want) {
				t.Errorf("SelectDeclared(%q).Selected = %+v, want %+v", tc.changed, got.Selected, tc.want)
			}
			if !reflect.DeepEqual(got.Unmapped, tc.wantUnmapped) {
				t.Errorf("SelectDeclared(%q).Unmapped = %q, want %q", tc.changed, got.Unmapped, tc.wantUnmapped)
			}
		})
	}
}

// TestDeclaredSelectionWithoutRows catches a gate without declaredDeps that selects a package or
// loses a changed file: every changed file is a gap, so the caller runs the suite of the module (L69).
func TestDeclaredSelectionWithoutRows(t *testing.T) {
	got := SelectDeclared(nil, []string{"b.txt", "a.txt"})
	if len(got.Selected) != 0 || !reflect.DeepEqual(got.Unmapped, []string{"a.txt", "b.txt"}) {
		t.Fatalf("SelectDeclared(nil rows) = %+v, want nothing selected and a.txt, b.txt unmapped", got)
	}
}

// parseDeclared parses a gate.json with the given declaredDeps rows (JSON objects).
func parseDeclared(rows string) (Definition, error) {
	const head = `{"formatVersion":1,"steps":[{"id":"test","command":["go","test","./..."],"required":true,` +
		`"expect":{"exitCode":0}}],"declaredDeps":[`
	return ParseDefinition([]byte(head + rows + `]}`))
}

// TestDeclaredRowWithoutTestsNeedsAReason catches a declaredDeps row without a reason, with or
// without tests (a silent row: SPEC-contracts 8.3 gives every row tests and a reason), a row that
// names no path or a glob that can never match, a refusal that is not a core.Error or does not
// name the row, and a gate without declaredDeps that is refused (SPEC L70).
func TestDeclaredRowWithoutTestsNeedsAReason(t *testing.T) {
	cases := []struct {
		name    string
		rows    string
		refused string
	}{
		{"a gate without declaredDeps stays valid", ``, ""},
		{"tests without a reason is refused, every row says why", `{"paths":["docs/SPEC.md"],"tests":["./internal/roles"]}`,
			"declaredDeps row 1"},
		{"tests with a blank reason is refused", `{"paths":["a.md"],"tests":["./a"],"reason":" \t"}`, "declaredDeps row 1"},
		{"a reason without tests is valid", `{"paths":["docs/guide.md"],"reason":"read by no test"}`, ""},
		{"tests and a reason is valid", `{"paths":["a/**"],"tests":["./a"],"reason":"x"}`, ""},
		{"no tests and no reason is refused", `{"paths":["docs/SPEC.md"]}`, "declaredDeps row 1"},
		{"an empty tests list and an empty reason is refused", `{"paths":["x.md"],"tests":[],"reason":""}`, "declaredDeps row 1"},
		{"a blank reason is no reason", `{"paths":["x.md"],"reason":"   "}`, "declaredDeps row 1"},
		{"the second row is the one named", `{"paths":["a.md"],"reason":"r"},{"paths":["b.md"]}`, "declaredDeps row 2"},
		{"a row with no path declares nothing", `{"tests":["./a"],"reason":"r"}`, "declaredDeps row 1"},
		{"an empty path glob", `{"paths":[""],"tests":["./a"],"reason":"r"}`, "declaredDeps row 1"},
		{"an empty test name", `{"paths":["a.md"],"tests":[""],"reason":"r"}`, "declaredDeps row 1"},
		{"a glob that path.Match rejects can never match", `{"paths":["docs/[x.md"],"tests":["./a"],"reason":"r"}`, "declaredDeps row 1"},
		{"an unknown field of a row", `{"paths":["a.md"],"tests":["./a"],"reason":"r","packages":["./b"]}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, err := parseDeclared(tc.rows)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("ParseDefinition error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseDefinition accepted %+v, want a refusal naming %q", def.DeclaredDeps, tc.refused)
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
				t.Fatalf("refusal = %v, want a core.Error with outcome %q", err, core.OutcomeError)
			}
			if !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("refusal = %q, want it to name %q", err.Error(), tc.refused)
			}
		})
	}
}

// TestDeclaredDepsFieldNames catches a declaredDeps field that does not read the names of SPEC
// L70 (paths, tests, reason) into the row, so gate.json would declare nothing.
func TestDeclaredDepsFieldNames(t *testing.T) {
	const body = `{"formatVersion":1,"steps":[{"id":"test","command":["go","test"],"expect":{"exitCode":0}}],` +
		`"declaredDeps":[{"paths":["docs/SPEC.md"],"tests":["./internal/roles"],"reason":"why"}]}`
	def, err := ParseDefinition([]byte(body))
	if err != nil {
		t.Fatalf("ParseDefinition error = %v, want nil", err)
	}
	want := []DeclaredDep{{Paths: []string{"docs/SPEC.md"}, Tests: []string{"./internal/roles"}, Reason: "why"}}
	if !reflect.DeepEqual(def.DeclaredDeps, want) {
		t.Fatalf("DeclaredDeps = %+v, want %+v", def.DeclaredDeps, want)
	}
}

// TestRepositoryGateDeclaresItsSpecFiles catches the repository gate.json that lists the SPEC
// files only as step inputs: each of the three must select the package whose test reads it.
func TestRepositoryGateDeclaresItsSpecFiles(t *testing.T) {
	def := repositoryGate(t)
	cases := []struct{ file, pkg string }{
		{"docs/SPEC-contracts.md", "./internal/roles"},
		{"docs/SPEC.md", "./internal/repocheck"},
		{"docs/standards.md", "./internal/repocheck"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got := SelectDeclared(def.DeclaredDeps, []string{tc.file})
			if len(got.Unmapped) != 0 || len(got.Selected) != 1 || got.Selected[0].Package != tc.pkg ||
				!strings.Contains(got.Selected[0].Reason, tc.file) {
				t.Fatalf("SelectDeclared(gate.json rows, %q) = %+v, want %s selected and nothing unmapped", tc.file, got, tc.pkg)
			}
		})
	}
}

// TestDeclaredTestEntryIsARelativeDir catches a tests entry that cannot join a Go selection on its
// directory: a pattern that names many packages, an import path, a path with a trailing slash, a
// dot or a doubled separator, or one that leaves the repository. #598 joins Package on
// GoSelection.Dir, so one package must have one spelling (SPEC L69).
func TestDeclaredTestEntryIsARelativeDir(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		ok    bool
	}{
		{"a relative directory", "./internal/roles", true},
		{"a nested directory", "./a/b/c", true},
		{"the root package", ".", true},
		{"the pattern that names the whole tree", "./...", false},
		{"a pattern below a directory", "./internal/...", false},
		{"an import path", durationsTestModule + "/internal/roles", false},
		{"a directory without the ./ prefix", "internal/roles", false},
		{"a trailing slash gives a second spelling", "./internal/roles/", false},
		{"a doubled separator", "./internal//roles", false},
		{"a dot segment", "./internal/./roles", false},
		{"a parent segment leaves the repository", "./internal/../../x", false},
		{"only the prefix", "./", false},
		{"dot dot", "..", false},
		{"an absolute path", "/internal/roles", false},
		{"a Windows separator", `./internal\roles`, false},
		{"a wildcard", "./internal/*", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.entry)
			if err != nil {
				t.Fatalf("marshal %q: %v", tc.entry, err)
			}
			_, err = parseDeclared(`{"paths":["a.md"],"tests":[` + string(raw) + `],"reason":"r"}`)
			if tc.ok && err != nil {
				t.Fatalf("ParseDefinition(tests %q) error = %v, want nil", tc.entry, err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "declaredDeps row 1")) {
				t.Fatalf("ParseDefinition(tests %q) = %v, want a refusal naming declaredDeps row 1", tc.entry, err)
			}
		})
	}
}

// TestDeclaredPartitionsEveryChangedFile catches a changed file that lands in none or in two of
// Selected, Exempt and Unmapped: a row without tests that swallows a file silently (the ** repro),
// a file both selected and exempt, an exemption without the row's reason, an unsorted or repeated
// list, a row with no path that matches every file, and a package or reason listed twice (L69).
func TestDeclaredPartitionsEveryChangedFile(t *testing.T) {
	sel := func(pkg, reason string) DeclaredSelection { return DeclaredSelection{Package: pkg, Reason: reason} }
	cases := []struct {
		name         string
		rows         []DeclaredDep
		changed      []string
		wantSelected []DeclaredSelection
		wantExempt   []DeclaredExempt
		wantUnmapped []string
	}{
		{"a ** row with a reason and no tests exempts a new file with the reason, it is not a silent skip",
			[]DeclaredDep{{Paths: []string{"**"}, Reason: "x"}}, []string{"data/new.bin"},
			nil, []DeclaredExempt{{File: "data/new.bin", Reason: "x"}}, nil},
		{"a file a row with tests selects is not also exempt by a row without tests",
			[]DeclaredDep{{Paths: []string{"a.md"}, Tests: []string{"./a"}, Reason: "r"},
				{Paths: []string{"**"}, Reason: "rest"}},
			[]string{"a.md", "zz.bin"},
			[]DeclaredSelection{sel("./a", "declared: a.md match a.md (r)")},
			[]DeclaredExempt{{File: "zz.bin", Reason: "rest"}}, nil},
		{"one run puts a selected, an exempt and an unmapped file each in one list, sorted",
			[]DeclaredDep{{Paths: []string{"s.md"}, Tests: []string{"./s"}, Reason: "r"},
				{Paths: []string{"e2.md", "e1.md"}, Reason: "none reads them"}},
			[]string{"u.md", "e2.md", "s.md", "e1.md", "e1.md"},
			[]DeclaredSelection{sel("./s", "declared: s.md match s.md (r)")},
			[]DeclaredExempt{{File: "e1.md", Reason: "none reads them"}, {File: "e2.md", Reason: "none reads them"}},
			[]string{"u.md"}},
		{"two rows without tests that match one file give both reasons in row order",
			[]DeclaredDep{{Paths: []string{"f.md"}, Reason: "first"}, {Paths: []string{"*.md"}, Reason: "second"}},
			[]string{"f.md"}, nil, []DeclaredExempt{{File: "f.md", Reason: "first; second"}}, nil},
		{"a row with no path matches no file even when it has tests",
			[]DeclaredDep{{Tests: []string{"./a"}, Reason: "r"}, {Reason: "none"}}, []string{"q.md"},
			nil, nil, []string{"q.md"}},
		{"a package listed twice in one row is selected once with one reason",
			[]DeclaredDep{{Paths: []string{"a.md"}, Tests: []string{"./a", "./a"}, Reason: "r"}}, []string{"a.md"},
			[]DeclaredSelection{sel("./a", "declared: a.md match a.md (r)")}, nil, nil},
		{"two identical rows give the package one reason, not the same reason twice",
			[]DeclaredDep{{Paths: []string{"a.md"}, Tests: []string{"./a"}, Reason: "r"},
				{Paths: []string{"a.md"}, Tests: []string{"./a"}, Reason: "r"}}, []string{"a.md"},
			[]DeclaredSelection{sel("./a", "declared: a.md match a.md (r)")}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectDeclared(tc.rows, tc.changed)
			if !reflect.DeepEqual(got.Selected, tc.wantSelected) {
				t.Errorf("Selected = %+v, want %+v", got.Selected, tc.wantSelected)
			}
			if !reflect.DeepEqual(got.Exempt, tc.wantExempt) {
				t.Errorf("Exempt = %+v, want %+v", got.Exempt, tc.wantExempt)
			}
			if !reflect.DeepEqual(got.Unmapped, tc.wantUnmapped) {
				t.Errorf("Unmapped = %q, want %q", got.Unmapped, tc.wantUnmapped)
			}
			for _, f := range tc.changed {
				if n := listsHolding(got, f); n != 1 {
					t.Errorf("file %q is in %d of Selected, Exempt and Unmapped, want exactly 1", f, n)
				}
			}
		})
	}
}

// listsHolding counts the lists of res that hold file: Selected by a reason that names it.
func listsHolding(res DeclaredSelectResult, file string) int {
	n := 0
	if slices.ContainsFunc(res.Selected, func(s DeclaredSelection) bool { return strings.Contains(s.Reason, file) }) {
		n++
	}
	if slices.ContainsFunc(res.Exempt, func(e DeclaredExempt) bool { return e.File == file }) {
		n++
	}
	if slices.Contains(res.Unmapped, file) {
		n++
	}
	return n
}
