package gates

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// coverBuilt is the moment the shared test index was built.
func coverBuilt() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) }

// coverDirOf maps the import paths of the module example.com/m to their directories.
func coverDirOf() map[string]string {
	return map[string]string{"example.com/m": ".", "example.com/m/internal/core": "internal/core",
		"example.com/m/internal/data": "internal/data", "example.com/m/internal/item": "internal/item"}
}

// coverProfiles are the cover profiles of two test packages: core covers its own a.go and, through
// a data file, internal/data/rules.go (never b.go, whose block ran 0 times); item covers core/a.go.
func coverProfiles() CoverProfiles {
	return CoverProfiles{
		"internal/core": []byte("mode: set\n" +
			"example.com/m/internal/core/a.go:3.10,5.2 1 1\n" +
			"example.com/m/internal/core/b.go:3.10,5.2 1 0\n" +
			"example.com/m/internal/data/rules.go:7.1,9.2 2 2\n" +
			"example.com/m/main.go:1.1,2.2 1 1\n"),
		"internal/item": []byte("mode: set\nexample.com/m/internal/core/a.go:3.10,5.2 1 1\n"),
	}
}

func coverTestIndex(t *testing.T) CoverIndex {
	t.Helper()
	idx, err := BuildCoverIndex(coverProfiles(), coverDirOf(), coverBuilt())
	if err != nil {
		t.Fatalf("BuildCoverIndex() error = %v, want nil", err)
	}
	return idx
}

// TestCoverIndexMapsFileToTestPackages catches a file mapped to a test package that did not run it,
// a data file no package owns that selects nothing, a block that ran 0 times counted as covered, a
// package listed twice or unsorted, a root file lost, a file no profile knows that is guessed, and
// a malformed profile read as empty (SPEC L66).
func TestCoverIndexMapsFileToTestPackages(t *testing.T) {
	idx := coverTestIndex(t)
	wantFiles := map[string][]string{
		"internal/core/a.go":     {"./internal/core", "./internal/item"},
		"internal/data/rules.go": {"./internal/core"},
		"main.go":                {"./internal/core"},
	}
	if !reflect.DeepEqual(idx.Files, wantFiles) {
		t.Errorf("index files = %v, want %v", idx.Files, wantFiles)
	}
	cases := []struct {
		name    string
		changed []string
		want    []CoverSelection
		unknown []string
	}{
		{"a data file selects the package whose test ran it", []string{"internal/data/rules.go"},
			[]CoverSelection{{"./internal/core", "coverage", "coverage: internal/data/rules.go covered by ./internal/core"}}, nil},
		{"a file two packages cover selects both, sorted", []string{"internal/core/a.go"},
			[]CoverSelection{
				{"./internal/core", "coverage", "coverage: internal/core/a.go covered by ./internal/core"},
				{"./internal/item", "coverage", "coverage: internal/core/a.go covered by ./internal/item"}}, nil},
		{"two files of one package give one reason", []string{"main.go", "internal/data/rules.go"},
			[]CoverSelection{{"./internal/core", "coverage",
				"coverage: internal/data/rules.go, main.go covered by ./internal/core"}}, nil},
		{"a block that ran 0 times is no coverage, and an unknown file is unmapped",
			[]string{"internal/core/b.go", "docs/x.md"}, nil, []string{"docs/x.md", "internal/core/b.go"}},
		{"a backslash path is the same file", []string{`internal\data\rules.go`},
			[]CoverSelection{{"./internal/core", "coverage", "coverage: internal/data/rules.go covered by ./internal/core"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := idx.Select(tc.changed, coverBuilt())
			if !reflect.DeepEqual(got.Selected, tc.want) {
				t.Errorf("Select(%q).Selected = %v, want %v", tc.changed, got.Selected, tc.want)
			}
			if !reflect.DeepEqual(got.Unmapped, tc.unknown) {
				t.Errorf("Select(%q).Unmapped = %v, want %v", tc.changed, got.Unmapped, tc.unknown)
			}
		})
	}
	for _, bad := range []string{"", "internal/core/a.go:3.10,5.2 1 1\n", "mode: set\nnot a block\n",
		"mode: set\nexample.com/m/main.go:1.1,2.2 x 1\n"} {
		_, err := BuildCoverIndex(CoverProfiles{"internal/core": []byte(bad)}, coverDirOf(), coverBuilt())
		if err == nil {
			t.Errorf("BuildCoverIndex(profile %q) error = nil, want a refusal", bad)
		}
	}
}

// TestCoverIndexStaleFallsBack catches an old index that still selects by coverage, a stale file
// that selects no suite or the wrong one (it must take the suite of its own directory, the module,
// with the word stale), an index exactly at the limit read as stale, and a missing file read as
// fresh (SPEC L66, L69).
func TestCoverIndexStaleFallsBack(t *testing.T) {
	idx := coverTestIndex(t)
	old := coverBuilt().Add(coverMaxAge + time.Second)
	got := idx.Select([]string{"internal/data/rules.go", "internal/data/other.go", "main.go", "newdir/x.go"}, old)
	if len(got.Unmapped) != 0 {
		t.Errorf("stale Select().Unmapped = %v, want none: a stale file falls to its module", got.Unmapped)
	}
	wantPkgs := []string{".", "./internal/data", "./newdir"}
	var pkgs []string
	for _, s := range got.Selected {
		pkgs = append(pkgs, s.Package)
		if s.Kind != "stale" || !strings.HasPrefix(s.Reason, "stale: ") {
			t.Errorf("selection %+v, want Kind stale and a reason that starts with %q", s, "stale: ")
		}
	}
	if !reflect.DeepEqual(pkgs, wantPkgs) {
		t.Errorf("stale Select() packages = %v, want %v", pkgs, wantPkgs)
	}
	if !strings.Contains(got.Selected[1].Reason, "internal/data/other.go, internal/data/rules.go") {
		t.Errorf("stale reason = %q, want both files of the module", got.Selected[1].Reason)
	}
	edge := idx.Select([]string{"internal/data/rules.go"}, coverBuilt().Add(coverMaxAge))
	if len(edge.Selected) != 1 || edge.Selected[0].Kind != "coverage" {
		t.Errorf("Select() at the age limit = %v, want a coverage selection", edge.Selected)
	}
	loaded, err := LoadCoverIndex(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("LoadCoverIndex(absent) error = %v, want nil", err)
	}
	none := loaded.Select([]string{"internal/core/a.go"}, coverBuilt())
	if len(none.Selected) != 1 || none.Selected[0].Kind != "stale" || none.Selected[0].Package != "./internal/core" {
		t.Errorf("Select() on a missing index = %v, want the module suite with reason stale", none.Selected)
	}
}

// TestCoverIndexFile catches an index that does not survive the file, and a corrupt or newer file
// read as an index (DR-6).
func TestCoverIndexFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cover.json")
	idx := coverTestIndex(t)
	if err := SaveCoverIndex(file, idx); err != nil {
		t.Fatalf("SaveCoverIndex() error = %v, want nil", err)
	}
	got, err := LoadCoverIndex(file)
	if err != nil || !reflect.DeepEqual(got, idx) {
		t.Errorf("LoadCoverIndex() = %v, %v, want %v, nil", got, err, idx)
	}
	for _, bad := range []string{"{", `{"formatVersion":99}`, `{"formatVersion":1} {}`} {
		if err := os.WriteFile(file, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCoverIndex(file); err == nil {
			t.Errorf("LoadCoverIndex(%q) error = nil, want a refusal", bad)
		}
	}
}

// TestCoverSelectorIsRegistered catches a coverage selector the register does not know and an
// unknown name it accepts.
func TestCoverSelectorIsRegistered(t *testing.T) {
	if !KnownSelector(SelectorCoverage) || KnownSelector("no-such-selector") {
		t.Errorf("KnownSelector(%q) = %v, KnownSelector(unknown) = %v, want true, false",
			SelectorCoverage, KnownSelector(SelectorCoverage), KnownSelector("no-such-selector"))
	}
}

// coverGoStub copies this test binary to a file named go, so a step that runs `go test` runs the
// helper of durationsTestStep: it prints the fixture the JSON variable names and exits 0.
func coverGoStub(t *testing.T) string {
	t.Helper()
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	stub := filepath.Join(t.TempDir(), "go"+filepath.Ext(os.Args[0]))
	dst, err := os.OpenFile(stub, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(dst, src)
	if err = errors.Join(err, dst.Close()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(durationsJSONEnv(), writeCoverFixture(t))
	return stub
}

func writeCoverFixture(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "go-test.json")
	if err := os.WriteFile(file, []byte(durationsTestPackage(durationsTestModule+"/internal/core", 2.4)), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestFullRunRefreshesTheIndex catches a green full run that leaves the index old, a red run, a
// partial run, a run of some packages and a failed collection that rewrite it, and a failed refresh
// that turns a green gate red or hides itself (SPEC L66).
func TestFullRunRefreshesTheIndex(t *testing.T) {
	stub := coverGoStub(t)
	format := GateStep{ID: "format", Command: []string{runnerTestGo(t), "version"}, Required: true}
	full := GateStep{ID: "test", Command: []string{stub, "test", "-json", "./..."}, Required: true}
	red := full
	red.Expect = Expect{ExitCode: 5}
	some := full
	some.Command = []string{stub, "test", "-json", "./internal/core"}
	collected := func(context.Context, string) (CoverCollection, error) {
		return CoverCollection{Profiles: coverProfiles(), DirOf: coverDirOf()}, nil
	}
	failed := func(context.Context, string) (CoverCollection, error) {
		return CoverCollection{}, errors.New("a test package did not run")
	}
	cases := []struct {
		name    string
		steps   []GateStep
		only    []string
		collect func(context.Context, string) (CoverCollection, error)
		gate    Result
		fresh   bool
		detail  string
	}{
		{"a green full run refreshes the index", []GateStep{full}, nil, collected, ResultGreen, true, ""},
		{"a red run leaves it", []GateStep{red}, nil, collected, ResultRed, false, ""},
		{"a partial run leaves it", []GateStep{format, full}, []string{"test"}, collected, ResultIncomplete, false, ""},
		{"a run of some packages leaves it", []GateStep{some}, nil, collected, ResultGreen, false, ""},
		{"a failed collection leaves it and says so", []GateStep{full}, nil, failed, ResultGreen, false,
			"coverage index not refreshed: a test package did not run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "cover.json")
			seed := []byte(`{"formatVersion":1,"built":"2026-01-01T00:00:00Z","files":{"old.go":["./old"]}}`)
			if err := os.WriteFile(file, seed, 0o600); err != nil {
				t.Fatal(err)
			}
			r := runnerTestRunner(t)
			r.Cover = &CoverRefresh{Path: file, Collect: tc.collect}
			report, err := r.Run(context.Background(), Definition{FormatVersion: 1, Steps: tc.steps}, t.TempDir(), tc.only)
			if err != nil || report.Result != tc.gate {
				t.Fatalf("Run() = %v, %v, want a %s gate", report.Result, err, tc.gate)
			}
			if tc.detail != "" && !strings.Contains(report.Detail, tc.detail) {
				t.Errorf("gate detail = %q, want it to contain %q", report.Detail, tc.detail)
			}
			got, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			idx, loadErr := LoadCoverIndex(file)
			if tc.fresh {
				if loadErr != nil || !reflect.DeepEqual(idx.Files, coverTestIndex(t).Files) || idx.Built.IsZero() {
					t.Errorf("refreshed index = %+v, %v, want the files of the collected profiles", idx, loadErr)
				}
			} else if !bytes.Equal(got, seed) {
				t.Errorf("index file = %s, want it left as it was: %s", got, seed)
			}
		})
	}
}

// TestCollectCoverProfilesRunsEachTestPackage catches a collection that misses the file a package
// reaches through another package: b's test covers a/a.go through b.B, so a/a.go maps to both.
func TestCollectCoverProfilesRunsEachTestPackage(t *testing.T) {
	runnerTestGo(t)
	dir := selectGoWriteModule(t, map[string]string{
		"go.mod":      "module example.com/tmp\n\ngo 1.21\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { _ = A() }\n",
		"b/b.go":      "package b\n\nimport \"example.com/tmp/a\"\n\nfunc B() int { return a.A() }\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { _ = B() }\n",
		"c/c.go":      "package c\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	got, err := runnerTestRunner(t).collectCover(ctx, dir)
	if err != nil {
		t.Fatalf("collectCover(temp module) error = %v, want nil", err)
	}
	idx, err := BuildCoverIndex(got.Profiles, got.DirOf, coverBuilt())
	if err != nil {
		t.Fatalf("BuildCoverIndex(collected) error = %v, want nil", err)
	}
	want := map[string][]string{"a/a.go": {"./a", "./b"}, "b/b.go": {"./b"}}
	if !reflect.DeepEqual(idx.Files, want) {
		t.Errorf("collected index files = %v, want %v", idx.Files, want)
	}
}
