package repocheck_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// The fixtures use a stand-in module so this file does not hold the real product name.
const fixtureModule = "github.com/example/zentra"

// goListModule is the Module object of one `go list -json` package.
type goListModule struct {
	Path    string `json:",omitempty"`
	Version string `json:",omitempty"`
	Main    bool   `json:",omitempty"`
}

// goListPackage is the subset of one `go list -deps -json` package that the checks read.
type goListPackage struct {
	ImportPath string        `json:",omitempty"`
	Standard   bool          `json:",omitempty"`
	Module     *goListModule `json:",omitempty"`
	Imports    []string      `json:",omitempty"`
}

// ownPackage is a package of the fixture module, at its repository-relative directory.
func ownPackage(dir string, imports ...string) goListPackage {
	return goListPackage{
		ImportPath: fixtureModule + "/" + dir,
		Module:     &goListModule{Path: fixtureModule, Main: true},
		Imports:    imports,
	}
}

// own is the import path of a fixture-module package at its repository-relative directory.
func own(dir string) string { return fixtureModule + "/" + dir }

// stdPackage is a standard-library package.
func stdPackage(path string) goListPackage {
	return goListPackage{ImportPath: path, Standard: true}
}

// goListOutput renders packages the way `go list -deps -json` prints them: indented objects back
// to back, one per package.
func goListOutput(t *testing.T, pkgs ...goListPackage) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, p := range pkgs {
		b, err := json.MarshalIndent(p, "", "\t")
		if err != nil {
			t.Fatalf("marshal fixture package %s: %v", p.ImportPath, err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// hasFinding reports whether got holds a finding of rule at path.
func hasFinding(got []repocheck.Finding, rule, path string) bool {
	for _, f := range got {
		if f.Rule == rule && f.Path == path {
			return true
		}
	}
	return false
}

// fixtureEdges is a module table in the shape of SPEC §6.1: each module directory and the
// module directories it may import. It stands in for core.Modules(), which lives in core.
func fixtureEdges() map[string][]string {
	return map[string][]string{
		"internal/core":          nil,
		"internal/gates":         {"internal/core"},
		"internal/ledger":        {"internal/core"},
		"internal/engine":        {"internal/core", "internal/gates", "internal/ledger"},
		"internal/repocheck":     {"internal/core"},
		"cmd/zentra":             {"internal/core", "internal/gates", "internal/ledger", "internal/engine"},
		"internal/cmd/repocheck": {"internal/repocheck"},
	}
}

func TestBoundaries(t *testing.T) {
	t.Run("declared edges and own private packages pass", func(t *testing.T) {
		// Guards against a check so broad that the allowed import forms of DR-24 turn it red.
		out := goListOutput(t,
			stdPackage("fmt"),
			ownPackage("internal/core", "fmt"),
			ownPackage("internal/gates/internal/runner", own("internal/core")),
			ownPackage("internal/gates", own("internal/core"), own("internal/gates/internal/runner")),
			ownPackage("internal/ledger", own("internal/core")),
			ownPackage("internal/engine",
				own("internal/core"), own("internal/gates"), own("internal/ledger"), "fmt"),
			ownPackage("internal/repocheck", own("internal/core")),
			ownPackage("internal/cmd/repocheck", own("internal/repocheck")),
			ownPackage("cmd/zentra", own("internal/engine"), own("internal/core")),
		)
		got, err := repocheck.Boundaries(fixtureEdges(), out)
		if err != nil {
			t.Fatalf("Boundaries(known-good) error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("Boundaries(known-good) = %+v, want no finding", got)
		}
	})

	bad := []struct {
		name     string
		pkgs     []goListPackage
		rule     string
		importer string
	}{
		{"a private package of a declared module",
			[]goListPackage{
				ownPackage("internal/core"),
				ownPackage("internal/gates/internal/runner", own("internal/core")),
				ownPackage("internal/gates", own("internal/gates/internal/runner")),
				ownPackage("internal/engine", own("internal/gates/internal/runner")),
			},
			"private_import", "internal/engine"},
		{"a root package over an undeclared edge",
			[]goListPackage{
				ownPackage("internal/core"),
				ownPackage("internal/gates", own("internal/core")),
				ownPackage("internal/ledger", own("internal/gates")),
			},
			"undeclared_edge", "internal/ledger"},
		{"the front importing repocheck",
			[]goListPackage{
				ownPackage("internal/core"),
				ownPackage("internal/repocheck", own("internal/core")),
				ownPackage("cmd/zentra", own("internal/repocheck")),
			},
			"undeclared_edge", "cmd/zentra"},
		{"core importing any module",
			[]goListPackage{
				ownPackage("internal/ledger"),
				ownPackage("internal/core", own("internal/ledger")),
			},
			"undeclared_edge", "internal/core"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repocheck.Boundaries(fixtureEdges(), goListOutput(t, tc.pkgs...))
			if err != nil {
				t.Fatalf("Boundaries error = %v, want nil", err)
			}
			if !hasFinding(got, tc.rule, tc.importer) {
				t.Fatalf("Boundaries = %+v, want a %q finding for %s", got, tc.rule, tc.importer)
			}
		})
	}

	t.Run("empty go list output is an error, not a pass", func(t *testing.T) {
		// Guards against absence read as evidence (DR-13): no package listed means nothing was checked.
		got, err := repocheck.Boundaries(fixtureEdges(), nil)
		if err == nil {
			t.Fatalf("Boundaries(empty) = %+v, nil error; want an error", got)
		}
	})

	t.Run("malformed go list output is an error", func(t *testing.T) {
		got, err := repocheck.Boundaries(fixtureEdges(), []byte(`{"ImportPath": `))
		if err == nil {
			t.Fatalf("Boundaries(malformed) = %+v, nil error; want an error", got)
		}
	})
}
