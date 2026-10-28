package gates

import (
	"errors"
	"reflect"
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

// TestDeclaredRowWithoutTestsNeedsAReason catches a declaredDeps row that selects no test with no
// word on why (a silent gap), a row that names no path or an empty test or a glob that can never
// match, a refusal that is not a core.Error or does not name the row, and a gate without
// declaredDeps or a row with only one of tests and reason that is refused (SPEC L70).
func TestDeclaredRowWithoutTestsNeedsAReason(t *testing.T) {
	const head = `{"formatVersion":1,"steps":[{"id":"test","command":["go","test","./..."],"required":true,` +
		`"expect":{"exitCode":0}}],"declaredDeps":[`
	cases := []struct {
		name    string
		rows    string
		refused string
	}{
		{"a gate without declaredDeps stays valid", ``, ""},
		{"tests without a reason is valid", `{"paths":["docs/SPEC.md"],"tests":["./internal/roles"]}`, ""},
		{"a reason without tests is valid", `{"paths":["docs/guide.md"],"reason":"read by no test"}`, ""},
		{"tests and a reason is valid", `{"paths":["a/**"],"tests":["./a"],"reason":"x"}`, ""},
		{"no tests and no reason is refused", `{"paths":["docs/SPEC.md"]}`, "declaredDeps row 1"},
		{"an empty tests list and an empty reason is refused", `{"paths":["x.md"],"tests":[],"reason":""}`, "declaredDeps row 1"},
		{"a blank reason is no reason", `{"paths":["x.md"],"reason":"   "}`, "declaredDeps row 1"},
		{"the second row is the one named", `{"paths":["a.md"],"reason":"r"},{"paths":["b.md"]}`, "declaredDeps row 2"},
		{"a row with no path declares nothing", `{"tests":["./a"],"reason":"r"}`, "declaredDeps row 1"},
		{"an empty path glob", `{"paths":[""],"tests":["./a"]}`, "declaredDeps row 1"},
		{"an empty test name", `{"paths":["a.md"],"tests":[""]}`, "declaredDeps row 1"},
		{"a glob that path.Match rejects can never match", `{"paths":["docs/[x.md"],"tests":["./a"]}`, "declaredDeps row 1"},
		{"an unknown field of a row", `{"paths":["a.md"],"tests":["./a"],"packages":["./b"]}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, err := ParseDefinition([]byte(head + tc.rows + `]}`))
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
