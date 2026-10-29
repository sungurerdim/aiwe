package gates

import (
	"bytes"
	"context"
	"errors"
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

// coverHashes are the content hashes of the Go files of each package when the index was built; as
// the hashes at the base, they say no package changed between the index and the base.
func coverHashes() map[string]string {
	return map[string]string{".": "h-root", "internal/core": "h-core", "internal/data": "h-data", "internal/item": "h-item"}
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

// coverCollection is what the shared test coverage run measured.
func coverCollection() CoverCollection {
	return CoverCollection{Profiles: coverProfiles(), DirOf: coverDirOf(), Hashes: coverHashes()}
}

func coverTestIndex(t *testing.T) CoverIndex {
	t.Helper()
	idx, err := BuildCoverIndex(coverCollection(), coverBuilt())
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
	if !reflect.DeepEqual(idx.Files, wantFiles) || !reflect.DeepEqual(idx.Hashes, coverHashes()) {
		t.Errorf("index files, hashes = %v, %v, want %v, %v", idx.Files, idx.Hashes, wantFiles, coverHashes())
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
	for _, tc := range cases { // the work tree changed these files; the base still equals the index
		t.Run(tc.name, func(t *testing.T) {
			got := idx.Select(tc.changed, coverHashes())
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
		c := coverCollection()
		c.Profiles = CoverProfiles{"internal/core": []byte(bad)}
		if _, err := BuildCoverIndex(c, coverBuilt()); err == nil {
			t.Errorf("BuildCoverIndex(profile %q) error = nil, want a refusal", bad)
		}
	}
}

// TestCoverIndexStaleFallsBack catches a work-tree change read as staleness, an entry stale at the
// base that still selects by coverage, a stale entry that takes more or less than its own package
// suite while that package exists at the base, an old index read as stale, a file in no Go package
// that selects its directory, a missing index read as fresh, and a package whose profile is
// missing read as covering nothing (SPEC L66, L69, DR-2, DR-6).
func TestCoverIndexStaleFallsBack(t *testing.T) {
	idx := coverTestIndex(t)
	old := idx
	old.Built = coverBuilt().AddDate(-1, 0, 0)
	moved := coverHashes()
	moved["internal/data"], moved["newdir"] = "h-data-at-base", "h-new"
	delete(moved, ".")
	missing, err := LoadCoverIndex(filepath.Join(t.TempDir(), "absent.json"))
	notMeasured := coverCollection()
	notMeasured.NotMeasured = []string{"internal/item"}
	partial, buildErr := BuildCoverIndex(notMeasured, coverBuilt())
	if err != nil || buildErr != nil {
		t.Fatalf("LoadCoverIndex(absent), BuildCoverIndex(not measured) = %v, %v, want nil, nil", err, buildErr)
	}
	const why = " (the Go files of their package at the base differ from the index)"
	cases := []struct {
		name    string
		idx     CoverIndex
		changed []string
		atBase  map[string]string
		want    []CoverSelection
		unknown []string
	}{
		{"a work-tree change in a package unchanged at the base selects by coverage, however old the index",
			old, []string{"internal/data/rules.go"}, coverHashes(),
			[]CoverSelection{{"./internal/core", "coverage", "coverage: internal/data/rules.go covered by ./internal/core"}}, nil},
		{"a package that differs at the base falls to its own package suite",
			idx, []string{"internal/data/rules.go", "internal/data/other.go", "newdir/x.go"}, moved,
			[]CoverSelection{
				{"./internal/data", "stale", "stale: internal/data/other.go, internal/data/rules.go" + why},
				{"./newdir", "stale", "stale: newdir/x.go" + why}}, nil},
		{"a package gone at the base falls to the full selection",
			idx, []string{"main.go"}, moved, []CoverSelection{{"./...", "stale", "stale: main.go" + why}}, nil},
		{"a file in no Go package never selects its directory",
			missing, []string{"internal/data/testdata/r.json"}, coverHashes(), nil, []string{"internal/data/testdata/r.json"}},
		{"a missing index falls to the suite of the file's package",
			missing, []string{"internal/core/a.go"}, coverHashes(),
			[]CoverSelection{{"./internal/core", "stale", "stale: internal/core/a.go" + why}}, nil},
		{"a package whose profile is missing leaves every file unmapped",
			partial, []string{"internal/data/rules.go", "main.go"}, coverHashes(), nil, []string{"internal/data/rules.go", "main.go"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.idx.Select(tc.changed, tc.atBase)
			if !reflect.DeepEqual(got.Selected, tc.want) || !reflect.DeepEqual(got.Unmapped, tc.unknown) {
				t.Errorf("Select(%q) = %v, unmapped %v, want %v, unmapped %v", tc.changed, got.Selected, got.Unmapped, tc.want, tc.unknown)
			}
		})
	}
	if !reflect.DeepEqual(partial.NotMeasured, []string{"./internal/item"}) {
		t.Errorf("index notMeasured = %v, want [./internal/item]", partial.NotMeasured)
	}
}

// TestCoverHashFollowsGoFiles catches a hash that misses a Go file change, a same-length one or a new
// Go file, one that moves on a non-Go file, a directory with no Go file hashed as a package, a base
// hash that differs from the work-tree hash of the same files, and a work-tree edit that makes its
// own package stale, so coverage never selects (DR-2, SPEC L66).
func TestCoverHashFollowsGoFiles(t *testing.T) {
	root := cacheTestRepo(t, map[string]string{"a/a.go": "package a\n\nconst N = 1\n", "a/x.json": "{}", "b/r.json": "{}"})
	dirs := []string{"a", "b", "gone"}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hash := func() map[string]string {
		got, err := HashGoDirs(root, dirs)
		if err != nil {
			t.Fatalf("HashGoDirs() error = %v, want nil", err)
		}
		return got
	}
	first := hash()
	atBase, err := BaseGoHashes(t.Context(), root, "HEAD", dirs)
	if err != nil || len(first) != 1 || first["a"] == "" || !reflect.DeepEqual(atBase, first) {
		t.Fatalf("HashGoDirs() = %v, BaseGoHashes(HEAD) = %v, %v, want one equal hash, for a", first, atBase, err)
	}
	write("a/x.json", `{"k":1}`)
	if got := hash(); got["a"] != first["a"] {
		t.Errorf("hash after a non-Go change = %q, want %q", got["a"], first["a"])
	}
	write("a/a.go", "package a\n\nconst N = 2\n")
	second := hash()
	if second["a"] == first["a"] {
		t.Errorf("hash after a same-length Go edit = %q, want a new hash", second["a"])
	}
	idx := CoverIndex{Hashes: first, Files: map[string][]string{"a/a.go": {"./a"}}}
	if got := idx.Select([]string{"a/a.go"}, atBase); len(got.Selected) != 1 || got.Selected[0].Kind != "coverage" {
		t.Errorf("Select(edited a/a.go, base hashes) = %v, want a coverage selection", got.Selected)
	}
	write("a/a_test.go", "package a\n")
	if got := hash(); got["a"] == second["a"] {
		t.Errorf("hash after a new Go file = %q, want a new hash", got["a"])
	}
}

// TestCoverIndexFile catches an index that does not survive the file, and a corrupt or other-format
// file read as an index (DR-6).
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
	for _, bad := range []string{"{", `{"formatVersion":1}`, `{"formatVersion":99}`, `{"formatVersion":2} {}`} {
		if err := os.WriteFile(file, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCoverIndex(file); err == nil {
			t.Errorf("LoadCoverIndex(%q) error = nil, want a refusal", bad)
		}
	}
}

// TestCoverSelectorIsRegistered catches a coverage selection whose reason is not the registered
// selector name, a second spelling of "coverage" (SPEC-contracts §8.2).
func TestCoverSelectorIsRegistered(t *testing.T) {
	if got := coverTestIndex(t).Select([]string{"main.go"}, coverHashes()); len(got.Selected) != 1 ||
		got.Selected[0].Kind != SelectorCoverage {
		t.Errorf("Select(main.go).Selected = %v, want one selection of kind %q", got.Selected, SelectorCoverage)
	}
}

// TestMissingProfileIsNotMeasured catches a coverage run that wrote no profile read as a package
// that covers nothing (DR-6).
func TestMissingProfileIsNotMeasured(t *testing.T) {
	dir := t.TempDir()
	if data, measured, err := readCoverProfile(dir, "cover-0.out"); err != nil || measured || data != nil {
		t.Errorf("readCoverProfile(missing) = %q, %v, %v, want nil, false, nil", data, measured, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cover-1.out"), []byte("mode: set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, measured, err := readCoverProfile(dir, "cover-1.out"); err != nil || !measured || string(data) != "mode: set\n" {
		t.Errorf("readCoverProfile(written) = %q, %v, %v, want the profile, true, nil", data, measured, err)
	}
}

// TestCoverRunTakesTheTestStepLimit catches a coverage run bounded by a fixed limit instead of the
// limit the test step is prepared with from its measured package durations (DR-1).
func TestCoverRunTakesTheTestStepLimit(t *testing.T) {
	r := runnerTestRunner(t)
	r.Recorded = []PackageDuration{{Package: "example.com/m/internal/core", Elapsed: 700 * time.Second}}
	def := Definition{FormatVersion: 1, Steps: []GateStep{{ID: "test", TimeoutSeconds: 900,
		Command: []string{"go", "test", "./..."}, Required: true}}}
	if got, err := r.coverLimit(def); err != nil || got != 1400 {
		t.Errorf("coverLimit() = %d, %v, want 1400, nil: twice the longest recorded package", got, err)
	}
	if _, err := r.coverLimit(Definition{FormatVersion: 1, Steps: []GateStep{{ID: "lint"}}}); err == nil {
		t.Errorf("coverLimit(no test step) error = nil, want a refusal")
	}
}

// coverGoStub copies this test binary to a file named go, so a step that runs `go test` runs the
// helper of durationsTestStep: it prints the fixture the JSON variable names and exits 0.
func coverGoStub(t *testing.T) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "go"+filepath.Ext(os.Args[0]))
	bin, err := os.ReadFile(os.Args[0])
	if err == nil {
		err = os.WriteFile(stub, bin, 0o700)
	}
	if err != nil {
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

// TestFullRunRefreshesTheIndex catches a green full run that leaves the index old; a red test step
// in a green gate, a red gate whose test step is green, a partial run that left out only a step that
// is not required, a run of some packages and a failed collection that rewrite it; and a failed
// refresh that turns a green gate red or hides itself (SPEC L66).
func TestFullRunRefreshesTheIndex(t *testing.T) {
	stub := coverGoStub(t)
	format := GateStep{ID: "format", Command: []string{runnerTestGo(t), "version"}, Required: true}
	optional := GateStep{ID: "format", Command: format.Command}
	redFormat := GateStep{ID: "format", Command: format.Command, Required: true, Expect: Expect{ExitCode: 5}}
	full := GateStep{ID: "test", Command: []string{stub, "test", "-json", "./..."}, Required: true}
	redOptionalTest := GateStep{ID: "test", Command: full.Command, Expect: Expect{ExitCode: 5}}
	some := full
	some.Command = []string{stub, "test", "-json", "./internal/core"}
	collected := func(context.Context, string) (CoverCollection, error) { return coverCollection(), nil }
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
		{"a red test step in a green gate leaves it", []GateStep{format, redOptionalTest}, nil, collected, ResultGreen, false, ""},
		{"a red gate whose test step is green leaves it", []GateStep{redFormat, full}, nil, collected, ResultRed, false, ""},
		{"a green partial run that left out a step not required leaves it", []GateStep{optional, full}, []string{"test"},
			collected, ResultGreen, false, ""},
		{"a run of some packages leaves it", []GateStep{some}, nil, collected, ResultGreen, false, ""},
		{"a failed collection leaves it and says so", []GateStep{full}, nil, failed, ResultGreen, false,
			"coverage index not refreshed: a test package did not run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "cover.json")
			seed := []byte(`{"formatVersion":2,"built":"2026-01-01T00:00:00Z","hashes":{"old":"h"},"files":{"old.go":["./old"]}}`)
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
			got, readErr := os.ReadFile(file)
			idx, loadErr := LoadCoverIndex(file)
			if tc.fresh {
				want := coverTestIndex(t)
				if loadErr != nil || !reflect.DeepEqual(idx.Files, want.Files) || !reflect.DeepEqual(idx.Hashes, want.Hashes) || idx.Built.IsZero() {
					t.Errorf("refreshed index = %+v, %v, want the files and hashes of the collection", idx, loadErr)
				}
			} else if readErr != nil || !bytes.Equal(got, seed) {
				t.Errorf("index file = %s, %v, want it left as it was: %s", got, readErr, seed)
			}
		})
	}
}

// TestCollectCoverProfilesRunsEachTestPackage catches a collection that misses the file a package
// reaches through another package (b's test covers a/a.go through b.B, so a/a.go maps to both) and
// one that does not hash every package.
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
	got, err := runnerTestRunner(t).collectCover(ctx, dir, 300)
	if err != nil {
		t.Fatalf("collectCover(temp module) error = %v, want nil", err)
	}
	idx, err := BuildCoverIndex(got, coverBuilt())
	if err != nil {
		t.Fatalf("BuildCoverIndex(collected) error = %v, want nil", err)
	}
	want := map[string][]string{"a/a.go": {"./a", "./b"}, "b/b.go": {"./b"}}
	if !reflect.DeepEqual(idx.Files, want) || len(idx.NotMeasured) != 0 || len(idx.Hashes) != 3 || idx.Hashes["c"] == "" {
		t.Errorf("collected index = %+v, want files %v, none not measured and a hash for each of a, b and c", idx, want)
	}
}
